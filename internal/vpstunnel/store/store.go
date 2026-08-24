// Package store — VPS Tunnel bo'limi uchun SQLite ombori. internal/tunnel/store
// bilan bir xil naqsh, lekin butunlay mustaqil fayl/jadval — Cloudflare
// bo'limiga (internal/tunnel/*) hech qanday bog'liqlik yo'q.
package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Project — bitta VPS tunnel loyihasi.
type Project struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Port       int       `json:"port"`
	Subdomain  string    `json:"subdomain"`
	BaseDomain string    `json:"baseDomain"` // odatda wildcard_domain sozlamasi bilan bir xil
	Protocol   string    `json:"protocol"`   // lokal servisga: http | https
	Status     string    `json:"status"`     // stopped | starting | running | error
	Autostart  bool      `json:"autostart"`
	LastError  string    `json:"lastError"`
	CreatedAt  time.Time `json:"createdAt"`
}

func (p *Project) Hostname() string { return HostnameFor(p.Subdomain, p.BaseDomain) }
func HostnameFor(subdomain, baseDomain string) string {
	if subdomain == "" {
		return baseDomain
	}
	return subdomain + "." + baseDomain
}
func (p *Project) URL() string { return "https://" + p.Hostname() }

// Dir — ~/.config/servergo/vpstunnel
func Dir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = "."
	}
	return filepath.Join(base, "servergo", "vpstunnel")
}

func LogDir() string { return filepath.Join(Dir(), "logs") }

type Store struct {
	db *sql.DB
}

func Open() (*Store, error) {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(LogDir(), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(Dir(), "vpstunnel.db"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS projects (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  port        INTEGER NOT NULL,
  subdomain   TEXT NOT NULL,
  base_domain TEXT NOT NULL,
  protocol    TEXT NOT NULL DEFAULT 'http',
  status      TEXT NOT NULL DEFAULT 'stopped',
  autostart   INTEGER NOT NULL DEFAULT 0,
  last_error  TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS settings (
  k TEXT PRIMARY KEY,
  v TEXT NOT NULL
);`)
	return err
}

const projCols = `id, name, port, subdomain, base_domain, protocol, status, autostart, last_error, created_at`

func scanProject(row interface{ Scan(...any) error }) (Project, error) {
	var p Project
	var auto int
	var created string
	err := row.Scan(&p.ID, &p.Name, &p.Port, &p.Subdomain, &p.BaseDomain, &p.Protocol,
		&p.Status, &auto, &p.LastError, &created)
	if err != nil {
		return p, err
	}
	p.Autostart = auto == 1
	p.CreatedAt, _ = time.Parse(time.RFC3339, created)
	return p, nil
}

func (s *Store) ListProjects() ([]Project, error) {
	rows, err := s.db.Query(`SELECT ` + projCols + ` FROM projects ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetProject(id string) (Project, error) {
	row := s.db.QueryRow(`SELECT `+projCols+` FROM projects WHERE id = ?`, id)
	return scanProject(row)
}

// SubdomainTaken — subdomen boshqa loyihada ishlatilganmi.
func (s *Store) SubdomainTaken(sub, baseDomain, excludeID string) (bool, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM projects WHERE subdomain = ? AND base_domain = ? AND id != ?`,
		sub, baseDomain, excludeID).Scan(&n)
	return n > 0, err
}

func (s *Store) SaveProject(p Project) error {
	auto := 0
	if p.Autostart {
		auto = 1
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}
	_, err := s.db.Exec(`
INSERT INTO projects (`+projCols+`) VALUES (?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  name=excluded.name, port=excluded.port, subdomain=excluded.subdomain,
  base_domain=excluded.base_domain, protocol=excluded.protocol,
  status=excluded.status, autostart=excluded.autostart, last_error=excluded.last_error`,
		p.ID, p.Name, p.Port, p.Subdomain, p.BaseDomain, p.Protocol,
		p.Status, auto, p.LastError, p.CreatedAt.Format(time.RFC3339))
	return err
}

func (s *Store) SetStatus(id, status, lastError string) error {
	_, err := s.db.Exec(`UPDATE projects SET status = ?, last_error = ? WHERE id = ?`, status, lastError, id)
	return err
}

func (s *Store) DeleteProject(id string) error {
	_, err := s.db.Exec(`DELETE FROM projects WHERE id = ?`, id)
	return err
}

func (s *Store) GetSetting(key, def string) string {
	var v string
	err := s.db.QueryRow(`SELECT v FROM settings WHERE k = ?`, key).Scan(&v)
	if err != nil {
		return def
	}
	return v
}

func (s *Store) SetSetting(key, val string) error {
	_, err := s.db.Exec(`INSERT INTO settings (k, v) VALUES (?, ?)
ON CONFLICT(k) DO UPDATE SET v = excluded.v`, key, val)
	return err
}

// ResetRunningStatuses — dastur ochilganda oldingi sessiyadan qolgan
// "running/starting" statuslarni tozalaydi.
func (s *Store) ResetRunningStatuses() error {
	_, err := s.db.Exec(`UPDATE projects SET status = 'stopped' WHERE status IN ('running','starting')`)
	return err
}
