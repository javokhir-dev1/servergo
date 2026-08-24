package server

import (
	"errors"
	"net/http"
	"strconv"

	"servergo/internal/vpstunnel"
)

// registerVPSTunnelRoutes — "VPS Tunnel" bo'limining API si.
// internal/server/tunnels.go bilan bir xil naqsh, mustaqil endpoint prefiksi.
func (s *Server) registerVPSTunnelRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/vpstunnel/setup", s.guard(s.vtSetup))
	mux.HandleFunc("/api/vpstunnel/relay", s.guard(s.vtRelaySet))
	mux.HandleFunc("/api/vpstunnel/domain/add", s.guard(s.vtDomainAdd))
	mux.HandleFunc("/api/vpstunnel/domain/remove", s.guard(s.vtDomainRemove))
	mux.HandleFunc("/api/vpstunnel/domain/active", s.guard(s.vtDomainActive))
	mux.HandleFunc("/api/vpstunnel/projects", s.guard(s.vtProjects))
	mux.HandleFunc("/api/vpstunnel/project/create", s.guard(s.vtCreate))
	mux.HandleFunc("/api/vpstunnel/project/update", s.guard(s.vtUpdate))
	mux.HandleFunc("/api/vpstunnel/project/action", s.guard(s.vtAction))
	mux.HandleFunc("/api/vpstunnel/project/delete", s.guard(s.vtDelete))
	mux.HandleFunc("/api/vpstunnel/project/logs", s.guard(s.vtProjectLogs))
	mux.HandleFunc("/api/vpstunnel/checkport", s.guard(s.vtCheckPort))
	mux.HandleFunc("/api/vpstunnel/events", s.guard(s.vtEvents))
	mux.HandleFunc("/api/vpstunnel/applogs", s.guard(s.vtAppLogs))
}

var errNoVPSTunnel = errors.New("VPS Tunnel bo'limi mavjud emas")

func (s *Server) vtSvc(w http.ResponseWriter) (*vpstunnel.Service, bool) {
	if s.vt == nil {
		writeErr(w, http.StatusServiceUnavailable, errNoVPSTunnel)
		return nil, false
	}
	return s.vt, true
}

func (s *Server) vtSetup(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.vtSvc(w)
	if !ok {
		return
	}
	writeOK(w, svc.SetupState())
}

func (s *Server) vtRelaySet(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.vtSvc(w)
	if !ok {
		return
	}
	var req struct {
		Addr        string `json:"addr"`
		Token       string `json:"token"`
		Fingerprint string `json:"fingerprint"`
	}
	if !decode(w, r, &req) {
		return
	}
	state, err := svc.SetRelayConfig(req.Addr, req.Token, req.Fingerprint)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeOK(w, state)
}

func (s *Server) vtDomainAdd(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.vtSvc(w)
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

func (s *Server) vtDomainRemove(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.vtSvc(w)
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

func (s *Server) vtDomainActive(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.vtSvc(w)
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

func (s *Server) vtProjects(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.vtSvc(w)
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

func (s *Server) vtCreate(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.vtSvc(w)
	if !ok {
		return
	}
	var req vpstunnel.ProjectInput
	if !decode(w, r, &req) {
		return
	}
	p, err := svc.CreateProject(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeOK(w, p)
}

func (s *Server) vtUpdate(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.vtSvc(w)
	if !ok {
		return
	}
	var req struct {
		vpstunnel.ProjectInput
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

func (s *Server) vtAction(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.vtSvc(w)
	if !ok {
		return
	}
	var req struct {
		Type string `json:"type"`
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

func (s *Server) vtDelete(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.vtSvc(w)
	if !ok {
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := svc.DeleteProject(req.ID); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeOK(w, true)
}

func (s *Server) vtProjectLogs(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.vtSvc(w)
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

func (s *Server) vtCheckPort(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.vtSvc(w)
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

func (s *Server) vtEvents(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.vtSvc(w)
	if !ok {
		return
	}
	since, _ := strconv.Atoi(r.URL.Query().Get("since"))
	events, seq := svc.Events(since)
	writeOK(w, map[string]any{"events": events, "seq": seq})
}

func (s *Server) vtAppLogs(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.vtSvc(w)
	if !ok {
		return
	}
	writeOK(w, svc.AppLogs())
}
