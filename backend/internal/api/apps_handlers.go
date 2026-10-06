package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Sandbox yo'llari bazada bitta matn ustunida, qator ajratgich bilan
// saqlanadi (yo'lda vergul bo'lishi mumkin), API'da esa ro'yxat sifatida
// ko'rinadi.
func splitPaths(v string) []string {
	out := []string{}
	for _, l := range strings.Split(v, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func splitPorts(v string) []int {
	out := []int{}
	for _, f := range strings.Split(v, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(f)); err == nil && n > 0 {
			out = append(out, n)
		}
	}
	return out
}

func joinPorts(list []int) string {
	parts := make([]string, 0, len(list))
	for _, n := range list {
		if n > 0 {
			parts = append(parts, strconv.Itoa(n))
		}
	}
	return strings.Join(parts, ",")
}

func joinPaths(list []string) string {
	clean := make([]string, 0, len(list))
	for _, l := range list {
		if l = strings.TrimSpace(l); l != "" {
			clean = append(clean, l)
		}
	}
	return strings.Join(clean, "\n")
}

type appRow struct {
	LocalID      string    `json:"local_id"`
	Name         string    `json:"name"`
	Command      string    `json:"command"`
	Cwd          string    `json:"cwd"`
	Autostart    bool      `json:"autostart"`
	Sandbox      bool      `json:"sandbox"`
	SandboxRO    []string  `json:"sandbox_ro"`
	SandboxRW    []string  `json:"sandbox_rw"`
	NetIsolate   bool      `json:"net_isolate"`
	NetHostPorts []int     `json:"net_host_ports"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (s *Server) handleListApps(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())

	rows, err := s.pool.Query(r.Context(),
		`SELECT local_id, name, command, cwd, autostart, sandbox, sandbox_ro, sandbox_rw,
		        net_isolate, net_ports, updated_at
		   FROM apps WHERE user_id = $1 ORDER BY name`,
		user.ID,
	)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list apps failed")
		return
	}
	defer rows.Close()

	out := []appRow{}
	for rows.Next() {
		var a appRow
		var ro, rw, ports string
		if err := rows.Scan(&a.LocalID, &a.Name, &a.Command, &a.Cwd, &a.Autostart,
			&a.Sandbox, &ro, &rw, &a.NetIsolate, &ports, &a.UpdatedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, "scan app failed")
			return
		}
		a.SandboxRO, a.SandboxRW = splitPaths(ro), splitPaths(rw)
		a.NetHostPorts = splitPorts(ports)
		out = append(out, a)
	}
	writeOK(w, out)
}

type upsertAppRequest struct {
	Name         string   `json:"name"`
	Command      string   `json:"command"`
	Cwd          string   `json:"cwd"`
	Autostart    bool     `json:"autostart"`
	Sandbox      bool     `json:"sandbox"`
	SandboxRO    []string `json:"sandbox_ro"`
	SandboxRW    []string `json:"sandbox_rw"`
	NetIsolate   bool     `json:"net_isolate"`
	NetHostPorts []int    `json:"net_host_ports"`
}

func (s *Server) handleUpsertApp(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	localID := r.PathValue("localID")

	var req upsertAppRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" || req.Command == "" {
		writeErr(w, http.StatusBadRequest, "name and command are required")
		return
	}

	_, err := s.pool.Exec(r.Context(), `
		INSERT INTO apps (user_id, local_id, name, command, cwd, autostart,
		                  sandbox, sandbox_ro, sandbox_rw, net_isolate, net_ports, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now())
		ON CONFLICT (user_id, local_id) DO UPDATE SET
			name = EXCLUDED.name, command = EXCLUDED.command, cwd = EXCLUDED.cwd,
			autostart = EXCLUDED.autostart, sandbox = EXCLUDED.sandbox,
			sandbox_ro = EXCLUDED.sandbox_ro, sandbox_rw = EXCLUDED.sandbox_rw,
			net_isolate = EXCLUDED.net_isolate, net_ports = EXCLUDED.net_ports,
			updated_at = now()
	`, user.ID, localID, req.Name, req.Command, req.Cwd, req.Autostart,
		req.Sandbox, joinPaths(req.SandboxRO), joinPaths(req.SandboxRW),
		req.NetIsolate, joinPorts(req.NetHostPorts))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "save app failed")
		return
	}

	writeOK(w, nil)
}

func (s *Server) handleDeleteApp(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	localID := r.PathValue("localID")

	tag, err := s.pool.Exec(r.Context(), `DELETE FROM apps WHERE user_id = $1 AND local_id = $2`, user.ID, localID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "delete app failed")
		return
	}
	if tag.RowsAffected() == 0 {
		writeErr(w, http.StatusNotFound, "app not found")
		return
	}
	writeOK(w, nil)
}
