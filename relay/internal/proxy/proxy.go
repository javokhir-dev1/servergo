// Package proxy — jamoatchilik uchun :443 (va ACME uchun :80) qatlami.
// Host header bo'yicha registry'dan agent sessiyasini topadi, so'rovni
// yamux oqimi (stream) ustidan xom HTTP baytlari sifatida yuboradi va
// javobni o'sha oqimdan o'qiydi — agent tomonida HTTP parsing shart emas
// (qarang: internal/vpstunnel/client/client.go, lokal modulda).
package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"

	"golang.org/x/crypto/acme/autocert"

	"servergo-relay/internal/registry"
)

// hostOnly — req.Host'dan portni olib tashlaydi (standart 443'da Host
// header'da port bo'lmaydi, lekin boshqa portlar — masalan sinov muhitida —
// bilan ulanilganda bo'ladi; registry hostname'ni portsiz saqlaydi).
func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}

// sessionTransport — har bir so'rovni req.Host bo'yicha topilgan yamux
// sessiyasidan ochilgan yangi oqimga yozadi va javobni o'sha oqimdan o'qiydi.
// Oqim tomonidan ko'rinishda bu xuddi TCP orqali origin serverga ulanishdek —
// shuning uchun agent tomoni faqat baytlarni ko'chirsa yetarli.
type sessionTransport struct {
	reg *registry.Registry
}

func (t *sessionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	sess, ok := t.reg.Lookup(hostOnly(req.Host))
	if !ok {
		return nil, fmt.Errorf("'%s' uchun faol agent topilmadi", req.Host)
	}
	stream, err := sess.Open()
	if err != nil {
		return nil, fmt.Errorf("oqim ochilmadi: %w", err)
	}
	if err := req.Write(stream); err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("so'rov yozilmadi: %w", err)
	}
	br := bufio.NewReader(stream)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("javob o'qilmadi: %w", err)
	}
	if resp.StatusCode == http.StatusSwitchingProtocols {
		// 101 — protokol almashinuvi (WebSocket, h2c va h.k.). Bu holatda
		// http.ReadResponse Body sifatida http.NoBody qaytaradi (1xx uchun
		// "tana" yo'q deb hisoblanadi), sarlavhadan keyingi baytlar esa
		// bufio buferida qolib ketadi — shuning uchun Body'ni o'zimiz
		// quramiz. ReverseProxy.handleUpgradeResponse Body'ni
		// io.ReadWriteCloser'ga cast qiladi va undan keyin ikki yo'nalishda
		// xom baytlarni ko'chiradi; cast bajarilmasa so'rov "101 switching
		// protocols response with non-writable body" bilan 502'ga aylanadi.
		resp.Body = &upgradedBody{br: br, stream: stream}
		return resp, nil
	}
	// http.ReadResponse'ning Body'si bufio ustida — Close() qilinganda
	// bufio manbasi (bizning yamux stream) o'zi yopilmaydi, shuning uchun
	// qo'lda ulaymiz: ReverseProxy javobni uzatib bo'lgach Body.Close()
	// chaqiradi, shu payt stream ham yopiladi.
	resp.Body = &streamBody{ReadCloser: resp.Body, stream: stream}
	return resp, nil
}

type streamBody struct {
	io.ReadCloser
	stream io.Closer
}

func (b *streamBody) Close() error {
	err := b.ReadCloser.Close()
	_ = b.stream.Close()
	return err
}

// upgradedBody — 101 javobdan keyingi ulanish uchun Body: o'qish sarlavhadan
// keyin buferda qolgan baytlarni yo'qotmaslik uchun bufio orqali, yozish esa
// to'g'ridan-to'g'ri yamux oqimiga (u to'liq net.Conn) ketadi. Shu bilan
// tunnel upgrade'dan so'ng oddiy ikki yo'nalishli bayt kanaliga aylanadi.
// (Standart kutubxonadagi analogi: net/http.readWriteCloserBody.)
type upgradedBody struct {
	br     *bufio.Reader
	stream net.Conn
}

func (b *upgradedBody) Read(p []byte) (int, error)  { return b.br.Read(p) }
func (b *upgradedBody) Write(p []byte) (int, error) { return b.stream.Write(p) }
func (b *upgradedBody) Close() error                { return b.stream.Close() }

func NewProxyHandler(reg *registry.Registry) http.Handler {
	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = req.Host
		},
		Transport: &sessionTransport{reg: reg},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("proxy: %s -> xato: %v", r.Host, err)
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("502 Bad Gateway — VPS Tunnel: agent ulanmagan yoki lokal servis javob bermayapti\n"))
		},
	}
	return rp
}

// NewAutocertManager — hostname faol registry'da bo'lsagina sertifikat
// beradi, aks holda begona SNI so'rovlari (masalan skanerlar) rad etiladi.
func NewAutocertManager(certDir string, reg *registry.Registry) *autocert.Manager {
	return &autocert.Manager{
		Prompt: autocert.AcceptTOS,
		Cache:  autocert.DirCache(certDir),
		HostPolicy: func(_ context.Context, host string) error {
			if _, ok := reg.Lookup(host); ok {
				return nil
			}
			return fmt.Errorf("'%s' faol emas — sertifikat berilmaydi", host)
		},
	}
}

// TLSConfigFor — autocert manager asosida https server uchun tls.Config.
func TLSConfigFor(m *autocert.Manager) *tls.Config {
	return m.TLSConfig()
}
