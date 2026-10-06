// Package store — Ilovalar bo'limi uchun SQLite ombori. Tunnellar bo'limining
// store paketi bilan bir xil naqsh, lekin butunlay mustaqil fayl (apps.db) —
// ikkita paket bir xil SQLite faylini bir vaqtda ochsa qulflanish (SQLITE_BUSY)
// xavfi bor edi.
package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// App — bitta ilova: buyruq + ishchi papka, ServerGo tomonidan boshqariladi
// (pm2'ga bog'liq emas).
type App struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Command   string `json:"command"` // to'liq buyruq qatori, `sh -c` orqali bajariladi
	Cwd       string `json:"cwd"`     // ishchi papka (bo'sh — uy papkasi)
	Autostart bool   `json:"autostart"`
	// Sandbox — ilova bubblewrap ichida, uy papkasi yopilgan holda ishlaydi.
	// Qarang: internal/apps/sandbox.
	Sandbox bool `json:"sandbox"`
	// SandboxRO — sandbox ichida faqat o'qish uchun ko'rinadigan yo'llar:
	// monorepo ildizidagi node_modules, python site-packages, umumiy .env...
	SandboxRO []string `json:"sandboxRo"`
	// SandboxRW — yozish ham mumkin bo'lgan qo'shimcha yo'llar.
	SandboxRW []string  `json:"sandboxRw"`
	Status    string    `json:"status"` // stopped | starting | running | error
	LastError string    `json:"lastError"`
	CreatedAt time.Time `json:"createdAt"`
}

// Dir — ~/.config/servergo/apps
func Dir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = "."
	}
	return filepath.Join(base, "servergo", "apps")
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
	db, err := sql.Open("sqlite", filepath.Join(Dir(), "apps.db"))
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
CREATE TABLE IF NOT EXISTS apps (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  command     TEXT NOT NULL,
  cwd         TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT 'stopped',
  autostart   INTEGER NOT NULL DEFAULT 0,
  sandbox     INTEGER NOT NULL DEFAULT 0,
  sandbox_rw  TEXT NOT NULL DEFAULT '',
  sandbox_ro  TEXT NOT NULL DEFAULT '',
  last_error  TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL
);`)
	if err != nil {
		return err
	}
	// Eski o'rnatishlar: jadval allaqachon mavjud, ustunlar yo'q.
	for _, c := range []string{
		`sandbox INTEGER NOT NULL DEFAULT 0`,
		`sandbox_rw TEXT NOT NULL DEFAULT ''`,
		`sandbox_ro TEXT NOT NULL DEFAULT ''`,
	} {
		if err := s.ensureColumn(c); err != nil {
			return err
		}
	}
	return nil
}

// ensureColumn — ustun bo'lmasa qo'shadi. SQLite "duplicate column name"
// xatosini qaytaradi, o'shani jim o'tkazamiz (IF NOT EXISTS qo'llanmaydi).
func (s *Store) ensureColumn(def string) error {
	_, err := s.db.Exec(`ALTER TABLE apps ADD COLUMN ` + def)
	if err != nil && strings.Contains(err.Error(), "duplicate column name") {
		return nil
	}
	return err
}

const appCols = `id, name, command, cwd, status, autostart, sandbox, sandbox_rw, sandbox_ro, last_error, created_at`

func scanApp(row interface{ Scan(...any) error }) (App, error) {
	var a App
	var auto, sb int
	var rw, ro, created string
	err := row.Scan(&a.ID, &a.Name, &a.Command, &a.Cwd, &a.Status, &auto, &sb, &rw, &ro, &a.LastError, &created)
	if err != nil {
		return a, err
	}
	a.Autostart = auto == 1
	a.Sandbox = sb == 1
	a.SandboxRW = splitPaths(rw)
	a.SandboxRO = splitPaths(ro)
	a.CreatedAt, _ = time.Parse(time.RFC3339, created)
	return a, nil
}

func (s *Store) ListApps() ([]App, error) {
	rows, err := s.db.Query(`SELECT ` + appCols + ` FROM apps ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []App{}
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) GetApp(id string) (App, error) {
	row := s.db.QueryRow(`SELECT `+appCols+` FROM apps WHERE id = ?`, id)
	return scanApp(row)
}

// NameTaken — nom boshqa ilovada ishlatilganmi.
func (s *Store) NameTaken(name, excludeID string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM apps WHERE name = ? AND id != ?`, name, excludeID).Scan(&n)
	return n > 0, err
}

func (s *Store) SaveApp(a App) error {
	auto := 0
	if a.Autostart {
		auto = 1
	}
	sb := 0
	if a.Sandbox {
		sb = 1
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	_, err := s.db.Exec(`
INSERT INTO apps (`+appCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  name=excluded.name, command=excluded.command, cwd=excluded.cwd,
  status=excluded.status, autostart=excluded.autostart,
  sandbox=excluded.sandbox, sandbox_rw=excluded.sandbox_rw,
  sandbox_ro=excluded.sandbox_ro,
  last_error=excluded.last_error`,
		a.ID, a.Name, a.Command, a.Cwd, a.Status, auto, sb,
		joinPaths(a.SandboxRW), joinPaths(a.SandboxRO), a.LastError,
		a.CreatedAt.Format(time.RFC3339))
	return err
}

func (s *Store) SetStatus(id, status, lastError string) error {
	_, err := s.db.Exec(`UPDATE apps SET status = ?, last_error = ? WHERE id = ?`, status, lastError, id)
	return err
}

func (s *Store) DeleteApp(id string) error {
	_, err := s.db.Exec(`DELETE FROM apps WHERE id = ?`, id)
	return err
}

// ResetRunningStatuses — dastur ochilganda oldingi sessiyadan qolgan
// "running/starting" statuslarni tozalaydi.
func (s *Store) ResetRunningStatuses() error {
	_, err := s.db.Exec(`UPDATE apps SET status = 'stopped' WHERE status IN ('running','starting')`)
	return err
}

// splitPaths / joinPaths — SandboxRW ro'yxati bazada bitta matn ustunida,
// qator ajratgich bilan saqlanadi (yo'llarda vergul bo'lishi mumkin).
func splitPaths(v string) []string {
	out := []string{}
	for _, l := range strings.Split(v, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func joinPaths(list []string) string {
	clean := make([]string, 0, len(list))
	for _, l := range list {
		if l = strings.TrimSpace(l); l != "" {
			clean = append(clean, l)
		}
	}
	return strings.Join(clean, "\n")
}
