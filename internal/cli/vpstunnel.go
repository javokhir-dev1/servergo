package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"servergo/internal/vpstunnel"
)

func cmdVPSTunnel(c *client, args []string) error {
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}

	switch sub {
	case "create", "add", "new":
		return vtCreate(c, args)
	case "list", "ls":
		watch, _ := takeBoolFlag(args, "-w")
		if watch {
			return watchLoop(3*time.Second, func() error { return vtList(c) })
		}
		return vtList(c)
	case "start", "stop", "restart":
		if len(args) < 1 {
			return fmt.Errorf("foydalanish: vpstunnel %s <id|nom>", sub)
		}
		return vtAction(c, sub, args[0])
	case "delete", "rm":
		if len(args) < 1 {
			return errors.New("foydalanish: vpstunnel delete <id|nom>")
		}
		return vtDelete(c, args[0])
	case "logs":
		if len(args) < 1 {
			return errors.New("foydalanish: vpstunnel logs <id|nom>")
		}
		return vtLogs(c, args[0])
	case "relay":
		if len(args) < 4 {
			return errors.New("foydalanish: vpstunnel relay <manzil:port> <token> <fingerprint> <wildcard-domen>")
		}
		return vtRelay(c, args[0], args[1], args[2], args[3])
	default:
		return fmt.Errorf("noma'lum buyruq: vpstunnel %s", sub)
	}
}

func vtCreate(c *client, args []string) error {
	autostart, args := takeBoolFlag(args, "-a")
	secure, args := takeBoolFlag(args, "-s")
	name, _, args := takeValueFlag(args, "-n")

	if len(args) < 2 {
		return errors.New("foydalanish: vpstunnel create <port> <subdomen> [-n nom] [-s] [-a]")
	}
	port, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("noto'g'ri port: %s", args[0])
	}
	subdomain := args[1]
	if name == "" {
		name = subdomain
	}
	protocol := "http"
	if secure {
		protocol = "https"
	}

	req := vpstunnel.ProjectInput{
		Name:      name,
		Port:      port,
		Subdomain: subdomain,
		Protocol:  protocol,
		Autostart: autostart,
	}
	var p vpstunnel.ProjectView
	if err := c.postInto("/api/vpstunnel/project/create", req, &p); err != nil {
		return err
	}
	fmt.Printf("'%s' yaratildi — %s\n", p.Name, p.URL)
	return nil
}

func fetchVPSProjects(c *client) ([]vpstunnel.ProjectView, error) {
	var ps []vpstunnel.ProjectView
	if err := c.getInto("/api/vpstunnel/projects", &ps); err != nil {
		return nil, err
	}
	return ps, nil
}

func resolveVPSProject(ps []vpstunnel.ProjectView, ref string) (*vpstunnel.ProjectView, error) {
	ref = strings.TrimSpace(ref)
	for i := range ps {
		if ps[i].ID == ref {
			return &ps[i], nil
		}
	}
	var match *vpstunnel.ProjectView
	for i := range ps {
		p := &ps[i]
		ok := strings.EqualFold(p.Name, ref) || strings.EqualFold(p.Subdomain, ref) ||
			(len(ref) >= 4 && strings.HasPrefix(p.ID, ref))
		if !ok {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("bir nechta loyiha '%s' ga mos keladi — to'liq id ko'rsating", ref)
		}
		match = p
	}
	if match == nil {
		return nil, fmt.Errorf("loyiha topilmadi: %s", ref)
	}
	return match, nil
}

func vtList(c *client) error {
	ps, err := fetchVPSProjects(c)
	if err != nil {
		return err
	}
	if len(ps) == 0 {
		fmt.Println("VPS tunnel loyihalari yo'q")
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

func vtAction(c *client, action, ref string) error {
	ps, err := fetchVPSProjects(c)
	if err != nil {
		return err
	}
	p, err := resolveVPSProject(ps, ref)
	if err != nil {
		return err
	}
	if _, err := c.post("/api/vpstunnel/project/action", map[string]any{"type": action, "id": p.ID}); err != nil {
		return err
	}
	fmt.Printf("'%s' — %s bajarildi\n", p.Name, action)
	return nil
}

func vtDelete(c *client, ref string) error {
	ps, err := fetchVPSProjects(c)
	if err != nil {
		return err
	}
	p, err := resolveVPSProject(ps, ref)
	if err != nil {
		return err
	}
	if _, err := c.post("/api/vpstunnel/project/delete", map[string]any{"id": p.ID}); err != nil {
		return err
	}
	fmt.Printf("'%s' o'chirildi\n", p.Name)
	return nil
}

func vtLogs(c *client, ref string) error {
	ps, err := fetchVPSProjects(c)
	if err != nil {
		return err
	}
	p, err := resolveVPSProject(ps, ref)
	if err != nil {
		return err
	}
	var lines []string
	if err := c.getInto("/api/vpstunnel/project/logs?id="+p.ID, &lines); err != nil {
		return err
	}
	for _, l := range lines {
		fmt.Println(l)
	}
	return nil
}

func vtRelay(c *client, addr, token, fingerprint, wildcardDomain string) error {
	req := map[string]any{
		"addr":           addr,
		"token":          token,
		"fingerprint":    fingerprint,
		"wildcardDomain": wildcardDomain,
	}
	var st vpstunnel.SetupState
	if err := c.postInto("/api/vpstunnel/relay", req, &st); err != nil {
		return err
	}
	fmt.Printf("relay sozlandi: %s (wildcard: *.%s)\n", st.RelayAddr, st.WildcardDomain)
	return nil
}
