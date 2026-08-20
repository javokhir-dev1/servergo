package server

import (
	"errors"
	"net/http"
	"strconv"

	"servergo/internal/tunnel"
)

// registerTunnelRoutes — "Tunnellar" bo'limining API si.
// Bo'lim ishga tushmagan bo'lsa (masalan SQLite ochilmadi) marshrutlar baribir
// ro'yxatga olinadi va tushunarli xato qaytaradi — UI ni sindirmaslik uchun.
func (s *Server) registerTunnelRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/tunnel/setup", s.guard(s.tunSetup))
	mux.HandleFunc("/api/tunnel/detect", s.guard(s.tunDetect))
	mux.HandleFunc("/api/tunnel/login", s.guard(s.tunLogin))
	mux.HandleFunc("/api/tunnel/domain/add", s.guard(s.tunDomainAdd))
	mux.HandleFunc("/api/tunnel/domain/remove", s.guard(s.tunDomainRemove))
	mux.HandleFunc("/api/tunnel/domain/active", s.guard(s.tunDomainActive))
	mux.HandleFunc("/api/tunnel/projects", s.guard(s.tunProjects))
	mux.HandleFunc("/api/tunnel/project/create", s.guard(s.tunCreate))
	mux.HandleFunc("/api/tunnel/project/update", s.guard(s.tunUpdate))
	mux.HandleFunc("/api/tunnel/project/action", s.guard(s.tunAction))
	mux.HandleFunc("/api/tunnel/project/delete", s.guard(s.tunDelete))
	mux.HandleFunc("/api/tunnel/project/logs", s.guard(s.tunProjectLogs))
	mux.HandleFunc("/api/tunnel/project/diagnose", s.guard(s.tunDiagnose))
	mux.HandleFunc("/api/tunnel/project/fixdns", s.guard(s.tunFixDNS))
	mux.HandleFunc("/api/tunnel/checkport", s.guard(s.tunCheckPort))
	mux.HandleFunc("/api/tunnel/events", s.guard(s.tunEvents))
	mux.HandleFunc("/api/tunnel/applogs", s.guard(s.tunAppLogs))
}

var errNoTunnel = errors.New("tunnellar bo'limi mavjud emas")

// svc — servis tayyor bo'lmasa 503 qaytaradi va false beradi.
func (s *Server) svc(w http.ResponseWriter) (*tunnel.Service, bool) {
	if s.tun == nil {
		writeErr(w, http.StatusServiceUnavailable, errNoTunnel)
		return nil, false
	}
	return s.tun, true
}

func (s *Server) tunSetup(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.svc(w)
	if !ok {
		return
	}
	writeOK(w, svc.SetupState())
}

func (s *Server) tunDetect(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.svc(w)
	if !ok {
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if !decode(w, r, &req) {
		return
	}
	state, err := svc.DetectCloudflared(req.Path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeOK(w, state)
}

func (s *Server) tunLogin(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.svc(w)
	if !ok {
		return
	}
	var req struct{}
	if !decode(w, r, &req) {
		return
	}
	state, err := svc.Login()
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeOK(w, state)
}

func (s *Server) tunDomainAdd(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.svc(w)
	if !ok {
		return
	}
	var req struct {
		Domain string `json:"domain"`
	}
	if !decode(w, r, &req) {
		return
	}
	state, err := svc.AddDomain(req.Domain)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeOK(w, state)
}

func (s *Server) tunDomainRemove(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.svc(w)
	if !ok {
		return
	}
	var req struct {
		Domain string `json:"domain"`
	}
	if !decode(w, r, &req) {
		return
	}
	state, err := svc.RemoveDomain(req.Domain)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeOK(w, state)
}

func (s *Server) tunDomainActive(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.svc(w)
	if !ok {
		return
	}
	var req struct {
		Domain string `json:"domain"`
	}
	if !decode(w, r, &req) {
		return
	}
	state, err := svc.SetActiveDomain(req.Domain)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeOK(w, state)
}

func (s *Server) tunProjects(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.svc(w)
	if !ok {
		return
	}
	ps, err := svc.ListProjects()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeOK(w, ps)
}

func (s *Server) tunCreate(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.svc(w)
	if !ok {
		return
	}
	var req struct {
		tunnel.ProjectInput
		Force bool `json:"force"`
	}
	if !decode(w, r, &req) {
		return
	}
	p, err := svc.CreateProject(req.ProjectInput, req.Force)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeOK(w, p)
}

func (s *Server) tunUpdate(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.svc(w)
	if !ok {
		return
	}
	var req struct {
		tunnel.ProjectInput
		ID string `json:"id"`
	}
	if !decode(w, r, &req) {
		return
	}
	p, err := svc.UpdateProject(req.ID, req.ProjectInput)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeOK(w, p)
}

func (s *Server) tunAction(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.svc(w)
	if !ok {
		return
	}
	var req struct {
		Type string `json:"type"` // start | stop | restart
		ID   string `json:"id"`
	}
	if !decode(w, r, &req) {
		return
	}
	var err error
	switch req.Type {
	case "start":
		err = svc.StartProject(req.ID)
	case "stop":
		err = svc.StopProject(req.ID)
	case "restart":
		err = svc.RestartProject(req.ID)
	default:
		writeErr(w, http.StatusBadRequest, errors.New("noma'lum amal: "+req.Type))
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeOK(w, true)
}

func (s *Server) tunDelete(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.svc(w)
	if !ok {
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if !decode(w, r, &req) {
		return
	}
	warn, err := svc.DeleteProject(req.ID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeOK(w, map[string]any{"warning": warn})
}

func (s *Server) tunProjectLogs(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.svc(w)
	if !ok {
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, errors.New("id ko'rsatilmagan"))
		return
	}
	writeOK(w, svc.ProjectLogs(id))
}

func (s *Server) tunDiagnose(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.svc(w)
	if !ok {
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if !decode(w, r, &req) {
		return
	}
	res, err := svc.Diagnose(req.ID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeOK(w, res)
}

func (s *Server) tunFixDNS(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.svc(w)
	if !ok {
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := svc.FixDNS(req.ID); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeOK(w, true)
}

func (s *Server) tunCheckPort(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.svc(w)
	if !ok {
		return
	}
	port, err := strconv.Atoi(r.URL.Query().Get("port"))
	if err != nil || port < 1 || port > 65535 {
		writeErr(w, http.StatusBadRequest, errors.New("port noto'g'ri"))
		return
	}
	writeOK(w, svc.CheckPort(port))
}

func (s *Server) tunEvents(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.svc(w)
	if !ok {
		return
	}
	since, _ := strconv.Atoi(r.URL.Query().Get("since"))
	events, seq := svc.Events(since)
	writeOK(w, map[string]any{"events": events, "seq": seq})
}

func (s *Server) tunAppLogs(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.svc(w)
	if !ok {
		return
	}
	writeOK(w, svc.AppLogs())
}
