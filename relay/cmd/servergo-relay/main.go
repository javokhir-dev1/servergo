// servergo-relay — VPS'da ishlaydigan reverse-tunnel relay.
// Lokal ServerGo nusxalari (agentlar) :control manzilga ulanadi (bir loyiha —
// bir ulanish), :443 orqali kelgan jamoatchilik so'rovlari Host header
// bo'yicha mos agentga yamux oqimi orqali uzatiladi. Sertifikatlar
// (jamoatchilik uchun) Let's Encrypt orqali avtomatik olinadi.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/net/netutil"

	"servergo-relay/internal/control"
	"servergo-relay/internal/proxy"
	"servergo-relay/internal/registry"
	"servergo-relay/internal/status"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envInt — musbat butun son bo'lsa o'sha, aks holda standart qiymat.
func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
		log.Printf("%s qiymati noto'g'ri (%q) — standart ishlatiladi: %d", key, v, def)
	}
	return def
}

// Jamoatchilik serverlari uchun vaqt chegaralari.
//
// readHeaderTimeout — Slowloris'ga qarshi ASOSIY himoya: sarlavhani
// tomchilab yuboradigan ulanish shu vaqtdan keyin uziladi. Go'da standart
// qiymat — cheksiz, ya'ni bittagina mashina minglab ulanish ochib relay'ni
// yiqita olardi.
//
// ReadTimeout va WriteTimeout ATAYLAB qo'yilmagan:
//   - WriteTimeout WebSocket va boshqa 101 (protokol almashinuvi)
//     ulanishlarini o'rtasidan uzadi — relay ularni qo'llab-quvvatlaydi.
//   - ReadTimeout katta fayl yuklashni (uzoq davom etadigan tana) uzadi.
//
// Ikkalasining o'rniga sekin TANA hujumi (R-U-Dead-Yet) VPS tomonidagi
// nftables qoidalari bilan cheklanadi: bir IP'dan bir vaqtda 64 ta ulanish.
const (
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 120 * time.Second
	maxHeaderBytes    = 64 << 10 // 64 KB (Go standarti 1 MB)
)

// Bir vaqtda ochiq bo'lishi mumkin bo'lgan ulanishlar soni. Chegaradan
// oshgan ulanish listener darajasida kutib turadi — Go goroutine yaratmaydi,
// ya'ni xotira o'smaydi. LimitNOFILE=65535 va MemoryMax=448M bilan mos.
const (
	defaultMaxPublicConns  = 4096
	defaultMaxControlConns = 256
)

func main() {
	token := os.Getenv("RELAY_TOKEN")
	if token == "" {
		log.Fatal("RELAY_TOKEN muhit o'zgaruvchisi majburiy (masalan: openssl rand -hex 32)")
	}
	controlAddr := envOr("RELAY_CONTROL_ADDR", ":9443")
	httpAddr := envOr("RELAY_HTTP_ADDR", ":80")
	httpsAddr := envOr("RELAY_HTTPS_ADDR", ":443")
	certDir := envOr("RELAY_CERT_DIR", "/var/lib/servergo-relay/certs")
	devTLS := os.Getenv("RELAY_DEV_TLS") == "1" // localhost sinovlari uchun — Let's Encrypt o'rniga o'z-o'zidan imzolangan sertifikat

	if err := os.MkdirAll(certDir, 0o700); err != nil {
		log.Fatalf("sertifikat papkasi yaratilmadi: %v", err)
	}

	// Diagnostika: faqat loopback'da, ya'ni faqat VPS'ga SSH bilan kirgan
	// odam ko'ra oladi. Bo'sh qiymat berilsa umuman ko'tarilmaydi.
	statusAddr := envOr("RELAY_STATUS_ADDR", "127.0.0.1:9090")
	boshlandi := time.Now()

	reg := registry.New()
	hisoblagich := &proxy.Hisoblagichlar{}

	// --- Control: agentlar shu yerga uladi ---
	controlCert, fingerprint, err := loadOrCreateControlCert(certDir)
	if err != nil {
		log.Fatalf("control sertifikati tayyorlanmadi: %v", err)
	}
	log.Printf("control fingerprint (SHA256): %s — buni ServerGo'dagi \"VPS Tunnel\" sozlamalariga kiriting", fingerprint)

	controlLn, err := net.Listen("tcp", controlAddr)
	if err != nil {
		log.Fatalf("control portini tinglab bo'lmadi (%s): %v", controlAddr, err)
	}
	maxControlConns := envInt("RELAY_MAX_CONTROL_CONNS", defaultMaxControlConns)
	controlLn = netutil.LimitListener(controlLn, maxControlConns)
	log.Printf("control ulanish chegarasi: %d", maxControlConns)
	controlTLSCfg := &tls.Config{Certificates: []tls.Certificate{controlCert}}
	go func() {
		log.Printf("control tinglanmoqda: %s", controlAddr)
		if err := control.Serve(controlLn, controlTLSCfg, token, reg); err != nil {
			log.Fatalf("control server to'xtadi: %v", err)
		}
	}()

	if statusAddr != "" {
		if _, err := status.Serve(statusAddr, reg, hisoblagich, boshlandi); err != nil {
			// Diagnostika ishlamasa ham relay o'z ishini davom ettiradi.
			log.Printf("holat serveri ko'tarilmadi (%s): %v", statusAddr, err)
		} else {
			log.Printf("holat serveri tinglanmoqda: %s (faqat loopback — 'ssh vps curl -s %s/holat')", statusAddr, statusAddr)
		}
	}

	// --- Jamoatchilik: :80 (ACME + redirect) va :443 (proxy) ---
	handler := proxy.NewProxyHandler(reg, hisoblagich)

	if devTLS {
		cert, err := selfSignedForAnyHost()
		if err != nil {
			log.Fatalf("dev TLS sertifikati yaratilmadi: %v", err)
		}
		srv := &http.Server{
			Addr:              httpsAddr,
			Handler:           handler,
			TLSConfig:         &tls.Config{Certificates: []tls.Certificate{cert}},
			ReadHeaderTimeout: readHeaderTimeout,
			IdleTimeout:       idleTimeout,
			MaxHeaderBytes:    maxHeaderBytes,
		}
		log.Printf("[DEV] https tinglanmoqda: %s (o'z-o'zidan imzolangan sertifikat)", httpsAddr)
		log.Fatal(srv.ListenAndServeTLS("", ""))
		return
	}

	maxPublicConns := envInt("RELAY_MAX_PUBLIC_CONNS", defaultMaxPublicConns)

	acm := proxy.NewAutocertManager(certDir, reg)

	go func() {
		log.Printf("http (ACME + redirect) tinglanmoqda: %s", httpAddr)
		redirectSrv := &http.Server{
			Addr: httpAddr,
			Handler: acm.HTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				target := "https://" + r.Host + r.URL.RequestURI()
				http.Redirect(w, r, target, http.StatusMovedPermanently)
			})),
			ReadHeaderTimeout: readHeaderTimeout,
			ReadTimeout:       30 * time.Second, // :80 da faqat ACME va redirect — uzun tana kutilmaydi
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       idleTimeout,
			MaxHeaderBytes:    maxHeaderBytes,
		}
		httpLn, err := net.Listen("tcp", httpAddr)
		if err != nil {
			log.Printf("http portini tinglab bo'lmadi (%s): %v", httpAddr, err)
			return
		}
		if err := redirectSrv.Serve(netutil.LimitListener(httpLn, maxPublicConns)); err != nil {
			log.Printf("http server to'xtadi: %v", err)
		}
	}()

	httpsSrv := &http.Server{
		Addr:              httpsAddr,
		Handler:           handler,
		TLSConfig:         proxy.TLSConfigFor(acm),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
	}
	httpsLn, err := net.Listen("tcp", httpsAddr)
	if err != nil {
		log.Fatalf("https portini tinglab bo'lmadi (%s): %v", httpsAddr, err)
	}
	log.Printf("https tinglanmoqda: %s (ulanish chegarasi %d, sarlavha kutish %s)",
		httpsAddr, maxPublicConns, readHeaderTimeout)
	log.Fatal(httpsSrv.ServeTLS(netutil.LimitListener(httpsLn, maxPublicConns), "", ""))
}

// loadOrCreateControlCert — control kanali uchun uzoq muddatli (10 yil)
// o'z-o'zidan imzolangan sertifikat; diskda saqlanadi, shunda qayta ishga
// tushirilganda ham agentlarning fingerprint sozlamasi buzilmaydi.
func loadOrCreateControlCert(certDir string) (tls.Certificate, string, error) {
	certPath := filepath.Join(certDir, "control-cert.pem")
	keyPath := filepath.Join(certDir, "control-key.pem")

	if _, err := os.Stat(certPath); err == nil {
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return tls.Certificate{}, "", err
		}
		return cert, fingerprintOf(cert.Certificate[0]), nil
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, "", err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "servergo-relay-control"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, "", err
	}

	if err := writePEM(certPath, "CERTIFICATE", der, 0o644); err != nil {
		return tls.Certificate{}, "", err
	}
	if err := writePEM(keyPath, "EC PRIVATE KEY", keyBytes, 0o600); err != nil {
		return tls.Certificate{}, "", err
	}

	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	return cert, fingerprintOf(der), nil
}

func fingerprintOf(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// writePEM — DER baytlarni PEM formatida faylga yozadi.
func writePEM(path, blockType string, der []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: blockType, Bytes: der})
}

// selfSignedForAnyHost — RELAY_DEV_TLS=1 rejimi uchun: har qanday SNI'ga mos
// (localhost bilan sinash uchun), Let's Encrypt shart emas.
func selfSignedForAnyHost() (tls.Certificate, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "servergo-relay-dev"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost", "*.local"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes}),
	)
}
