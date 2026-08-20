// Package manager — ilovalarni (foydalanuvchi buyruqlarini) boshqarish:
// start/stop/restart, qulab tushsa avto-restart, loglar. internal/tunnel/manager
// bilan bir xil naqsh, lekin cloudflared/DNS bilan bog'liq emas — istalgan
// buyruq (`node server.js`, `python3 bot.py`...) uchun umumiy.
package manager

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"servergo/internal/apps/store"
)

const (
	maxRestarts  = 3                       // ketma-ket avto-restart chegarasi
	stopTimeout  = 10 * time.Second        // SIGTERM dan SIGKILL gacha
	aliveGrace   = 1500 * time.Millisecond // shuncha vaqt tirik qolsa "running" deb hisoblanadi
	maxRingLines = 500
	maxLogFileMB = 10
)

// EmitFunc — UI'ga hodisa yuborish (tunnel.Service.emit bilan bir xil shakl).
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
	mu    sync.Mutex
	procs map[string]*proc
	st    *store.Store
	emit  EmitFunc
}

func New(st *store.Store, emit EmitFunc) *Manager {
	return &Manager{
		procs: map[string]*proc{},
		st:    st,
		emit:  emit,
	}
}

func (m *Manager) setStatus(id, status, lastErr string) {
	_ = m.st.SetStatus(id, status, lastErr)
	if lastErr != "" {
		log.Printf("[apps][%s] %s: %s", short(id), status, lastErr)
	}
	m.emit("app_status", map[string]string{"id": id, "status": status, "error": lastErr})
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// Start — ilovani ishga tushiradi.
func (m *Manager) Start(a store.App) error {
	m.mu.Lock()
	if pr, ok := m.procs[a.ID]; ok && pr.desired {
		m.mu.Unlock()
		return nil // allaqachon ishlamoqda
	}
	m.mu.Unlock()
	if strings.TrimSpace(a.Command) == "" {
		return fmt.Errorf("buyruq bo'sh")
	}
	return m.spawn(a, 0)
}

func (m *Manager) spawn(a store.App, restarts int) error {
	cwd := a.Cwd
	if cwd == "" {
		if home, err := os.UserHomeDir(); err == nil {
			cwd = home
		}
	}
	if _, err := os.Stat(cwd); err != nil {
		m.setStatus(a.ID, "error", "ishchi papka topilmadi: "+cwd)
		return fmt.Errorf("ishchi papka topilmadi: %s", cwd)
	}

	cmd := exec.Command("sh", "-c", a.Command)
	cmd.Dir = cwd
	setupProcAttr(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	logPath := filepath.Join(store.LogDir(), a.ID+".log")
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

	if err := cmd.Start(); err != nil {
		if lf != nil {
			lf.Close()
		}
		m.setStatus(a.ID, "error", "ishga tushmadi: "+err.Error())
		return err
	}

	m.mu.Lock()
	m.procs[a.ID] = pr
	m.mu.Unlock()
	m.setStatus(a.ID, "starting", "")

	readPipe := func(rd interface{ Read([]byte) (int, error) }) {
		sc := bufio.NewScanner(rd)
		sc.Buffer(make([]byte, 64*1024), 64*1024)
		for sc.Scan() {
			line := sc.Text()
			pr.logs.add(line)
			if pr.logFile != nil {
				fmt.Fprintln(pr.logFile, line)
			}
			m.emit("app_log", map[string]string{"id": a.ID, "line": line})
		}
	}
	go readPipe(stdout)
	go readPipe(stderr)

	// Qisqa muddat tirik qolsa — "running" deb belgilaymiz (pm2'ning "online"
	// mantiqiga o'xshash — chindan portga/log satriga tayanish umumiy buyruq
	// uchun imkonsiz).
	go func() {
		select {
		case <-time.After(aliveGrace):
			m.mu.Lock()
			alive := pr.desired
			m.mu.Unlock()
			if alive {
				m.setStatus(a.ID, "running", "")
			}
		case <-pr.done:
		}
	}()

	// Jarayon tugashini kuzatish + avto-restart.
	go func() {
		err := cmd.Wait()
		close(pr.done)
		if pr.logFile != nil {
			pr.logFile.Close()
		}
		m.mu.Lock()
		wanted := pr.desired
		delete(m.procs, a.ID)
		m.mu.Unlock()

		if !wanted {
			m.setStatus(a.ID, "stopped", "")
			return
		}
		if pr.restarts < maxRestarts {
			wait := time.Duration(1<<pr.restarts) * time.Second // 1s, 2s, 4s
			m.emit("app_log", map[string]string{
				"id":   a.ID,
				"line": fmt.Sprintf("[ServerGo] jarayon kutilmaganda to'xtadi, %s dan so'ng qayta urinish (%d/%d)", wait, pr.restarts+1, maxRestarts),
			})
			time.Sleep(wait)
			if fresh, gerr := m.st.GetApp(a.ID); gerr == nil {
				_ = m.spawn(fresh, pr.restarts+1)
				return
			}
		}
		msg := "jarayon kutilmaganda to'xtadi"
		if err != nil {
			msg += ": " + err.Error()
		}
		m.setStatus(a.ID, "error", msg)
	}()

	return nil
}

// Stop — SIGTERM, so'ng SIGKILL.
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
		_ = forceKill(pr.cmd)
		select {
		case <-pr.done:
		case <-time.After(3 * time.Second):
		}
	}
	return nil
}

// Restart.
func (m *Manager) Restart(a store.App) error {
	if err := m.Stop(a.ID); err != nil {
		return err
	}
	return m.Start(a)
}

// StopAll — dastur yopilishida.
func (m *Manager) StopAll() {
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

// Logs — oxirgi log qatorlari.
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
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > maxRingLines {
		lines = lines[len(lines)-maxRingLines:]
	}
	return lines
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
