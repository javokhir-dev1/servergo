package proxy

import (
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/yamux"

	"servergo-relay/internal/registry"
)

func yamuxCfg() *yamux.Config {
	c := yamux.DefaultConfig()
	c.LogOutput = nil
	c.Logger = log.New(io.Discard, "", 0)
	return c
}

// pair — relay tomoni (yamux.Server) va agent tomoni (yamux.Client).
func pair(t *testing.T) (relaySide, agentSide *yamux.Session) {
	t.Helper()
	c, s := net.Pipe()
	srv, err := yamux.Server(s, yamuxCfg())
	if err != nil {
		t.Fatalf("yamux.Server: %v", err)
	}
	cli, err := yamux.Client(c, yamuxCfg())
	if err != nil {
		t.Fatalf("yamux.Client: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close(); _ = cli.Close() })
	return srv, cli
}

// goodAgent — oqimlarni qabul qilib, ularga HTTP javob beradigan agent.
// yamux.Session net.Listener'ni qanoatlantiradi, shuning uchun to'g'ridan-
// to'g'ri http.Serve'ga beriladi — bu haqiqiy agent bilan bir xil manzara.
func goodAgent(t *testing.T, reg *registry.Registry, host, body string, hits *int64) {
	t.Helper()
	relaySide, agentSide := pair(t)
	go func() {
		_ = http.Serve(agentSide, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if hits != nil {
				atomic.AddInt64(hits, 1)
			}
			_, _ = io.WriteString(w, body)
		}))
	}()
	reg.Register(host, relaySide)
}

// brokenAgent — oqimni qabul qiladi, lekin javob bermay darhol yopadi.
// Bu "agent ulanib turibdi, lekin sessiya aslida yaroqsiz" holatini
// modellashtiradi — aynan shu holat ilgari tashrifchiga 502 berardi.
func brokenAgent(t *testing.T, reg *registry.Registry, host string, hits *int64) {
	t.Helper()
	relaySide, agentSide := pair(t)
	go func() {
		for {
			stream, err := agentSide.Accept()
			if err != nil {
				return
			}
			if hits != nil {
				atomic.AddInt64(hits, 1)
			}
			_ = stream.Close()
		}
	}()
	reg.Register(host, relaySide)
}

func newReq(t *testing.T, method, host string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, "http://"+host+"/", body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	// ReverseProxy'dan kelgan so'rovda Host maydoni to'ldirilgan bo'ladi —
	// transport aynan shundan hostname'ni oladi.
	req.Host = host
	return req
}

func TestRetriesOntoHealthySessionWhenFirstIsBroken(t *testing.T) {
	reg := registry.New()
	var brokenHits, goodHits int64
	// Tartib muhim: birinchi ro'yxatga olingani birinchi sinaladi.
	brokenAgent(t, reg, "x.uz", &brokenHits)
	goodAgent(t, reg, "x.uz", "salom", &goodHits)

	hs := &Hisoblagichlar{}
	tr := &sessionTransport{reg: reg, hs: hs}
	resp, err := tr.RoundTrip(newReq(t, http.MethodGet, "x.uz", nil))
	if err != nil {
		t.Fatalf("RoundTrip xato berdi, holbuki pool'da sog'lom sessiya bor: %v", err)
	}
	defer resp.Body.Close()

	got, _ := io.ReadAll(resp.Body)
	if string(got) != "salom" {
		t.Fatalf("javob tanasi %q, kutilgan %q", got, "salom")
	}
	if brokenHits == 0 {
		t.Fatal("yaroqsiz sessiya umuman sinalmadi — test o'z maqsadini tekshirmayapti")
	}
	if goodHits != 1 {
		t.Fatalf("sog'lom sessiyaga %d so'rov yetdi, kutilgan 1", goodHits)
	}
	// Hisoblagichlar diagnostikaning yagona manbasi, shuning uchun ular
	// haqiqatan sanayotganini ham tekshiramiz.
	if got := hs.Sorovlar.Load(); got != 1 {
		t.Fatalf("Sorovlar=%d, kutilgan 1", got)
	}
	if got := hs.QaytaUrinish.Load(); got != 1 {
		t.Fatalf("QaytaUrinish=%d, kutilgan 1 (ikkinchi sessiyaga o'tildi)", got)
	}
}

func TestRetriesWhenSessionIsClosed(t *testing.T) {
	reg := registry.New()
	var goodHits int64

	deadRelaySide, deadAgentSide := pair(t)
	reg.Register("x.uz", deadRelaySide)
	goodAgent(t, reg, "x.uz", "tirik", &goodHits)
	_ = deadAgentSide.Close()
	_ = deadRelaySide.Close()

	hs := &Hisoblagichlar{}
	tr := &sessionTransport{reg: reg, hs: hs}
	resp, err := tr.RoundTrip(newReq(t, http.MethodGet, "x.uz", nil))
	if err != nil {
		t.Fatalf("RoundTrip xato berdi: %v", err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if string(got) != "tirik" {
		t.Fatalf("javob tanasi %q", got)
	}
}

// Tanasi bor so'rovni qayta yuborib bo'lmaydi (u birinchi urinishda o'qib
// bo'lingan) va POST idempotent emas — shuning uchun ikkinchi sessiyaga
// o'tmasdan xato qaytishi kerak. Aks holda buyurtma ikki marta yaratilishi
// mumkin edi.
func TestNoRetryForRequestWithBody(t *testing.T) {
	reg := registry.New()
	var brokenHits, goodHits int64
	brokenAgent(t, reg, "x.uz", &brokenHits)
	goodAgent(t, reg, "x.uz", "salom", &goodHits)

	hs := &Hisoblagichlar{}
	tr := &sessionTransport{reg: reg, hs: hs}
	resp, err := tr.RoundTrip(newReq(t, http.MethodPost, "x.uz", strings.NewReader("buyurtma")))
	if err == nil {
		resp.Body.Close()
		t.Fatal("POST qayta yuborildi — bu xavfli, xato kutilgan edi")
	}
	if goodHits != 0 {
		t.Fatalf("POST ikkinchi sessiyaga yuborildi (%d marta) — takroriy amal xavfi", goodHits)
	}
}

func TestNoSessionsGivesError(t *testing.T) {
	reg := registry.New()
	hs := &Hisoblagichlar{}
	tr := &sessionTransport{reg: reg, hs: hs}
	// agentWait to'liq kutiladi, shuning uchun bu test biroz sekin (8s).
	if _, err := tr.RoundTrip(newReq(t, http.MethodGet, "yoq.uz", nil)); err == nil {
		t.Fatal("agent yo'q bo'lsa xato kutilgan edi")
	}
	if got := hs.AgentYoq.Load(); got != 1 {
		t.Fatalf("AgentYoq=%d, kutilgan 1", got)
	}
}
