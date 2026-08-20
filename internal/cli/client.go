// Package cli — "servergo <buyruq>" terminal interfeysi.
//
// GUI oynasi yoki systemd demoni allaqachon ishlab turgan HTTP API ga
// ulanadi (xuddi ikkinchi oyna nusxasi ulanganidek — internal/daemon
// orqali manzil va tokenni topadi). Yangi jarayon ko'tarmaydi, tunnel
// yoki pm2 bilan to'g'ridan-to'g'ri ishlamaydi — shu bilan "bitta egasi"
// qoidasi buzilmaydi.
package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"servergo/internal/daemon"
)

type client struct {
	base  string
	token string
	http  *http.Client
}

func connect() (*client, error) {
	ep, err := daemon.ReadEndpoint(800 * time.Millisecond)
	if err != nil {
		return nil, errors.New(
			"ServerGo ishlamayapti. Avval oynani oching yoki demonni ishga tushiring:\n" +
				"  systemctl --user start servergo")
	}
	// 5 daqiqa — "tunnel add-domain" ikkinchi/keyingi domen uchun brauzerda
	// cloudflared login kutishi mumkin (server tarafida ham xuddi shu chegara).
	return &client{base: ep.URL, token: ep.Token, http: &http.Client{Timeout: 5 * time.Minute}}, nil
}

type envelope struct {
	OK    bool            `json:"ok"`
	Error string          `json:"error"`
	Data  json.RawMessage `json:"data"`
}

func (c *client) do(method, path string, body any) (json.RawMessage, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Token", c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ulanib bo'lmadi: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("buzuq javob: %s", raw)
	}
	if !env.OK {
		return nil, errors.New(env.Error)
	}
	return env.Data, nil
}

func (c *client) get(path string) (json.RawMessage, error) { return c.do(http.MethodGet, path, nil) }

func (c *client) post(path string, body any) (json.RawMessage, error) {
	if body == nil {
		body = struct{}{}
	}
	return c.do(http.MethodPost, path, body)
}

func (c *client) getInto(path string, v any) error {
	raw, err := c.get(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

func (c *client) postInto(path string, body any, v any) error {
	raw, err := c.post(path, body)
	if err != nil {
		return err
	}
	if v == nil {
		return nil
	}
	return json.Unmarshal(raw, v)
}
