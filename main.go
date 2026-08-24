// ServerGo — PM2 jarayonlari, tizim xotirasi va Cloudflare tunnellarini
// bitta oynadan boshqarish uchun Ubuntu desktop dasturi.
//
// Tuzilishi: Go HTTP server loopback'da ishlaydi, UI esa webkit2gtk nativ
// oynasida ochiladi. Frontend binar ichiga embed qilingan.
//
// Uch rejim:
//
//	-daemon    oynasiz, systemd user service uchun — tunnellarni fonda ushlab turadi
//	(bayroqsiz) oyna; qulfni olsa o'zi server bo'ladi, ololmasa demonga ulanadi
//	-headless  oynasiz, manzilni stdout ga chiqaradi (nosozlikni tuzatish uchun)
//
// Bundan tashqari "servergo <buyruq> ..." ko'rinishidagi terminal buyruqlari
// bor (servergo help) — ular ishlab turgan nusxaga ulanib, pm2/RAM/tunnel
// bo'limlarini oynasiz boshqarish imkonini beradi. Qarang: internal/cli.
package main

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"servergo/internal/apps"
	"servergo/internal/cli"
	"servergo/internal/daemon"
	"servergo/internal/pm2"
	"servergo/internal/server"
	"servergo/internal/tunnel"
	"servergo/internal/vpstunnel"

	webview "github.com/webview/webview_go"
)

//go:embed web
var webAssets embed.FS

const (
	winWidth     = 1280
	winHeight    = 820
	winMinWidth  = 900
	winMinHeight = 560
)

// cliVerbs — "servergo <buyruq> ..." rejimida tanilgan birinchi so'zlar.
// Shu ro'yxatdagi biri bo'lsa, flag/lock mantig'iga umuman kirmaymiz —
// mavjud nusxaga ulanib, natijani chiqarib chiqamiz.
var cliVerbs = map[string]bool{
	"status": true, "ps": true, "ram": true, "apps": true, "tunnel": true, "tun": true, "help": true,
	"login": true, "logout": true, "sync": true, "vpstunnel": true, "vtun": true,
}

func main() {
	if len(os.Args) > 1 && cliVerbs[os.Args[1]] {
		os.Exit(cli.Run(os.Args[1:]))
	}

	headless := flag.Bool("headless", false, "oynasiz — manzilni chiqaradi (nosozlikni tuzatish uchun)")
	daemonMode := flag.Bool("daemon", false, "oynasiz fon rejimi — systemd user service uchun")
	flag.Parse()

	// Kim qulfni olsa — o'sha tunnellarning egasi. Bu ikkita cloudflared
	// jarayonining bir loyihaga urilishining oldini oladi.
	release, owner, err := daemon.TryLock()
	if err != nil {
		log.Fatalf("qulf faylini ochib bo'lmadi: %v", err)
	}

	if !owner {
		attach(*daemonMode, *headless)
		return
	}
	defer release()

	runOwner(*daemonMode, *headless)
}

// attach — boshqa nusxa (odatda systemd demoni) allaqachon ishlayapti.
// O'z serverimizni ko'tarmaymiz: oynani o'shaning manziliga yo'naltiramiz.
func attach(daemonMode, headless bool) {
	if daemonMode {
		log.Fatalf("ServerGo allaqachon ishlayapti — ikkinchi demon ishga tushirilmadi")
	}
	ep, err := daemon.ReadEndpoint(3 * time.Second)
	if err != nil {
		log.Fatalf("%v", err)
	}
	if headless {
		fmt.Printf("%s/?t=%s\n", ep.URL, ep.Token)
		return
	}
	openWindow(fmt.Sprintf("%s/?t=%s", ep.URL, ep.Token))
}

// runOwner — biz egamiz: tunnel servisi va HTTP serverni ko'taramiz.
func runOwner(daemonMode, headless bool) {
	assets, err := fs.Sub(webAssets, "web")
	if err != nil {
		log.Fatalf("web papkasini ochib bo'lmadi: %v", err)
	}

	// Tunnellar bo'limi: SQLite ochiladi, cloudflared qidiriladi, avtostart
	// belgilangan loyihalar ishga tushadi. Xato bo'lsa ham nil qaytmaydi —
	// bo'lim UI da sababni ko'rsatadi, qolgan bo'limlar ishlayveradi.
	tun := tunnel.New()
	defer tun.Close()

	// Ilovalar bo'limi: ServerGo'ning o'z jarayon boshqaruvchisi (pm2'ga
	// bog'liq emas) — "Avtostart" belgilangan ilovalar shu yerda ishga
	// tushadi, "pm2 save" esdan chiqishi muammosisiz.
	appsSvc := apps.New()
	defer appsSvc.Close()

	// VPS Tunnel bo'limi: foydalanuvchining o'z VPS'idagi servergo-relay
	// orqali reverse-tunnel — Cloudflare bo'limiga mustaqil.
	vt := vpstunnel.New()
	defer vt.Close()

	// pm2: saqlangan jarayonlar ro'yxatini tiklaydi (`pm2 save` qilinganlar).
	// Shu bilan pm2'ning o'z systemd/sudo sozlashiga hojat qolmaydi — boot'da
	// faqat ServerGo demoni (systemd user service) ko'tariladi, qolganini
	// shu tiklaydi. Jarayonlar allaqachon ishlab tursa, pm2 jim o'tkazadi.
	go func() {
		time.Sleep(1 * time.Second)
		if out, err := pm2.Resurrect(); err != nil {
			log.Printf("pm2 avtostart: %v", err)
		} else if strings.TrimSpace(out) != "" {
			log.Printf("pm2 avtostart: %s", strings.TrimSpace(out))
		}
	}()

	token := newToken()
	srv, err := server.New(assets, token, tun, appsSvc, vt)
	if err != nil {
		log.Fatalf("serverni ishga tushirib bo'lmadi: %v", err)
	}

	go func() {
		if err := srv.Serve(); err != nil {
			log.Fatalf("server to'xtadi: %v", err)
		}
	}()

	if err := daemon.WriteEndpoint(daemon.Endpoint{
		URL:   srv.BaseURL(),
		Token: token,
		PID:   os.Getpid(),
	}); err != nil {
		log.Printf("ogohlantirish: manzil faylini yozib bo'lmadi: %v", err)
	}
	defer daemon.RemoveEndpoint()

	if daemonMode || headless {
		if headless {
			fmt.Println(srv.URL())
		} else {
			log.Printf("ServerGo demoni ishga tushdi: %s", srv.BaseURL())
		}
		waitForSignal()
		return
	}

	openWindow(srv.URL())
}

// openWindow — webview GTK ni chaqiradi; u faqat asosiy OS thread'ida ishlashi
// kerak (runtime.LockOSThread webview_go ichida bajariladi).
func openWindow(url string) {
	w := webview.New(os.Getenv("SERVERGO_DEBUG") != "")
	defer w.Destroy()

	w.SetTitle("ServerGo")
	w.SetSize(winMinWidth, winMinHeight, webview.HintMin)
	w.SetSize(winWidth, winHeight, webview.HintNone)
	w.Navigate(url)
	w.Run()
}

// waitForSignal — systemd `stop` bosganda defer'lar ishlashi uchun.
// Bularsiz tunnellar to'xtatilmay, cloudflared jarayonlari yetim qolardi.
func waitForSignal() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
}

// newToken — API uchun bir martalik tasodifiy token.
func newToken() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("token yaratib bo'lmadi: %v", err)
	}
	return hex.EncodeToString(b)
}
