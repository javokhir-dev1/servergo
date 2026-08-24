// Package tunnel — Cloudflare Tunnel orqali lokal portlarni internetga
// chiqarish bo'limi. LocalGo loyihasidan ko'chirilgan; Wails bindinglari
// o'rniga ServerGo ning HTTP qatlami ishlatiladi.
//
// Wails EventsEmit yo'q, shuning uchun hodisalar halqa buferga yoziladi va
// UI ularni /api/tunnel/events orqali so'rab oladi.
package tunnel

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"servergo/internal/tunnel/applog"
	"servergo/internal/tunnel/cf"
	"servergo/internal/tunnel/manager"
	"servergo/internal/tunnel/store"
)

// maxEvents — halqa bufer sig'imi. UI har 1-2 soniyada so'raydi, shuning uchun
// bu chegara juda uzoq uzilishlarda ham yetarli.
const maxEvents = 500

// Event — UI ga yetkaziladigan hodisa (progress, project_status, project_log…).
type Event struct {
	Seq  int    `json:"seq"`
	Name string `json:"name"`
	Data any    `json:"data"`
	At   string `json:"at"`
}

type Service struct {
	mu        sync.RWMutex
	st        *store.Store
	cf        *cf.Client
	mgr       *manager.Manager
	cfVersion string
	fatalErr  string

	// loginMu — `cloudflared tunnel login` bir vaqtda faqat bitta bo'lishi
	// shart: ikkinchisi birinchisi hali chetga olib qo'ygan cert.pem bilan
	// to'qnashadi va uni yo'qotib qo'yishi mumkin (foydalanuvchi tugmani
	// bir necha marta bossa yuz beradigan holat).
	loginMu sync.Mutex
	// domainLogin — LoginForDomain ishlab turgan payt true. Bu vaqtda
	// standart cert.pem vaqtincha chetga olib qo'yilgan bo'ladi (cloudflared
	// shunday talab qiladi), shuning uchun cf.CertExists() soxta "chiqib
	// ketildi" holatini ko'rsatib qo'yishi mumkin edi — SetupState buni shu
	// bayroq bilan to'g'rilaydi, aks holda UI sozlash sehrgariga qaytib
	// ketadi (foydalanuvchini chalg'itadi, garchi hech narsa buzilmagan
	// bo'lsa ham).
	domainLogin atomic.Bool

	// crossCheck — VPS Tunnel bo'limidan subdomen bandligini so'rash uchun
	// (main.go orqali ulanadi). Ikkala bo'lim mustaqil bazada ishlagani
	// uchun bittasi ikkinchisining subdomenlarini bilmaydi — shu funksiya
	// bo'lmasa, bir xil subdomen ikkalasida ham yaratilib, DNS'da qaysi
	// biri "aniqroq" bo'lsa o'sha jim-jimgina g'olib chiqib qoladi.
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

// New — bo'limni ishga tushiradi. Xatolik bo'lsa ham *Service qaytadi:
// UI "Tunnellar" bo'limida sababni ko'rsatadi, qolgan bo'limlar ishlayveradi.
func New() *Service {
	s := &Service{events: make([]Event, 0, maxEvents)}

	applog.Init(store.LogDir())
	applog.SetEmitter(s.emit)
	applog.Info("Tunnellar bo'limi ishga tushdi (konfiguratsiya: %s)", store.Dir())

	// Dastur oldingi safar (masalan xizmat qayta ishga tushirilgani sabab)
	// LoginForDomain jarayoni o'rtasida to'xtagan bo'lishi mumkin — bu holda
	// cert.pem hali chetga olib qo'yilgan holicha qolgan bo'ladi. Shuni
	// tiklaymiz, aks holda avval ishlagan domen ham "chiqib ketilgan" bo'lib
	// ko'rinaveradi.
	if cf.RecoverInterruptedLogin() {
		applog.Warn("Tugallanmagan Cloudflare login topildi — cert.pem avtomatik tiklandi")
	}

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

	client, ver, err := cf.Find(st.GetSetting("cloudflared_path", ""), store.NeutralConfig())
	if err == nil {
		s.cf = client
		s.cfVersion = ver
	}

	s.mgr = manager.New(s.cf, st, s.emit)
	go s.runAutostart()
	return s
}

// Close — dastur yopilayotganda barcha tunnellarni to'xtatadi.
func (s *Service) Close() {
	if s.mgr != nil {
		applog.Info("Dastur yopilmoqda — tunnellar to'xtatilmoqda")
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
	s.events = append(s.events, Event{
		Seq:  s.seq,
		Name: name,
		Data: payload,
		At:   time.Now().Format(time.RFC3339Nano),
	})
	if len(s.events) > maxEvents {
		s.events = s.events[len(s.events)-maxEvents:]
	}
}

// Events — `since` dan keyingi hodisalar va oxirgi seq.
// since=0 bo'lsa faqat joriy seq qaytadi (UI birinchi so'rovda tarixni olmaydi).
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
		return errors.New("tunnellar bo'limi ishga tushmagan")
	}
	return nil
}

// ---- Loglar ----

func (s *Service) AppLogs() []applog.Entry { return applog.All() }
func (s *Service) ClearAppLogs()           { applog.Clear() }
func (s *Service) LogDir() string          { return store.LogDir() }
func (s *Service) FatalError() string      { return s.fatalErr }

func (s *Service) runAutostart() {
	if s.st == nil || s.cf == nil || !cf.CertExists() {
		return
	}
	time.Sleep(800 * time.Millisecond) // UI tayyor bo'lishini kutish
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
		applog.Info("Avtostart: %d loyiha ishga tushirilmoqda", n)
	}
}

// ---- Sozlash ----

type SetupState struct {
	CloudflaredFound bool     `json:"cloudflaredFound"`
	CloudflaredPath  string   `json:"cloudflaredPath"`
	Version          string   `json:"version"`
	LoggedIn         bool     `json:"loggedIn"`
	Domains          []string `json:"domains"`
	ActiveDomain     string   `json:"activeDomain"`
	Ready            bool     `json:"ready"`
	FatalError       string   `json:"fatalError"`
	LogDir           string   `json:"logDir"`
}

func (s *Service) SetupState() SetupState {
	st := SetupState{Domains: []string{}, FatalError: s.fatalErr, LogDir: store.LogDir()}
	if s.st == nil {
		return st
	}
	if s.cf != nil {
		st.CloudflaredFound = true
		st.CloudflaredPath = s.cf.Path
		st.Version = s.cfVersion
	}
	st.LoggedIn = cf.CertExists() || s.domainLogin.Load()
	st.Domains = s.domains()
	st.ActiveDomain = s.st.GetSetting("active_domain", "")
	if st.ActiveDomain == "" && len(st.Domains) > 0 {
		st.ActiveDomain = st.Domains[0]
	}
	st.Ready = st.CloudflaredFound && st.LoggedIn && st.ActiveDomain != ""
	return st
}

func (s *Service) domains() []string {
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

// DetectCloudflared — foydalanuvchi o'rnatgandan keyin qayta tekshirish.
func (s *Service) DetectCloudflared(customPath string) (SetupState, error) {
	if err := s.ready(); err != nil {
		return s.SetupState(), err
	}
	client, ver, err := cf.Find(customPath, store.NeutralConfig())
	if err != nil {
		return s.SetupState(), errors.New("cloudflared topilmadi. Uni o'rnating: " +
			"https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/")
	}
	s.cf, s.cfVersion = client, ver
	_ = s.st.SetSetting("cloudflared_path", customPath)
	s.mgr.SetClient(client)
	return s.SetupState(), nil
}

// Login — `cloudflared tunnel login`. Brauzer ochiladi, uzoq davom etishi mumkin.
func (s *Service) Login() (SetupState, error) {
	if err := s.ready(); err != nil {
		return s.SetupState(), err
	}
	if s.cf == nil {
		return s.SetupState(), errors.New("avval cloudflared o'rnatilishi kerak")
	}
	if !s.loginMu.TryLock() {
		return s.SetupState(), errors.New(
			"Cloudflare login allaqachon davom etmoqda (boshqa oynada yoki oldingi urinishda) — " +
				"tugmani qayta bosmang, oldingisi tugashini kuting")
	}
	defer s.loginMu.Unlock()
	if err := s.cf.Login(s.onLoginURL); err != nil {
		return s.SetupState(), err
	}
	return s.SetupState(), nil
}

// onLoginURL — cloudflared'ning login havolasi topilishi bilan UI'ga
// jo'natiladi (progress hodisasi orqali, toast sifatida ko'rinadi). Ba'zi
// muhitlarda (masalan RDP sessiya, snap-brauzer) cloudflared brauzerni
// avtomatik ocholmaydi — bu holda foydalanuvchi havolani qo'lda bosishi
// kerak bo'ladi.
func (s *Service) onLoginURL(url string) {
	s.progress("Brauzer avtomatik ochilmasa, shu havolani oching: " + url)
}

var domainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// legacyCertDomain — standart (domenga xos bo'lmagan) ~/.cloudflared/cert.pem
// qaysi domenga tegishli ekanini qaytaradi, "default_cert_domain" sozlamasida
// saqlaydi.
//
// Bu ilova birinchi marta ishga tushirilgan (ServerGo'da domen ro'yxati hali
// bo'sh) paytda AddDomain uni to'g'ridan-to'g'ri belgilaydi. Lekin bu kod
// qo'shilishidan OLDIN domen qo'shib ulgurgan o'rnatishlar uchun ham ishlashi
// kerak — shu holatda ro'yxatdagi ENG BIRINCHI (har doim ro'yxat oxiriga
// qo'shilgani uchun eng ilk qo'shilgan) domenni standart certga tegishli deb
// hisoblaymiz, chunki u — "Cloudflare bilan bog'lanish" qadamida ulangan
// domen.
func (s *Service) legacyCertDomain() string {
	if d := s.st.GetSetting("default_cert_domain", ""); d != "" {
		return d
	}
	if !cf.CertExists() {
		return ""
	}
	list := s.domains()
	if len(list) == 0 {
		return ""
	}
	d := list[0]
	_ = s.st.SetSetting("default_cert_domain", d)
	return d
}

// certPathFor — `domain` uchun to'g'ri sertifikat yo'lini topadi.
//
// Cloudflare hisobingiz bir nechta domenga (zonaga) ega bo'lishi mumkin, lekin
// cloudflared'ning cert.pem'i har doim bitta login paytida tanlangan BITTA
// zonaga tegishli. Shu sabab har bir domen o'z alohida sertifikatiga ega
// bo'lishi kerak (qarang: cf.LoginForDomain) — aks holda `route dns` xato
// bermay, yozuvni noto'g'ri zonaga yozib qo'yadi (Error 1033 ning aslida
// ko'rinmas sababi).
func (s *Service) certPathFor(domain string) (string, error) {
	if cf.DomainAuthorized(domain) {
		return cf.CertPathFor(domain), nil
	}
	if s.legacyCertDomain() == domain {
		return "", nil
	}
	return "", fmt.Errorf(
		"'%s' domeni hali Cloudflare bilan avtorizatsiya qilinmagan. "+
			"Tunnellar bo'limida domenni qayta qo'shing — brauzer ochiladi, o'sha yerda aynan shu domenni tanlang",
		domain)
}

func (s *Service) AddDomain(domain string) (SetupState, error) {
	if err := s.ready(); err != nil {
		return s.SetupState(), err
	}
	if s.cf == nil {
		return s.SetupState(), errors.New("cloudflared topilmadi")
	}
	domain = strings.ToLower(strings.TrimSpace(domain))
	domain = strings.TrimPrefix(strings.TrimPrefix(domain, "https://"), "http://")
	domain = strings.Trim(domain, "/")
	if !domainRe.MatchString(domain) {
		return s.SetupState(), errors.New("domen noto'g'ri formatda (masalan: javohir.uz)")
	}
	listed := false
	for _, d := range s.domains() {
		if d == domain {
			listed = true
			break
		}
	}

	// Avtorizatsiyani ta'minlaymiz — domen ro'yxatda bo'lsa ham (masalan bu
	// kod qo'shilishidan oldin, cloudflared'ga hech narsa so'ralmasdan
	// qo'shilgan bo'lsa), agar hali sertifikati bo'lmasa shu yerda so'raladi.
	if !cf.DomainAuthorized(domain) {
		// Bir vaqtda faqat bitta login: ikkinchisi cert.pem'ni chetga olib
		// qo'ygan birinchisi bilan to'qnashib, uni yo'qotib qo'yishi mumkin.
		if !s.loginMu.TryLock() {
			return s.SetupState(), errors.New(
				"Cloudflare login allaqachon davom etmoqda — tugmani qayta bosmang, oldingisi tugashini kuting")
		}
		s.domainLogin.Store(true)
		err := func() error {
			switch legacy := s.legacyCertDomain(); {
			case legacy == domain:
				// standart cert.pem shu domenga tegishli — hech narsa qilinmaydi.
				return nil
			case legacy == "" && !cf.CertExists():
				// Eng birinchi domen, hali umuman login qilinmagan.
				_, err := s.cf.LoginForDomain(domain, s.onLoginURL)
				return err
			case legacy == "":
				// cert.pem bor, lekin hali hech qaysi domenga bog'lanmagan
				// (ro'yxat bo'sh) — shu domen standart certni meros qiladi.
				_ = s.st.SetSetting("default_cert_domain", domain)
				return nil
			default:
				// Standart cert boshqa domenga tegishli — bu domen uchun
				// alohida login kerak (brauzer ochiladi).
				_, err := s.cf.LoginForDomain(domain, s.onLoginURL)
				return err
			}
		}()
		s.domainLogin.Store(false)
		s.loginMu.Unlock()
		if err != nil {
			return s.SetupState(), err
		}
	}

	if listed {
		_ = s.st.SetSetting("active_domain", domain)
		return s.SetupState(), nil
	}
	list := append(s.domains(), domain)
	_ = s.st.SetSetting("base_domains", strings.Join(list, ","))
	_ = s.st.SetSetting("active_domain", domain)
	return s.SetupState(), nil
}

// RemoveDomain — domenni ServerGo ro'yxatidan olib tashlaydi.
// Cloudflare akkauntingizga tegmaydi — bu faqat lokal ro'yxat.
// Domen loyihalarda ishlatilayotgan bo'lsa rad etiladi: aks holda o'sha
// loyihalarning hostnomi hech qaysi bazaviy domenga bog'lanmay qoladi.
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
	// Faol domen o'chirilgan bo'lsa — qolganlarining birinchisiga o'tamiz.
	if s.st.GetSetting("active_domain", "") == domain {
		next := ""
		if len(out) > 0 {
			next = out[0]
		}
		_ = s.st.SetSetting("active_domain", next)
	}
	if s.st.GetSetting("default_cert_domain", "") == domain {
		_ = s.st.SetSetting("default_cert_domain", "")
	}
	_ = os.Remove(cf.CertPathFor(domain))
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

// ProjectView — UI uchun loyiha (hisoblangan maydonlar bilan).
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
	// "@" — DNS'da apex/root yozuv uchun keng tarqalgan belgi: domenning
	// o'zi uchun tunnel (subdomensiz), masalan https://javakhir.uz.
	if in.Subdomain == "@" {
		in.Subdomain = ""
	}
	if in.Subdomain != "" && !subRe.MatchString(in.Subdomain) {
		return errors.New("subdomen faqat kichik harflar, raqamlar va '-' dan iborat bo'lishi kerak (domenning o'zi uchun bo'sh qoldiring yoki '@' yozing)")
	}
	if in.BaseDomain == "" {
		return errors.New("bazaviy domen tanlanmagan")
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
			applog.Warn("VPS Tunnel bo'limidan subdomen tekshiruvi muvaffaqiyatsiz: %v", err)
		} else if taken {
			return fmt.Errorf("'%s' VPS Tunnel bo'limida allaqachon ishlatilgan — boshqa subdomen tanlang",
				store.HostnameFor(in.Subdomain, in.BaseDomain))
		}
	}
	return nil
}

// CheckPort — lokal portda servis borligini tekshiradi (UI ogohlantirishi uchun).
func (s *Service) CheckPort(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 1500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// ErrDNSTaken — UI shu prefiksni ko'rib "almashtirish" taklifini chiqaradi.
const ErrDNSTaken = "DNS_EXISTS"

// CreateProject — tunnel yaratish + config + DNS.
// overwriteDNS=true bo'lsa mavjud DNS yozuvi almashtiriladi.
func (s *Service) CreateProject(in ProjectInput, overwriteDNS bool) (*ProjectView, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if s.cf == nil {
		return nil, errors.New("cloudflared topilmadi")
	}
	if !cf.CertExists() {
		return nil, errors.New("avval Cloudflare bilan bog'laning")
	}
	if err := s.validate(&in, ""); err != nil {
		return nil, err
	}
	certPath, err := s.certPathFor(in.BaseDomain)
	if err != nil {
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
	subLabel := p.Subdomain
	if subLabel == "" {
		subLabel = "root" // domenning o'zi uchun tunnel — bo'sh subdomen emas, o'qilishi mumkin bo'lgan yorliq
	}
	p.TunnelName = "servergo-" + subLabel + "-" + p.ID[:8]

	s.progress("Tunnel yaratilmoqda…")
	credFile := filepath.Join(store.TunnelDir(p.ID), p.TunnelName+".json")
	tunnelID, err := s.cf.CreateTunnel(p.TunnelName, credFile, certPath)
	if err != nil {
		_ = os.RemoveAll(store.TunnelDir(p.ID))
		return nil, err
	}
	p.TunnelID = tunnelID

	s.progress("Konfiguratsiya yozilmoqda…")
	cfgPath := filepath.Join(store.TunnelDir(p.ID), "config.yml")
	if err := cf.WriteConfig(cfgPath, p.TunnelID, credFile, p.Hostname(), p.Port, p.Protocol); err != nil {
		_ = s.cf.DeleteTunnel(p.TunnelName, certPath)
		_ = os.RemoveAll(store.TunnelDir(p.ID))
		return nil, err
	}

	s.progress("DNS sozlanmoqda…")
	if err := s.cf.RouteDNS(p.TunnelName, p.Hostname(), certPath, overwriteDNS); err != nil {
		_ = s.cf.DeleteTunnel(p.TunnelName, certPath) // rollback
		_ = os.RemoveAll(store.TunnelDir(p.ID))
		if errors.Is(err, cf.ErrDNSExists) {
			return nil, fmt.Errorf("%s|'%s' uchun DNS yozuvi allaqachon mavjud — ehtimol eski tunneldan qolgan",
				ErrDNSTaken, p.Hostname())
		}
		return nil, err
	}

	if err := s.st.SaveProject(p); err != nil {
		applog.Error("loyiha bazaga saqlanmadi: %v", err)
		return nil, err
	}
	s.progress("Tayyor")
	applog.Info("Loyiha yaratildi: '%s' — localhost:%d → %s", p.Name, p.Port, p.URL())
	return &ProjectView{Project: p, URL: p.URL()}, nil
}

// ImportProject — cloudsync uchun: backend'dan kelgan loyihani BERILGAN ID
// va MAVJUD tunnel (tunnelID/tunnelName) bilan yozadi. CreateProject'dan
// farqi — yangi cloudflared tunnel yaratmaydi, DNS ham sozlamaydi (bular
// allaqachon boshqa mashinada qilingan) — faqat metadata yoziladi.
// cloudflared/cert.pem talab qilinmaydi. Loyiha "stopped" holatda qoladi —
// credentials.json/config.yml fayllarini tiklash va ishga tushirish
// chaqiruvchi tomonda (cloudsync.Pull) amalga oshiriladi.
func (s *Service) ImportProject(id string, in ProjectInput, tunnelID, tunnelName string) (*ProjectView, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.validate(&in, id); err != nil {
		return nil, err
	}
	p := store.Project{
		ID:         id,
		Name:       in.Name,
		Port:       in.Port,
		Subdomain:  in.Subdomain,
		BaseDomain: in.BaseDomain,
		Protocol:   in.Protocol,
		TunnelID:   tunnelID,
		TunnelName: tunnelName,
		Autostart:  in.Autostart,
		Status:     "stopped",
		CreatedAt:  time.Now(),
	}
	if err := s.st.SaveProject(p); err != nil {
		return nil, err
	}
	return &ProjectView{Project: p, URL: p.URL()}, nil
}

// UpdateProject — port/subdomen tahriri.
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
	subChanged := in.Subdomain != p.Subdomain || in.BaseDomain != p.BaseDomain

	if wasRunning {
		_ = s.mgr.Stop(id)
	}

	if subChanged {
		if s.cf == nil {
			return nil, errors.New("cloudflared topilmadi")
		}
		certPath, cerr := s.certPathFor(in.BaseDomain)
		if cerr != nil {
			if wasRunning {
				_ = s.mgr.Start(p)
			}
			return nil, cerr
		}
		newHostname := store.HostnameFor(in.Subdomain, in.BaseDomain)
		s.progress("Yangi DNS yozuvi sozlanmoqda…")
		if err := s.cf.RouteDNS(p.TunnelName, newHostname, certPath, false); err != nil {
			if wasRunning {
				_ = s.mgr.Start(p)
			}
			if errors.Is(err, cf.ErrDNSExists) {
				return nil, fmt.Errorf("'%s' uchun DNS yozuvi allaqachon mavjud. Boshqa subdomen tanlang",
					newHostname)
			}
			return nil, err
		}
	}

	p.Name, p.Port, p.Subdomain, p.BaseDomain, p.Protocol, p.Autostart =
		in.Name, in.Port, in.Subdomain, in.BaseDomain, in.Protocol, in.Autostart

	credFile := filepath.Join(store.TunnelDir(p.ID), p.TunnelName+".json")
	cfgPath := filepath.Join(store.TunnelDir(p.ID), "config.yml")
	if err := cf.WriteConfig(cfgPath, p.TunnelID, credFile, p.Hostname(), p.Port, p.Protocol); err != nil {
		return nil, err
	}
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

// DeleteProject — to'liq tozalash. Masofaviy xato bo'lsa ham lokal tozalanadi;
// qaytgan matn bo'sh bo'lmasa — UI da ogohlantirish sifatida ko'rsatiladi.
func (s *Service) DeleteProject(id string) (string, error) {
	if err := s.ready(); err != nil {
		return "", err
	}
	p, err := s.st.GetProject(id)
	if err != nil {
		return "", errors.New("loyiha topilmadi")
	}
	_ = s.mgr.Stop(id)

	warn := ""
	if s.cf != nil && p.TunnelName != "" {
		s.progress("Tunnel o'chirilmoqda…")
		certPath, cerr := s.certPathFor(p.BaseDomain)
		if cerr != nil {
			warn = "Eslatma: Cloudflare tomonida tunnel o'chirilmadi (" + cerr.Error() +
				"). DNS yozuvini panel orqali tekshiring."
		} else if err := s.cf.DeleteTunnel(p.TunnelName, certPath); err != nil {
			warn = "Eslatma: Cloudflare tomonida tunnel to'liq o'chmadi (" + err.Error() +
				"). DNS yozuvini panel orqali tekshiring."
		}
	}
	_ = os.RemoveAll(store.TunnelDir(id))
	_ = os.Remove(filepath.Join(store.LogDir(), id+".log"))
	if err := s.st.DeleteProject(id); err != nil {
		applog.Error("loyiha bazadan o'chirilmadi: %v", err)
		return warn, err
	}
	applog.Info("Loyiha o'chirildi: '%s'", p.Name)
	return warn, nil
}

func (s *Service) ProjectLogs(id string) []string {
	if s.mgr == nil {
		return []string{}
	}
	return s.mgr.Logs(id)
}

// ---- Diagnostika ----

type DiagCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok | warn | fail
	Detail string `json:"detail"`
}

type DiagResult struct {
	Checks  []DiagCheck `json:"checks"`
	Summary string      `json:"summary"`
	CanFix  bool        `json:"canFix"`
}

// Diagnose — "Error 1033" va shunga o'xshash muammolarni aniqlaydi.
func (s *Service) Diagnose(id string) (*DiagResult, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	p, err := s.st.GetProject(id)
	if err != nil {
		return nil, errors.New("loyiha topilmadi")
	}
	applog.Info("Diagnostika boshlandi: '%s' (%s)", p.Name, p.Hostname())

	res := &DiagResult{Checks: []DiagCheck{}}
	add := func(name, status, detail string) {
		res.Checks = append(res.Checks, DiagCheck{Name: name, Status: status, Detail: detail})
	}

	if s.mgr.IsRunning(id) {
		add("cloudflared jarayoni", "ok", "ishlamoqda")
	} else {
		add("cloudflared jarayoni", "fail", "to'xtatilgan — avval Run bosing")
	}

	if s.CheckPort(p.Port) {
		add("Lokal servis", "ok", fmt.Sprintf("localhost:%d javob bermoqda", p.Port))
	} else {
		add("Lokal servis", "warn", fmt.Sprintf("localhost:%d javob bermayapti", p.Port))
	}

	credFile := filepath.Join(store.TunnelDir(p.ID), p.TunnelName+".json")
	realID, cerr := cf.TunnelIDFromCreds(credFile)
	if cerr != nil {
		add("Tunnel credentials", "fail", "o'qib bo'lmadi: "+cerr.Error())
	} else if realID != p.TunnelID {
		add("Tunnel UUID", "warn", fmt.Sprintf("bazada %s, credentials faylida %s — baza yangilandi", p.TunnelID, realID))
		p.TunnelID = realID
		_ = s.st.SaveProject(p)
	} else {
		add("Tunnel UUID", "ok", realID)
	}
	if realID == "" {
		realID = p.TunnelID
	}

	expected := realID + ".cfargotunnel.com"
	cname, derr := cf.LookupCNAME(p.Hostname())
	switch {
	case derr != nil:
		add("DNS yozuvi", "fail", "topilmadi: "+derr.Error())
		res.CanFix = true
	case cname == p.Hostname():
		add("DNS yozuvi", "fail", "CNAME yo'q (A/AAAA yozuv yoki proxy)")
		res.CanFix = true
	case strings.EqualFold(cname, expected):
		add("DNS yozuvi", "ok", cname)
	case strings.HasSuffix(cname, ".cfargotunnel.com"):
		add("DNS yozuvi", "fail", fmt.Sprintf("boshqa tunnelga ko'rsatmoqda:\nhozir:  %s\nkerak:  %s", cname, expected))
		res.CanFix = true
	default:
		add("DNS yozuvi", "fail", "tunnelga ko'rsatmayapti: "+cname)
		res.CanFix = true
	}

	fails := 0
	for _, c := range res.Checks {
		if c.Status == "fail" {
			fails++
		}
	}
	switch {
	case fails == 0:
		res.Summary = "Hammasi joyida. Sayt ochilmasa — DNS tarqalishini 1-2 daqiqa kuting."
	case res.CanFix:
		res.Summary = "DNS yozuvi noto'g'ri tunnelga ko'rsatmoqda — bu Error 1033 ning sababi. " +
			"\"DNS'ni tuzatish\" tugmasi yozuvni shu loyihaning tunneliga qayta yo'naltiradi."
	default:
		res.Summary = "Muammo topildi — quyidagi tekshiruvlarga qarang."
	}
	applog.Info("Diagnostika yakunlandi: %d ta muammo", fails)
	return res, nil
}

// FixDNS — DNS yozuvini shu loyihaning tunneliga majburan qayta yo'naltiradi.
func (s *Service) FixDNS(id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	p, err := s.st.GetProject(id)
	if err != nil {
		return errors.New("loyiha topilmadi")
	}
	if s.cf == nil {
		return errors.New("cloudflared topilmadi")
	}
	certPath, err := s.certPathFor(p.BaseDomain)
	if err != nil {
		return err
	}
	applog.Info("DNS yozuvi majburan yangilanmoqda: %s", p.Hostname())
	return s.cf.RouteDNS(p.TunnelName, p.Hostname(), certPath, true)
}

func (s *Service) progress(msg string) { s.emit("progress", msg) }
