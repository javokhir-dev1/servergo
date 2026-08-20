package server

import (
	"net/http"

	"servergo/internal/cloudsync"
)

// registerSyncRoutes — bulutli backend (api.servergo.uz) bilan
// login/logout/push/pull. Boshqa /api/* yo'llar kabi lokal token bilan
// himoyalanadi — haqiqiy backend autentifikatsiyasi (bearer token)
// ~/.config/servergo/auth.json'da saqlanadi va shu daemon jarayoni
// tomonidan ishlatiladi.
func (s *Server) registerSyncRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/auth/login", s.guard(s.authLogin))
	mux.HandleFunc("/api/auth/logout", s.guard(s.authLogout))
	mux.HandleFunc("/api/auth/status", s.guard(s.authStatus))
	mux.HandleFunc("/api/sync/push", s.guard(s.syncPush))
	mux.HandleFunc("/api/sync/pull", s.guard(s.syncPull))
}

func (s *Server) authLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email      string `json:"email"`
		Password   string `json:"password"`
		DeviceName string `json:"deviceName"`
		BackendURL string `json:"backendUrl"`
	}
	if !decode(w, r, &req) {
		return
	}
	backendURL := req.BackendURL
	if backendURL == "" {
		backendURL = cloudsync.DefaultBackendURL
	}
	token, email, err := cloudsync.Login(backendURL, req.Email, req.Password, req.DeviceName)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, err)
		return
	}
	if err := cloudsync.Save(cloudsync.AuthInfo{BackendURL: backendURL, Email: email, Token: token}); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeOK(w, map[string]string{"email": email})
}

func (s *Server) authLogout(w http.ResponseWriter, r *http.Request) {
	auth, err := cloudsync.Load()
	if err == nil && auth != nil {
		_ = cloudsync.NewClient(auth.BackendURL, auth.Token).Logout() // best-effort
	}
	if err := cloudsync.Clear(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeOK(w, true)
}

func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	auth, err := cloudsync.Load()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if auth == nil {
		writeOK(w, map[string]any{"loggedIn": false})
		return
	}
	writeOK(w, map[string]any{"loggedIn": true, "email": auth.Email, "backendUrl": auth.BackendURL})
}

func (s *Server) syncPush(w http.ResponseWriter, r *http.Request) {
	appsSvc, ok := s.appsSvc(w)
	if !ok {
		return
	}
	if s.tun == nil {
		writeErr(w, http.StatusServiceUnavailable, errNoTunnel)
		return
	}
	sum, err := cloudsync.Push(appsSvc, s.tun)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeOK(w, map[string]any{"apps": sum.Apps, "projects": sum.Projects, "domains": sum.Domains})
}

func (s *Server) syncPull(w http.ResponseWriter, r *http.Request) {
	appsSvc, ok := s.appsSvc(w)
	if !ok {
		return
	}
	if s.tun == nil {
		writeErr(w, http.StatusServiceUnavailable, errNoTunnel)
		return
	}
	sum, err := cloudsync.Pull(appsSvc, s.tun)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeOK(w, map[string]any{"apps": sum.Apps, "projects": sum.Projects, "domains": sum.Domains})
}
