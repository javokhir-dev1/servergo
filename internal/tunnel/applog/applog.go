// Package applog — dastur darajasidagi loglar: startup, cloudflared buyruqlari,
// xatolar. Ring buffer (xotirada) + faylga yozish + UI'ga real-vaqt uzatish.
package applog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxEntries = 1000

// Entry — bitta log yozuvi.
type Entry struct {
	Time  string `json:"time"`  // HH:MM:SS
	Level string `json:"level"` // info | warn | error
	Msg   string `json:"msg"`
}

// EmitFunc — UI'ga hodisa yuborish.
type EmitFunc func(event string, data ...interface{})

var (
	mu      sync.Mutex
	entries []Entry
	file    *os.File
	emit    EmitFunc
)

// Init — log faylini ochadi. dir bo'sh bo'lsa faqat xotirada saqlanadi.
func Init(dir string) {
	mu.Lock()
	defer mu.Unlock()
	if dir == "" {
		return
	}
	path := filepath.Join(dir, "app.log")
	if st, err := os.Stat(path); err == nil && st.Size() > 5*1024*1024 {
		_ = os.Rename(path, path+".old")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		file = f
	}
}

// SetEmitter — UI ulangandan keyin chaqiriladi.
func SetEmitter(fn EmitFunc) {
	mu.Lock()
	emit = fn
	mu.Unlock()
}

func Close() {
	mu.Lock()
	defer mu.Unlock()
	if file != nil {
		_ = file.Close()
		file = nil
	}
}

// All — xotiradagi barcha yozuvlar.
func All() []Entry {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Entry, len(entries))
	copy(out, entries)
	return out
}

func Clear() {
	mu.Lock()
	entries = nil
	mu.Unlock()
}

func write(level, msg string) {
	now := time.Now()
	e := Entry{Time: now.Format("15:04:05"), Level: level, Msg: msg}

	mu.Lock()
	entries = append(entries, e)
	if len(entries) > maxEntries {
		entries = entries[len(entries)-maxEntries:]
	}
	f, em := file, emit
	mu.Unlock()

	if f != nil {
		fmt.Fprintf(f, "%s [%s] %s\n", now.Format("2006-01-02 15:04:05"), strings.ToUpper(level), msg)
	}
	if em != nil {
		em("app_log", e)
	}
}

func Info(format string, a ...interface{})  { write("info", fmt.Sprintf(format, a...)) }
func Warn(format string, a ...interface{})  { write("warn", fmt.Sprintf(format, a...)) }
func Error(format string, a ...interface{}) { write("error", fmt.Sprintf(format, a...)) }

// Cmd — bajarilgan buyruqni loglaydi (maxfiy ma'lumotlarsiz).
func Cmd(name string, args []string) {
	write("info", "$ "+name+" "+strings.Join(args, " "))
}

// CmdResult — buyruq natijasi. out bo'sh bo'lmasa qisqartirilgan holda yoziladi.
func CmdResult(label string, err error, out string) {
	out = strings.TrimSpace(out)
	if err != nil {
		msg := label + " — xato: " + err.Error()
		if out != "" {
			msg += "\n" + Tail(out, 8)
		}
		write("error", msg)
		return
	}
	msg := label + " — muvaffaqiyatli"
	if out != "" {
		msg += "\n" + Tail(out, 4)
	}
	write("info", msg)
}

// Tail — matnning oxirgi n qatorini qaytaradi.
func Tail(s string, n int) string {
	s = strings.TrimSpace(s)
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
