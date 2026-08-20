package cli

import (
	"fmt"

	"servergo/internal/tunnel"
)

func cmdStatus(c *client, _ []string) error {
	fmt.Println("== Jarayonlar (pm2) ==")
	procs, err := fetchProcs(c)
	if err != nil {
		fmt.Println("  xato:", err)
	} else {
		online, other := 0, 0
		for _, p := range procs {
			if p.Status == "online" {
				online++
			} else {
				other++
			}
		}
		fmt.Printf("  jami %d — online %d, boshqa %d\n", len(procs), online, other)
	}

	var info struct {
		Daemon struct {
			Alive bool   `json:"alive"`
			Error string `json:"error"`
		} `json:"daemon"`
		PM2Version string `json:"pm2Version"`
		Host       struct {
			CPUTempC *float64 `json:"cpuTempC"`
		} `json:"host"`
	}
	if err := c.getInto("/api/pm2/info", &info); err == nil {
		state := "ishlamayapti"
		if info.Daemon.Alive {
			state = "ishlayapti"
		}
		fmt.Printf("  pm2 demoni: %s (%s)\n", state, info.PM2Version)
	}

	fmt.Println("\n== RAM ==")
	var snap snapshot
	if err := c.getInto("/api/sys/snapshot?accurate=0", &snap); err != nil {
		fmt.Println("  xato:", err)
	} else {
		m := snap.Mem
		fmt.Printf("  band %s / %s, mavjud %s\n", formatBytes(m.Used), formatBytes(m.Total), formatBytes(m.Available))
	}
	if info.Host.CPUTempC != nil {
		fmt.Printf("  CPU harorat: %.1f°C\n", *info.Host.CPUTempC)
	}

	fmt.Println("\n== Tunnellar ==")
	var st tunnel.SetupState
	if err := c.getInto("/api/tunnel/setup", &st); err != nil {
		fmt.Println("  xato:", err)
		return nil
	}
	if !st.Ready {
		fmt.Println("  sozlanmagan (cloudflared/login/domen kutilmoqda)")
		return nil
	}
	fmt.Printf("  faol domen: %s\n", st.ActiveDomain)
	var ps []tunnel.ProjectView
	if err := c.getInto("/api/tunnel/projects", &ps); err == nil {
		running := 0
		for _, p := range ps {
			if p.Running {
				running++
			}
		}
		fmt.Printf("  loyihalar: jami %d, ishlayapti %d\n", len(ps), running)
		for _, p := range ps {
			if p.LastError != "" {
				fmt.Printf("  ! %s: %s\n", p.Name, p.LastError)
			}
		}
	}
	return nil
}
