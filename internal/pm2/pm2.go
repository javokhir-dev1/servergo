// Package pm2 pm2 bilan CLI orqali ishlaydi.
//
// pm2 ning programmatic API si versiyadan versiyaga o'zgarib turadi, CLI esa
// barqaror — shuning uchun barcha amallar `pm2 <buyruq>` chaqiruvi orqali
// bajariladi va mavjud pm2 demoniga tegilmaydi.
package pm2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Bin — pm2 ijro etuvchi fayli; PM2_BIN muhit o'zgaruvchisi bilan almashtiriladi.
var Bin = envOr("PM2_BIN", "pm2")

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// run — pm2 ni chaqiradi. silent=true bo'lsa pm2 banner satrlarini o'chiradi;
// jlist uchun shart, aks holda "[PM2] …" satrlari JSON ni buzadi.
func run(args []string, timeout time.Duration, silent bool) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, Bin, args...)
	cmd.Env = append(os.Environ(), "FORCE_COLOR=0")
	if silent {
		cmd.Env = append(cmd.Env, "PM2_SILENT=true")
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("pm2 %s: vaqt tugadi", strings.Join(args, " "))
		}
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			return "", fmt.Errorf("pm2 topilmadi (%s). \"npm i -g pm2\" bilan o'rnating", Bin)
		}
		if msg := pickError(stderr.String(), stdout.String()); msg != "" {
			return "", errors.New(msg)
		}
		return "", err
	}
	return stdout.String(), nil
}

// pickError — pm2 xato matnini goh stderr ga, goh stdout ga yozadi.
func pickError(streams ...string) string {
	var lines []string
	for _, s := range streams {
		for _, l := range strings.Split(s, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				lines = append(lines, l)
			}
		}
	}
	for _, l := range lines {
		if strings.Contains(l, "[PM2][ERROR]") {
			return strings.TrimSpace(strings.SplitN(l, "[PM2][ERROR]", 2)[1])
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if !strings.HasPrefix(lines[i], "[PM2] Applying") {
			return lines[i]
		}
	}
	return ""
}

// Proc — UI ga uzatiladigan normallashtirilgan jarayon.
type Proc struct {
	ID               int     `json:"id"`
	Name             string  `json:"name"`
	Namespace        string  `json:"namespace"`
	Status           string  `json:"status"`
	PID              int     `json:"pid"`
	CPU              float64 `json:"cpu"`
	Memory           int64   `json:"memory"`
	Uptime           int64   `json:"uptime"` // ms epoch, online bo'lmasa 0
	Restarts         int     `json:"restarts"`
	UnstableRestarts int     `json:"unstableRestarts"`
	Mode             string  `json:"mode"`
	Instances        int     `json:"instances"`
	ExecPath         string  `json:"execPath"`
	Cwd              string  `json:"cwd"`
	Interpreter      string  `json:"interpreter"`
	Args             string  `json:"args"`
	NodeVersion      string  `json:"nodeVersion"`
	Version          string  `json:"version"`
	User             string  `json:"user"`
	Watching         bool    `json:"watching"`
	Autorestart      bool    `json:"autorestart"`
	MaxMemoryRestart string  `json:"maxMemoryRestart"`
	CreatedAt        int64   `json:"createdAt"`
	OutLog           string  `json:"outLog"`
	ErrLog           string  `json:"errLog"`
}

// jlist chiqishidagi xom tuzilma. pm2 ba'zi maydonlarni turli tiplarda
// qaytaradi (args — massiv yoki satr, watch — bool yoki massiv,
// max_memory_restart — son yoki satr), shuning uchun ular RawMessage.
type rawProc struct {
	PmID  *int   `json:"pm_id"`
	Name  string `json:"name"`
	PID   int    `json:"pid"`
	Monit struct {
		CPU    float64 `json:"cpu"`
		Memory int64   `json:"memory"`
	} `json:"monit"`
	Env struct {
		Namespace        string          `json:"namespace"`
		Status           string          `json:"status"`
		PmUptime         int64           `json:"pm_uptime"`
		RestartTime      int             `json:"restart_time"`
		UnstableRestarts int             `json:"unstable_restarts"`
		ExecMode         string          `json:"exec_mode"`
		Instances        json.RawMessage `json:"instances"`
		PmExecPath       string          `json:"pm_exec_path"`
		PmCwd            string          `json:"pm_cwd"`
		ExecInterpreter  string          `json:"exec_interpreter"`
		Args             json.RawMessage `json:"args"`
		NodeVersion      string          `json:"node_version"`
		Version          string          `json:"version"`
		Username         string          `json:"username"`
		Watch            json.RawMessage `json:"watch"`
		Autorestart      *bool           `json:"autorestart"`
		MaxMemoryRestart json.RawMessage `json:"max_memory_restart"`
		CreatedAt        int64           `json:"created_at"`
		OutLog           string          `json:"pm_out_log_path"`
		ErrLog           string          `json:"pm_err_log_path"`
	} `json:"pm2_env"`
}

func normalize(r rawProc) Proc {
	p := Proc{
		Name:             r.Name,
		Namespace:        orDefault(r.Env.Namespace, "default"),
		Status:           orDefault(r.Env.Status, "unknown"),
		PID:              r.PID,
		CPU:              r.Monit.CPU,
		Memory:           r.Monit.Memory,
		Restarts:         r.Env.RestartTime,
		UnstableRestarts: r.Env.UnstableRestarts,
		Instances:        1,
		ExecPath:         r.Env.PmExecPath,
		Cwd:              r.Env.PmCwd,
		Interpreter:      r.Env.ExecInterpreter,
		NodeVersion:      r.Env.NodeVersion,
		Version:          r.Env.Version,
		User:             r.Env.Username,
		CreatedAt:        r.Env.CreatedAt,
		OutLog:           r.Env.OutLog,
		ErrLog:           r.Env.ErrLog,
	}
	if r.PmID != nil {
		p.ID = *r.PmID
	}
	if r.Env.ExecMode == "cluster_mode" {
		p.Mode = "cluster"
	} else {
		p.Mode = "fork"
	}
	if n := asInt(r.Env.Instances); n > 0 {
		p.Instances = n
	}
	p.Args = asStringList(r.Env.Args)
	p.Watching = isTruthy(r.Env.Watch)
	p.Autorestart = r.Env.Autorestart == nil || *r.Env.Autorestart
	p.MaxMemoryRestart = asScalarString(r.Env.MaxMemoryRestart)

	// Uptime faqat online jarayon uchun ma'noli.
	if p.Status == "online" {
		p.Uptime = r.Env.PmUptime
	}
	return p
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// asStringList — massivni bo'shliq bilan qo'shadi, satrni o'zini qaytaradi.
func asStringList(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return strings.Join(list, " ")
	}
	return asScalarString(raw)
}

// asScalarString — satr yoki sonni matnga aylantiradi, boshqasini bo'sh qoldiradi.
func asScalarString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return ""
}

func asInt(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return int(f)
	}
	return 0
}

// isTruthy — watch bool ham, fayl naqshlari massivi ham bo'lishi mumkin.
func isTruthy(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return b
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return len(list) > 0
	}
	return false
}

// extractJson — pm2 JSON dan oldin banner chiqarib yuborishi mumkin.
func extractJSON(out string) []rawProc {
	start := strings.IndexByte(out, '[')
	end := strings.LastIndexByte(out, ']')
	if start < 0 || end < 0 || end < start {
		return nil
	}
	var list []rawProc
	if err := json.Unmarshal([]byte(out[start:end+1]), &list); err != nil {
		return nil
	}
	return list
}

// List — barcha pm2 jarayonlari.
func List() ([]Proc, error) {
	out, err := run([]string{"jlist"}, 20*time.Second, true)
	if err != nil {
		return nil, err
	}
	raw := extractJSON(out)
	procs := make([]Proc, 0, len(raw))
	for _, r := range raw {
		procs = append(procs, normalize(r))
	}
	return procs, nil
}

var actions = map[string]bool{
	"start": true, "stop": true, "restart": true, "reload": true, "delete": true,
}

// Action — jarayon ustida amal bajaradi.
func Action(kind string, id int) error {
	if !actions[kind] {
		return fmt.Errorf("noma'lum amal: %s", kind)
	}
	_, err := run([]string{kind, strconv.Itoa(id)}, 30*time.Second, false)
	return err
}

// Flush — jarayonning log fayllarini bo'shatadi.
func Flush(id int) error {
	_, err := run([]string{"flush", strconv.Itoa(id)}, 20*time.Second, false)
	return err
}

// Resurrect — `pm2 save` orqali saqlangan jarayonlar ro'yxatini ko'taradi.
// ServerGo demoni (systemd user service) yuklanganda chaqiriladi, shu bilan
// `pm2 startup`ning alohida sudo/systemd sozlashiga ehtiyoj qolmaydi:
// boot'da faqat ServerGo ko'tariladi, u esa pm2 ilovalarini o'zi tiklaydi.
// Jarayon allaqachon ishlab tursa, pm2 uni jim o'tkazib yuboradi.
//
// Timeout boshqa amallardan ancha uzoqroq (3 daqiqa) — ro'yxatda o'nlab
// jarayon bo'lishi mumkin, pm2 ularni birma-bir ko'taradi. Bu fon
// goroutine'da, server ishga tushishini bloklamasdan ishlaydi.
func Resurrect() (string, error) {
	return run([]string{"resurrect"}, 3*time.Minute, false)
}

// DaemonStatus — pm2 demoni javob beryaptimi?
func DaemonStatus() (bool, string) {
	if _, err := run([]string{"ping"}, 8*time.Second, true); err != nil {
		return false, err.Error()
	}
	return true, ""
}

// Version — pm2 versiyasi.
func Version() string {
	out, err := run([]string{"-v"}, 8*time.Second, true)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
