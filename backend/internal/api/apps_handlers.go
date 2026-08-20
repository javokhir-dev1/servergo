package api

import (
	"encoding/json"
	"net/http"
	"time"
)

type appRow struct {
	LocalID   string    `json:"local_id"`
	Name      string    `json:"name"`
	Command   string    `json:"command"`
	Cwd       string    `json:"cwd"`
	Autostart bool      `json:"autostart"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *Server) handleListApps(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())

	rows, err := s.pool.Query(r.Context(),
		`SELECT local_id, name, command, cwd, autostart, updated_at FROM apps WHERE user_id = $1 ORDER BY name`,
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
		if err := rows.Scan(&a.LocalID, &a.Name, &a.Command, &a.Cwd, &a.Autostart, &a.UpdatedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, "scan app failed")
			return
		}
		out = append(out, a)
	}
	writeOK(w, out)
}

type upsertAppRequest struct {
	Name      string `json:"name"`
	Command   string `json:"command"`
	Cwd       string `json:"cwd"`
	Autostart bool   `json:"autostart"`
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
		INSERT INTO apps (user_id, local_id, name, command, cwd, autostart, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (user_id, local_id) DO UPDATE SET
			name = EXCLUDED.name, command = EXCLUDED.command, cwd = EXCLUDED.cwd,
			autostart = EXCLUDED.autostart, updated_at = now()
	`, user.ID, localID, req.Name, req.Command, req.Cwd, req.Autostart)
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
