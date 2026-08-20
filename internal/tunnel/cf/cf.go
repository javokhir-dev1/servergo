// Package cf — cloudflared binary bilan ishlash (TZ 2.4).
// Har bir buyruq va uning natijasi applog orqali yoziladi.
package cf

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"servergo/internal/tunnel/applog"
)

type Client struct {
	Path string // cloudflared binary yo'li

	// NeutralConfig — bo'sh config fayli yo'li. Boshqaruv buyruqlariga beriladi,
	// shunda cloudflared foydalanuvchining ~/.cloudflared/config.yml faylidagi
	// `tunnel:` qiymatini buyruq argumentidan ustun qo'ymaydi.
	NeutralConfig string
}

// run — boshqaruv buyrug'ini standart (cloudflared default) sertifikat bilan
// bajaradi. Domenga xos sertifikat kerak bo'lgan buyruqlar uchun runCert'ga
// qarang.
func (c *Client) run(label string, args ...string) (string, error) {
	return c.runCert(label, "", args...)
}

// runCert — boshqaruv buyrug'ini bajaradi va loglaydi. certPath berilsa
// --origincert bilan aynan shu sertifikat ishlatiladi — hisob bir nechta
// domenga (zonaga) ega bo'lsa, har biri o'z login'idan olingan alohida
// sertifikatga ega bo'lishi kerak (qarang: LoginForDomain). Bo'sh bo'lsa,
// cloudflared'ning o'z standart ~/.cloudflared/cert.pem'i ishlatiladi.
func (c *Client) runCert(label, certPath string, args ...string) (string, error) {
	full := make([]string, 0, len(args)+4)
	if c.NeutralConfig != "" {
		full = append(full, "--config", c.NeutralConfig)
	}
	if certPath != "" {
		full = append(full, "--origincert", certPath)
	}
	full = append(full, args...)
	applog.Cmd(c.Path, full)
	out, err := exec.Command(c.Path, full...).CombinedOutput()
	applog.CmdResult(label, err, string(out))
	return string(out), err
}

var loginURLRe = regexp.MustCompile(`https://dash\.cloudflare\.com/argotunnel\?\S+`)

// runLoginStream — `tunnel login`ni oqim sifatida bajaradi. cloudflared login
// URL'ini chiqishi bilanoq chop etadi, keyin brauzer o'zi ochilishini kutadi —
// lekin ba'zi muhitlarda (masalan snap-brauzer, RDP sessiya, DISPLAY/DBUS
// yo'q) avtomatik ochilish ishlamaydi. Shu sabab URL topilishi bilan
// onURL(url) chaqiramiz — chaqiruvchi buni foydalanuvchiga ko'rsatib, qo'lda
// bosish imkonini beradi (cloudflared o'zi ham xuddi shu URL'ni "brauzer
// ochilmasa qo'lda oching" deb taklif qiladi).
func (c *Client) runLoginStream(label string, onURL func(url string)) (string, error) {
	full := []string{}
	if c.NeutralConfig != "" {
		full = append(full, "--config", c.NeutralConfig)
	}
	full = append(full, "tunnel", "login")
	applog.Cmd(c.Path, full)

	cmd := exec.Command(c.Path, full...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}

	var buf strings.Builder
	var mu sync.Mutex
	found := false
	scan := func(r io.Reader) {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 4096), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			mu.Lock()
			buf.WriteString(line)
			buf.WriteByte('\n')
			if !found {
				if m := loginURLRe.FindString(line); m != "" {
					found = true
					if onURL != nil {
						onURL(m)
					}
				}
			}
			mu.Unlock()
		}
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); scan(stdout) }()
	go func() { defer wg.Done(); scan(stderr) }()
	wg.Wait()

	err = cmd.Wait()
	out := buf.String()
	applog.CmdResult(label, err, out)
	return out, err
}

// Find — cloudflared'ni PATH'dan qidiradi va versiyasini qaytaradi (FR-1.1).
// neutralConfig — bo'sh config fayli yo'li (store.NeutralConfig()).
func Find(customPath, neutralConfig string) (*Client, string, error) {
	path := customPath
	if path == "" {
		p, err := exec.LookPath("cloudflared")
		if err != nil {
			applog.Warn("cloudflared PATH'dan topilmadi")
			return nil, "", fmt.Errorf("cloudflared topilmadi: %w", err)
		}
		path = p
	}
	out, err := exec.Command(path, "--version").CombinedOutput()
	if err != nil {
		applog.Error("cloudflared versiyasi aniqlanmadi (%s): %v", path, err)
		return nil, "", fmt.Errorf("cloudflared versiyasini aniqlab bo'lmadi: %w", err)
	}
	ver := strings.TrimSpace(string(out))
	applog.Info("cloudflared topildi: %s (%s)", path, ver)

	// Foydalanuvchida global config bo'lsa — ogohlantiramiz (u bizga xalaqit berardi).
	if home, herr := os.UserHomeDir(); herr == nil {
		gc := filepath.Join(home, ".cloudflared", "config.yml")
		if _, serr := os.Stat(gc); serr == nil {
			applog.Warn("Global config topildi: %s — ServerGo uni neytrallashtiradi (--config)", gc)
		}
	}
	return &Client{Path: path, NeutralConfig: neutralConfig}, ver, nil
}

// CertPath — ~/.cloudflared/cert.pem (login natijasi).
func CertPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cloudflared", "cert.pem")
}

func CertExists() bool {
	st, err := os.Stat(CertPath())
	return err == nil && st.Size() > 0
}

// backupCertPath — LoginForDomain cert.pem'ni vaqtincha chetga olib
// qo'yayotganda ishlatadigan joy.
func backupCertPath() string { return CertPath() + ".servergo-bak" }

// RecoverInterruptedLogin — dastur ishga tushganda chaqiriladi. Agar
// LoginForDomain davomida (masalan xizmat qayta ishga tushirilgani yoki
// jarayon o'ldirilgani sabab) to'xtab qolgan bo'lsa, cert.pem chetga olib
// qo'yilgan holicha qolib ketishi mumkin — bu holda asl domen "chiqib
// ketilgan" bo'lib ko'rinadi. Zaxira nusxa topilsa, joyiga qaytaradi.
// Qaytargan bo'lsa true beradi (chaqiruvchi loglashi uchun).
func RecoverInterruptedLogin() bool {
	if CertExists() {
		return false
	}
	backup := backupCertPath()
	if st, err := os.Stat(backup); err != nil || st.Size() == 0 {
		return false
	}
	if err := os.Rename(backup, CertPath()); err != nil {
		return false
	}
	return true
}

// Login — brauzer orqali autentifikatsiya (FR-1.2). Bloklovchi chaqiruv.
// onURL — cloudflared chiqargan login havolasi topilishi bilan chaqiriladi
// (brauzer avtomatik ochilmasa, foydalanuvchi qo'lda bosishi/nusxalashi
// uchun). nil bo'lishi mumkin.
func (c *Client) Login(onURL func(string)) error {
	applog.Info("Cloudflare login boshlandi — brauzer ochilishi kutilmoqda")
	out, err := c.runLoginStream("tunnel login", onURL)
	if err != nil {
		return fmt.Errorf("login xatosi: %s", applog.Tail(out, 6))
	}
	if !CertExists() {
		applog.Error("login tugadi, lekin cert.pem topilmadi: %s", CertPath())
		return fmt.Errorf("login yakunlanmadi: cert.pem topilmadi")
	}
	applog.Info("Cloudflare login muvaffaqiyatli")
	return nil
}

// certDir — domenlarga xos sertifikat nusxalari saqlanadigan papka.
// cloudflared login har doim bitta joyga (CertPath) yozadi, shuning uchun
// har bir domen uchun login qilingandan keyin natija shu yerga nusxalanadi.
func certDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cloudflared", "servergo-domains")
}

var domainFileRe = regexp.MustCompile(`[^a-z0-9.-]+`)

// CertPathFor — berilgan domen uchun saqlangan sertifikat yo'li.
func CertPathFor(domain string) string {
	safe := domainFileRe.ReplaceAllString(strings.ToLower(domain), "_")
	return filepath.Join(certDir(), safe+".pem")
}

// DomainAuthorized — bu domen uchun alohida login qilinganmi (sertifikat
// saqlanganmi)?
func DomainAuthorized(domain string) bool {
	st, err := os.Stat(CertPathFor(domain))
	return err == nil && st.Size() > 0
}

// LoginForDomain — cloudflared hisobingiz bir nechta domenga (zonaga) ega
// bo'lsa, har biri uchun ALOHIDA login kerak: cert.pem faqat login paytida
// brauzerda tanlangan bitta zonaga yozish huquqi beradi. Shu login natijasini
// domenga xos faylga nusxalab qo'yamiz, aks holda keyingi domen uchun
// qilingan login avvalgisini almashtirib yuboradi va DNS buyruqlari
// (route dns) sukut bo'yicha noto'g'ri zonaga yozilib qoladi.
//
// cloudflared allaqachon cert.pem mavjud bo'lsa, login'ni RAD ETADI —
// brauzerni umuman ochmasdan, "existing certificate" xabari bilan (lekin
// exit kodi baribir 0!). Shu sabab mavjud cert.pem'ni vaqtincha chetga
// olib qo'yamiz, shundagina cloudflared haqiqatan brauzer ochadi; keyin
// asl faylni joyiga qaytaramiz — u hali "legacy" domenga tegishli bo'lib
// qoladi.
//
// Bloklovchi chaqiruv — brauzer ochiladi, foydalanuvchi ro'yxatdan aynan
// `domain`ni tanlashi kerak. onURL — login havolasi topilishi bilan
// chaqiriladi (brauzer avtomatik ochilmasa qo'lda bosish uchun); nil bo'lishi
// mumkin.
func (c *Client) LoginForDomain(domain string, onURL func(string)) (string, error) {
	backup := backupCertPath()
	hadExisting := CertExists()
	if hadExisting {
		if err := os.Rename(CertPath(), backup); err != nil {
			return "", fmt.Errorf("mavjud cert.pem chetga olinmadi: %w", err)
		}
	}
	// Har qanday chiqish yo'lida asl cert.pem joyiga qaytishi shart —
	// aks holda "legacy" domen sertifikatsiz qolib ketadi.
	restore := func() {
		if !hadExisting {
			return
		}
		if _, err := os.Stat(backup); err == nil {
			_ = os.Remove(CertPath())
			_ = os.Rename(backup, CertPath())
		}
	}

	applog.Info("Cloudflare login boshlandi ('%s' uchun) — brauzerda aynan shu domenni tanlang", domain)
	out, err := c.runLoginStream("tunnel login ("+domain+")", onURL)
	if err != nil {
		restore()
		return "", fmt.Errorf("login xatosi: %s", applog.Tail(out, 6))
	}
	if !CertExists() {
		restore()
		if strings.Contains(out, "existing certificate") {
			return "", fmt.Errorf("cloudflared eski sertifikatni chetlashtirib bo'lmadi — qayta urinib ko'ring")
		}
		return "", fmt.Errorf("login yakunlanmadi: cert.pem topilmadi. Chiqish: %s", applog.Tail(out, 6))
	}

	data, err := os.ReadFile(CertPath())
	if err != nil {
		restore()
		return "", fmt.Errorf("cert.pem o'qilmadi: %w", err)
	}
	dst := CertPathFor(domain)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		restore()
		return "", err
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		restore()
		return "", fmt.Errorf("sertifikat saqlanmadi: %w", err)
	}
	restore()
	applog.Info("'%s' uchun sertifikat saqlandi: %s", domain, dst)
	return dst, nil
}

var uuidRe = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// CreateTunnel — named tunnel yaratadi, credentials'ni credFile'ga yozadi (FR-2.4).
// Tunnel UUID'sini qaytaradi. certPath — loyihaning bazaviy domeniga xos
// sertifikat (bo'sh bo'lsa cloudflared standart cert.pem'ini ishlatadi).
func (c *Client) CreateTunnel(name, credFile, certPath string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(credFile), 0o700); err != nil {
		applog.Error("papka yaratilmadi (%s): %v", filepath.Dir(credFile), err)
		return "", err
	}
	out, err := c.runCert("tunnel create "+name, certPath, "tunnel", "create", "--credentials-file", credFile, name)
	if err != nil {
		return "", fmt.Errorf("tunnel yaratib bo'lmadi: %s", applog.Tail(out, 6))
	}
	_ = os.Chmod(credFile, 0o600) // TZ 7: xavfsizlik

	// UUID'ni credentials faylidan olamiz — bu eng ishonchli manba.
	// (Chiqish matnidagi regexp fayl yo'lidagi UUID'ni ham ushlab qolishi mumkin.)
	id, err := TunnelIDFromCreds(credFile)
	if err != nil {
		// Zaxira variant: chiqishdagi "with id <uuid>" naqshi
		if m := createdIDRe.FindStringSubmatch(out); len(m) == 2 {
			id = m[1]
		}
	}
	if id == "" {
		applog.Error("tunnel UUID aniqlanmadi (credentials: %s)", credFile)
		return "", fmt.Errorf("tunnel UUID aniqlanmadi. Chiqish: %s", applog.Tail(out, 6))
	}
	applog.Info("Tunnel yaratildi: %s (UUID %s)", name, id)
	return id, nil
}

var createdIDRe = regexp.MustCompile(`(?i)with id\s+([0-9a-f-]{36})`)

// credentials — cloudflared credentials JSON tuzilmasi.
type credentials struct {
	TunnelID string `json:"TunnelID"`
}

// TunnelIDFromCreds — credentials faylidan tunnel UUID'sini o'qiydi.
func TunnelIDFromCreds(credFile string) (string, error) {
	data, err := os.ReadFile(credFile)
	if err != nil {
		return "", err
	}
	var c credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return "", err
	}
	if !uuidRe.MatchString(c.TunnelID) {
		return "", fmt.Errorf("credentials faylida TunnelID yo'q")
	}
	return c.TunnelID, nil
}

// ErrDNSExists — subdomen uchun DNS yozuvi allaqachon mavjud.
// Foydalanuvchi tasdiqlasa RouteDNS(overwrite=true) bilan qayta urinish mumkin.
var ErrDNSExists = fmt.Errorf("DNS yozuvi allaqachon mavjud")

// RouteDNS — subdomen uchun CNAME yozuvi (FR-2.4).
// overwrite=true bo'lsa mavjud yozuv almashtiriladi (eski/o'chirilgan tunnel qolgan holat).
// certPath — hostname qaysi domenga tegishli bo'lsa, o'sha domenning
// sertifikati (bo'sh bo'lsa standart cert.pem). Noto'g'ri (boshqa domen)
// sertifikat bilan chaqirilsa, cloudflared xato bermay, yozuvni sertifikat
// zonasiga noto'g'ri joylashtirib qo'yishi mumkin — shu sabab bu muhim.
func (c *Client) RouteDNS(tunnelName, hostname, certPath string, overwrite bool) error {
	args := []string{"tunnel", "route", "dns"}
	if overwrite {
		args = append(args, "--overwrite-dns")
	}
	args = append(args, tunnelName, hostname)

	out, err := c.runCert("tunnel route dns "+hostname, certPath, args...)
	if err != nil {
		msg := applog.Tail(out, 6)
		if strings.Contains(msg, "already exists") || strings.Contains(msg, "1003") {
			return ErrDNSExists
		}
		return fmt.Errorf("DNS sozlashda xato: %s", msg)
	}
	applog.Info("DNS yozuvi sozlandi: %s → %s", hostname, tunnelName)
	return nil
}

// TunnelInfo — tunnel haqida ma'lumot (diagnostika uchun).
func (c *Client) TunnelInfo(name string) (string, error) {
	out, err := c.run("tunnel info "+name, "tunnel", "info", name)
	return out, err
}

// LookupCNAME — hostname qayerga ko'rsatayotganini tekshiradi (diagnostika).
// Cloudflare tunnel uchun kutilgan natija: <tunnel-uuid>.cfargotunnel.com
func LookupCNAME(hostname string) (string, error) {
	cname, err := net.LookupCNAME(hostname)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(cname, "."), nil
}

// DeleteTunnel — tunnelni o'chiradi (FR-3.4). Avval cleanup qilinadi.
// certPath — tunnel qaysi domenga tegishli bo'lsa, o'sha domenning sertifikati.
func (c *Client) DeleteTunnel(name, certPath string) error {
	_, _ = c.runCert("tunnel cleanup "+name, certPath, "tunnel", "cleanup", name)
	time.Sleep(500 * time.Millisecond)
	out, err := c.runCert("tunnel delete "+name, certPath, "tunnel", "delete", name)
	if err != nil {
		return fmt.Errorf("tunnelni o'chirib bo'lmadi: %s", applog.Tail(out, 6))
	}
	applog.Info("Tunnel o'chirildi: %s", name)
	return nil
}

// WriteConfig — config.yml generatsiyasi (TZ 3.5).
func WriteConfig(cfgPath, tunnelID, credFile, hostname string, port int, protocol string) error {
	if protocol == "" {
		protocol = "http"
	}
	// HTTPS origin — lokal serverlarda deyarli har doim self-signed sertifikat
	// bo'ladi, shuning uchun sertifikat tekshiruvini o'chiramiz.
	extra := ""
	if protocol == "https" {
		extra = "    originRequest:\n      noTLSVerify: true\n"
	}
	yml := fmt.Sprintf(`tunnel: %s
credentials-file: %s

ingress:
  - hostname: %s
    service: %s://localhost:%d
%s  - service: http_status:404
`, tunnelID, credFile, hostname, protocol, port, extra)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(cfgPath, []byte(yml), 0o600); err != nil {
		applog.Error("config.yml yozilmadi (%s): %v", cfgPath, err)
		return err
	}
	applog.Info("config.yml yozildi: %s → %s://localhost:%d", hostname, protocol, port)
	return nil
}
