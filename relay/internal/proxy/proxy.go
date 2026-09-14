// Package proxy — jamoatchilik uchun :443 (va ACME uchun :80) qatlami.
// Host header bo'yicha registry'dan agent sessiyalarini topadi, so'rovni
// yamux oqimi (stream) ustidan xom HTTP baytlari sifatida yuboradi va
// javobni o'sha oqimdan o'qiydi — agent tomonida HTTP parsing shart emas
// (qarang: internal/vpstunnel/client/client.go, lokal modulda).
//
// Bitta hostname ortida bir nechta sessiya bo'ladi (qarang: registry).
// Bittasi yiqilsa so'rov keyingisida qayta bajariladi, shuning uchun bitta
// ulanishning uzilishi tashrifchiga umuman ko'rinmaydi.
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
	"sync/atomic"
	"time"

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
// Hisoblagichlar — relay nima qilayotganini tashqaridan ko'rish uchun.
// Nosozlik qaytganda (masalan so'rovlar sababsiz sekinlashsa) birinchi
// qaraladigan joy shu: qayta urinishlar o'sdimi, agentsiz qolgan so'rovlar
// bormi, xatolar qaysi turdan.
type Hisoblagichlar struct {
	Sorovlar      atomic.Int64 // proxy qabul qilgan jami so'rov
	QaytaUrinish  atomic.Int64 // sessiya yiqilib, boshqasida takrorlangani
	AgentYoq      atomic.Int64 // pool bo'sh bo'lgani uchun rad etilgani
	Xatolar       atomic.Int64 // 502 bilan tugagani
	Yuklanish     atomic.Int64 // 101 protokol almashinuvi (WebSocket)
}

func (h *Hisoblagichlar) JSON() map[string]int64 {
	return map[string]int64{
		"sorovlar":      h.Sorovlar.Load(),
		"qayta_urinish": h.QaytaUrinish.Load(),
		"agent_yoq":     h.AgentYoq.Load(),
		"xatolar":       h.Xatolar.Load(),
		"yuklanish":     h.Yuklanish.Load(),
	}
}

type sessionTransport struct {
	reg *registry.Registry
	hs  *Hisoblagichlar
}

// agentWait — hostname uchun BITTA ham sessiya qolmagan bo'lsa, so'rovni
// darhol 502 bilan rad etmasdan shuncha vaqt kutamiz. Pool bilan bu holat
// kamdan-kam bo'ladi (barcha ulanishlar bir vaqtda uzilishi kerak), lekin
// relay qayta ishga tushganda hali ham yuzaga keladi.
const agentWait = 8 * time.Second

// retryable — so'rovni BOSHQA sessiyada qaytadan yuborish xavfsizmi.
//
// Shart ikkita: (1) so'rov tanasi yo'q — aks holda u birinchi urinishda
// allaqachon o'qib bo'lingan bo'lishi mumkin va ikkinchi marta yozib
// bo'lmaydi; (2) metod idempotent — birinchi urinish origin'ga yetib borib,
// javob yo'lda yo'qolgan bo'lsa, qayta yuborish ta'sirni takrorlamasligi
// kerak. Go'ning o'z http.Transport'i ham aynan shu qoidaga amal qiladi.
func retryable(req *http.Request) bool {
	if req.Body != nil && req.Body != http.NoBody {
		return false
	}
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	}
	return false
}

func (t *sessionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.hs.Sorovlar.Add(1)
	host := hostOnly(req.Host)
	sessions := t.reg.SessionsWait(host, agentWait)
	if len(sessions) == 0 {
		t.hs.AgentYoq.Add(1)
		return nil, fmt.Errorf("'%s' uchun faol agent topilmadi", req.Host)
	}

	var lastErr error
	for i, sess := range sessions {
		if i > 0 {
			t.hs.QaytaUrinish.Add(1)
		}
		stream, err := sess.Open()
		if err != nil {
			// Hali bitta ham bayt yozilmadi — bu bosqichdagi xatodan keyin
			// ISTALGAN so'rovni boshqa sessiyada takrorlash xavfsiz.
			lastErr = fmt.Errorf("oqim ochilmadi: %w", err)
			continue
		}

		resp, err := roundTripOn(stream, req)
		if err == nil {
			if resp.StatusCode == http.StatusSwitchingProtocols {
				t.hs.Yuklanish.Add(1)
			}
			return resp, nil
		}
		_ = stream.Close()
		lastErr = err

		// Bu yerga yetib kelgan bo'lsak, so'rov qisman yozilgan bo'lishi
		// mumkin — shuning uchun qayta urinish faqat xavfsiz so'rovlar uchun.
		if !retryable(req) {
			break
		}
	}
	return nil, lastErr
}

// roundTripOn — bitta oqim ustida so'rov-javob almashinuvi.
func roundTripOn(stream net.Conn, req *http.Request) (*http.Response, error) {
	if err := req.Write(stream); err != nil {
		return nil, fmt.Errorf("so'rov yozilmadi: %w", err)
	}
	br := bufio.NewReader(stream)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		return nil, fmt.Errorf("javob o'qilmadi: %w", err)
	}

	if resp.StatusCode == http.StatusSwitchingProtocols {
		// Protokol almashinuvi (WebSocket va h.k.): httputil.ReverseProxy
		// javob tanasidan IKKI TOMONLAMA kanal sifatida foydalanadi va uni
		// io.ReadWriteCloser'ga keltirib oladi. http.ReadResponse esa 101
		// uchun Body'ga NoBody qo'yadi (1xx da tana bo'lmaydi), shuning
		// uchun o'rniga oqimning o'zini beramiz — o'qish bufio orqali
		// (unda javob sarlavhasidan keyingi baytlar bo'lishi mumkin),
		// yozish to'g'ridan-to'g'ri oqimga.
		resp.Body = &streamConn{r: br, stream: stream}
	} else {
		// http.ReadResponse'ning Body'si bufio ustida — Close() qilinganda
		// bufio manbasi (bizning yamux stream) o'zi yopilmaydi, shuning uchun
		// qo'lda ulaymiz: ReverseProxy javobni uzatib bo'lgach Body.Close()
		// chaqiradi, shu payt stream ham yopiladi.
		resp.Body = &streamBody{ReadCloser: resp.Body, stream: stream}
	}
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

// streamConn — 101 javoblari uchun io.ReadWriteCloser.
type streamConn struct {
	r      io.Reader
	stream net.Conn
}

func (c *streamConn) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c *streamConn) Write(p []byte) (int, error) { return c.stream.Write(p) }
func (c *streamConn) Close() error                { return c.stream.Close() }

func NewProxyHandler(reg *registry.Registry, hs *Hisoblagichlar) http.Handler {
	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = req.Host
		},
		Transport: &sessionTransport{reg: reg, hs: hs},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			hs.Xatolar.Add(1)
			log.Printf("proxy: %s -> xato: %v", r.Host, err)
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("502 Bad Gateway — VPS Tunnel: agent ulanmagan yoki lokal servis javob bermayapti\n"))
		},
	}
	return rp
}

// NewAutocertManager — hostname registry'ga tanish bo'lsagina sertifikat
// beradi, aks holda begona SNI so'rovlari (masalan skanerlar) rad etiladi.
//
// "Tanish" — hozir ulangan YOKI yaqinda ulangan (qarang: registry.Known).
// Faqat "hozir ulangan" shartiga tayanib bo'lmaydi: agent qayta ulanayotgan
// bir necha soniyada TLS handshake rad etilib, tashrifchi 502 o'rniga
// brauzerdagi xavfsizlik xatosini ko'rardi.
func NewAutocertManager(certDir string, reg *registry.Registry) *autocert.Manager {
	return &autocert.Manager{
		Prompt: autocert.AcceptTOS,
		Cache:  autocert.DirCache(certDir),
		HostPolicy: func(_ context.Context, host string) error {
			if reg.Known(host) {
				return nil
			}
			return fmt.Errorf("'%s' tanish emas — sertifikat berilmaydi", host)
		},
	}
}

// TLSConfigFor — autocert manager asosida https server uchun tls.Config.
func TLSConfigFor(m *autocert.Manager) *tls.Config {
	return m.TLSConfig()
}
