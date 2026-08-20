package cli

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"
)

func newTable() *tabwriter.Writer {
	return tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
}

func formatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// formatUptime — pm2 dagi ms-epoch boshlanish vaqtidan hozirgacha bo'lgan davr.
func formatUptime(startMS int64) string {
	if startMS <= 0 {
		return "-"
	}
	return formatDuration(time.Since(time.UnixMilli(startMS)))
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int64(d.Seconds())
	days := s / 86400
	hours := (s % 86400) / 3600
	mins := (s % 3600) / 60
	secs := s % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh%dm", hours, mins)
	case mins > 0:
		return fmt.Sprintf("%dm%ds", mins, secs)
	default:
		return fmt.Sprintf("%ds", secs)
	}
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04")
}

const clearScreen = "\x1b[H\x1b[2J"

// watchLoop — Ctrl+C bosilguncha `render`ni har `interval`da qayta chaqiradi.
// `top` / `watch pm2 list` ga o'xshash monitoring rejimi uchun. Render xato
// qaytarsa, tsikl to'xtaydi va o'sha xato yuqoriga uzatiladi.
func watchLoop(interval time.Duration, render func() error) error {
	for {
		fmt.Print(clearScreen)
		fmt.Printf("ServerGo — %s (Ctrl+C — chiqish)\n\n", time.Now().Format("15:04:05"))
		if err := render(); err != nil {
			return err
		}
		time.Sleep(interval)
	}
}
