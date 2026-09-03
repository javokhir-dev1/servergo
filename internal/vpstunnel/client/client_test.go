package client

import (
	"io"
	"net"
	"testing"
	"time"
)

// tcpPair — ikkita ulangan TCP uchi (yamux oqimi o'rniga: unda ham, bunda
// ham yarim yopish semantikasi bir xil).
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
	a, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	r := <-ch
	if r.err != nil {
		t.Fatal(r.err)
	}
	t.Cleanup(func() { a.Close(); r.c.Close() })
	return a, r.c
}

// TestForwardSurvivesLocalHalfClose — lokal servis o'z tomonini yozishdan
// yopgach ham (CloseWrite) qarama-qarshi yo'nalish ishlashda davom etishi
// kerak. Regressiya: forward ilgari ikkita ko'chirishdan BIRINCHISI
// tugashi bilan ikkala ulanishni ham yopar edi — natijada upgrade qilingan
// (WebSocket kabi) ulanishning hali faol yo'nalishi kesilib qolardi.
func TestForwardSurvivesLocalHalfClose(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	got := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		// Darhol javob yozib, o'z yozish tomonini yopamiz — lekin o'qishda
		// qolamiz.
		if _, err := io.WriteString(c, "SERVERHELLO"); err != nil {
			return
		}
		if err := c.(*net.TCPConn).CloseWrite(); err != nil {
			return
		}
		b, _ := io.ReadAll(c)
		got <- string(b)
	}()

	clientEnd, streamEnd := tcpPair(t)
	go forward(streamEnd, port, nil)

	if err := clientEnd.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	hello := make([]byte, len("SERVERHELLO"))
	if _, err := io.ReadFull(clientEnd, hello); err != nil {
		t.Fatalf("javob o'qilmadi: %v", err)
	}
	if string(hello) != "SERVERHELLO" {
		t.Fatalf("javob = %q, kutilgan %q", hello, "SERVERHELLO")
	}

	// Lokal servis allaqachon yarim yopgan; shundan keyin ham biz yozgan
	// baytlar unga yetib borishi kerak.
	if _, err := io.WriteString(clientEnd, "CLIENTDATA"); err != nil {
		t.Fatalf("yozib bo'lmadi: %v", err)
	}
	if err := clientEnd.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}

	select {
	case s := <-got:
		if s != "CLIENTDATA" {
			t.Fatalf("lokal servis %q oldi, kutilgan %q", s, "CLIENTDATA")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("lokal servis ma'lumot olmadi — yo'nalish erta yopilgan")
	}
}

// TestForwardCopiesBothWays — oddiy ikki yo'nalishli echo.
func TestForwardCopiesBothWays(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = io.Copy(c, c)
	}()

	clientEnd, streamEnd := tcpPair(t)
	go forward(streamEnd, port, nil)

	if err := clientEnd.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"bir", "ikki", "uch"} {
		if _, err := io.WriteString(clientEnd, want); err != nil {
			t.Fatalf("yozish: %v", err)
		}
		b := make([]byte, len(want))
		if _, err := io.ReadFull(clientEnd, b); err != nil {
			t.Fatalf("o'qish: %v", err)
		}
		if string(b) != want {
			t.Fatalf("echo = %q, kutilgan %q", b, want)
		}
	}
}
