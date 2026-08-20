// Package cloudsync — ServerGo daemon'ining markaziy backend (api.servergo.uz)
// bilan gaplashuvi: login/logout va apps/tunnel loyihalarini push/pull qilish.
package cloudsync

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	tunnelstore "servergo/internal/tunnel/store"
)

// DefaultBackendURL — servergo-backend'ning standart manzili.
const DefaultBackendURL = "https://api.servergo.uz"

// errNotLoggedIn — push/pull hisobga kirilmasdan chaqirilsa.
var errNotLoggedIn = errors.New("avval kiring: servergo login <email>")

// AuthInfo — ~/.config/servergo/auth.json.
type AuthInfo struct {
	BackendURL string `json:"backendUrl"`
	Email      string `json:"email"`
	Token      string `json:"token"`
}

func authPath() string {
	return filepath.Join(tunnelstore.Dir(), "auth.json")
}

// Load — hisob ma'lumotlarini o'qiydi. Fayl yo'q bo'lsa (nil, nil) qaytadi
// (kirilmagan holat — xato emas).
func Load() (*AuthInfo, error) {
	data, err := os.ReadFile(authPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var a AuthInfo
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

func Save(a AuthInfo) error {
	if err := os.MkdirAll(tunnelstore.Dir(), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(authPath(), data, 0o600)
}

func Clear() error {
	err := os.Remove(authPath())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
