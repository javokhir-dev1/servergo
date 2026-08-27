// Package client — bitta VPS Tunnel loyihasi uchun relay bilan ulanish:
// TLS dial (fingerprint pinning bilan) → handshake → yamux sessiya → har
// qabul qilingan oqimni 127.0.0.1:PORT ga xom baytlarda ko'chirish.
//
// HTTP semantikasi (so'rov/javob) bu tomonda umuman ishlatilmaydi — relay
// tomonidagi RoundTripper allaqachon to'g'ri HTTP/1.1 so'rov baytlarini
// yozadi va javobni o'sha formatda kutadi, shuning uchun bu yerda faqat
// TCP darajasida ko'chirish yetarli (qarang: relay/internal/proxy).
package client

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"time"

	"github.com/hashicorp/yamux"
)

// Config — bitta ULANISH uchun parametrlar. Bir loyiha uchun bir nechta
// ulanish ochiladi (qarang: manager.connsPerProject), ular faqat ConnIndex
// bilan farq qiladi.
type Config struct {
	RelayAddr   string // "host:port" — control manzil
	Fingerprint string // relay control sertifikatining SHA256 (hex)
	Token       string
	ProjectID   string
	Hostname    string
	LocalPort   int
	ConnIndex   int // shu loyihaning nechanchi ulanishi (0 dan boshlab)
}

const dialTimeout = 10 * time.Second

// tcpKeepAlive — OS darajasidagi TCP keepalive davri. Bu NAT/firewall
// jadvalidagi ulanish yozuvini tirik ushlab turishga yordam beradi (bo'sh
// TCP ulanishlar ko'p marshrutizatorlarda ~1-2 daqiqada tozalanadi).
const tcpKeepAlive = 15 * time.Second

// keepAliveInterval — yamux ping davri. Qisqa bo'lgani NAT/firewall
// jadvalidagi yozuvni yangilab turadi.
const keepAliveInterval = 15 * time.Second

// stallTolerance — ping javobi (pong) kutiladigan eng uzoq vaqt, ya'ni
// sessiyani o'ldirmasdan chidab beriladigan "qotish" uzunligi.
//
// Nima uchun bunchalik uzun: o'lchovlar shuni ko'rsatdiki, agent va relay
// orasidagi yo'lda TCP oqimi vaqti-vaqti bilan o'nlab soniyaga muzlab qoladi
// (bir vaqtning o'zida o'sha hostga ochilgan BARCHA ulanishlarda — port va
// protokoldan qat'i nazar), so'ng o'z-o'zidan tiklanadi va ulanish yaroqli
// bo'lib qolaveradi. yamux'ning standart 10s / oldingi 30s chegarasi bunday
// qotishni "ulanish o'ldi" deb baholab, sessiyani buzar edi: natijada tunnel
// har 2 daqiqada uzilib, sayt bir necha soniya 502 qaytarardi. Endi qotish
// shunchaki kechikishga aylanadi — oqim tiklanganda sessiya davom etadi.
//
// Bu qiymat ConnectionWriteTimeout sifatida ishlatiladi: u ham pong kutish
// muddati, ham oqimga yozish muddati. Haqiqatan o'lgan ulanish shu vaqtdan
// keyin aniqlanadi va manager darhol qayta ulanadi.
const stallTolerance = 120 * time.Second

// yamuxConfig — sozlamalar relay tomonidagi nusxasi bilan BIR XIL bo'lishi
// kerak: relay/internal/control/control.go.
func yamuxConfig(onLog func(string)) *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.KeepAliveInterval = keepAliveInterval
	cfg.ConnectionWriteTimeout = stallTolerance
	// yamux'ning ichki diagnostikasi loyiha logiga tushsin — uzilish sababini
	// (keepalive timeout, oqim xatosi, protokol xatosi) shusiz bilib bo'lmaydi.
	if onLog != nil {
		cfg.Logger = log.New(logWriter(onLog), "[yamux] ", 0)
		cfg.LogOutput = nil
	}
	return cfg
}

// logWriter — log.Logger chiqishini onLog chaqiruviga aylantiradi.
type logWriter func(string)

func (w logWriter) Write(p []byte) (int, error) {
	w(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// Run — bitta ulanish davri: bloklaydi, sessiya uzilguncha yoki ctx bekor
// qilinguncha ishlaydi. onUp — ulanish tasdiqlangach (bitta marta)
// chaqiriladi. onLog — sarlavhali diagnostika xabarlari uchun.
func Run(ctx context.Context, cfg Config, onUp func(), onLog func(string)) error {
	dialer := &net.Dialer{Timeout: dialTimeout, KeepAlive: tcpKeepAlive}
	rawConn, err := dialer.DialContext(ctx, "tcp", cfg.RelayAddr)
	if err != nil {
		return fmt.Errorf("relay'ga ulanib bo'lmadi (%s): %w", cfg.RelayAddr, err)
	}

	fp := cfg.Fingerprint
	tlsConn := tls.Client(rawConn, &tls.Config{
		InsecureSkipVerify: true, // sertifikat CA bilan emas, quyidagi fingerprint bilan tekshiriladi
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("relay sertifikat taqdim etmadi")
			}
			sum := sha256.Sum256(rawCerts[0])
			got := hex.EncodeToString(sum[:])
			if subtle.ConstantTimeCompare([]byte(got), []byte(fp)) != 1 {
				return fmt.Errorf("relay sertifikat barmoq izi mos kelmadi — MITM yoki noto'g'ri fingerprint sozlamasi bo'lishi mumkin")
			}
			return nil
		},
	})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = rawConn.Close()
		return fmt.Errorf("TLS handshake xato: %w", err)
	}

	if err := sendHandshake(tlsConn, handshake{
		Token:     cfg.Token,
		ProjectID: cfg.ProjectID,
		Hostname:  cfg.Hostname,
		ConnIndex: cfg.ConnIndex,
	}); err != nil {
		_ = tlsConn.Close()
		return err
	}

	sess, err := yamux.Client(tlsConn, yamuxConfig(onLog))
	if err != nil {
		_ = tlsConn.Close()
		return fmt.Errorf("yamux sessiya ochilmadi: %w", err)
	}
	defer sess.Close()

	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = sess.Close()
		case <-stop:
		}
	}()
	defer close(stop)

	if onUp != nil {
		onUp()
	}

	for {
		stream, err := sess.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("sessiya uzildi: %w", err)
		}
		go forward(stream, cfg.LocalPort, onLog)
	}
}

// forward — bitta oqimni 127.0.0.1:port ga ikki yo'nalishli ko'chiradi.
func forward(stream net.Conn, port int, onLog func(string)) {
	defer stream.Close()
	local, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
	if err != nil {
		if onLog != nil {
			onLog(fmt.Sprintf("localhost:%d ulanishni rad etdi: %v", port, err))
		}
		return
	}
	defer local.Close()

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(local, stream); done <- struct{}{} }()
	go func() { _, _ = io.Copy(stream, local); done <- struct{}{} }()
	<-done
}
