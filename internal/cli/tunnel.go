package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"servergo/internal/tunnel"
)

func cmdTunnel(c *client, args []string) error {
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}

	switch sub {
	case "create", "add", "new":
		return tunCreate(c, args)
	case "list", "ls":
		watch, _ := takeBoolFlag(args, "-w")
		if watch {
			return watchLoop(3*time.Second, func() error { return tunList(c) })
		}
		return tunList(c)
	case "start", "stop", "restart":
		if len(args) < 1 {
			return fmt.Errorf("foydalanish: tunnel %s <id|nom>", sub)
		}
		return tunAction(c, sub, args[0])
	case "delete", "rm":
		if len(args) < 1 {
			return errors.New("foydalanish: tunnel delete <id|nom>")
		}
		return tunDelete(c, args[0])
	case "logs":
		if len(args) < 1 {
			return errors.New("foydalanish: tunnel logs <id|nom>")
		}
		return tunLogs(c, args[0])
	case "diagnose", "diag":
		if len(args) < 1 {
			return errors.New("foydalanish: tunnel diagnose <id|nom>")
		}
		return tunDiagnose(c, args[0])
	case "fixdns":
		if len(args) < 1 {
			return errors.New("foydalanish: tunnel fixdns <id|nom>")
		}
		return tunFixDNS(c, args[0])
	case "domains":
		return tunDomains(c)
	case "add-domain":
		if len(args) < 1 {
			return errors.New("foydalanish: tunnel add-domain <domen>")
		}
		return tunAddDomain(c, args[0])
	case "remove-domain", "rm-domain":
		if len(args) < 1 {
			return errors.New("foydalanish: tunnel remove-domain <domen>")
		}
		return tunRemoveDomain(c, args[0])
	case "active-domain":
		if len(args) < 1 {
			return errors.New("foydalanish: tunnel active-domain <domen>")
		}
		return tunActiveDomain(c, args[0])
	default:
		return fmt.Errorf("noma'lum buyruq: tunnel %s", sub)
	}
}

func tunCreate(c *client, args []string) error {
	force, args := takeBoolFlag(args, "-f")
	secure, args := takeBoolFlag(args, "-s")
	autostart, args := takeBoolFlag(args, "-a")
	domain, _, args := takeValueFlag(args, "-d")
	name, _, args := takeValueFlag(args, "-n")

	if len(args) < 1 {
		return errors.New("foydalanish: tunnel create <port> [subdomen] [-n nom] [-d domen] [-s] [-a] [-f]\n" +
			"  subdomen ko'rsatilmasa (yoki '@') — domenning o'zi uchun tunnel quriladi")
	}
	port, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("noto'g'ri port: %s", args[0])
	}
	subdomain := ""
	if len(args) > 1 {
		subdomain = args[1]
	}
	if subdomain == "@" {
		subdomain = ""
	}

	if domain == "" {
		var st tunnel.SetupState
		if err := c.getInto("/api/tunnel/setup", &st); err != nil {
			return err
		}
		if st.ActiveDomain == "" {
			return errors.New("bazaviy domen tanlanmagan — avval `servergo tunnel add-domain <domen>` bajaring yoki -d bilan ko'rsating")
		}
		domain = st.ActiveDomain
	}
	if name == "" {
		if subdomain == "" {
			name = domain
		} else {
			name = subdomain
		}
	}
	protocol := "http"
	if secure {
		protocol = "https"
	}

	var req struct {
		tunnel.ProjectInput
		Force bool `json:"force"`
	}
	req.Name = name
	req.Port = port
	req.Subdomain = subdomain
	req.BaseDomain = domain
	req.Protocol = protocol
	req.Autostart = autostart
	req.Force = force

	var p tunnel.ProjectView
	if err := c.postInto("/api/tunnel/project/create", req, &p); err != nil {
		if msg, ok := strings.CutPrefix(err.Error(), tunnel.ErrDNSTaken+"|"); ok {
			return fmt.Errorf("%s\n`-f` bayrog'i bilan qayta urinib ko'ring (mavjud DNS yozuvi almashtiriladi)", msg)
		}
		return err
	}
	fmt.Printf("'%s' yaratildi — %s\n", p.Name, p.URL)
	return nil
}

func fetchProjects(c *client) ([]tunnel.ProjectView, error) {
	var ps []tunnel.ProjectView
	if err := c.getInto("/api/tunnel/projects", &ps); err != nil {
		return nil, err
	}
	return ps, nil
}

// resolveProject — id (to'liq yoki qisqa prefiks), nom yoki subdomen bo'yicha topadi.
func resolveProject(ps []tunnel.ProjectView, ref string) (*tunnel.ProjectView, error) {
	ref = strings.TrimSpace(ref)
	for i := range ps {
		if ps[i].ID == ref {
			return &ps[i], nil
		}
	}
	var match *tunnel.ProjectView
	tryMatch := func(ok bool, p *tunnel.ProjectView) error {
		if !ok {
			return nil
		}
		if match != nil {
			return fmt.Errorf("bir nechta loyiha '%s' ga mos keladi — to'liq id ko'rsating", ref)
		}
		match = p
		return nil
	}
	for i := range ps {
		p := &ps[i]
		ok := strings.EqualFold(p.Name, ref) || strings.EqualFold(p.Subdomain, ref) ||
			(len(ref) >= 4 && strings.HasPrefix(p.ID, ref))
		if err := tryMatch(ok, p); err != nil {
			return nil, err
		}
	}
	if match == nil {
		return nil, fmt.Errorf("loyiha topilmadi: %s", ref)
	}
	return match, nil
}

func tunList(c *client) error {
	ps, err := fetchProjects(c)
	if err != nil {
		return err
	}
	if len(ps) == 0 {
		fmt.Println("tunnel loyihalari yo'q")
		return nil
	}
	t := newTable()
	fmt.Fprintln(t, "ID\tNOM\tHOLAT\tMANZIL\t→ PORT\tAVTOSTART")
	for _, p := range ps {
		id := p.ID
		if len(id) > 8 {
			id = id[:8]
		}
		auto := ""
		if p.Autostart {
			auto = "ha"
		}
		fmt.Fprintf(t, "%s\t%s\t%s\t%s\t%d\t%s\n", id, p.Name, p.Status, p.Hostname(), p.Port, auto)
	}
	if err := t.Flush(); err != nil {
		return err
	}
	for _, p := range ps {
		if p.LastError != "" {
			fmt.Printf("  ! %s: %s\n", p.Name, p.LastError)
		}
	}
	return nil
}

func tunAction(c *client, action, ref string) error {
	ps, err := fetchProjects(c)
	if err != nil {
		return err
	}
	p, err := resolveProject(ps, ref)
	if err != nil {
		return err
	}
	if _, err := c.post("/api/tunnel/project/action", map[string]any{"type": action, "id": p.ID}); err != nil {
		return err
	}
	fmt.Printf("'%s' — %s bajarildi\n", p.Name, action)
	return nil
}

func tunDelete(c *client, ref string) error {
	ps, err := fetchProjects(c)
	if err != nil {
		return err
	}
	p, err := resolveProject(ps, ref)
	if err != nil {
		return err
	}
	var res struct {
		Warning string `json:"warning"`
	}
	if err := c.postInto("/api/tunnel/project/delete", map[string]any{"id": p.ID}, &res); err != nil {
		return err
	}
	if res.Warning != "" {
		fmt.Println("ogohlantirish:", res.Warning)
	} else {
		fmt.Printf("'%s' o'chirildi\n", p.Name)
	}
	return nil
}

func tunLogs(c *client, ref string) error {
	ps, err := fetchProjects(c)
	if err != nil {
		return err
	}
	p, err := resolveProject(ps, ref)
	if err != nil {
		return err
	}
	var lines []string
	if err := c.getInto("/api/tunnel/project/logs?id="+p.ID, &lines); err != nil {
		return err
	}
	for _, l := range lines {
		fmt.Println(l)
	}
	return nil
}

func tunDiagnose(c *client, ref string) error {
	ps, err := fetchProjects(c)
	if err != nil {
		return err
	}
	p, err := resolveProject(ps, ref)
	if err != nil {
		return err
	}
	var res tunnel.DiagResult
	if err := c.postInto("/api/tunnel/project/diagnose", map[string]any{"id": p.ID}, &res); err != nil {
		return err
	}
	for _, chk := range res.Checks {
		mark := map[string]string{"ok": "✓", "warn": "!", "fail": "✗"}[chk.Status]
		fmt.Printf("  %s %-18s %s\n", mark, chk.Name, chk.Detail)
	}
	fmt.Println()
	fmt.Println(res.Summary)
	if res.CanFix {
		fmt.Printf("\n`servergo tunnel fixdns %s` bilan tuzatish mumkin\n", ref)
	}
	return nil
}

func tunFixDNS(c *client, ref string) error {
	ps, err := fetchProjects(c)
	if err != nil {
		return err
	}
	p, err := resolveProject(ps, ref)
	if err != nil {
		return err
	}
	if _, err := c.post("/api/tunnel/project/fixdns", map[string]any{"id": p.ID}); err != nil {
		return err
	}
	fmt.Printf("'%s' uchun DNS yozuvi qayta yo'naltirildi\n", p.Name)
	return nil
}

func tunDomains(c *client) error {
	var st tunnel.SetupState
	if err := c.getInto("/api/tunnel/setup", &st); err != nil {
		return err
	}
	if len(st.Domains) == 0 {
		fmt.Println("bazaviy domenlar qo'shilmagan")
		return nil
	}
	for _, d := range st.Domains {
		mark := " "
		if d == st.ActiveDomain {
			mark = "*"
		}
		fmt.Printf("%s %s\n", mark, d)
	}
	return nil
}

func tunAddDomain(c *client, domain string) error {
	fmt.Printf("tekshirilmoqda… hisobingizda qo'shimcha domen bo'lsa, brauzer ochiladi — '%s'ni tanlang\n", domain)
	var st tunnel.SetupState
	if err := c.postInto("/api/tunnel/domain/add", map[string]any{"domain": domain}, &st); err != nil {
		return err
	}
	fmt.Printf("'%s' qo'shildi va faol domen qilib tanlandi\n", domain)
	return nil
}

func tunRemoveDomain(c *client, domain string) error {
	var st tunnel.SetupState
	if err := c.postInto("/api/tunnel/domain/remove", map[string]any{"domain": domain}, &st); err != nil {
		return err
	}
	fmt.Printf("'%s' ro'yxatdan o'chirildi\n", domain)
	return nil
}

func tunActiveDomain(c *client, domain string) error {
	var st tunnel.SetupState
	if err := c.postInto("/api/tunnel/domain/active", map[string]any{"domain": domain}, &st); err != nil {
		return err
	}
	fmt.Printf("faol domen: %s\n", domain)
	return nil
}
