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

// Pull — backend'dagi apps/projects'ni joriy mashinaga tortib oladi.
// Tunnel loyihalari uchun credentials.json/config.yml fayllarini ham
// tiklaydi — shu bilan mavjud tunnel yangi mashinada cloudflared login
// qilinmasdan ishga tushirilishi mumkin bo'ladi. Xavfsizlik uchun hech
// narsa avtomatik ishga tushirilmaydi (status "stopped" qoladi).
func Pull(appsSvc *apps.Service, tunSvc *tunnel.Service) (Summary, error) {
	var sum Summary

	auth, err := Load()
	if err != nil {
		return sum, err
	}
	if auth == nil {
		return sum, errNotLoggedIn
	}
	c := NewClient(auth.BackendURL, auth.Token)

	appList, err := c.ListApps()
	if err != nil {
		return sum, fmt.Errorf("ilovalar ro'yxati olinmadi: %w", err)
	}
	for _, a := range appList {
		_, err := appsSvc.ImportApp(a.LocalID, apps.AppInput{
			Name: a.Name, Command: a.Command, Cwd: a.Cwd, Autostart: a.Autostart,
		})
		if err != nil {
			return sum, fmt.Errorf("'%s' ilovasi import qilinmadi: %w", a.Name, err)
		}
		sum.Apps++
	}

	projList, err := c.ListProjects()
	if err != nil {
		return sum, fmt.Errorf("loyihalar ro'yxati olinmadi: %w", err)
	}
	pulledDomains := map[string]bool{}
	for _, p := range projList {
		_, err := tunSvc.ImportProject(p.LocalID, tunnel.ProjectInput{
			Name: p.Name, Port: p.Port, Subdomain: p.Subdomain, BaseDomain: p.BaseDomain,
			Protocol: p.Protocol, Autostart: p.Autostart,
		}, p.TunnelID, p.TunnelName)
		if err != nil {
			return sum, fmt.Errorf("'%s' loyihasi import qilinmadi: %w", p.Name, err)
		}
		sum.Projects++

		if p.TunnelSecret != "" && p.TunnelID != "" {
			if err := writeCreds(p.LocalID, p.AccountTag, p.TunnelSecret, p.TunnelID); err != nil {
				return sum, fmt.Errorf("'%s' uchun credentials.json yozilmadi: %w", p.Name, err)
			}
			hostname := tunnelstore.HostnameFor(p.Subdomain, p.BaseDomain)
			cfgPath := filepath.Join(tunnelstore.TunnelDir(p.LocalID), "config.yml")
			credFile := filepath.Join(tunnelstore.TunnelDir(p.LocalID), "credentials.json")
			if err := cf.WriteConfig(cfgPath, p.TunnelID, credFile, hostname, p.Port, p.Protocol); err != nil {
				return sum, fmt.Errorf("'%s' uchun config.yml yozilmadi: %w", p.Name, err)
			}
		}

		if p.BaseDomain != "" && !pulledDomains[p.BaseDomain] {
			pulledDomains[p.BaseDomain] = true
			if !cf.DomainAuthorized(p.BaseDomain) {
				pem, err := c.GetDomainCert(p.BaseDomain)
				if err == nil && pem != "" {
					if err := os.MkdirAll(filepath.Dir(cf.CertPathFor(p.BaseDomain)), 0o700); err == nil {
						if err := os.WriteFile(cf.CertPathFor(p.BaseDomain), []byte(pem), 0o600); err == nil {
							sum.Domains++
							// tunnel.Service.CreateProject umumiy ~/.cloudflared/cert.pem
							// borligini ("Cloudflare bilan bog'langanmi?" tekshiruvi
							// sifatida) alohida talab qiladi — domen-maxsus sertifikat
							// bilan bir xil emas. Shu yerda yozib qo'yamiz, aks holda
							// yangi mashinada domen-maxsus sertifikat tiklangan bo'lsa
							// ham, YANGI loyiha yaratish "avval Cloudflare bilan
							// bog'laning" xatosi bilan bloklanib qoladi (mavjud
							// tunnelni ISHGA TUSHIRISHga bu tekshiruv ta'sir qilmaydi —
							// faqat yangi yaratishga).
							if !cf.CertExists() {
								_ = os.WriteFile(cf.CertPath(), []byte(pem), 0o600)
							}
						}
					}
				}
			}
		}
	}

	return sum, nil
}

func writeCreds(projectID, accountTag, tunnelSecret, tunnelID string) error {
	dir := tunnelstore.TunnelDir(projectID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(credsFile{AccountTag: accountTag, TunnelSecret: tunnelSecret, TunnelID: tunnelID})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "credentials.json"), data, 0o600)
}
