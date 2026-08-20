package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// App — server javobidagi shakl (servergo/internal/apps.AppView bilan mos).
type App struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Command   string `json:"command"`
	Cwd       string `json:"cwd"`
	Autostart bool   `json:"autostart"`
	Status    string `json:"status"`
	LastError string `json:"lastError"`
	CreatedAt string `json:"createdAt"`
	Running   bool   `json:"running"`
}

func cmdApps(c *client, args []string) error {
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}

	switch sub {
	case "list", "ls":
		watch, _ := takeBoolFlag(args, "-w")
		if watch {
			return watchLoop(2*time.Second, func() error { return appsListCmd(c) })
		}
		return appsListCmd(c)
	case "create", "add", "new":
		return appsCreateCmd(c, args)
	case "start", "stop", "restart":
		if len(args) < 1 {
			return fmt.Errorf("foydalanish: apps %s <id|nom>", sub)
		}
		return appAction(c, sub, args[0])
	case "delete", "rm":
		if len(args) < 1 {
			return errors.New("foydalanish: apps delete <id|nom>")
		}
		return appDelete(c, args[0])
	case "logs":
		if len(args) < 1 {
			return errors.New("foydalanish: apps logs <id|nom>")
		}
		return appLogsCmd(c, args[0])
	default:
		return fmt.Errorf("noma'lum buyruq: apps %s", sub)
	}
}

func fetchApps(c *client) ([]App, error) {
	var list []App
	if err := c.getInto("/api/apps/list", &list); err != nil {
		return nil, err
	}
	return list, nil
}

func resolveApp(list []App, ref string) (*App, error) {
	ref = strings.TrimSpace(ref)
	for i := range list {
		if list[i].ID == ref {
			return &list[i], nil
		}
	}
	var match *App
	for i := range list {
		a := &list[i]
		ok := strings.EqualFold(a.Name, ref) || (len(ref) >= 4 && strings.HasPrefix(a.ID, ref))
		if !ok {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("bir nechta ilova '%s' ga mos keladi — to'liq id ko'rsating", ref)
		}
		match = a
	}
	if match == nil {
		return nil, fmt.Errorf("ilova topilmadi: %s", ref)
	}
	return match, nil
}

func appsListCmd(c *client) error {
	list, err := fetchApps(c)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("ilovalar yo'q")
		return nil
	}
	t := newTable()
	fmt.Fprintln(t, "ID\tNOM\tHOLAT\tAVTOSTART\tBUYRUQ")
	for _, a := range list {
		id := a.ID
		if len(id) > 8 {
			id = id[:8]
		}
		auto := ""
		if a.Autostart {
			auto = "ha"
		}
		fmt.Fprintf(t, "%s\t%s\t%s\t%s\t%s\n", id, a.Name, a.Status, auto, a.Command)
	}
	if err := t.Flush(); err != nil {
		return err
	}
	for _, a := range list {
		if a.LastError != "" {
			fmt.Printf("  ! %s: %s\n", a.Name, a.LastError)
		}
	}
	return nil
}

func appsCreateCmd(c *client, args []string) error {
	autostart, args := takeBoolFlag(args, "-a")
	cwd, _, args := takeValueFlag(args, "-c")

	if len(args) < 2 {
		return errors.New("foydalanish: apps create <nom> <buyruq...> [-c ishchi-papka] [-a]")
	}
	name := args[0]
	command := strings.Join(args[1:], " ")

	var a App
	if err := c.postInto("/api/apps/create", map[string]any{
		"name": name, "command": command, "cwd": cwd, "autostart": autostart,
	}, &a); err != nil {
		return err
	}
	fmt.Printf("'%s' yaratildi\n", a.Name)
	return nil
}

func appAction(c *client, action, ref string) error {
	list, err := fetchApps(c)
	if err != nil {
		return err
	}
	a, err := resolveApp(list, ref)
	if err != nil {
		return err
	}
	if _, err := c.post("/api/apps/action", map[string]any{"type": action, "id": a.ID}); err != nil {
		return err
	}
	fmt.Printf("'%s' — %s bajarildi\n", a.Name, action)
	return nil
}

func appDelete(c *client, ref string) error {
	list, err := fetchApps(c)
	if err != nil {
		return err
	}
	a, err := resolveApp(list, ref)
	if err != nil {
		return err
	}
	if _, err := c.post("/api/apps/delete", map[string]any{"id": a.ID}); err != nil {
		return err
	}
	fmt.Printf("'%s' o'chirildi\n", a.Name)
	return nil
}

func appLogsCmd(c *client, ref string) error {
	list, err := fetchApps(c)
	if err != nil {
		return err
	}
	a, err := resolveApp(list, ref)
	if err != nil {
		return err
	}
	var lines []string
	if err := c.getInto("/api/apps/logs?id="+a.ID, &lines); err != nil {
		return err
	}
	for _, l := range lines {
		fmt.Println(l)
	}
	return nil
}
