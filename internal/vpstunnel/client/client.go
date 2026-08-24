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
	"net"
	"time"

	"github.com/hashicorp/yamux"
)

// Config — bitta loyiha uchun ulanish parametrlari.
type Config struct {
	RelayAddr   string // "host:port" — control manzil
	Fingerprint string // relay control sertifikatining SHA256 (hex)
	Token       string
	ProjectID   string
	Hostname    string
	LocalPort   int
}

const dialTimeout = 10 * time.Second

// Run — bitta ulanish davri: bloklaydi, sessiya uzilguncha yoki ctx bekor
// qilinguncha ishlaydi. onUp — ulanish tasdiqlangach (bitta marta)
// chaqiriladi. onLog — sarlavhali diagnostika xabarlari uchun.
func Run(ctx context.Context, cfg Config, onUp func(), onLog func(string)) error {
	dialer := &net.Dialer{Timeout: dialTimeout}
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
	}); err != nil {
		_ = tlsConn.Close()
		return err
	}

	sess, err := yamux.Client(tlsConn, yamux.DefaultConfig())
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
