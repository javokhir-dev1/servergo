package vpstunnel

import (
	"testing"

	"servergo/internal/vpstunnel/store"
)

func TestValidateCrossCheck(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_ = st.SetSetting("wildcard_domain", "example.uz")

	s := &Service{st: st}
	in := &ProjectInput{Name: "x", Port: 3000, Subdomain: "test"}

	s.crossCheck = func(sub, dom string) (bool, error) { return true, nil }
	if _, err := s.validate(in, ""); err == nil {
		t.Fatal("expected error when crossCheck reports taken, got nil")
	} else {
		t.Logf("got expected error: %v", err)
	}

	s.crossCheck = func(sub, dom string) (bool, error) { return false, nil }
	if _, err := s.validate(in, ""); err != nil {
		t.Fatalf("expected no error when crossCheck reports free, got: %v", err)
	}
}
