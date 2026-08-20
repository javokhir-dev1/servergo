// Package daemon — bir vaqtning o'zida faqat bitta ServerGo nusxasi tunnellarni
// boshqarishini kafolatlaydi.
//
// Muammo: tunnellar systemd user service sifatida fonda ishlaydi, lekin
// foydalanuvchi oynani ham ochadi. Ikkala jarayon ham o'z manager'ini ishga
// tushirsa, bitta loyiha uchun ikkita cloudflared paydo bo'ladi.
//
// Yechim: kim flock'ni olsa — o'sha egasi. Egasi o'z HTTP manzilini va tokenini
// endpoint fayliga yozadi; qulfni ololmagan nusxa esa o'z serverini umuman
// ko'tarmaydi va oynani egasining manziliga yo'naltiradi.
package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"servergo/internal/tunnel/store"
)

// Endpoint — egasi qoldiradigan "meni shu yerdan toping" yozuvi.
type Endpoint struct {
	URL   string `json:"url"`   // masalan http://127.0.0.1:41234
	Token string `json:"token"` // /api/ uchun bir martalik token
	PID   int    `json:"pid"`
}

func lockPath() string     { return filepath.Join(store.Dir(), "servergo.lock") }
func endpointPath() string { return filepath.Join(store.Dir(), "daemon.json") }

// TryLock — eksklyuziv egalikni olishga urinadi.
// ok=false bo'lsa boshqa nusxa allaqachon ishlayapti (bu xato emas).
// Qaytgan release funksiyasi qulfni bo'shatadi.
func TryLock() (release func(), ok bool, err error) {
	if err := os.MkdirAll(store.Dir(), 0o700); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(lockPath(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	// LOCK_NB — kutmaymiz; band bo'lsa darhol qaytamiz.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, err
	}
	// Qulf fayl deskriptori ochiq turganda saqlanadi — Close qulfni bo'shatadi.
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, true, nil
}

func WriteEndpoint(e Endpoint) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	// Token ichida — faqat egasiga o'qish huquqi.
	return os.WriteFile(endpointPath(), b, 0o600)
}

func RemoveEndpoint() { _ = os.Remove(endpointPath()) }

// ReadEndpoint — ishlab turgan nusxaning manzilini o'qiydi. Demon endigina
// ko'tarilayotgan bo'lishi mumkin, shuning uchun qisqa muddat kutib ko'ramiz.
func ReadEndpoint(wait time.Duration) (Endpoint, error) {
	deadline := time.Now().Add(wait)
	for {
		b, err := os.ReadFile(endpointPath())
		if err == nil {
			var e Endpoint
			if err := json.Unmarshal(b, &e); err == nil && e.URL != "" && e.Token != "" {
				return e, nil
			}
		}
		if time.Now().After(deadline) {
			return Endpoint{}, fmt.Errorf(
				"ServerGo ishlab turibdi, lekin uning manzili topilmadi (%s). "+
					"Demon holatini tekshiring: systemctl --user status servergo", endpointPath())
		}
		time.Sleep(150 * time.Millisecond)
	}
}
