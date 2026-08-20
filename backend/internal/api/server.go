// Package api implements the servergo backend's HTTP surface: user auth and
// user-scoped CRUD for synced apps, tunnel projects, and domain certs.
package api

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"servergo-backend/internal/auth"
	"servergo-backend/internal/crypto"
)

type Server struct {
	pool *pgxpool.Pool
	box  *crypto.Box
	mux  *http.ServeMux
}

func New(pool *pgxpool.Pool, box *crypto.Box) *Server {
	s := &Server{pool: pool, box: box, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("POST /api/auth/register", s.handleRegister)
	s.mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/auth/logout", s.withAuth(s.handleLogout))

	s.mux.HandleFunc("GET /api/apps", s.withAuth(s.handleListApps))
	s.mux.HandleFunc("PUT /api/apps/{localID}", s.withAuth(s.handleUpsertApp))
	s.mux.HandleFunc("DELETE /api/apps/{localID}", s.withAuth(s.handleDeleteApp))

	s.mux.HandleFunc("GET /api/projects", s.withAuth(s.handleListProjects))
	s.mux.HandleFunc("PUT /api/projects/{localID}", s.withAuth(s.handleUpsertProject))
	s.mux.HandleFunc("DELETE /api/projects/{localID}", s.withAuth(s.handleDeleteProject))

	s.mux.HandleFunc("GET /api/domains/{domain}/cert", s.withAuth(s.handleGetDomainCert))
	s.mux.HandleFunc("PUT /api/domains/{domain}/cert", s.withAuth(s.handlePutDomainCert))
}

// envelope mirrors the {ok,error,data} convention already used by the local
// servergo CLI-daemon protocol (internal/cli/client.go), so a future CLI
// client can reuse the same decoding helpers.
type envelope struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Data  any    `json:"data,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, env envelope) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(env); err != nil {
		log.Printf("write response: %v", err)
	}
}

func writeOK(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, envelope{OK: true, Data: data})
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, envelope{OK: false, Error: msg})
}

type ctxKey int

const userCtxKey ctxKey = 0

func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		user, err := auth.Authenticate(r.Context(), s.pool, token)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		ctx := r.Context()
		next(w, r.WithContext(setUser(ctx, user)))
	}
}

func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) > len(prefix) && h[:len(prefix)] == prefix {
		return h[len(prefix):]
	}
	return ""
}
