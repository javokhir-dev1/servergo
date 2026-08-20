package sysmon

import (
	"os/exec"
	"testing"
	"time"
)

func TestParseLabel(t *testing.T) {
	cases := []struct{ in, name, mode string }{
		{"snap.firefox.firefox (enforce)\n", "snap.firefox.firefox", "enforce"},
		{"claude-desktop (unconfined)\n", "claude-desktop", "unconfined"},
		{"unconfined\n", "unconfined", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		name, mode := parseLabel(c.in)
		if name != c.name || mode != c.mode {
			t.Errorf("parseLabel(%q) = (%q, %q), kutilgan (%q, %q)", c.in, name, mode, c.name, c.mode)
		}
	}
}

// Zararsiz jarayon daraxti yaratib, oddiy yopish yo'lini tekshiradi.
func TestKillTree(t *testing.T) {
	cmd := exec.Command("bash", "-c", "sleep 300 & sleep 300 & wait")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Wait() }()

	time.Sleep(400 * time.Millisecond)
	res, err := KillTree(cmd.Process.Pid, 2*time.Second)
	if err != nil {
		t.Fatalf("KillTree: %v", err)
	}
	if res.Total < 2 {
		t.Errorf("daraxtda %d ta jarayon topildi, kamida 2 ta kutilgandi", res.Total)
	}
	if len(res.Termed) != res.Total {
		t.Errorf("termed=%v, %d ta kutilgandi", res.Termed, res.Total)
	}
	if len(res.Survived) != 0 || len(res.Denied) != 0 {
		t.Errorf("survived=%v denied=%v — ikkalasi ham bo'sh bo'lishi kerak", res.Survived, res.Denied)
	}
	if res.Reason != "" {
		t.Errorf("kutilmagan sabab: %q", res.Reason)
	}
}

// Himoyalangan pid'larga signal yubormasligini tekshiradi.
func TestKillTreeProtected(t *testing.T) {
	for _, pid := range []int{0, 1} {
		if _, err := KillTree(pid, time.Second); err == nil {
			t.Errorf("pid %d uchun xato kutilgandi", pid)
		}
	}
}
