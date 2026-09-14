// Package manager — VPS Tunnel loyihalarining hayot davri: ulanish/uzish,
// avto-qayta-ulanish, health-check va loglar. internal/tunnel/manager bilan
// bir xil naqsh, lekin cloudflared subprocess o'rniga
// internal/vpstunnel/client.Run goroutine'ini boshqaradi.
package manager

import (
	"context"
	"fmt"
	"math/rand"
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
	healthPeriod = 10 * time.Second
	maxRingLines = 500
	maxLogFileMB = 10

	// Qayta ulanish hech qachon to'xtamaydi — foydalanuvchi Stop bosmaguncha
	// nazoratchi (supervise) ishlab turadi. Kutish vaqti eksponensial o'sadi,
	// lekin maxBackoff bilan cheklanadi: noutbuk uyquda yotgan yoki internet
	// yarim soatga uzilgan holatda ham tunnel o'zi tiklanadi.
	initialBackoff = 1 * time.Second
	maxBackoff     = 30 * time.Second

	// errorAfterFails — shuncha ketma-ket muvaffaqiyatsiz urinishdan keyin
	// ulanish "yiqilgan" deb belgilanadi (qayta urinish baribir davom etadi).
	// Loyiha "error" holatiga faqat POOL'dagi hamma ulanish shunday bo'lganda
	// o'tadi — qarang: proc.derive.
	errorAfterFails = 3

	// connsPerProject — har bir loyiha uchun relay'ga ochiladigan MUSTAQIL
	// ulanishlar soni. cloudflared ham aynan shu tamoyilda ishlaydi (4 ta
	// ulanish, kamida 2 xil datacentrga): bittasi uzilganda yoki
	// sekinlashganda trafik qolganlari orqali ketaveradi va tashrifchi
	// uzilishni umuman sezmaydi.
	//
	// Nega 2: bitta relay uchun asosiy foyda birinchi qo'shimcha ulanishdan
	// keladi — yamux'da bitta TCP ustidagi BARCHA oqimlar bir-birini kutadi
	// (head-of-line blocking), ya'ni bitta so'rov sekinlashsa qolganlari ham
	// sekinlashadi. Ikkinchi ulanish shu bog'liqlikni uzadi. Undan keyingilari
	// bir xil VPS'ga borgani uchun kam narsa qo'shadi — chinakam zaxira
	// ikkinchi relay bilan paydo bo'ladi.
	connsPerProject = 2

	// connStagger — pool'dagi ulanishlarning boshlanish oralig'i. Bir vaqtda
	// ochilgan ulanishlar bir vaqtda uzilishga ham moyil bo'ladi (bir xil
	// keepalive jadvali, bir xil NAT yozuvi yoshi) — bu esa pool'ning ma'nosini
	// yo'qotardi.
	connStagger = 3 * time.Second
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

// proc — bitta loyihaning ulanishlar pool'i. Loyiha to'xtatilgunga qadar
// m.procs xaritasida qoladi — qayta ulanish kutuvi paytida ham. Shu sababli
// Start() takroriy nusxa yaratib yubormaydi va UI loyihani "ishlayotgan" deb
// ko'rsatishda davom etadi.
type proc struct {
	cancel  context.CancelFunc
	desired bool
	logs    *ring
	done    chan struct{} // pool'dagi BARCHA nazoratchilar tugaganda yopiladi

	mu      sync.Mutex
	up      []bool // har bir ulanish hozir tirikmi
	failing []bool // har bir ulanish ketma-ket errorAfterFails marta yiqildimi
	status  string // oxirgi e'lon qilingan status (takroriy emit'ni bostirish)
	lastErr string
}

// derive — pool holatidan loyihaning umumiy statusini hisoblaydi va uni
// eslab qoladi. changed=false bo'lsa status o'zgarmagan, ya'ni UI'ga qayta
// yubormaslik kerak — aks holda ikkita ulanish navbatma-navbat qayta
// ulanayotganda loglar bir xil xabar bilan to'lib ketardi.
//
// Asosiy qoida: kamida BITTA ulanish tirik bo'lsa, loyiha ishlayapti —
// qolganlarining uzilishi tashrifchiga ko'rinmaydi, aynan shu pool'ning
// maqsadi. "error" faqat hamma ulanish yiqilganda beriladi.
//
// pr.mu ushlab turilgan holda chaqiriladi.
func (pr *proc) derive(errMsg string) (status, lastErr string, changed bool) {
	up, failing := 0, 0
	for i := range pr.up {
		if pr.up[i] {
			up++
		}
		if pr.failing[i] {
			failing++
		}
	}
	switch {
	case up > 0:
		status, lastErr = "running", ""
	case failing == len(pr.failing) && len(pr.failing) > 0:
		status, lastErr = "error", errMsg
	default:
		status, lastErr = "starting", ""
	}
	if status == pr.status && lastErr == pr.lastErr {
		return status, lastErr, false
	}
	pr.status, pr.lastErr = status, lastErr
	return status, lastErr, true
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

	ctx, cancel := context.WithCancel(context.Background())
	pr := &proc{
		cancel:  cancel,
		desired: true,
		logs:    &ring{},
		done:    make(chan struct{}),
		up:      make([]bool, connsPerProject),
		failing: make([]bool, connsPerProject),
		status:  "starting",
	}
	m.mu.Lock()
	m.procs[p.ID] = pr
	m.mu.Unlock()
	m.setStatus(p.ID, "starting", "")

	go m.runPool(ctx, pr, p)
	return nil
}

// syncStatus — pool holatini qayta hisoblab, o'zgargan bo'lsagina e'lon qiladi.
func (m *Manager) syncStatus(id string, pr *proc, errMsg string) {
	pr.mu.Lock()
	status, lastErr, changed := pr.derive(errMsg)
	pr.mu.Unlock()
	if changed {
		m.setStatus(id, status, lastErr)
	}
}

// runPool — loyihaning barcha ulanishlarini ko'taradi va hammasi tugaguncha
// kutadi. Log fayli bitta — pool'dagi ulanishlar unga "[#N]" prefiksi bilan
// yozadi, shunda qaysi ulanish nima qilayotgani ko'rinib turadi.
func (m *Manager) runPool(ctx context.Context, pr *proc, p store.Project) {
	defer close(pr.done)

	logPath := filepath.Join(store.LogDir(), p.ID+".log")
	rotateIfBig(logPath)
	logFile, _ := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if logFile != nil {
		defer logFile.Close()
	}
	var logMu sync.Mutex
	logLine := func(idx int, line string) {
		line = fmt.Sprintf("[#%d] %s", idx+1, line)
		logMu.Lock()
		pr.logs.add(line)
		if logFile != nil {
			fmt.Fprintln(logFile, line)
		}
		logMu.Unlock()
		m.emit("vt_project_log", map[string]string{"id": p.ID, "line": line})
	}

	var wg sync.WaitGroup
	for i := 0; i < connsPerProject; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if idx > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Duration(idx) * connStagger):
				}
			}
			m.supervise(ctx, pr, p, idx, logLine)
		}(i)
	}
	wg.Wait()

	// Faqat o'zimizni olib tashlaymiz: Stop() kutishdan charchab qaytgan va
	// undan keyin Start() yangi pool yaratgan bo'lsa, uni o'chirib
	// yubormaslik kerak.
	m.mu.Lock()
	if cur, ok := m.procs[p.ID]; ok && cur == pr {
		delete(m.procs, p.ID)
		m.mu.Unlock()
		m.setStatus(p.ID, "stopped", "")
		return
	}
	m.mu.Unlock()
}

// supervise — pool'dagi BITTA ulanishni loyiha to'xtatilgunga qadar tirik
// ushlab turadi: client.Run qaytishi bilan (uzilish sababidan qat'i nazar)
// kutib, qaytadan ulanadi. Chidamli bo'lishi SHART — relay bilan ulanish
// tarmoqdagi qisqa uzilishlarda ham, uzoq (soatlab) uzilishlarda ham
// o'z-o'zidan tiklanishi kerak, aks holda foydalanuvchi qo'lda Restart
// bosmaguncha sayt ochilmaydi.
func (m *Manager) supervise(ctx context.Context, pr *proc, p store.Project, idx int, logLine func(int, string)) {
	backoff := initialBackoff
	fails := 0
	attempt := 0

	for ctx.Err() == nil {
		// Har urinishda eng yangi sozlamalarni olamiz: foydalanuvchi kutish
		// paytida portni yoki relay ma'lumotlarini o'zgartirgan bo'lishi mumkin.
		if fresh, err := m.st.GetProject(p.ID); err == nil {
			p = fresh
		}
		m.mu.Lock()
		relay := m.relay
		m.mu.Unlock()

		attempt++
		var err error
		connected := false
		if !relay.ready() {
			err = fmt.Errorf("relay sozlanmagan")
		} else {
			// onUp shu goroutine ichidan sinxron chaqiriladi (client.Run
			// bloklovchi), shuning uchun connected'ga yozish xavfsiz.
			cfg := client.Config{
				RelayAddr:   relay.Addr,
				Fingerprint: relay.Fingerprint,
				Token:       relay.Token,
				ProjectID:   p.ID,
				Hostname:    p.Hostname(),
				LocalPort:   p.Port,
				ConnIndex:   idx,
			}
			err = client.Run(ctx, cfg,
				func() {
					connected = true
					fails = 0
					backoff = initialBackoff
					pr.mu.Lock()
					pr.up[idx] = true
					pr.failing[idx] = false
					pr.mu.Unlock()
					m.syncStatus(p.ID, pr, "")
				},
				func(line string) { logLine(idx, line) },
			)
		}

		if ctx.Err() != nil {
			break
		}
		if !connected {
			fails++
		}

		reason := "ulanish uzildi"
		if err != nil {
			reason += ": " + err.Error()
		}
		wait := jitter(backoff)
		applog.Warn("[%s#%d] %s — %s dan so'ng qayta ulanadi (urinish #%d)", short(p.ID), idx+1, reason, wait, attempt+1)
		logLine(idx, fmt.Sprintf("[ServerGo] %s — %s dan so'ng qayta ulanadi (urinish #%d)", reason, wait, attempt+1))

		// Bu ulanish yiqildi. Loyihaning umumiy statusi faqat POOL'dagi
		// hamma ulanish yiqilgan bo'lsa "error"ga o'tadi — bittasi tirik
		// bo'lsa sayt ishlayveradi va foydalanuvchini bezovta qilish shart emas.
		pr.mu.Lock()
		pr.up[idx] = false
		pr.failing[idx] = fails >= errorAfterFails
		pr.mu.Unlock()
		m.syncStatus(p.ID, pr, reason)

		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}

	pr.mu.Lock()
	pr.up[idx] = false
	pr.mu.Unlock()
}

// jitter — kutish vaqtiga tasodifiy ±25% qo'shadi. Pool'dagi ulanishlar bir
// sababdan (masalan relay qayta ishga tushdi) birga uzilgan bo'lsa, ular
// birga qayta ulanmasligi kerak — aks holda ikkalasi bir vaqtda "yo'q"
// bo'lgan oynalar saqlanib qolaverardi.
func jitter(d time.Duration) time.Duration {
	delta := int64(d) / 4
	if delta <= 0 {
		return d
	}
	return d + time.Duration(rand.Int63n(2*delta)-delta)
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
