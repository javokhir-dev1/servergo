// Package store — SQLite asosidagi ma'lumotlar ombori (loyihalar va sozlamalar).
package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Project — bitta tunnel loyihasi (TZ 6.1).
type Project struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Port       int       `json:"port"`
	Subdomain  string    `json:"subdomain"`
	BaseDomain string    `json:"baseDomain"`
	Protocol   string    `json:"protocol"`
	TunnelID   string    `json:"tunnelId"`
	TunnelName string    `json:"tunnelName"`
	Status     string    `json:"status"` // stopped | starting | running | error
	Autostart  bool      `json:"autostart"`
	LastError  string    `json:"lastError"`
	CreatedAt  time.Time `json:"createdAt"`
}

// Hostname — Subdomain bo'sh bo'lsa (domenning o'zi uchun tunnel — apex/root),
// faqat BaseDomain qaytadi, aks holda "subdomen.domen".
func (p *Project) Hostname() string { return HostnameFor(p.Subdomain, p.BaseDomain) }

func HostnameFor(subdomain, baseDomain string) string {
	if subdomain == "" {
		return baseDomain
	}
	return subdomain + "." + baseDomain
}
func (p *Project) URL() string { return "https://" + p.Hostname() }

// Dir — ~/.config/servergo
func Dir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = "."
	}
	return filepath.Join(base, "servergo")
}

func TunnelDir(projectID string) string { return filepath.Join(Dir(), "tunnels", projectID) }
func LogDir() string                    { return filepath.Join(Dir(), "logs") }

// NeutralConfig — bo'sh config fayli. Barcha boshqaruv buyruqlariga (create,
// route dns, delete, info) shu fayl beriladi, aks holda cloudflared
// foydalanuvchining ~/.cloudflared/config.yml faylini o'qib, undagi `tunnel:`
// qiymatini buyruq argumentidan ustun qo'yadi va noto'g'ri tunnel bilan ishlaydi.
func NeutralConfig() string {
	path := filepath.Join(Dir(), "neutral.yml")
	_ = os.MkdirAll(Dir(), 0o700)
	// `loglevel` — beziyon kalit. Fayl butunlay bo'sh bo'lsa cloudflared
	// "Configuration file was empty" ERR yozadi va loglarni chalkashtiradi.
	content := "# ServerGo — global ~/.cloudflared/config.yml ni neytrallashtirish uchun.\n" +
		"# Bu yerga `tunnel:` yozmang.\n" +
		"loglevel: info\n"
	_ = os.WriteFile(path, []byte(content), 0o600)
	return path
}

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
	db, err := sql.Open("sqlite", filepath.Join(Dir(), "servergo.db"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite uchun xavfsiz
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
  tunnel_id   TEXT NOT NULL DEFAULT '',
  tunnel_name TEXT NOT NULL DEFAULT '',
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

const projCols = `id, name, port, subdomain, base_domain, protocol, tunnel_id, tunnel_name, status, autostart, last_error, created_at`

func scanProject(row interface{ Scan(...any) error }) (Project, error) {
	var p Project
	var auto int
	var created string
	err := row.Scan(&p.ID, &p.Name, &p.Port, &p.Subdomain, &p.BaseDomain, &p.Protocol,
		&p.TunnelID, &p.TunnelName, &p.Status, &auto, &p.LastError, &created)
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

// SubdomainTaken — subdomen boshqa loyihada ishlatilganmi (FR-2.2).
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
INSERT INTO projects (`+projCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  name=excluded.name, port=excluded.port, subdomain=excluded.subdomain,
  base_domain=excluded.base_domain, protocol=excluded.protocol,
  tunnel_id=excluded.tunnel_id, tunnel_name=excluded.tunnel_name,
  status=excluded.status, autostart=excluded.autostart, last_error=excluded.last_error`,
		p.ID, p.Name, p.Port, p.Subdomain, p.BaseDomain, p.Protocol,
		p.TunnelID, p.TunnelName, p.Status, auto, p.LastError,
		p.CreatedAt.Format(time.RFC3339))
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
// "running/starting" statuslarni tozalaydi (FR-5.4 soddalashtirilgan MVP).
func (s *Store) ResetRunningStatuses() error {
	_, err := s.db.Exec(`UPDATE projects SET status = 'stopped' WHERE status IN ('running','starting')`)
	return err
}
