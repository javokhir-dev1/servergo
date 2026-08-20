package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"servergo/internal/pm2"
)

func cmdPS(c *client, args []string) error {
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}

	switch sub {
	case "list", "ls":
		watch, _ := takeBoolFlag(args, "-w")
		if watch {
			return watchLoop(2*time.Second, func() error { return psList(c) })
		}
		return psList(c)
	case "start", "stop", "restart", "delete":
		if len(args) < 1 {
			return fmt.Errorf("foydalanish: ps %s <id|nom>", sub)
		}
		return psAction(c, sub, args[0])
	case "logs":
		if len(args) < 1 {
			return errors.New("foydalanish: ps logs <id|nom> [-n N]")
		}
		nStr, _, rest := takeValueFlag(args, "-n")
		if len(rest) < 1 {
			return errors.New("foydalanish: ps logs <id|nom> [-n N]")
		}
		ref := rest[0]
		lines := 100
		if nStr != "" {
			if n, err := strconv.Atoi(nStr); err == nil && n > 0 {
				lines = n
			}
		}
		return psLogs(c, ref, lines)
	case "flush":
		if len(args) < 1 {
			return errors.New("foydalanish: ps flush <id|nom>")
		}
		return psFlush(c, args[0])
	default:
		return fmt.Errorf("noma'lum buyruq: ps %s", sub)
	}
}

func fetchProcs(c *client) ([]pm2.Proc, error) {
	var procs []pm2.Proc
	if err := c.getInto("/api/pm2/list", &procs); err != nil {
		return nil, err
	}
	return procs, nil
}

func resolveProc(procs []pm2.Proc, ref string) (*pm2.Proc, error) {
	if id, err := strconv.Atoi(ref); err == nil {
		for i := range procs {
			if procs[i].ID == id {
				return &procs[i], nil
			}
		}
		return nil, fmt.Errorf("jarayon topilmadi: id=%d", id)
	}
	var match *pm2.Proc
	for i := range procs {
		if strings.EqualFold(procs[i].Name, ref) {
			if match != nil {
				return nil, fmt.Errorf("bir nechta jarayon '%s' nomiga mos keladi — id ko'rsating", ref)
			}
			match = &procs[i]
		}
	}
	if match == nil {
		return nil, fmt.Errorf("jarayon topilmadi: %s", ref)
	}
	return match, nil
}

func psList(c *client) error {
	procs, err := fetchProcs(c)
	if err != nil {
		return err
	}
	if len(procs) == 0 {
		fmt.Println("pm2 jarayonlari yo'q")
		return nil
	}
	t := newTable()
	fmt.Fprintln(t, "ID\tNOM\tHOLAT\tCPU\tXOTIRA\tUPTIME\tRESTART\tPID")
	for _, p := range procs {
		fmt.Fprintf(t, "%d\t%s\t%s\t%.0f%%\t%s\t%s\t%d\t%d\n",
			p.ID, p.Name, p.Status, p.CPU, formatBytes(uint64(p.Memory)),
			formatUptime(p.Uptime), p.Restarts, p.PID)
	}
	return t.Flush()
}

func psAction(c *client, action, ref string) error {
	procs, err := fetchProcs(c)
	if err != nil {
		return err
	}
	p, err := resolveProc(procs, ref)
	if err != nil {
		return err
	}
	if _, err := c.post("/api/pm2/action", map[string]any{"type": action, "id": p.ID}); err != nil {
		return err
	}
	fmt.Printf("'%s' — %s bajarildi\n", p.Name, action)
	return nil
}

func psFlush(c *client, ref string) error {
	procs, err := fetchProcs(c)
	if err != nil {
		return err
	}
	p, err := resolveProc(procs, ref)
	if err != nil {
		return err
	}
	if _, err := c.post("/api/pm2/flush", map[string]any{"id": p.ID}); err != nil {
		return err
	}
	fmt.Printf("'%s' loglari tozalandi\n", p.Name)
	return nil
}

func psLogs(c *client, ref string, lines int) error {
	procs, err := fetchProcs(c)
	if err != nil {
		return err
	}
	p, err := resolveProc(procs, ref)
	if err != nil {
		return err
	}
	var logs pm2.Logs
	if err := c.getInto(fmt.Sprintf("/api/pm2/logs?id=%d&lines=%d", p.ID, lines), &logs); err != nil {
		return err
	}
	fmt.Printf("=== %s — stdout (%s) ===\n%s\n", logs.Name, logs.Out.Path, logs.Out.Text)
	fmt.Printf("=== %s — stderr (%s) ===\n%s\n", logs.Name, logs.Err.Path, logs.Err.Text)
	return nil
}
