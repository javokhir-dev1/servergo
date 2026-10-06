// Package apps — "Ilovalar" bo'limi: foydalanuvchi buyruqlarini (masalan
// `node server.js`) pm2'ga bog'liq bo'lmagan holda boshqarish. Har bir ilova
// ServerGo'ning o'z bazasida saqlanadi va "Avtostart" belgilangan bo'lsa,
// ServerGo demoni ko'tarilganda o'zi ishga tushiriladi — `pm2 save`ni esdan
// chiqarish muammosi butunlay yo'q.
package apps

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"servergo/internal/apps/manager"
	"servergo/internal/apps/sandbox"
	"servergo/internal/apps/store"
)

const maxEvents = 500

// Event — UI ga yetkaziladigan hodisa (app_status, app_log).
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

	events []Event
	seq    int
}

// New — bo'limni ishga tushiradi. Xatolik bo'lsa ham *Service qaytadi: UI
// sababni ko'rsatadi, qolgan bo'limlar ishlayveradi.
func New() *Service {
	s := &Service{events: make([]Event, 0, maxEvents)}

	st, err := store.Open()
	if err != nil {
		s.fatalErr = "Ilovalar bazasini ochib bo'lmadi: " + err.Error()
		return s
	}
	s.st = st
	_ = st.ResetRunningStatuses() // oldingi sessiyadan qolgan holatlarni tozalash — kritik emas

	s.mgr = manager.New(st, s.emit)
	go s.runAutostart()
	return s
}

func (s *Service) Close() {
	if s.mgr != nil {
		s.mgr.StopAll()
	}
	if s.st != nil {
		_ = s.st.Close()
	}
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

// Events — `since`dan keyingi hodisalar va oxirgi seq.
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
		return errors.New("Ilovalar bo'limi ishga tushmagan")
	}
	return nil
}

func (s *Service) FatalError() string { return s.fatalErr }

func (s *Service) runAutostart() {
	time.Sleep(800 * time.Millisecond) // UI tayyor bo'lishini kutish
	apps, err := s.st.ListApps()
	if err != nil {
		return
	}
	for _, a := range apps {
		if a.Autostart {
			_ = s.mgr.Start(a)
		}
	}
}

// AppView — ro'yxat/javoblarda qaytariladigan ko'rinish.
type AppView struct {
	store.App
	Running bool `json:"running"`
}

func (s *Service) ListApps() ([]AppView, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	list, err := s.st.ListApps()
	if err != nil {
		return nil, err
	}
	out := make([]AppView, 0, len(list))
	for _, a := range list {
		out = append(out, AppView{App: a, Running: s.mgr.IsRunning(a.ID)})
	}
	return out, nil
}

// AppInput — yaratish/tahrirlash uchun kiritma.
type AppInput struct {
	Name      string `json:"name"`
	Command   string `json:"command"`
	Cwd       string `json:"cwd"`
	Autostart bool   `json:"autostart"`
	// Sandbox — ilovani bubblewrap ichida ishga tushirish (uy papkasi
	// yopiladi, faqat o'z papkasi ko'rinadi).
	Sandbox bool `json:"sandbox"`
	// SandboxRO — faqat o'qish uchun qo'shimcha yo'llar (monorepo
	// node_modules, site-packages, umumiy .env...).
	SandboxRO []string `json:"sandboxRo"`
	// SandboxRW — yozish ham mumkin bo'lgan qo'shimcha yo'llar.
	SandboxRW []string `json:"sandboxRw"`
	// NetIsolate — alohida tarmoq namespace (hostning localhost'i yopiladi).
	NetIsolate bool `json:"netIsolate"`
	// NetHostPorts — izolyatsiyada ichkaridan ko'rinadigan host portlari.
	NetHostPorts []int `json:"netHostPorts"`
}

// SandboxStatus — UI/CLI uchun: bu tizimda sandbox va tarmoq izolyatsiyasi
// ishlaydimi (ikkinchisi alohida paketga — passt'ga tayanadi).
type SandboxStatus struct {
	Available    bool   `json:"available"`
	Reason       string `json:"reason"`
	NetAvailable bool   `json:"netAvailable"`
	NetReason    string `json:"netReason"`
}

func (s *Service) SandboxStatus() SandboxStatus {
	out := SandboxStatus{Available: true}
	if err := sandbox.Available(); err != nil {
		out.Available, out.Reason = false, err.Error()
	}
	if err := sandbox.NetAvailable(); err != nil {
		out.NetReason = err.Error()
	} else {
		out.NetAvailable = true
	}
	return out
}

// cleanPaths — bo'sh qatorlarni va bo'shliqlarni tozalaydi.
func cleanPaths(list []string) []string {
	out := make([]string, 0, len(list))
	for _, p := range list {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

var nameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9 ._-]{0,63}$`)

// validate — kiritmani tekshiradi. checkPaths=false bo'lsa sandbox
// yo'llarining mavjudligi tekshirilmaydi: bulutdan tiklanayotgan ilovaning
// papkasi bu mashinada hali bo'lmasligi mumkin, lekin sozlamani yo'qotib
// yuborish (ya'ni ilovani jim izolyatsiyasiz qoldirish) xavfliroq.
func (s *Service) validate(in *AppInput, excludeID string, checkPaths bool) error {
	in.Name = strings.TrimSpace(in.Name)
	in.Command = strings.TrimSpace(in.Command)
	in.Cwd = strings.TrimSpace(in.Cwd)
	if !nameRe.MatchString(in.Name) {
		return errors.New("nom noto'g'ri (harf/raqam bilan boshlanishi, 64 belgigacha)")
	}
	if in.Command == "" {
		return errors.New("buyruq bo'sh bo'lmasligi kerak")
	}
	in.SandboxRO = cleanPaths(in.SandboxRO)
	in.SandboxRW = cleanPaths(in.SandboxRW)
	if in.Sandbox {
		if in.Cwd == "" {
			return errors.New("sandbox yoqilgan bo'lsa ishchi papka ko'rsatilishi shart — ilova aynan shu papkani ko'radi")
		}
		if checkPaths {
			if err := sandbox.Available(); err != nil {
				return errors.New("sandbox ishlamaydi: " + err.Error())
			}
			spec := sandbox.Spec{Cwd: in.Cwd, RO: in.SandboxRO, RW: in.SandboxRW}
			if in.NetIsolate {
				spec.Net = &sandbox.NetSpec{HostPorts: in.NetHostPorts}
			}
			if err := sandbox.Validate(spec); err != nil {
				return err
			}
		}
	} else if len(in.SandboxRW) > 0 || len(in.SandboxRO) > 0 {
		return errors.New("qo'shimcha yo'llar faqat sandbox yoqilganda ma'noga ega")
	} else if in.NetIsolate {
		// Tarmoq izolyatsiyasi sandbox'ning bir qismi: alohida tarmoq
		// namespace'i bwrap yaratgan muhit ichida beriladi.
		return errors.New("tarmoq izolyatsiyasi uchun sandbox yoqilishi shart")
	}
	taken, err := s.st.NameTaken(in.Name, excludeID)
	if err != nil {
		return err
	}
	if taken {
		return fmt.Errorf("'%s' nomi allaqachon ishlatilgan", in.Name)
	}
	return nil
}

func (s *Service) CreateApp(in AppInput) (*AppView, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.validate(&in, "", true); err != nil {
		return nil, err
	}
	a := store.App{
		ID:           uuid.NewString(),
		Name:         in.Name,
		Command:      in.Command,
		Cwd:          in.Cwd,
		Autostart:    in.Autostart,
		Sandbox:      in.Sandbox,
		SandboxRO:    in.SandboxRO,
		SandboxRW:    in.SandboxRW,
		NetIsolate:   in.NetIsolate,
		NetHostPorts: in.NetHostPorts,
		Status:       "stopped",
		CreatedAt:    time.Now(),
	}
	if err := s.st.SaveApp(a); err != nil {
		return nil, err
	}
	return &AppView{App: a}, nil
}

// ImportApp — cloudsync uchun: backend'dan kelgan ilovani BERILGAN ID bilan
// yozadi (CreateApp'dan farqi — yangi UUID generatsiya qilmaydi). Shu ID'li
// yozuv allaqachon mavjud bo'lsa, yangilanadi (idempotent — qayta sync
// qilinsa xavfsiz).
func (s *Service) ImportApp(id string, in AppInput) (*AppView, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.validate(&in, id, false); err != nil {
		return nil, err
	}
	a := store.App{
		ID:           id,
		Name:         in.Name,
		Command:      in.Command,
		Cwd:          in.Cwd,
		Autostart:    in.Autostart,
		Sandbox:      in.Sandbox,
		SandboxRO:    in.SandboxRO,
		SandboxRW:    in.SandboxRW,
		NetIsolate:   in.NetIsolate,
		NetHostPorts: in.NetHostPorts,
		Status:       "stopped",
		CreatedAt:    time.Now(),
	}
	if err := s.st.SaveApp(a); err != nil {
		return nil, err
	}
	return &AppView{App: a}, nil
}

func (s *Service) UpdateApp(id string, in AppInput) (*AppView, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	a, err := s.st.GetApp(id)
	if err != nil {
		return nil, errors.New("ilova topilmadi")
	}
	if err := s.validate(&in, id, true); err != nil {
		return nil, err
	}
	wasRunning := s.mgr.IsRunning(id)
	if wasRunning {
		_ = s.mgr.Stop(id)
	}
	a.Name, a.Command, a.Cwd, a.Autostart = in.Name, in.Command, in.Cwd, in.Autostart
	a.Sandbox, a.SandboxRO, a.SandboxRW = in.Sandbox, in.SandboxRO, in.SandboxRW
	a.NetIsolate, a.NetHostPorts = in.NetIsolate, in.NetHostPorts
	if err := s.st.SaveApp(a); err != nil {
		return nil, err
	}
	if wasRunning {
		_ = s.mgr.Start(a)
	}
	return &AppView{App: a, Running: s.mgr.IsRunning(id)}, nil
}

func (s *Service) DeleteApp(id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	_ = s.mgr.Stop(id)
	return s.st.DeleteApp(id)
}

func (s *Service) StartApp(id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	a, err := s.st.GetApp(id)
	if err != nil {
		return errors.New("ilova topilmadi")
	}
	return s.mgr.Start(a)
}

func (s *Service) StopApp(id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.mgr.Stop(id)
}

func (s *Service) RestartApp(id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	a, err := s.st.GetApp(id)
	if err != nil {
		return errors.New("ilova topilmadi")
	}
	return s.mgr.Restart(a)
}

func (s *Service) AppLogs(id string) []string {
	if s.mgr == nil {
		return []string{}
	}
	return s.mgr.Logs(id)
}
