package client

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
)

// handshake — relay/internal/control.Handshake bilan BIR XIL formatda
// bo'lishi shart (4-bayt big-endian uzunlik + JSON). Ikkala tomon ham
// modul jihatidan mustaqil (relay/ alohida go.mod), shuning uchun struktura
// har ikki joyda alohida e'lon qilingan — o'zgartirilsa ikkalasi ham
// yangilanishi kerak.
type handshake struct {
	Token     string `json:"token"`
	ProjectID string `json:"project_id"`
	Hostname  string `json:"hostname"`

	// ConnIndex — shu loyihaning nechanchi ulanishi (0 dan boshlab). Relay
	// buni faqat logga yozadi; eski relay uni umuman bilmaydi va JSON
	// bo'lgani uchun beparvo o'tkazib yuboradi.
	ConnIndex int `json:"conn_index"`
}

// sendHandshake — handshake'ni yozadi va relay javobini o'qiydi
// (0x01 — muvaffaqiyat, 0x00 + xabar — xato).
func sendHandshake(conn net.Conn, h handshake) error {
	body, err := json.Marshal(h)
	if err != nil {
		return err
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(body)))
	if _, err := conn.Write(lenBuf[:]); err != nil {
		return fmt.Errorf("handshake uzunligi yozilmadi: %w", err)
	}
	if _, err := conn.Write(body); err != nil {
		return fmt.Errorf("handshake yozilmadi: %w", err)
	}

	var status [1]byte
	if _, err := io.ReadFull(conn, status[:]); err != nil {
		return fmt.Errorf("relay javobi o'qilmadi: %w", err)
	}
	if status[0] == 0x01 {
		return nil
	}
	var msgLen [2]byte
	if _, err := io.ReadFull(conn, msgLen[:]); err != nil {
		return fmt.Errorf("relay xato xabari o'qilmadi: %w", err)
	}
	n := binary.BigEndian.Uint16(msgLen[:])
	msg := make([]byte, n)
	if _, err := io.ReadFull(conn, msg); err != nil {
		return fmt.Errorf("relay xato xabari to'liq o'qilmadi: %w", err)
	}
	return fmt.Errorf("relay rad etdi: %s", string(msg))
}
