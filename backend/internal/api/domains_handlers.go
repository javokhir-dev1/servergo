package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Server) handleGetDomainCert(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	domain := r.PathValue("domain")

	var certEnc []byte
	var updatedAt time.Time
	err := s.pool.QueryRow(r.Context(),
		`SELECT cert_pem_enc, updated_at FROM domain_certs WHERE user_id = $1 AND domain = $2`,
		user.ID, domain,
	).Scan(&certEnc, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "no cert stored for this domain")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "get domain cert failed")
		return
	}

	plain, err := s.box.Decrypt(certEnc)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "decrypt domain cert failed")
		return
	}

	writeOK(w, map[string]any{"domain": domain, "cert_pem": string(plain), "updated_at": updatedAt})
}

type putDomainCertRequest struct {
	CertPEM string `json:"cert_pem"`
}

func (s *Server) handlePutDomainCert(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	domain := r.PathValue("domain")

	var req putDomainCertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.CertPEM == "" {
		writeErr(w, http.StatusBadRequest, "cert_pem is required")
		return
	}

	certEnc, err := s.box.Encrypt([]byte(req.CertPEM))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "encrypt domain cert failed")
		return
	}

	_, err = s.pool.Exec(r.Context(), `
		INSERT INTO domain_certs (user_id, domain, cert_pem_enc, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (user_id, domain) DO UPDATE SET cert_pem_enc = EXCLUDED.cert_pem_enc, updated_at = now()
	`, user.ID, domain, certEnc)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "save domain cert failed")
		return
	}

	writeOK(w, nil)
}
