package cloudsync

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

type envelope struct {
	OK    bool            `json:"ok"`
	Error string          `json:"error"`
	Data  json.RawMessage `json:"data"`
}

// Login — email+parol bilan backend'ga kiradi, bearer token qaytaradi.
// Hali tokensiz chaqiriladi, shuning uchun paket darajasidagi funksiya.
func Login(baseURL, email, password, deviceName string) (token, userEmail string, err error) {
	body, _ := json.Marshal(map[string]string{
		"email": email, "password": password, "device_name": deviceName,
	})
	raw, err := doRequest(http.MethodPost, baseURL+"/api/auth/login", "", body)
	if err != nil {
		return "", "", err
	}
	var out struct {
		Token string `json:"token"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", "", fmt.Errorf("buzuq javob: %w", err)
	}
	return out.Token, out.Email, nil
}

// Client — kirilgandan keyingi so'rovlar uchun.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

func NewClient(baseURL, token string) *Client {
	return &Client{baseURL: baseURL, token: token, http: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) Logout() error {
	_, err := doRequest(http.MethodPost, c.baseURL+"/api/auth/logout", c.token, nil)
	return err
}

type AppRecord struct {
	LocalID   string `json:"local_id"`
	Name      string `json:"name"`
	Command   string `json:"command"`
	Cwd       string `json:"cwd"`
	Autostart bool   `json:"autostart"`
}

func (c *Client) ListApps() ([]AppRecord, error) {
	raw, err := doRequest(http.MethodGet, c.baseURL+"/api/apps", c.token, nil)
	if err != nil {
		return nil, err
	}
	var out []AppRecord
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("buzuq javob: %w", err)
	}
	return out, nil
}

func (c *Client) UpsertApp(localID string, a AppRecord) error {
	body, _ := json.Marshal(a)
	_, err := doRequest(http.MethodPut, c.baseURL+"/api/apps/"+localID, c.token, body)
	return err
}

func (c *Client) DeleteApp(localID string) error {
	_, err := doRequest(http.MethodDelete, c.baseURL+"/api/apps/"+localID, c.token, nil)
	return err
}

type ProjectRecord struct {
	LocalID      string `json:"local_id"`
	Name         string `json:"name"`
	Port         int    `json:"port"`
	Subdomain    string `json:"subdomain"`
	BaseDomain   string `json:"base_domain"`
	Protocol     string `json:"protocol"`
	TunnelID     string `json:"tunnel_id"`
	TunnelName   string `json:"tunnel_name"`
	AccountTag   string `json:"account_tag"`
	TunnelSecret string `json:"tunnel_secret,omitempty"`
	Autostart    bool   `json:"autostart"`
}

func (c *Client) ListProjects() ([]ProjectRecord, error) {
	raw, err := doRequest(http.MethodGet, c.baseURL+"/api/projects", c.token, nil)
	if err != nil {
		return nil, err
	}
	var out []ProjectRecord
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("buzuq javob: %w", err)
	}
	return out, nil
}

func (c *Client) UpsertProject(localID string, p ProjectRecord) error {
	body, _ := json.Marshal(p)
	_, err := doRequest(http.MethodPut, c.baseURL+"/api/projects/"+localID, c.token, body)
	return err
}

func (c *Client) DeleteProject(localID string) error {
	_, err := doRequest(http.MethodDelete, c.baseURL+"/api/projects/"+localID, c.token, nil)
	return err
}

// GetDomainCert — domen uchun saqlangan cert.pem. Topilmasa ("", nil) qaytadi.
func (c *Client) GetDomainCert(domain string) (string, error) {
	raw, err := doRequest(http.MethodGet, c.baseURL+"/api/domains/"+domain+"/cert", c.token, nil)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	var out struct {
		CertPEM string `json:"cert_pem"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("buzuq javob: %w", err)
	}
	return out.CertPEM, nil
}

func (c *Client) PutDomainCert(domain, certPEM string) error {
	body, _ := json.Marshal(map[string]string{"cert_pem": certPEM})
	_, err := doRequest(http.MethodPut, c.baseURL+"/api/domains/"+domain+"/cert", c.token, body)
	return err
}

// ErrNotFound — backend 404 qaytardi.
var ErrNotFound = errors.New("topilmadi")

var httpClient = &http.Client{Timeout: 30 * time.Second}

func doRequest(method, url, token string, body []byte) (json.RawMessage, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("backend'ga ulanib bo'lmadi: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("buzuq javob (%d): %s", resp.StatusCode, raw)
	}
	if !env.OK {
		if resp.StatusCode == http.StatusNotFound {
			return nil, ErrNotFound
		}
		return nil, errors.New(env.Error)
	}
	return env.Data, nil
}
