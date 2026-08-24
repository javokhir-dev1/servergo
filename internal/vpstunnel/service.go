// Package vpstunnel — "VPS Tunnel" bo'limi: foydalanuvchining o'z VPS'idagi
// servergo-relay (qarang: relay/) orqali lokal portlarni internetga chiqarish.
// Cloudflare Tunnel bo'limiga (internal/tunnel) mustaqil — o'z bazasi, o'z
// hodisa oqimi, UI'da alohida bo'lim.
package vpstunnel

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"servergo/internal/vpstunnel/applog"
	"servergo/internal/vpstunnel/manager"
	"servergo/internal/vpstunnel/store"
)

const maxEvents = 500

type Event struct {
	Seq  int    `json:"seq"`
	Name string `json:"name"`
	Data any    `json:"data"`
	At   string `json:"at"`
}

type Service struct {
	mu       sync.RWMutex
	st       *store.Store
	mgr      *manager.Manager
	fatalErr string

	// crossCheck — Tunnellar (Cloudflare) bo'limidan subdomen bandligini
	// so'rash uchun (main.go orqali ulanadi). Qarang: tunnel.Service.crossCheck.
	crossCheck func(subdomain, baseDomain string) (bool, error)

	events []Event
	seq    int
}

// SetCrossChecker — boshqa bo'limning subdomen tekshiruvchisini ulaydi.
func (s *Service) SetCrossChecker(fn func(subdomain, baseDomain string) (bool, error)) {
	s.crossCheck = fn
}

// SubdomainTaken — boshqa bo'lim shu yerdan so'rashi uchun ochiq metod.
func (s *Service) SubdomainTaken(subdomain, baseDomain string) (bool, error) {
	if s.st == nil {
		return false, nil
	}
	return s.st.SubdomainTaken(subdomain, baseDomain, "")
}

func New() *Service {
	s := &Service{events: make([]Event, 0, maxEvents)}

	applog.Init(store.LogDir())
	applog.SetEmitter(s.emit)
	applog.Info("VPS Tunnel bo'limi ishga tushdi (konfiguratsiya: %s)", store.Dir())

	st, err := store.Open()
	if err != nil {
		s.fatalErr = "Ma'lumotlar bazasini ochib bo'lmadi: " + err.Error()
		applog.Error("%s", s.fatalErr)
		return s
	}
	s.st = st
	if err := st.ResetRunningStatuses(); err != nil {
		applog.Warn("oldingi sessiya statuslari tozalanmadi: %v", err)
	}

	s.mgr = manager.New(st, s.emit)
	s.mgr.SetRelayConfig(s.relayConfig())
	go s.runAutostart()
	return s
}

func (s *Service) Close() {
	if s.mgr != nil {
		applog.Info("Dastur yopilmoqda — VPS tunnellar to'xtatilmoqda")
		s.mgr.StopAll()
	}
	if s.st != nil {
		_ = s.st.Close()
	}
	applog.Close()
}

func (s *Service) emit(name string, data ...any) {
	var payload any
	if len(data) == 1 {
		payload = data[0]
	} else if len(data) > 1 {
		payload = data
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	s.events = append(s.events, Event{Seq: s.seq, Name: name, Data: payload, At: time.Now().Format(time.RFC3339Nano)})
	if len(s.events) > maxEvents {
		s.events = s.events[len(s.events)-maxEvents:]
	}
}

func (s *Service) Events(since int) ([]Event, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if since <= 0 || len(s.events) == 0 {
		return []Event{}, s.seq
	}
	out := []Event{}
	for _, e := range s.events {
		if e.Seq > since {
			out = append(out, e)
		}
	}
	return out, s.seq
}

func (s *Service) ready() error {
	if s.st == nil {
		if s.fatalErr != "" {
			return errors.New(s.fatalErr)
		}
		return errors.New("VPS Tunnel bo'limi ishga tushmagan")
	}
	return nil
}

func (s *Service) AppLogs() []applog.Entry { return applog.All() }
func (s *Service) ClearAppLogs()           { applog.Clear() }
func (s *Service) LogDir() string          { return store.LogDir() }
func (s *Service) FatalError() string      { return s.fatalErr }

func (s *Service) runAutostart() {
	if s.st == nil {
		return
	}
	time.Sleep(800 * time.Millisecond)
	rc := s.relayConfig()
	if rc.Addr == "" || rc.Token == "" || rc.Fingerprint == "" {
		return
	}
	projects, err := s.st.ListProjects()
	if err != nil {
		applog.Error("avtostart uchun loyihalar o'qilmadi: %v", err)
		return
	}
	n := 0
	for _, p := range projects {
		if p.Autostart {
			n++
			if err := s.mgr.Start(p); err != nil {
				applog.Error("avtostart '%s': %v", p.Name, err)
			}
		}
	}
	if n > 0 {
		applog.Info("Avtostart: %d loyiha ulanmoqda", n)
	}
}

// ---- Relay sozlash ----

type SetupState struct {
	RelayAddr       string   `json:"relayAddr"`
	RelayConfigured bool     `json:"relayConfigured"`
	Domains         []string `json:"domains"`
	ActiveDomain    string   `json:"activeDomain"`
	Ready           bool     `json:"ready"`
	FatalError      string   `json:"fatalError"`
	LogDir          string   `json:"logDir"`
}

func (s *Service) relayConfig() manager.RelayConfig {
	if s.st == nil {
		return manager.RelayConfig{}
	}
	return manager.RelayConfig{
		Addr:        s.st.GetSetting("relay_addr", ""),
		Token:       s.st.GetSetting("relay_token", ""),
		Fingerprint: s.st.GetSetting("relay_fingerprint", ""),
	}
}

// migrateLegacyDomain — dastlabki versiyada bitta "wildcard_domain"
// sozlamasi bor edi (bir nechta domenni qo'llab-quvvatlamasdi). Shu
// qiymatni bir martalik ro'yxatga (base_domains/active_domain) ko'chiradi,
// aks holda eski o'rnatishlarda domen ro'yxati bo'sh ko'rinib qolar edi.
func (s *Service) migrateLegacyDomain() {
	if s.st.GetSetting("base_domains", "") != "" {
		return
	}
	legacy := s.st.GetSetting("wildcard_domain", "")
	if legacy == "" {
		return
	}
	_ = s.st.SetSetting("base_domains", legacy)
	_ = s.st.SetSetting("active_domain", legacy)
	applog.Info("Eski wildcard domen sozlamasi ko'chirildi: %s", legacy)
}

func (s *Service) domains() []string {
	s.migrateLegacyDomain()
	raw := s.st.GetSetting("base_domains", "")
	if raw == "" {
		return []string{}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (s *Service) SetupState() SetupState {
	st := SetupState{Domains: []string{}, FatalError: s.fatalErr, LogDir: store.LogDir()}
	if s.st == nil {
		return st
	}
	rc := s.relayConfig()
	st.RelayAddr = rc.Addr
	st.RelayConfigured = rc.Addr != "" && rc.Token != "" && rc.Fingerprint != ""
	st.Domains = s.domains()
	st.ActiveDomain = s.st.GetSetting("active_domain", "")
	if st.ActiveDomain == "" && len(st.Domains) > 0 {
		st.ActiveDomain = st.Domains[0]
	}
	st.Ready = st.RelayConfigured && len(st.Domains) > 0
	return st
}

var hostPortRe = regexp.MustCompile(`^[a-zA-Z0-9.\-]+:\d+$`)
var fingerprintRe = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
var domainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// SetRelayConfig — relay manzili/token/fingerprintini saqlaydi. Domenlar
// alohida (AddDomain/RemoveDomain) boshqariladi.
func (s *Service) SetRelayConfig(addr, token, fingerprint string) (SetupState, error) {
	if err := s.ready(); err != nil {
		return s.SetupState(), err
	}
	addr = strings.TrimSpace(addr)
	token = strings.TrimSpace(token)
	fingerprint = strings.ToLower(strings.TrimSpace(fingerprint))

	if !hostPortRe.MatchString(addr) {
		return s.SetupState(), errors.New("relay manzili noto'g'ri (masalan: 1.2.3.4:9443)")
	}
	if token == "" {
		return s.SetupState(), errors.New("token bo'sh bo'lishi mumkin emas")
	}
	if !fingerprintRe.MatchString(fingerprint) {
		return s.SetupState(), errors.New("fingerprint noto'g'ri — relay ishga tushganda chiqqan SHA256 qatorini nusxalang")
	}

	_ = s.st.SetSetting("relay_addr", addr)
	_ = s.st.SetSetting("relay_token", token)
	_ = s.st.SetSetting("relay_fingerprint", fingerprint)
	s.mgr.SetRelayConfig(s.relayConfig())
	applog.Info("Relay sozlamalari saqlandi: %s", addr)
	return s.SetupState(), nil
}

// AddDomain — bazaviy domenlar ro'yxatiga qo'shadi va faol qilib tanlaydi.
// Cloudflare bo'limidan farqli — bu yerda avtorizatsiya/login kerak emas,
// faqat lokal ro'yxat: domen uchun wildcard DNS (*.domen → VPS IP) qo'lda
// sozlanadi.
func (s *Service) AddDomain(domain string) (SetupState, error) {
	if err := s.ready(); err != nil {
		return s.SetupState(), err
	}
	domain = strings.ToLower(strings.TrimSpace(domain))
	domain = strings.TrimPrefix(strings.TrimPrefix(domain, "https://"), "http://")
	domain = strings.Trim(domain, "/")
	if !domainRe.MatchString(domain) {
		return s.SetupState(), errors.New("domen noto'g'ri formatda (masalan: vps.domeningiz.uz)")
	}
	for _, d := range s.domains() {
		if d == domain {
			_ = s.st.SetSetting("active_domain", domain)
			return s.SetupState(), nil
		}
	}
	list := append(s.domains(), domain)
	_ = s.st.SetSetting("base_domains", strings.Join(list, ","))
	_ = s.st.SetSetting("active_domain", domain)
	applog.Info("Bazaviy domen qo'shildi: %s", domain)
	return s.SetupState(), nil
}

// RemoveDomain — ro'yxatdan olib tashlaydi (DNS'ga tegmaydi). Domen biror
// loyihada ishlatilayotgan bo'lsa rad etiladi.
func (s *Service) RemoveDomain(domain string) (SetupState, error) {
	if err := s.ready(); err != nil {
		return s.SetupState(), err
	}
	domain = strings.ToLower(strings.TrimSpace(domain))

	ps, err := s.st.ListProjects()
	if err != nil {
		return s.SetupState(), err
	}
	var used []string
	for _, p := range ps {
		if p.BaseDomain == domain {
			used = append(used, p.Name)
		}
	}
	if len(used) > 0 {
		return s.SetupState(), fmt.Errorf(
			"'%s' hali %d ta loyihada ishlatilyapti (%s) — avval o'sha loyihalarni o'chiring yoki boshqa domenga ko'chiring",
			domain, len(used), strings.Join(used, ", "))
	}

	list := s.domains()
	out := make([]string, 0, len(list))
	found := false
	for _, d := range list {
		if d == domain {
			found = true
			continue
		}
		out = append(out, d)
	}
	if !found {
		return s.SetupState(), fmt.Errorf("'%s' ro'yxatda yo'q", domain)
	}
	_ = s.st.SetSetting("base_domains", strings.Join(out, ","))
	if s.st.GetSetting("active_domain", "") == domain {
		next := ""
		if len(out) > 0 {
			next = out[0]
		}
		_ = s.st.SetSetting("active_domain", next)
	}
	applog.Info("Bazaviy domen ro'yxatdan o'chirildi: %s", domain)
	return s.SetupState(), nil
}

func (s *Service) SetActiveDomain(domain string) (SetupState, error) {
	if err := s.ready(); err != nil {
		return s.SetupState(), err
	}
	_ = s.st.SetSetting("active_domain", domain)
	return s.SetupState(), nil
}

// ---- Loyihalar ----

type ProjectView struct {
	store.Project
	URL     string `json:"url"`
	Running bool   `json:"running"`
}

func (s *Service) ListProjects() ([]ProjectView, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	ps, err := s.st.ListProjects()
	if err != nil {
		return nil, err
	}
	out := make([]ProjectView, 0, len(ps))
	for _, p := range ps {
		out = append(out, ProjectView{Project: p, URL: p.URL(), Running: s.mgr.IsRunning(p.ID)})
	}
	return out, nil
}

type ProjectInput struct {
	Name       string `json:"name"`
	Port       int    `json:"port"`
	Subdomain  string `json:"subdomain"`
	BaseDomain string `json:"baseDomain"`
	Protocol   string `json:"protocol"`
	Autostart  bool   `json:"autostart"`
}

var subRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func (s *Service) validate(in *ProjectInput, excludeID string) error {
	in.Name = strings.TrimSpace(in.Name)
	in.Subdomain = strings.ToLower(strings.TrimSpace(in.Subdomain))
	in.BaseDomain = strings.ToLower(strings.TrimSpace(in.BaseDomain))
	if in.Protocol == "" {
		in.Protocol = "http"
	}
	if in.Name == "" {
		return errors.New("loyiha nomini kiriting")
	}
	if in.Port < 1 || in.Port > 65535 {
		return errors.New("port 1 dan 65535 gacha bo'lishi kerak")
	}
	// "@" — domenning o'zi uchun tunnel (subdomensiz). Diqqat: bunga wildcard
	// DNS (*.domen) YETARLI EMAS — domenning o'zi uchun alohida A yozuv ham
	// kerak (masalan domen.uz → VPS IP), aks holda bu loyiha ishlamaydi.
	if in.Subdomain == "@" {
		in.Subdomain = ""
	}
	if in.Subdomain != "" && !subRe.MatchString(in.Subdomain) {
		return errors.New("subdomen faqat kichik harflar, raqamlar va '-' dan iborat bo'lishi kerak (domenning o'zi uchun bo'sh qoldiring yoki '@' yozing)")
	}
	if in.BaseDomain == "" {
		return errors.New("bazaviy domen tanlanmagan")
	}
	found := false
	for _, d := range s.domains() {
		if d == in.BaseDomain {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("'%s' bazaviy domenlar ro'yxatida yo'q — avval qo'shing", in.BaseDomain)
	}
	taken, err := s.st.SubdomainTaken(in.Subdomain, in.BaseDomain, excludeID)
	if err != nil {
		return err
	}
	if taken {
		return fmt.Errorf("'%s' allaqachon boshqa loyihada ishlatilgan", store.HostnameFor(in.Subdomain, in.BaseDomain))
	}
	if s.crossCheck != nil {
		taken, err := s.crossCheck(in.Subdomain, in.BaseDomain)
		if err != nil {
			applog.Warn("Tunnellar (Cloudflare) bo'limidan subdomen tekshiruvi muvaffaqiyatsiz: %v", err)
		} else if taken {
			return fmt.Errorf("'%s' Tunnellar (Cloudflare) bo'limida allaqachon ishlatilgan — boshqa subdomen tanlang",
				store.HostnameFor(in.Subdomain, in.BaseDomain))
		}
	}
	return nil
}

// CheckPort — lokal portda servis borligini tekshiradi.
func (s *Service) CheckPort(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 1500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (s *Service) CreateProject(in ProjectInput) (*ProjectView, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.validate(&in, ""); err != nil {
		return nil, err
	}
	p := store.Project{
		ID:         uuid.NewString(),
		Name:       in.Name,
		Port:       in.Port,
		Subdomain:  in.Subdomain,
		BaseDomain: in.BaseDomain,
		Protocol:   in.Protocol,
		Autostart:  in.Autostart,
		Status:     "stopped",
		CreatedAt:  time.Now(),
	}
	if err := s.st.SaveProject(p); err != nil {
		applog.Error("loyiha bazaga saqlanmadi: %v", err)
		return nil, err
	}
	applog.Info("Loyiha yaratildi: '%s' — localhost:%d → %s", p.Name, p.Port, p.URL())
	return &ProjectView{Project: p, URL: p.URL()}, nil
}

func (s *Service) UpdateProject(id string, in ProjectInput) (*ProjectView, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	p, err := s.st.GetProject(id)
	if err != nil {
		return nil, errors.New("loyiha topilmadi")
	}
	if err := s.validate(&in, id); err != nil {
		return nil, err
	}
	wasRunning := s.mgr.IsRunning(id)
	if wasRunning {
		_ = s.mgr.Stop(id)
	}
	p.Name, p.Port, p.Subdomain, p.BaseDomain, p.Protocol, p.Autostart =
		in.Name, in.Port, in.Subdomain, in.BaseDomain, in.Protocol, in.Autostart
	if err := s.st.SaveProject(p); err != nil {
		return nil, err
	}
	if wasRunning {
		_ = s.mgr.Start(p)
	}
	return &ProjectView{Project: p, URL: p.URL(), Running: s.mgr.IsRunning(id)}, nil
}

func (s *Service) StartProject(id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	p, err := s.st.GetProject(id)
	if err != nil {
		return errors.New("loyiha topilmadi")
	}
	return s.mgr.Start(p)
}

func (s *Service) StopProject(id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.mgr.Stop(id)
}

func (s *Service) RestartProject(id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	p, err := s.st.GetProject(id)
	if err != nil {
		return errors.New("loyiha topilmadi")
	}
	return s.mgr.Restart(p)
}

func (s *Service) DeleteProject(id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	p, err := s.st.GetProject(id)
	if err != nil {
		return errors.New("loyiha topilmadi")
	}
	_ = s.mgr.Stop(id)
	_ = os.Remove(filepath.Join(store.LogDir(), id+".log"))
	if err := s.st.DeleteProject(id); err != nil {
		applog.Error("loyiha bazadan o'chirilmadi: %v", err)
		return err
	}
	applog.Info("Loyiha o'chirildi: '%s'", p.Name)
	return nil
}

func (s *Service) ProjectLogs(id string) []string {
	if s.mgr == nil {
		return []string{}
	}
	return s.mgr.Logs(id)
}
