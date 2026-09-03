package proxy

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"servergo-relay/internal/registry"
)

const testHost = "test.local"

// tcpPair — ikkita ulangan TCP uchi (net.Pipe emas: bizga haqiqiy yarim
// yopish (CloseWrite) va buferlangan yozuv semantikasi kerak).
func tcpPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	type res struct {
		c   net.Conn
		err error
	}
	ch := make(chan res, 1)
	go func() {
		c, err := ln.Accept()
		ch <- res{c, err}
	}()
	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	r := <-ch
	if r.err != nil {
		t.Fatal(r.err)
	}
	t.Cleanup(func() { client.Close(); r.c.Close() })
	return client, r.c
}

// startOrigin — agent ortidagi "lokal servis". /plain oddiy javob beradi,
// qolgan yo'llarda upgrade so'ralsa 101 qaytaradi — muhimi, 101 sarlavhasi
// va birinchi payload BITTA Write bilan yuboriladi, ya'ni proxy tomonidagi
// bufio o'sha baytlarni sarlavha bilan birga bufericha o'qib qo'yadi.
// Upgrade'dan keyin kelgan hamma narsa qaytarib aks ettiriladi (echo).
func startOrigin(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				br := bufio.NewReader(c)
				req, err := http.ReadRequest(br)
				if err != nil {
					return
				}
				up := req.Header.Get("Upgrade")
				if up == "" {
					_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
					return
				}
				_, _ = io.WriteString(c, "HTTP/1.1 101 Switching Protocols\r\n"+
					"Upgrade: "+up+"\r\nConnection: Upgrade\r\n\r\nPUSH1")
				_, _ = io.Copy(c, br) // upgrade'dan keyin xom echo
			}()
		}
	}()
	return ln.Addr().String()
}

// startTunnel — relay proxy'si oldida turgan to'liq zanjir: registry'ga
// yozilgan yamux sessiya + uning narigi uchida oqimlarni originga
// ko'chiruvchi "agent". httptest server manzilini qaytaradi.
func startTunnel(t *testing.T, originAddr string) string {
	t.Helper()
	relayConn, agentConn := tcpPair(t)

	serverSess, err := yamux.Server(relayConn, yamux.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	clientSess, err := yamux.Client(agentConn, yamux.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { serverSess.Close(); clientSess.Close() })

	// Agent tomoni: har bir oqimni originga ikki yo'nalishda ko'chiradi
	// (internal/vpstunnel/client.forward bilan bir xil vazifa).
	go func() {
		for {
			stream, err := clientSess.Accept()
			if err != nil {
				return
			}
			go func() {
				defer stream.Close()
				local, err := net.Dial("tcp", originAddr)
				if err != nil {
					return
				}
				defer local.Close()
				done := make(chan struct{}, 2)
				go func() { _, _ = io.Copy(local, stream); done <- struct{}{} }()
				go func() { _, _ = io.Copy(stream, local); done <- struct{}{} }()
				<-done
			}()
		}
	}()

	reg := registry.New()
	reg.Register(testHost, serverSess)
	srv := httptest.NewServer(NewProxyHandler(reg))
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String()
}

// TestUpgradeIsTunneled — 101 javob 502'ga aylanib qolmasligi kerak.
// Regressiya: streamBody faqat io.ReadCloser bo'lganida ReverseProxy
// "101 switching protocols response with non-writable body" bilan
// yiqilar edi va WebSocket umuman ishlamas edi.
func TestUpgradeIsTunneled(t *testing.T) {
	addr := startTunnel(t, startOrigin(t))

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}

	req := "GET /ws HTTP/1.1\r\nHost: " + testHost + "\r\n" +
		"Connection: Upgrade\r\nUpgrade: websocket\r\n\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		t.Fatal(err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("javob o'qilmadi: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		t.Fatalf("status = %d (%s), kutilgan 101", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// 101 sarlavhasi bilan bitta paketda kelgan baytlar yo'qolmasligi kerak.
	push := make([]byte, len("PUSH1"))
	if _, err := io.ReadFull(br, push); err != nil {
		t.Fatalf("push baytlari o'qilmadi: %v", err)
	}
	if string(push) != "PUSH1" {
		t.Fatalf("push = %q, kutilgan %q", push, "PUSH1")
	}

	// Upgrade'dan keyin ikki yo'nalishli xom kanal ishlashi kerak.
	for i := range 3 {
		want := fmt.Sprintf("ping-%d", i)
		if _, err := io.WriteString(conn, want); err != nil {
			t.Fatalf("yozish %d: %v", i, err)
		}
		got := make([]byte, len(want))
		if _, err := io.ReadFull(br, got); err != nil {
			t.Fatalf("o'qish %d: %v", i, err)
		}
		if string(got) != want {
			t.Fatalf("echo %d = %q, kutilgan %q", i, got, want)
		}
	}
}

// TestPlainRequestStillWorks — upgrade uchun qo'shilgan yo'l oddiy
// so'rovlarni buzmaganini tekshiradi.
func TestPlainRequestStillWorks(t *testing.T) {
	addr := startTunnel(t, startOrigin(t))

	req, err := http.NewRequest("GET", "http://"+addr+"/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = testHost
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, kutilgan 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "ok" {
		t.Fatalf("body = %q, kutilgan %q", body, "ok")
	}
}
