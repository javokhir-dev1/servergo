// Package registry — hostname -> faol yamux sessiya xaritasi.
// Control paketi (agent uladi) yozadi, proxy paketi (jamoatchilik HTTP so'rovi)
// va autocert HostPolicy o'qiydi.
package registry

import (
	"sync"

	"github.com/hashicorp/yamux"
)

type Registry struct {
	mu       sync.RWMutex
	sessions map[string]*yamux.Session
}

func New() *Registry {
	return &Registry{sessions: map[string]*yamux.Session{}}
}

// Register — hostname'ni shu sessiyaga bog'laydi. Shu hostname uchun eski
// sessiya bo'lsa (masalan agent qayta ulangan), eskisi yopiladi.
func (r *Registry) Register(hostname string, sess *yamux.Session) {
	r.mu.Lock()
	old := r.sessions[hostname]
	r.sessions[hostname] = sess
	r.mu.Unlock()
	if old != nil && old != sess {
		_ = old.Close()
	}
}

// Unregister — faqat hozir ro'yxatdagi aynan shu sessiya bo'lsa olib tashlaydi
// (eski sessiya yopilib, register qayta chaqirilib ulgurmasin degan holat uchun).
func (r *Registry) Unregister(hostname string, sess *yamux.Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, ok := r.sessions[hostname]; ok && cur == sess {
		delete(r.sessions, hostname)
	}
}

func (r *Registry) Lookup(hostname string) (*yamux.Session, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sessions[hostname]
	if !ok || s.IsClosed() {
		return nil, false
	}
	return s, true
}

// Hostnames — hozir faol barcha hostname'lar (autocert HostPolicy uchun).
func (r *Registry) Hostnames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.sessions))
	for h, s := range r.sessions {
		if !s.IsClosed() {
			out = append(out, h)
		}
	}
	return out
}
