package api

import (
	"encoding/json"
	"net/http"
	"time"
)

type projectRow struct {
	LocalID      string    `json:"local_id"`
	Name         string    `json:"name"`
	Port         int       `json:"port"`
	Subdomain    string    `json:"subdomain"`
	BaseDomain   string    `json:"base_domain"`
	Protocol     string    `json:"protocol"`
	TunnelID     string    `json:"tunnel_id"`
	TunnelName   string    `json:"tunnel_name"`
	AccountTag   string    `json:"account_tag"`
	TunnelSecret string    `json:"tunnel_secret,omitempty"` // decrypted, base64 as cloudflared writes it
	Autostart    bool      `json:"autostart"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())

	rows, err := s.pool.Query(r.Context(), `
		SELECT local_id, name, port, subdomain, base_domain, protocol,
		       tunnel_id, tunnel_name, account_tag, tunnel_secret_enc, autostart, updated_at
		FROM projects WHERE user_id = $1 ORDER BY name
	`, user.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list projects failed")
		return
	}
	defer rows.Close()

	out := []projectRow{}
	for rows.Next() {
		var p projectRow
		var secretEnc []byte
		if err := rows.Scan(&p.LocalID, &p.Name, &p.Port, &p.Subdomain, &p.BaseDomain, &p.Protocol,
			&p.TunnelID, &p.TunnelName, &p.AccountTag, &secretEnc, &p.Autostart, &p.UpdatedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, "scan project failed")
			return
		}
		if len(secretEnc) > 0 {
			plain, err := s.box.Decrypt(secretEnc)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, "decrypt tunnel secret failed")
				return
			}
			p.TunnelSecret = string(plain)
		}
		out = append(out, p)
	}
	writeOK(w, out)
}

type upsertProjectRequest struct {
	Name         string `json:"name"`
	Port         int    `json:"port"`
	Subdomain    string `json:"subdomain"`
	BaseDomain   string `json:"base_domain"`
	Protocol     string `json:"protocol"`
	TunnelID     string `json:"tunnel_id"`
	TunnelName   string `json:"tunnel_name"`
	AccountTag   string `json:"account_tag"`
	TunnelSecret string `json:"tunnel_secret"` // plaintext in transit (TLS via the tunnel fronting this backend), encrypted at rest
	Autostart    bool   `json:"autostart"`
}

func (s *Server) handleUpsertProject(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	localID := r.PathValue("localID")

	var req upsertProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" || req.Port <= 0 {
		writeErr(w, http.StatusBadRequest, "name and a positive port are required")
		return
	}

	var secretEnc []byte
	if req.TunnelSecret != "" {
		enc, err := s.box.Encrypt([]byte(req.TunnelSecret))
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "encrypt tunnel secret failed")
			return
		}
		secretEnc = enc
	}

	_, err := s.pool.Exec(r.Context(), `
		INSERT INTO projects (user_id, local_id, name, port, subdomain, base_domain, protocol,
		                       tunnel_id, tunnel_name, account_tag, tunnel_secret_enc, autostart, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12, now())
		ON CONFLICT (user_id, local_id) DO UPDATE SET
			name = EXCLUDED.name, port = EXCLUDED.port, subdomain = EXCLUDED.subdomain,
			base_domain = EXCLUDED.base_domain, protocol = EXCLUDED.protocol,
			tunnel_id = EXCLUDED.tunnel_id, tunnel_name = EXCLUDED.tunnel_name,
			account_tag = EXCLUDED.account_tag,
			tunnel_secret_enc = COALESCE(EXCLUDED.tunnel_secret_enc, projects.tunnel_secret_enc),
			autostart = EXCLUDED.autostart, updated_at = now()
	`, user.ID, localID, req.Name, req.Port, req.Subdomain, req.BaseDomain, req.Protocol,
		req.TunnelID, req.TunnelName, req.AccountTag, secretEnc, req.Autostart)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "save project failed")
		return
	}

	writeOK(w, nil)
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	localID := r.PathValue("localID")

	tag, err := s.pool.Exec(r.Context(), `DELETE FROM projects WHERE user_id = $1 AND local_id = $2`, user.ID, localID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "delete project failed")
		return
	}
	if tag.RowsAffected() == 0 {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	writeOK(w, nil)
}
