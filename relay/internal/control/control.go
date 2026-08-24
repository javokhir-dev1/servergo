// Package control — agentlar (lokal ServerGo nusxalari) ulanadigan qism.
// Har bir loyiha uchun agent alohida TLS ulanish ochadi, handshake yuboradi
// (token + hostname), so'ng ulanish yamux server sifatida ishlatiladi —
// proxy paketi shu sessiya orqali jamoatchilik so'rovlarini oqim (stream)
// sifatida agentga uzatadi.
package control

import (
	"crypto/subtle"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"regexp"

	"github.com/hashicorp/yamux"

	"servergo-relay/internal/registry"
)

// maxHandshakeBytes — handshake JSON uchun oqilona chegara (DoS'dan himoya).
const maxHandshakeBytes = 4096

// Handshake — agent ulanish ochgach yuboradigan birinchi xabar.
// Lokal tomondagi nusxasi: internal/vpstunnel/client/wire.go — ikkalasi ham
// bir xil formatda bo'lishi SHART (4-bayt uzunlik + JSON, io.ReadFull bilan
// aniq shuncha bayt o'qiladi — bufio ishlatilmaydi, aks holda undan keyingi
// yamux freym baytlari "yeb qo'yilishi" mumkin).
type Handshake struct {
	Token     string `json:"token"`
	ProjectID string `json:"project_id"`
	Hostname  string `json:"hostname"`
}

var hostnameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

func readHandshake(conn net.Conn) (Handshake, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
		return Handshake{}, fmt.Errorf("uzunlik o'qilmadi: %w", err)
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n == 0 || n > maxHandshakeBytes {
		return Handshake{}, fmt.Errorf("handshake hajmi noto'g'ri: %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return Handshake{}, fmt.Errorf("handshake o'qilmadi: %w", err)
	}
	var h Handshake
	if err := json.Unmarshal(buf, &h); err != nil {
		return Handshake{}, fmt.Errorf("handshake JSON noto'g'ri: %w", err)
	}
	return h, nil
}

// writeResult — 1 bayt (0x01 ok / 0x00 xato) + xato bo'lsa 2-bayt uzunlik +
// xabar. Muvaffaqiyat holida shundan keyin conn yamux server sifatida davom
// etadi.
func writeResult(conn net.Conn, err error) error {
	if err == nil {
		_, werr := conn.Write([]byte{0x01})
		return werr
	}
	msg := err.Error()
	if len(msg) > 65535 {
		msg = msg[:65535]
	}
	buf := make([]byte, 3+len(msg))
	buf[0] = 0x00
	binary.BigEndian.PutUint16(buf[1:3], uint16(len(msg)))
	copy(buf[3:], msg)
	_, werr := conn.Write(buf)
	return werr
}

// Serve — control TLS listener'ni tinglaydi. Bloklovchi chaqiruv.
func Serve(ln net.Listener, tlsCfg *tls.Config, token string, reg *registry.Registry) error {
	tln := tls.NewListener(ln, tlsCfg)
	for {
		conn, err := tln.Accept()
		if err != nil {
			return err
		}
		go handleConn(conn, token, reg)
	}
}

func handleConn(conn net.Conn, token string, reg *registry.Registry) {
	hs, err := readHandshake(conn)
	if err != nil {
		log.Printf("control: handshake xato (%s): %v", conn.RemoteAddr(), err)
		_ = writeResult(conn, err)
		_ = conn.Close()
		return
	}
	if subtle.ConstantTimeCompare([]byte(hs.Token), []byte(token)) != 1 {
		log.Printf("control: noto'g'ri token (%s, loyiha=%s)", conn.RemoteAddr(), hs.ProjectID)
		_ = writeResult(conn, fmt.Errorf("token noto'g'ri"))
		_ = conn.Close()
		return
	}
	hostname := hs.Hostname
	if !hostnameRe.MatchString(hostname) {
		log.Printf("control: noto'g'ri hostname (%s): %q", conn.RemoteAddr(), hostname)
		_ = writeResult(conn, fmt.Errorf("hostname noto'g'ri: %q", hostname))
		_ = conn.Close()
		return
	}

	sess, err := yamux.Server(conn, yamux.DefaultConfig())
	if err != nil {
		log.Printf("control: yamux server ochilmadi (%s): %v", conn.RemoteAddr(), err)
		_ = conn.Close()
		return
	}
	if err := writeResult(conn, nil); err != nil {
		_ = sess.Close()
		return
	}

	reg.Register(hostname, sess)
	log.Printf("control: agent ulandi — %s (loyiha=%s, %s)", hostname, hs.ProjectID, conn.RemoteAddr())

	<-sess.CloseChan()
	reg.Unregister(hostname, sess)
	log.Printf("control: agent uzildi — %s", hostname)
}
