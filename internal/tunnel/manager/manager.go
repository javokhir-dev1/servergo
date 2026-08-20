// Package manager — cloudflared jarayonlarini boshqarish: start/stop/restart,
// avto-restart, health-check va loglar (TZ 4.3, 4.4).
package manager

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"servergo/internal/tunnel/applog"
	"servergo/internal/tunnel/cf"
	"servergo/internal/tunnel/store"
)

const (
	maxRestarts  = 3                // FR-4.3
	stopTimeout  = 10 * time.Second // FR-3.2
	startTimeout = 15 * time.Second // FR-3.1
	healthPeriod = 10 * time.Second // FR-4.2
	maxRingLines = 500
	maxLogFileMB = 10
)

// EmitFunc — UI'ga hodisa yuborish (Wails EventsEmit bilan bog'lanadi).
type EmitFunc func(event string, data ...interface{})

type ring struct {
	mu    sync.Mutex
	lines []string
}

func (r *ring) add(l string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, l)
	if len(r.lines) > maxRingLines {
		r.lines = r.lines[len(r.lines)-maxRingLines:]
	}
}

func (r *ring) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.lines))
	copy(out, r.lines)
	return out
}

type proc struct {
	cmd      *exec.Cmd
	desired  bool // foydalanuvchi "ishlashini" xohlaydimi
	restarts int
	logs     *ring
	logFile  *os.File
	done     chan struct{}
}

type Manager struct {
	mu     sync.Mutex
	procs  map[string]*proc
	portOK map[string]bool // health-check holati (o'zgarganda loglash uchun)
	issues map[string]bool // "<id>:<xato-turi>" — takroriy maslahatlarni bostirish
	cf     *cf.Client
	st     *store.Store
	emit   EmitFunc
	quit   chan struct{}
}

func New(client *cf.Client, st *store.Store, emit EmitFunc) *Manager {
	m := &Manager{
		procs:  map[string]*proc{},
		portOK: map[string]bool{},
		issues: map[string]bool{},
		cf:     client,
		st:     st,
		emit:   emit,
		quit:   make(chan struct{}),
	}
	go m.healthLoop()
	return m
}

func (m *Manager) SetClient(c *cf.Client) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cf = c
}

// ---- Status ----

func (m *Manager) setStatus(id, status, lastErr string) {
	_ = m.st.SetStatus(id, status, lastErr)
	if lastErr != "" {
		applog.Error("[%s] %s: %s", short(id), status, lastErr)
	} else {
		applog.Info("[%s] status: %s", short(id), status)
	}
	m.emit("project_status", map[string]string{"id": id, "status": status, "error": lastErr})
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// ---- Start / Stop ----

// Start — loyihani ishga tushiradi (FR-3.1).
func (m *Manager) Start(p store.Project) error {
	m.mu.Lock()
	if m.cf == nil {
		m.mu.Unlock()
		return fmt.Errorf("cloudflared topilmadi — sozlamalarni tekshiring")
	}
	if pr, ok := m.procs[p.ID]; ok && pr.desired {
		m.mu.Unlock()
		return nil // allaqachon ishlamoqda
	}
	m.mu.Unlock()

	cfgPath := filepath.Join(store.TunnelDir(p.ID), "config.yml")
	if _, err := os.Stat(cfgPath); err != nil {
		applog.Error("[%s] config.yml topilmadi: %s", short(p.ID), cfgPath)
		return fmt.Errorf("config.yml topilmadi — loyihani qayta yarating")
	}
	applog.Info("[%s] '%s' ishga tushirilmoqda (localhost:%d → %s)", short(p.ID), p.Name, p.Port, p.Hostname())
	return m.spawn(p, cfgPath, 0)
}

func (m *Manager) spawn(p store.Project, cfgPath string, restarts int) error {
	m.mu.Lock()
	client := m.cf
	m.mu.Unlock()
	if client == nil {
		return fmt.Errorf("cloudflared topilmadi")
	}

	// --config subkomandadan OLDIN beriladi — global bayroq sifatida u
	// ~/.cloudflared/config.yml ni ishonchli tarzda almashtiradi.
	runArgs := []string{"--config", cfgPath, "tunnel", "run", p.TunnelName}
	cmd := exec.Command(client.Path, runArgs...)
	setupProcAttr(cmd) // platformaga xos (process_unix.go / process_windows.go)

	// cloudflared loglarni asosan stderr'ga yozadi — ikkalasini ham o'qiymiz.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	logPath := filepath.Join(store.LogDir(), p.ID+".log")
	rotateIfBig(logPath)
	lf, _ := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)

	pr := &proc{
		cmd:      cmd,
		desired:  true,
		restarts: restarts,
		logs:     &ring{},
		logFile:  lf,
		done:     make(chan struct{}),
	}

	applog.Cmd(client.Path, runArgs)
	if err := cmd.Start(); err != nil {
		if lf != nil {
			lf.Close()
		}
		m.setStatus(p.ID, "error", "cloudflared ishga tushmadi: "+err.Error())
		return err
	}

	m.mu.Lock()
	m.procs[p.ID] = pr
	m.mu.Unlock()
	m.setStatus(p.ID, "starting", "")

	registered := make(chan struct{}, 1)
	readPipe := func(rd interface{ Read([]byte) (int, error) }) {
		sc := bufio.NewScanner(rd)
		sc.Buffer(make([]byte, 64*1024), 64*1024)
		for sc.Scan() {
			line := sc.Text()
			pr.logs.add(line)
			if pr.logFile != nil {
				fmt.Fprintln(pr.logFile, line)
			}
			m.emit("project_log", map[string]string{"id": p.ID, "line": line})
			if strings.Contains(line, "Registered tunnel connection") {
				select {
				case registered <- struct{}{}:
				default:
				}
			}
			m.detectCommonIssue(p, line)
		}
	}
	go readPipe(stdout)
	go readPipe(stderr)

	// FR-3.1: 15 soniyada ulanish tasdig'ini kutish
	go func() {
		select {
		case <-registered:
			m.setStatus(p.ID, "running", "")
		case <-time.After(startTimeout):
			m.mu.Lock()
			alive := pr.desired
			m.mu.Unlock()
			if alive {
				m.setStatus(p.ID, "error", "15 soniyada ulanish tasdiqlanmadi — loglarni tekshiring")
			}
		case <-pr.done:
		}
	}()

	// Jarayon tugashini kuzatish + avto-restart (FR-4.3)
	go func() {
		err := cmd.Wait()
		close(pr.done)
		if pr.logFile != nil {
			pr.logFile.Close()
		}
		m.mu.Lock()
		wanted := pr.desired
		delete(m.procs, p.ID)
		m.mu.Unlock()

		if !wanted {
			m.setStatus(p.ID, "stopped", "")
			return
		}
		// Kutilmagan tugash
		if pr.restarts < maxRestarts {
			wait := time.Duration(1<<pr.restarts) * time.Second // 1s, 2s, 4s
			applog.Warn("[%s] cloudflared kutilmaganda to'xtadi, %s dan so'ng qayta urinish (%d/%d)",
				short(p.ID), wait, pr.restarts+1, maxRestarts)
			m.emit("project_log", map[string]string{
				"id":   p.ID,
				"line": fmt.Sprintf("[ServerGo] cloudflared to'xtadi, %s dan so'ng qayta urinish (%d/%d)", wait, pr.restarts+1, maxRestarts),
			})
			time.Sleep(wait)
			if fresh, gerr := m.st.GetProject(p.ID); gerr == nil {
				_ = m.spawn(fresh, cfgPath, pr.restarts+1)
				return
			}
		}
		msg := "cloudflared kutilmaganda to'xtadi"
		if err != nil {
			msg += ": " + err.Error()
		}
		m.setStatus(p.ID, "error", msg)
	}()

	return nil
}

// Stop — SIGTERM, 10 soniyadan so'ng SIGKILL (FR-3.2).
func (m *Manager) Stop(id string) error {
	m.mu.Lock()
	pr, ok := m.procs[id]
	if ok {
		pr.desired = false
	}
	m.mu.Unlock()
	if !ok {
		m.setStatus(id, "stopped", "")
		return nil
	}

	_ = terminate(pr.cmd)
	select {
	case <-pr.done:
	case <-time.After(stopTimeout):
		applog.Warn("[%s] %s ichida to'xtamadi — majburiy yopilmoqda", short(id), stopTimeout)
		_ = forceKill(pr.cmd)
		select {
		case <-pr.done:
		case <-time.After(3 * time.Second):
		}
	}
	return nil
}

// Restart (FR-3.3).
func (m *Manager) Restart(p store.Project) error {
	if err := m.Stop(p.ID); err != nil {
		return err
	}
	return m.Start(p)
}

// StopAll — dastur yopilishida.
func (m *Manager) StopAll() {
	close(m.quit)
	m.mu.Lock()
	ids := make([]string, 0, len(m.procs))
	for id := range m.procs {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) { defer wg.Done(); _ = m.Stop(id) }(id)
	}
	wg.Wait()
}

// IsRunning — jarayon boshqaruvda va ishlashi kutilmoqda.
func (m *Manager) IsRunning(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	pr, ok := m.procs[id]
	return ok && pr.desired
}

// Logs — oxirgi log qatorlari (FR-4.4).
func (m *Manager) Logs(id string) []string {
	m.mu.Lock()
	pr, ok := m.procs[id]
	m.mu.Unlock()
	if ok {
		return pr.logs.all()
	}
	// Jarayon yo'q — fayldan oxirgi qismini o'qish
	data, err := os.ReadFile(filepath.Join(store.LogDir(), id+".log"))
	if err != nil {
		return []string{}
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > maxRingLines {
		lines = lines[len(lines)-maxRingLines:]
	}
	return lines
}

// ---- Keng tarqalgan xatolarni tanib olish ----

// detectCommonIssue — cloudflared log qatoridan tushunarsiz texnik xatolarni
// aniqlab, foydalanuvchiga aniq yechim taklif qiladi. Har xato turi bir marta.
func (m *Manager) detectCommonIssue(p store.Project, line string) {
	var key, hint string

	switch {
	case strings.Contains(line, "does not look like a TLS handshake"):
		key = "tls"
		hint = fmt.Sprintf("'%s' loyihasi HTTPS bilan sozlangan, lekin localhost:%d oddiy HTTP javob bermoqda. "+
			"Tahrir → Protokol → http qilib o'zgartiring.", p.Name, p.Port)

	case strings.Contains(line, "connection refused"):
		key = "refused"
		hint = fmt.Sprintf("localhost:%d ulanishni rad etdi — '%s' loyihasining lokal serveri ishga tushganini tekshiring.",
			p.Port, p.Name)

	case strings.Contains(line, "Unable to reach the origin service"):
		key = "origin"
		hint = fmt.Sprintf("cloudflared localhost:%d ga yetib bora olmadi ('%s').", p.Port, p.Name)
	}

	if key == "" {
		return
	}
	m.mu.Lock()
	seen := m.issues[p.ID+":"+key]
	m.issues[p.ID+":"+key] = true
	m.mu.Unlock()
	if seen {
		return
	}

	applog.Warn("%s", hint)
	m.emit("project_hint", map[string]string{"id": p.ID, "hint": hint})
}

// ---- Health check (FR-4.2) ----

func (m *Manager) healthLoop() {
	t := time.NewTicker(healthPeriod)
	defer t.Stop()
	for {
		select {
		case <-m.quit:
			return
		case <-t.C:
			m.checkHealth()
		}
	}
}

func (m *Manager) checkHealth() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.procs))
	for id, pr := range m.procs {
		if pr.desired {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()

	for _, id := range ids {
		p, err := m.st.GetProject(id)
		if err != nil {
			continue
		}
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p.Port), 2*time.Second)
		portOK := err == nil
		if conn != nil {
			conn.Close()
		}
		m.mu.Lock()
		prev, seen := m.portOK[id]
		m.portOK[id] = portOK
		m.mu.Unlock()
		if !seen || prev != portOK {
			if portOK {
				applog.Info("[%s] localhost:%d javob bermoqda", short(id), p.Port)
			} else {
				applog.Warn("[%s] localhost:%d javob bermayapti (tunnel tirik)", short(id), p.Port)
			}
		}
		m.emit("project_health", map[string]interface{}{"id": id, "portOk": portOK})
	}
}

// rotateIfBig — sodda log rotatsiyasi (10 MB dan katta bo'lsa .old ga ko'chiriladi).
func rotateIfBig(path string) {
	st, err := os.Stat(path)
	if err != nil {
		return
	}
	if st.Size() > maxLogFileMB*1024*1024 {
		_ = os.Rename(path, path+".old")
	}
}
