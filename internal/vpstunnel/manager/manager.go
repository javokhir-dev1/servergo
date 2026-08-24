// Package manager — VPS Tunnel loyihalarining hayot davri: ulanish/uzish,
// avto-qayta-ulanish, health-check va loglar. internal/tunnel/manager bilan
// bir xil naqsh, lekin cloudflared subprocess o'rniga
// internal/vpstunnel/client.Run goroutine'ini boshqaradi.
package manager

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"servergo/internal/vpstunnel/applog"
	"servergo/internal/vpstunnel/client"
	"servergo/internal/vpstunnel/store"
)

const (
	maxRestarts  = 3
	healthPeriod = 10 * time.Second
	maxRingLines = 500
	maxLogFileMB = 10
)

// EmitFunc — UI'ga hodisa yuborish.
type EmitFunc func(event string, data ...interface{})

// RelayConfig — control ulanish parametrlari (Service.SetRelayConfig orqali
// yangilanadi).
type RelayConfig struct {
	Addr        string // host:port
	Token       string
	Fingerprint string
}

func (c RelayConfig) ready() bool {
	return c.Addr != "" && c.Token != "" && c.Fingerprint != ""
}

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
	cancel   context.CancelFunc
	desired  bool
	restarts int
	logs     *ring
	logFile  *os.File
	done     chan struct{}
}

type Manager struct {
	mu     sync.Mutex
	procs  map[string]*proc
	portOK map[string]bool
	relay  RelayConfig
	st     *store.Store
	emit   EmitFunc
	quit   chan struct{}
}

func New(st *store.Store, emit EmitFunc) *Manager {
	m := &Manager{
		procs:  map[string]*proc{},
		portOK: map[string]bool{},
		st:     st,
		emit:   emit,
		quit:   make(chan struct{}),
	}
	go m.healthLoop()
	return m
}

func (m *Manager) SetRelayConfig(cfg RelayConfig) {
	m.mu.Lock()
	m.relay = cfg
	m.mu.Unlock()
}

func (m *Manager) setStatus(id, status, lastErr string) {
	_ = m.st.SetStatus(id, status, lastErr)
	if lastErr != "" {
		applog.Error("[%s] %s: %s", short(id), status, lastErr)
	} else {
		applog.Info("[%s] status: %s", short(id), status)
	}
	m.emit("vt_project_status", map[string]string{"id": id, "status": status, "error": lastErr})
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// Start — loyihani ishga tushiradi (relay bilan ulanadi).
func (m *Manager) Start(p store.Project) error {
	m.mu.Lock()
	relay := m.relay
	if pr, ok := m.procs[p.ID]; ok && pr.desired {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()
	if !relay.ready() {
		return fmt.Errorf("relay sozlanmagan — avval manzil/token/fingerprint kiriting")
	}
	applog.Info("[%s] '%s' ulanmoqda (localhost:%d → %s)", short(p.ID), p.Name, p.Port, p.Hostname())
	return m.spawn(p, relay, 0)
}

func (m *Manager) spawn(p store.Project, relay RelayConfig, restarts int) error {
	ctx, cancel := context.WithCancel(context.Background())

	logPath := filepath.Join(store.LogDir(), p.ID+".log")
	rotateIfBig(logPath)
	lf, _ := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)

	pr := &proc{
		cancel:   cancel,
		desired:  true,
		restarts: restarts,
		logs:     &ring{},
		logFile:  lf,
		done:     make(chan struct{}),
	}
	m.mu.Lock()
	m.procs[p.ID] = pr
	m.mu.Unlock()
	m.setStatus(p.ID, "starting", "")

	logLine := func(line string) {
		pr.logs.add(line)
		if pr.logFile != nil {
			fmt.Fprintln(pr.logFile, line)
		}
		m.emit("vt_project_log", map[string]string{"id": p.ID, "line": line})
	}

	cfg := client.Config{
		RelayAddr:   relay.Addr,
		Fingerprint: relay.Fingerprint,
		Token:       relay.Token,
		ProjectID:   p.ID,
		Hostname:    p.Hostname(),
		LocalPort:   p.Port,
	}

	go func() {
		defer close(pr.done)
		defer func() {
			if pr.logFile != nil {
				pr.logFile.Close()
			}
		}()

		err := client.Run(ctx, cfg,
			func() { pr.restarts = 0; m.setStatus(p.ID, "running", "") },
			logLine,
		)

		m.mu.Lock()
		wanted := pr.desired
		delete(m.procs, p.ID)
		m.mu.Unlock()

		if !wanted {
			m.setStatus(p.ID, "stopped", "")
			return
		}
		if pr.restarts < maxRestarts {
			wait := time.Duration(1<<pr.restarts) * time.Second
			applog.Warn("[%s] ulanish uzildi, %s dan so'ng qayta urinish (%d/%d)",
				short(p.ID), wait, pr.restarts+1, maxRestarts)
			logLine(fmt.Sprintf("[ServerGo] ulanish uzildi, %s dan so'ng qayta urinish (%d/%d)", wait, pr.restarts+1, maxRestarts))
			time.Sleep(wait)
			if fresh, gerr := m.st.GetProject(p.ID); gerr == nil {
				m.mu.Lock()
				relay := m.relay
				m.mu.Unlock()
				_ = m.spawn(fresh, relay, pr.restarts+1)
				return
			}
		}
		msg := "ulanish uzildi"
		if err != nil {
			msg += ": " + err.Error()
		}
		m.setStatus(p.ID, "error", msg)
	}()

	return nil
}

// Stop — ulanishni yopadi.
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
	pr.cancel()
	select {
	case <-pr.done:
	case <-time.After(15 * time.Second):
	}
	return nil
}

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

func (m *Manager) IsRunning(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	pr, ok := m.procs[id]
	return ok && pr.desired
}

func (m *Manager) Logs(id string) []string {
	m.mu.Lock()
	pr, ok := m.procs[id]
	m.mu.Unlock()
	if ok {
		return pr.logs.all()
	}
	data, err := os.ReadFile(filepath.Join(store.LogDir(), id+".log"))
	if err != nil {
		return []string{}
	}
	lines := splitLines(string(data))
	if len(lines) > maxRingLines {
		lines = lines[len(lines)-maxRingLines:]
	}
	return lines
}

func splitLines(s string) []string {
	if s == "" {
		return []string{}
	}
	var out []string
	start := 0
	for i, c := range s {
		if c == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// ---- Health check ----

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
				applog.Warn("[%s] localhost:%d javob bermayapti (ulanish tirik)", short(id), p.Port)
			}
		}
		m.emit("vt_project_health", map[string]interface{}{"id": id, "portOk": portOK})
	}
}

func rotateIfBig(path string) {
	st, err := os.Stat(path)
	if err != nil {
		return
	}
	if st.Size() > maxLogFileMB*1024*1024 {
		_ = os.Rename(path, path+".old")
	}
}
