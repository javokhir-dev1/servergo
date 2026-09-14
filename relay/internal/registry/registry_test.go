package registry

import (
	"io"
	"log"
	"net"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

// newSession — sinov uchun haqiqiy yamux sessiya juftligi (net.Pipe ustida).
// Yopilganini IsClosed() to'g'ri ko'rsatishi uchun soxta emas, chinakam
// sessiya kerak.
func newSession(t *testing.T) *yamux.Session {
	t.Helper()
	c, s := net.Pipe()
	cfg := yamux.DefaultConfig()
	cfg.LogOutput = nil
	cfg.Logger = log.New(io.Discard, "", 0)
	srv, err := yamux.Server(s, cfg)
	if err != nil {
		t.Fatalf("yamux.Server: %v", err)
	}
	cli, err := yamux.Client(c, cfg)
	if err != nil {
		t.Fatalf("yamux.Client: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close(); _ = cli.Close() })
	return srv
}

func TestPoolKeepsMultipleSessions(t *testing.T) {
	r := New()
	a, b := newSession(t), newSession(t)

	if n := r.Register("x.uz", a); n != 1 {
		t.Fatalf("birinchi Register: pool=%d, kutilgan 1", n)
	}
	// Eng muhim farq: ikkinchi sessiya birinchisini YOPMASLIGI kerak.
	if n := r.Register("x.uz", b); n != 2 {
		t.Fatalf("ikkinchi Register: pool=%d, kutilgan 2", n)
	}
	if a.IsClosed() {
		t.Fatal("birinchi sessiya yopilib ketdi — pool'ning mohiyati shu bilan yo'qoladi")
	}
	if got := len(r.Sessions("x.uz")); got != 2 {
		t.Fatalf("Sessions: %d, kutilgan 2", got)
	}
}

func TestSessionsRoundRobin(t *testing.T) {
	r := New()
	a, b := newSession(t), newSession(t)
	r.Register("x.uz", a)
	r.Register("x.uz", b)

	first := r.Sessions("x.uz")[0]
	second := r.Sessions("x.uz")[0]
	if first == second {
		t.Fatal("ketma-ket chaqiruvlar bir xil sessiyani birinchi qaytardi — round-robin ishlamayapti")
	}
}

func TestClosedSessionIsSkipped(t *testing.T) {
	r := New()
	dead, live := newSession(t), newSession(t)
	r.Register("x.uz", dead)
	r.Register("x.uz", live)

	_ = dead.Close()
	// yamux yopilishni darhol belgilaydi, lekin kafolat uchun biroz kutamiz.
	deadline := time.Now().Add(time.Second)
	for !dead.IsClosed() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	ss := r.Sessions("x.uz")
	if len(ss) != 1 {
		t.Fatalf("Sessions: %d, kutilgan 1 (yopilgani tashlab ketilishi kerak)", len(ss))
	}
	if ss[0] != live {
		t.Fatal("tirik sessiya o'rniga boshqasi qaytdi")
	}
}

func TestUnregisterRemovesOnlyThatSession(t *testing.T) {
	r := New()
	a, b := newSession(t), newSession(t)
	r.Register("x.uz", a)
	r.Register("x.uz", b)

	if left := r.Unregister("x.uz", a); left != 1 {
		t.Fatalf("Unregister: qolgan=%d, kutilgan 1", left)
	}
	ss := r.Sessions("x.uz")
	if len(ss) != 1 || ss[0] != b {
		t.Fatalf("noto'g'ri sessiya olib tashlandi: %v", ss)
	}
	if left := r.Unregister("x.uz", b); left != 0 {
		t.Fatalf("oxirgi Unregister: qolgan=%d, kutilgan 0", left)
	}
	if _, ok := r.Lookup("x.uz"); ok {
		t.Fatal("pool bo'shagach Lookup hali ham sessiya qaytarmoqda")
	}
}

func TestMaxPerHostEvictsOldest(t *testing.T) {
	r := New()
	sessions := make([]*yamux.Session, 0, maxPerHost+2)
	for i := 0; i < maxPerHost+2; i++ {
		s := newSession(t)
		sessions = append(sessions, s)
		r.Register("x.uz", s)
	}
	if got := len(r.Sessions("x.uz")); got != maxPerHost {
		t.Fatalf("pool hajmi %d, kutilgan %d", got, maxPerHost)
	}
	if !sessions[0].IsClosed() {
		t.Fatal("chegaradan oshganda eng eski sessiya yopilishi kerak edi")
	}
}

func TestHostnamesOnlyLiveHosts(t *testing.T) {
	r := New()
	s := newSession(t)
	r.Register("x.uz", s)
	if got := r.Hostnames(); len(got) != 1 || got[0] != "x.uz" {
		t.Fatalf("Hostnames: %v", got)
	}
	r.Unregister("x.uz", s)
	if got := r.Hostnames(); len(got) != 0 {
		t.Fatalf("Unregister'dan keyin Hostnames: %v", got)
	}
}

func TestKnownSurvivesDisconnect(t *testing.T) {
	r := New()
	s := newSession(t)
	r.Register("x.uz", s)

	if !r.Known("x.uz") {
		t.Fatal("ulangan hostname tanish bo'lishi kerak")
	}
	r.Unregister("x.uz", s)
	// Ulanish uzilgan, lekin hostname hali tanish — aks holda relay qayta
	// ulanish oynasida TLS handshake'ni rad etib, tashrifchiga xavfsizlik
	// xatosini ko'rsatardi.
	if !r.Known("x.uz") {
		t.Fatal("uzilgandan keyin ham hostname knownTTL ichida tanish qolishi kerak")
	}
	if _, ok := r.Lookup("x.uz"); ok {
		t.Fatal("Lookup uzilgandan keyin sessiya qaytarmasligi kerak")
	}
}

func TestUnknownHostRejected(t *testing.T) {
	r := New()
	if r.Known("begona.uz") {
		t.Fatal("hech qachon ulanmagan hostname tanish bo'lmasligi kerak")
	}
}

func TestKnownExpiresAfterTTL(t *testing.T) {
	r := New()
	s := newSession(t)
	r.Register("x.uz", s)
	r.Unregister("x.uz", s)

	r.mu.Lock()
	r.seen["x.uz"] = time.Now().Add(-knownTTL - time.Minute)
	r.mu.Unlock()

	if r.Known("x.uz") {
		t.Fatal("knownTTL o'tgach hostname tanish bo'lmasligi kerak")
	}
}
