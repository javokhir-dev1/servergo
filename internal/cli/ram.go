package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"servergo/internal/sysmon"
)

func cmdRAM(c *client, args []string) error {
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}

	switch sub {
	case "list", "ls":
		accurate, args := takeBoolFlag(args, "-a")
		watch, _ := takeBoolFlag(args, "-w")
		if watch {
			return watchLoop(2*time.Second, func() error { return ramList(c, accurate) })
		}
		return ramList(c, accurate)
	case "kill":
		if len(args) < 1 {
			return errors.New("foydalanish: ram kill <pid>")
		}
		pid, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("pid noto'g'ri: %s", args[0])
		}
		return ramKill(c, pid)
	default:
		return fmt.Errorf("noma'lum buyruq: ram %s", sub)
	}
}

type snapshot struct {
	Mem      sysmon.MemInfo `json:"mem"`
	Groups   []sysmon.Group `json:"groups"`
	Accurate bool           `json:"accurate"`
	Took     string         `json:"took"`
}

func ramList(c *client, accurate bool) error {
	q := "0"
	if accurate {
		q = "1"
	}
	var snap snapshot
	if err := c.getInto("/api/sys/snapshot?accurate="+q, &snap); err != nil {
		return err
	}
	m := snap.Mem
	fmt.Printf("Band: %s / %s   Bo'sh: %s   Mavjud: %s   Kesh: %s",
		formatBytes(m.Used), formatBytes(m.Total), formatBytes(m.Free),
		formatBytes(m.Available), formatBytes(m.Cached))
	if m.SwapTotal > 0 {
		fmt.Printf("   Swap: %s / %s", formatBytes(m.SwapUsed), formatBytes(m.SwapTotal))
	}
	mode := "RSS"
	if snap.Accurate {
		mode = "aniq (PSS)"
	}
	fmt.Printf("   [%s]\n\n", mode)

	t := newTable()
	fmt.Fprintln(t, "NOM\tFOYDALANUVCHI\tJARAYONLAR\tXOTIRA\t%\tROOT PID")
	for _, g := range snap.Groups {
		size := g.RSS
		if snap.Accurate {
			size = g.PSS
		}
		name := g.Name
		if g.Protect {
			name += " 🔒"
		}
		fmt.Fprintf(t, "%s\t%s\t%d\t%s\t%.1f%%\t%d\n",
			name, g.User, g.Count, formatBytes(size), g.Percent, g.RootPID)
	}
	return t.Flush()
}

func ramKill(c *client, pid int) error {
	raw, err := c.post("/api/sys/kill", map[string]any{"pid": pid})
	if err != nil {
		return err
	}
	fmt.Printf("pid %d — daraxt to'xtatildi: %s\n", pid, string(raw))
	return nil
}
