package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"servergo-backend/internal/auth"
)

// handleRegister — ro'yxatdan o'tish yopilgan: bu backend bitta hisob
// (egasi) uchun mo'ljallangan, yangi hisob ochish imkoniyati yo'q.
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	writeErr(w, http.StatusForbidden, "registration is closed")
}

type loginRequest struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	DeviceName string `json:"device_name"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	token, user, err := auth.Login(r.Context(), s.pool, req.Email, req.Password, req.DeviceName)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		writeErr(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "login failed")
		return
	}

	writeOK(w, map[string]string{"token": token, "user_id": user.ID, "email": user.Email})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	token := bearerToken(r)
	if err := auth.Logout(r.Context(), s.pool, token); err != nil {
		writeErr(w, http.StatusInternalServerError, "logout failed")
		return
	}
	writeOK(w, nil)
}
