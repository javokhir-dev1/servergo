package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// App — server javobidagi shakl (servergo/internal/apps.AppView bilan mos).
type App struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Command   string   `json:"command"`
	Cwd       string   `json:"cwd"`
	Autostart bool     `json:"autostart"`
	Sandbox   bool     `json:"sandbox"`
	SandboxRW []string `json:"sandboxRw"`
	Status    string   `json:"status"`
	LastError string   `json:"lastError"`
	CreatedAt string   `json:"createdAt"`
	Running   bool     `json:"running"`
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
	case "sandbox", "sb":
		return appsSandboxCmd(c, args)
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
	fmt.Fprintln(t, "ID\tNOM\tHOLAT\tAVTOSTART\tSANDBOX\tBUYRUQ")
	for _, a := range list {
		id := a.ID
		if len(id) > 8 {
			id = id[:8]
		}
		auto := ""
		if a.Autostart {
			auto = "ha"
		}
		sb := "YO'Q"
		if a.Sandbox {
			sb = "ha"
		}
		fmt.Fprintf(t, "%s\t%s\t%s\t%s\t%s\t%s\n", id, a.Name, a.Status, auto, sb, a.Command)
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
	noSandbox, args := takeBoolFlag(args, "--no-sandbox")
	rw, args := takeValueFlags(args, "-r")
	cwd, _, args := takeValueFlag(args, "-c")

	if len(args) < 2 {
		return errors.New("foydalanish: apps create <nom> <buyruq...> [-c ishchi-papka] [-a] [-r qo'shimcha-yo'l] [--no-sandbox]")
	}
	name := args[0]
	command := strings.Join(args[1:], " ")

	// Sandbox standart holatda YOQILGAN — yangi ilova buzilsa uy papkasiga
	// yetib bormasligi kerak. Ishchi papka ko'rsatilmasa sandbox'ning ma'nosi
	// yo'q (ilova aynan shu papkani ko'radi), shuning uchun o'chadi.
	sandbox := !noSandbox && strings.TrimSpace(cwd) != ""
	if !noSandbox && !sandbox {
		fmt.Println("eslatma: ishchi papka (-c) ko'rsatilmagani uchun sandbox yoqilmadi")
	}

	var a App
	if err := c.postInto("/api/apps/create", map[string]any{
		"name": name, "command": command, "cwd": cwd, "autostart": autostart,
		"sandbox": sandbox, "sandboxRw": rw,
	}, &a); err != nil {
		return err
	}
	if a.Sandbox {
		fmt.Printf("'%s' yaratildi — sandbox YOQILGAN (faqat %s ko'rinadi)\n", a.Name, a.Cwd)
	} else {
		fmt.Printf("'%s' yaratildi — sandbox o'chirilgan\n", a.Name)
	}
	return nil
}

// appsSandboxCmd — mavjud ilovada sandbox'ni yoqish/o'chirish.
func appsSandboxCmd(c *client, args []string) error {
	rw, args := takeValueFlags(args, "-r")
	if len(args) < 2 {
		return errors.New("foydalanish: apps sandbox <id|nom> on|off [-r qo'shimcha-yo'l]")
	}
	var on bool
	switch strings.ToLower(args[1]) {
	case "on", "yoq", "1", "true":
		on = true
	case "off", "ochir", "0", "false":
		on = false
	default:
		return fmt.Errorf("noma'lum qiymat: %s (on yoki off)", args[1])
	}

	list, err := fetchApps(c)
	if err != nil {
		return err
	}
	a, err := resolveApp(list, args[0])
	if err != nil {
		return err
	}
	paths := a.SandboxRW
	if len(rw) > 0 {
		paths = rw
	}
	if !on {
		paths = nil
	}

	var out App
	if err := c.postInto("/api/apps/update", map[string]any{
		"id": a.ID, "name": a.Name, "command": a.Command, "cwd": a.Cwd,
		"autostart": a.Autostart, "sandbox": on, "sandboxRw": paths,
	}, &out); err != nil {
		return err
	}
	state := "o'chirildi"
	if out.Sandbox {
		state = "yoqildi"
	}
	fmt.Printf("'%s' — sandbox %s", out.Name, state)
	if out.Sandbox && len(out.SandboxRW) > 0 {
		fmt.Printf(" (qo'shimcha: %s)", strings.Join(out.SandboxRW, ", "))
	}
	fmt.Println()
	if a.Status == "running" || a.Status == "starting" {
		fmt.Println("eslatma: o'zgarish qo'llanishi uchun ilova qayta ishga tushirildi")
	}
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
