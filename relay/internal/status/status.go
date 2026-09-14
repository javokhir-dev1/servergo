// Package status — relay diagnostikasi. FAQAT loopback'da tinglaydi
// (127.0.0.1), shuning uchun tashqaridan ko'rinmaydi va autentifikatsiya
// talab qilmaydi: unga yetish uchun VPS'ga SSH bilan kirish kerak.
//
//	ssh vps curl -s localhost:9090/holat
//	ssh -L 9090:127.0.0.1:9090 vps    # so'ng brauzerdan /debug/pprof/
//
// Nima uchun kerak: so'rovlar sababsiz sekinlashganda yoki tunnel g'alati
// ishlaganda, jarayonning ichida nima bo'layotganini ko'rish uchun boshqa
// yo'l yo'q edi — loglar faqat ulanish/uzilishni ko'rsatadi.
package status

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/pprof"
	"runtime"
	"time"

	"servergo-relay/internal/proxy"
	"servergo-relay/internal/registry"
)

type javob struct {
	IshlashVaqti string               `json:"ishlash_vaqti"`
	Goroutine    int                  `json:"goroutine"`
	XotiraMB     uint64               `json:"xotira_mb"`
	Poollar      []registry.PoolHolat `json:"poollar"`
	Hisoblagich  map[string]int64     `json:"hisoblagich"`
	Vaqt         string               `json:"vaqt"`
}

// Serve — holat serverini ishga tushiradi. Bloklovchi emas; xato bo'lsa
// qaytaradi (chaqiruvchi uni faqat loglaydi — bu qism ishlamasa ham relay
// o'z ishini davom ettirishi kerak).
func Serve(addr string, reg *registry.Registry, hs *proxy.Hisoblagichlar, boshlandi time.Time) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/holat", func(w http.ResponseWriter, r *http.Request) {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(javob{
			IshlashVaqti: time.Since(boshlandi).Round(time.Second).String(),
			Goroutine:    runtime.NumGoroutine(),
			XotiraMB:     ms.Alloc / 1024 / 1024,
			Poollar:      reg.Holat(),
			Hisoblagich:  hs.JSON(),
			Vaqt:         time.Now().Format(time.RFC3339),
		})
	})

	// pprof — qotish qaytganda goroutine dumpi eng qimmatli dalil bo'ladi:
	//   ssh vps curl -s 'localhost:9090/debug/pprof/goroutine?debug=2'
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
	return ln, nil
}
