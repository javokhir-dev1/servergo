package server

import (
	"errors"
	"net/http"
	"strconv"

	"servergo/internal/apps"
)

// registerAppRoutes — "Ilovalar" bo'limining API si. Bo'lim ishga tushmagan
// bo'lsa ham marshrutlar ro'yxatga olinadi va tushunarli xato qaytaradi.
func (s *Server) registerAppRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/apps/list", s.guard(s.appsList))
	mux.HandleFunc("/api/apps/create", s.guard(s.appsCreate))
	mux.HandleFunc("/api/apps/update", s.guard(s.appsUpdate))
	mux.HandleFunc("/api/apps/action", s.guard(s.appsAction))
	mux.HandleFunc("/api/apps/delete", s.guard(s.appsDelete))
	mux.HandleFunc("/api/apps/logs", s.guard(s.appsLogs))
	mux.HandleFunc("/api/apps/events", s.guard(s.appsEvents))
}

var errNoApps = errors.New("ilovalar bo'limi mavjud emas")

func (s *Server) appsSvc(w http.ResponseWriter) (*apps.Service, bool) {
	if s.apps == nil {
		writeErr(w, http.StatusServiceUnavailable, errNoApps)
		return nil, false
	}
	return s.apps, true
}

func (s *Server) appsList(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.appsSvc(w)
	if !ok {
		return
	}
	list, err := svc.ListApps()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeOK(w, list)
}

func (s *Server) appsCreate(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.appsSvc(w)
	if !ok {
		return
	}
	var in apps.AppInput
	if !decode(w, r, &in) {
		return
	}
	a, err := svc.CreateApp(in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeOK(w, a)
}

func (s *Server) appsUpdate(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.appsSvc(w)
	if !ok {
		return
	}
	var req struct {
		apps.AppInput
		ID string `json:"id"`
	}
	if !decode(w, r, &req) {
		return
	}
	a, err := svc.UpdateApp(req.ID, req.AppInput)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeOK(w, a)
}

func (s *Server) appsAction(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.appsSvc(w)
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
		err = svc.StartApp(req.ID)
	case "stop":
		err = svc.StopApp(req.ID)
	case "restart":
		err = svc.RestartApp(req.ID)
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

func (s *Server) appsDelete(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.appsSvc(w)
	if !ok {
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := svc.DeleteApp(req.ID); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeOK(w, true)
}

func (s *Server) appsLogs(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.appsSvc(w)
	if !ok {
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, errors.New("id ko'rsatilmagan"))
		return
	}
	writeOK(w, svc.AppLogs(id))
}

func (s *Server) appsEvents(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.appsSvc(w)
	if !ok {
		return
	}
	since, _ := strconv.Atoi(r.URL.Query().Get("since"))
	events, seq := svc.Events(since)
	writeOK(w, map[string]any{"events": events, "seq": seq})
}
