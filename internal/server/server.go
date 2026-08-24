// Package server dastur ichidagi HTTP API va statik fayllarni beradi.
//
// Server faqat 127.0.0.1 da, tasodifiy portda tinglaydi va har bir /api/
// so'rovidan tasodifiy token talab qiladi — shu mashinadagi boshqa dasturlar
// (masalan brauzerdagi sahifa) jarayonlarni o'ldira olmasligi uchun.
package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"os/exec"
	"runtime/debug"
	"strconv"
	"time"

	"servergo/internal/apps"
	"servergo/internal/pm2"
	"servergo/internal/sysmon"
	"servergo/internal/tunnel"
	"servergo/internal/vpstunnel"
)

// KillGrace — SIGTERM dan SIGKILL gacha kutish vaqti.
const KillGrace = 5 * time.Second

type Server struct {
	token   string
	assets  fs.FS
	ln      net.Listener
	baseURL string
	tun     *tunnel.Service    // nil bo'lishi mumkin — u holda bo'lim 503 qaytaradi
	apps    *apps.Service      // nil bo'lishi mumkin — u holda bo'lim 503 qaytaradi
	vt      *vpstunnel.Service // nil bo'lishi mumkin — u holda bo'lim 503 qaytaradi
}

// New — loopback'da tasodifiy portni band qiladi va serverni tayyorlaydi.
func New(assets fs.FS, token string, tun *tunnel.Service, appsSvc *apps.Service, vt *vpstunnel.Service) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{token: token, assets: assets, ln: ln, tun: tun, apps: appsSvc, vt: vt}
	s.baseURL = "http://" + ln.Addr().String()
	return s, nil
}

// URL — webview yuklashi kerak bo'lgan manzil (token bilan).
func (s *Server) URL() string {
	return s.baseURL + "/?t=" + s.token
}

// BaseURL — tokensiz manzil (daemon.json ga yozish uchun; token alohida saqlanadi).
func (s *Server) BaseURL() string {
	return s.baseURL
}

// Serve — bloklaydi; odatda alohida goroutine da chaqiriladi.
func (s *Server) Serve() error {
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(s.assets)))

	mux.HandleFunc("/api/pm2/list", s.guard(s.pm2List))
	mux.HandleFunc("/api/pm2/action", s.guard(s.pm2Action))
	mux.HandleFunc("/api/pm2/flush", s.guard(s.pm2Flush))
	mux.HandleFunc("/api/pm2/logs", s.guard(s.pm2Logs))
	mux.HandleFunc("/api/pm2/info", s.guard(s.pm2Info))
	mux.HandleFunc("/api/sys/snapshot", s.guard(s.sysSnapshot))
	mux.HandleFunc("/api/sys/kill", s.guard(s.sysKill))
	mux.HandleFunc("/api/open", s.guard(s.openPath))
	s.registerTunnelRoutes(mux)
	s.registerAppRoutes(mux)
	s.registerSyncRoutes(mux)
	s.registerVPSTunnelRoutes(mux)

	srv := &http.Server{
		Handler:     mux,
		ReadTimeout: 15 * time.Second,
		// cloudflared login (domen qo'shishda) brauzerda foydalanuvchi
		// javobini kutadi — jlist/kill grace'dan ancha uzoqroq zaxira kerak.
		WriteTimeout: 5 * time.Minute,
	}
	return srv.Serve(s.ln)
}

// guard — tokenni tekshiradi va handler panic qilsa 500 qaytaradi
// (bitta buzuq so'rov butun dasturni yiqitmasin).
func (s *Server) guard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Token") != s.token {
			writeErr(w, http.StatusUnauthorized, errors.New("token noto'g'ri"))
			return
		}
		defer func() {
			if rec := recover(); rec != nil {
				writeErr(w, http.StatusInternalServerError,
					errors.New("ichki xato: "+string(debug.Stack())))
			}
		}()
		h(w, r)
	}
}

/* ---------------- pm2 ---------------- */

func (s *Server) pm2List(w http.ResponseWriter, r *http.Request) {
	procs, err := pm2.List()
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeOK(w, procs)
}

func (s *Server) pm2Action(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type string `json:"type"`
		ID   int    `json:"id"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := pm2.Action(req.Type, req.ID); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeOK(w, true)
}

func (s *Server) pm2Flush(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int `json:"id"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := pm2.Flush(req.ID); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeOK(w, true)
}

func (s *Server) pm2Logs(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.URL.Query().Get("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("id noto'g'ri"))
		return
	}
	lines := 500
	if n, err := strconv.Atoi(r.URL.Query().Get("lines")); err == nil && n > 0 {
		lines = n
	}
	logs, err := pm2.GetLogs(id, lines)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeOK(w, logs)
}

func (s *Server) pm2Info(w http.ResponseWriter, r *http.Request) {
	alive, daemonErr := pm2.DaemonStatus()
	host, err := hostMetrics()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeOK(w, map[string]any{
		"daemon":     map[string]any{"alive": alive, "error": daemonErr},
		"pm2Version": pm2.Version(),
		"host":       host,
	})
}

/* ---------------- tizim / RAM ---------------- */

func (s *Server) sysSnapshot(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	accurate := r.URL.Query().Get("accurate") == "1"

	mem, err := sysmon.ReadMemInfo()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	procs, err := sysmon.ReadProcs()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeOK(w, map[string]any{
		"mem":      mem,
		"groups":   sysmon.BuildGroups(procs, mem.Total, accurate),
		"accurate": accurate,
		"took":     time.Since(start).Round(time.Millisecond).String(),
	})
}

func (s *Server) sysKill(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PID int `json:"pid"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.PID <= 1 {
		writeErr(w, http.StatusBadRequest, errors.New("noto'g'ri pid"))
		return
	}
	res, err := sysmon.KillTree(req.PID, KillGrace)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, sysmon.ErrProtected) {
			status = http.StatusForbidden
		}
		writeErr(w, status, err)
		return
	}
	writeOK(w, res)
}

/* ---------------- yordamchi ---------------- */

// openPath — faylni yoki havolani tizim standart dasturida ochadi.
// Electron dagi shell.openPath / openExternal o'rniga.
func (s *Server) openPath(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Target string `json:"target"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Target == "" {
		writeErr(w, http.StatusBadRequest, errors.New("yo'l ko'rsatilmagan"))
		return
	}
	cmd := exec.Command("xdg-open", req.Target)
	if err := cmd.Start(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Zombi qoldirmaslik uchun fonda kutamiz.
	go func() { _ = cmd.Wait() }()
	writeOK(w, true)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("faqat POST"))
		return false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

// Javob formati renderer kutgani bilan bir xil: {ok, data} yoki {ok, error}.
func writeOK(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": data})
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"ok": false, "error": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
