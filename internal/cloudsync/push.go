package cloudsync

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"servergo/internal/apps"
	"servergo/internal/tunnel"
	"servergo/internal/tunnel/cf"
	tunnelstore "servergo/internal/tunnel/store"
)

// credsFile — cloudflared credentials.json tuzilmasi (standart format).
type credsFile struct {
	AccountTag   string `json:"AccountTag"`
	TunnelSecret string `json:"TunnelSecret"`
	TunnelID     string `json:"TunnelID"`
}

type Summary struct {
	Apps     int
	Projects int
	Domains  int
}

func (s Summary) String() string {
	return fmt.Sprintf("%d ilova, %d loyiha, %d domen sertifikati", s.Apps, s.Projects, s.Domains)
}

// Push — joriy mashinadagi apps/projects va tunnel credentials'ini
// backend'ga yuboradi.
func Push(appsSvc *apps.Service, tunSvc *tunnel.Service) (Summary, error) {
	var sum Summary

	auth, err := Load()
	if err != nil {
		return sum, err
	}
	if auth == nil {
		return sum, errNotLoggedIn
	}
	c := NewClient(auth.BackendURL, auth.Token)

	appList, err := appsSvc.ListApps()
	if err != nil {
		return sum, fmt.Errorf("ilovalar ro'yxati: %w", err)
	}
	for _, a := range appList {
		err := c.UpsertApp(a.ID, AppRecord{
			Name: a.Name, Command: a.Command, Cwd: a.Cwd, Autostart: a.Autostart,
		})
		if err != nil {
			return sum, fmt.Errorf("'%s' ilovasi yuborilmadi: %w", a.Name, err)
		}
		sum.Apps++
	}

	projList, err := tunSvc.ListProjects()
	if err != nil {
		return sum, fmt.Errorf("loyihalar ro'yxati: %w", err)
	}
	pushedDomains := map[string]bool{}
	for _, p := range projList {
		rec := ProjectRecord{
			Name: p.Name, Port: p.Port, Subdomain: p.Subdomain, BaseDomain: p.BaseDomain,
			Protocol: p.Protocol, TunnelID: p.TunnelID, TunnelName: p.TunnelName,
			Autostart: p.Autostart,
		}
		// cf.CreateTunnel credentials faylini `<tunnelName>.json` nomi bilan
		// yozadi (servergo/internal/tunnel/service.go), "credentials.json" emas.
		if p.TunnelName != "" {
			credFile := filepath.Join(tunnelstore.TunnelDir(p.ID), p.TunnelName+".json")
			if creds, err := readCreds(credFile); err == nil {
				rec.AccountTag = creds.AccountTag
				rec.TunnelSecret = creds.TunnelSecret
			}
		}
		if err := c.UpsertProject(p.ID, rec); err != nil {
			return sum, fmt.Errorf("'%s' loyihasi yuborilmadi: %w", p.Name, err)
		}
		sum.Projects++

		if p.BaseDomain != "" && !pushedDomains[p.BaseDomain] {
			pushedDomains[p.BaseDomain] = true
			if cf.DomainAuthorized(p.BaseDomain) {
				pem, err := os.ReadFile(cf.CertPathFor(p.BaseDomain))
				if err == nil {
					if err := c.PutDomainCert(p.BaseDomain, string(pem)); err == nil {
						sum.Domains++
					}
				}
			}
		}
	}

	return sum, nil
}

func readCreds(credFile string) (credsFile, error) {
	var c credsFile
	data, err := os.ReadFile(credFile)
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(data, &c)
	return c, err
}
