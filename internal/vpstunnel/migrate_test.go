package vpstunnel

import (
	"testing"

	"servergo/internal/vpstunnel/store"
)

// TestMigrateLegacyDomain — dastlabki versiyada bitta "wildcard_domain"
// sozlamasi bor edi; ko'p-domen ro'yxatiga (base_domains/active_domain)
// avtomatik ko'chishi kerak, aks holda eski o'rnatishlarda (production
// tizimlar) domen ro'yxati bo'sh ko'rinib, mavjud loyihalar "sozlanmagan"
// bo'lib qolar edi.
func TestMigrateLegacyDomain(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	_ = st.SetSetting("wildcard_domain", "servergo.uz")
	_ = st.SetSetting("relay_addr", "1.2.3.4:9443")
	_ = st.SetSetting("relay_token", "tok")
	_ = st.SetSetting("relay_fingerprint",
		"5d8179ecf1831038f9fd78aff6f701d34ff9d1e6468fe6f57af307ac019d6843")

	s := &Service{st: st}
	setup := s.SetupState()

	if !setup.Ready {
		t.Fatalf("expected Ready=true after legacy migration, got state: %+v", setup)
	}
	if setup.ActiveDomain != "servergo.uz" {
		t.Fatalf("expected active domain 'servergo.uz', got %q", setup.ActiveDomain)
	}
	if len(setup.Domains) != 1 || setup.Domains[0] != "servergo.uz" {
		t.Fatalf("expected domains=[servergo.uz], got %v", setup.Domains)
	}
	if got := st.GetSetting("base_domains", ""); got != "servergo.uz" {
		t.Fatalf("expected base_domains persisted as 'servergo.uz', got %q", got)
	}
}
