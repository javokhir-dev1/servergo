package vpstunnel

// Uchdan-uchga sinov: haqiqiy relay binari + haqiqiy agent (manager/client)
// + haqiqiy lokal servis. Maqsad — ikki tomon o'rtasidagi ULANISHNI
// tekshirish: handshake formati, ulanishlar pool'i va oqim ustidagi HTTP.
// Paketlarning ichki mantiqi alohida testlarda (relay/internal/registry,
// relay/internal/proxy) tekshiriladi.
//
// Sekin va tashqi buyruqqa (`go build`) bog'liq, shuning uchun `-short`
// rejimida o'tkazib yuboriladi.

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"servergo/internal/vpstunnel/manager"
	"servergo/internal/vpstunnel/store"
)

const e2eToken = "e2e-sinov-tokeni"

// freePort — bo'sh portni band qilib, darhol qo'yib yuboradi. Kichik poyga
// bor (port oradan boshqasiga tegishi mumkin), lekin sinov muhitida yetarli.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("port olinmadi: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// buildRelay — relay binarini quradi (alohida Go moduli).
func buildRelay(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	relayDir := filepath.Join(wd, "..", "..", "relay")
	if _, err := os.Stat(relayDir); err != nil {
		t.Skipf("relay moduli topilmadi: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "servergo-relay")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/servergo-relay")
	cmd.Dir = relayDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("relay qurilmadi: %v\n%s", err, out)
	}
	return bin
}

var e2eFingerprintRe = regexp.MustCompile(`fingerprint \(SHA256\): ([0-9a-f]{64})`)

// startRelay — relay'ni dev-TLS rejimida ishga tushiradi. Fingerprint bilan
// birga loglarni tekshirish funksiyasini qaytaradi (pool holatini shu orqali
// kuzatamiz).
func startRelay(t *testing.T, bin string, controlPort, httpsPort int) (string, func(string) bool) {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"RELAY_TOKEN="+e2eToken,
		"RELAY_DEV_TLS=1",
		fmt.Sprintf("RELAY_CONTROL_ADDR=127.0.0.1:%d", controlPort),
		fmt.Sprintf("RELAY_HTTPS_ADDR=127.0.0.1:%d", httpsPort),
		"RELAY_CERT_DIR="+t.TempDir(),
	)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("stderr: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("relay ishga tushmadi: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	var mu sync.Mutex
	var logs strings.Builder
	fpCh := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			line := sc.Text()
			mu.Lock()
			logs.WriteString(line + "\n")
			mu.Unlock()
			if m := e2eFingerprintRe.FindStringSubmatch(line); len(m) == 2 {
				select {
				case fpCh <- m[1]:
				default:
				}
			}
		}
	}()
	// Relay loglarini test yordamchisiga ulab qo'yamiz — muvaffaqiyatsizlikda
	// sabab shu yerda ko'rinadi.
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		if t.Failed() {
			t.Logf("relay loglari:\n%s", logs.String())
		}
	})
	logContains := func(sub string) bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(logs.String(), sub)
	}

	select {
	case fp := <-fpCh:
		return fp, logContains
	case <-time.After(15 * time.Second):
		t.Fatal("relay fingerprint'ni 15 soniyada chiqarmadi")
	}
	return "", logContains
}

// waitFor — shart bajarilishini kutadi.
func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return cond()
}

// startOrigin — tunnel ortidagi "lokal servis".
func startOrigin(t *testing.T, port int, body string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, body)
	})
	// Protokol almashinuvi (WebSocket'ning soddalashtirilgan modeli):
	// ReverseProxy javob tanasini ikki tomonlama kanal sifatida ishlatishi
	// kerak — bu yo'l ilgari umuman ishlamas edi.
	mux.HandleFunc("/upgrade", func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijack yo'q", http.StatusInternalServerError)
			return
		}
		conn, rw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: sinov\r\nConnection: Upgrade\r\n\r\n")
		_ = rw.Flush()
		line, err := rw.ReadString('\n')
		if err != nil {
			return
		}
		_, _ = rw.WriteString("javob:" + line)
		_ = rw.Flush()
	})

	srv := &http.Server{Handler: mux}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("origin tinglamadi: %v", err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
}

func TestEndToEndTunnel(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: uchdan-uchga sinov o'tkazib yuborildi")
	}
	// Store va loglar sinov papkasida qolsin — foydalanuvchining haqiqiy
	// ~/.config/servergo/vpstunnel bazasiga tegmaymiz.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	const hostname = "e2e.sinov"
	const originBody = "salom-tunneldan"

	relayBin := buildRelay(t)
	controlPort, httpsPort, originPort := freePort(t), freePort(t), freePort(t)

	startOrigin(t, originPort, originBody)
	fp, relayLogContains := startRelay(t, relayBin, controlPort, httpsPort)

	st, err := store.Open()
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()

	p := store.Project{
		ID:         "e2e-loyiha",
		Name:       "e2e",
		Port:       originPort,
		Subdomain:  "e2e",
		BaseDomain: "sinov",
		Protocol:   "http",
	}
	if err := st.SaveProject(p); err != nil {
		t.Fatalf("loyiha saqlanmadi: %v", err)
	}
	if p.Hostname() != hostname {
		t.Fatalf("hostname %q, kutilgan %q", p.Hostname(), hostname)
	}

	m := manager.New(st, func(string, ...interface{}) {})
	m.SetRelayConfig(manager.RelayConfig{
		Addr:        fmt.Sprintf("127.0.0.1:%d", controlPort),
		Token:       e2eToken,
		Fingerprint: fp,
	})
	if err := m.Start(p); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(m.StopAll)

	// Pool to'lishini kutamiz: ulanishlar connStagger bilan navbatma-navbat
	// ochiladi, shuning uchun ikkinchisi bir necha soniyadan keyin keladi.
	if !waitFor(30*time.Second, func() bool { return relayLogContains("pool=2") }) {
		t.Fatal("relay ikkita ulanishni ko'rmadi — pool ishlamayapti")
	}

	cli := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: hostname},
		},
	}
	url := fmt.Sprintf("https://127.0.0.1:%d/", httpsPort)

	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Host = hostname
	resp, err := cli.Do(req)
	if err != nil {
		t.Fatalf("tunnel orqali so'rov: %v", err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if string(got) != originBody {
		t.Fatalf("javob %q, kutilgan %q (status %d)", got, originBody, resp.StatusCode)
	}

	// Ketma-ket so'rovlar — round-robin ikkala sessiyani ham ishlatadi.
	for i := 0; i < 10; i++ {
		r, _ := http.NewRequest(http.MethodGet, url, nil)
		r.Host = hostname
		rs, err := cli.Do(r)
		if err != nil {
			t.Fatalf("so'rov #%d: %v", i, err)
		}
		b, _ := io.ReadAll(rs.Body)
		rs.Body.Close()
		if string(b) != originBody {
			t.Fatalf("so'rov #%d javobi %q", i, b)
		}
	}
}

func TestEndToEndProtocolUpgrade(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: uchdan-uchga sinov o'tkazib yuborildi")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	const hostname = "ws.sinov"
	relayBin := buildRelay(t)
	controlPort, httpsPort, originPort := freePort(t), freePort(t), freePort(t)

	startOrigin(t, originPort, "-")
	fp, relayLogContains := startRelay(t, relayBin, controlPort, httpsPort)

	st, err := store.Open()
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()
	p := store.Project{ID: "ws-loyiha", Name: "ws", Port: originPort, Subdomain: "ws", BaseDomain: "sinov", Protocol: "http"}
	if err := st.SaveProject(p); err != nil {
		t.Fatalf("loyiha saqlanmadi: %v", err)
	}

	m := manager.New(st, func(string, ...interface{}) {})
	m.SetRelayConfig(manager.RelayConfig{
		Addr:        fmt.Sprintf("127.0.0.1:%d", controlPort),
		Token:       e2eToken,
		Fingerprint: fp,
	})
	if err := m.Start(p); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(m.StopAll)

	if !waitFor(30*time.Second, func() bool { return relayLogContains("pool=") }) {
		t.Fatal("agent relay'ga ulanmadi")
	}

	conn, err := tls.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", httpsPort),
		&tls.Config{InsecureSkipVerify: true, ServerName: hostname})
	if err != nil {
		t.Fatalf("TLS dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))

	fmt.Fprintf(conn, "GET /upgrade HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: sinov\r\n\r\n", hostname)

	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("javob o'qilmadi: %v", err)
	}
	if !strings.Contains(status, "101") {
		t.Fatalf("101 kutilgan edi, kelgani: %q", strings.TrimSpace(status))
	}
	// Sarlavhalarni oxirigacha o'qiymiz.
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("sarlavha o'qilmadi: %v", err)
		}
		if strings.TrimSpace(line) == "" {
			break
		}
	}
	// 101 dan keyingi ikki tomonlama almashinuv — aynan shu yo'l ilgari
	// buzilgan edi (javob tanasi io.ReadWriteCloser emasligi sababli).
	if _, err := io.WriteString(conn, "salom\n"); err != nil {
		t.Fatalf("upgrade'dan keyin yozilmadi: %v", err)
	}
	echo, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("upgrade'dan keyin o'qilmadi: %v", err)
	}
	if strings.TrimSpace(echo) != "javob:salom" {
		t.Fatalf("almashinuv natijasi %q, kutilgan %q", strings.TrimSpace(echo), "javob:salom")
	}
}
