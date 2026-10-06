//go:build linux

package sandbox

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// argvString — tekshirishni osonlashtirish uchun argumentlarni bitta qatorga.
func argvString(t *testing.T, sp Spec) string {
	t.Helper()
	argv, err := Argv(sp, "true", NoFD, NoFD)
	if err != nil {
		t.Fatalf("Argv: %v", err)
	}
	return strings.Join(argv, " ")
}

// TestArgvSealsHome — eng muhim shart: uy papkasi ustiga tmpfs qo'yiladi va
// ilovaning o'z papkasi undan KEYIN bind qilinadi (bwrap argumentlarni
// tartib bilan qo'llaydi — teskari bo'lsa loyiha papkasi ham yopilib qolardi).
func TestArgvSealsHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("uy papkasi aniqlanmadi")
	}
	cwd := t.TempDir()
	line := argvString(t, Spec{Cwd: cwd})

	tmpfsAt := strings.Index(line, "--tmpfs "+home)
	bindAt := strings.Index(line, "--bind "+cwd+" "+cwd)
	if tmpfsAt < 0 {
		t.Fatalf("uy papkasi yopilmagan: %s", line)
	}
	if bindAt < 0 {
		t.Fatalf("ishchi papka bind qilinmagan: %s", line)
	}
	if bindAt < tmpfsAt {
		t.Fatal("ishchi papka tmpfs'dan OLDIN bind qilingan — tmpfs uni yopib qo'yadi")
	}
	if !strings.Contains(line, "--unshare-pid") {
		t.Error("PID namespace ajratilmagan — ilova qo'shnilarining /proc/<pid>/environ'ini o'qiy oladi")
	}
	if strings.Contains(line, "/run/user/"+os.Getenv("UID")+" /run/user") {
		t.Error("haqiqiy XDG_RUNTIME_DIR berilgan — ichida systemd --user sokiti bor")
	}
}

func TestArgvRequiresCwd(t *testing.T) {
	if _, err := Argv(Spec{}, "true", NoFD, NoFD); err == nil {
		t.Fatal("ishchi papkasiz sandbox yaratildi")
	}
	if _, err := Argv(Spec{Cwd: "nisbiy/yo'l"}, "true", NoFD, NoFD); err == nil {
		t.Fatal("nisbiy yo'l qabul qilindi")
	}
	if _, err := Argv(Spec{Cwd: filepath.Join(t.TempDir(), "yo'q")}, "true", NoFD, NoFD); err == nil {
		t.Fatal("mavjud bo'lmagan papka qabul qilindi")
	}
}

// TestValidateRejectsDangerousPaths — qo'shimcha yo'l orqali himoyani bekor
// qilib bo'lmasligi kerak.
func TestValidateRejectsDangerousPaths(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("uy papkasi aniqlanmadi")
	}
	cwd := t.TempDir()
	for _, bad := range []string{"/", "/etc", "/usr", home, filepath.Join(home, ".ssh")} {
		if _, serr := os.Stat(bad); serr != nil {
			continue // bu tizimda yo'q
		}
		if err := Validate(Spec{Cwd: cwd, RW: []string{bad}}); err == nil {
			t.Errorf("xavfli yo'l qabul qilindi: %s", bad)
		}
	}
	// Oddiy papka esa o'tishi kerak.
	if err := Validate(Spec{Cwd: cwd, RW: []string{t.TempDir()}}); err != nil {
		t.Errorf("oddiy papka rad etildi: %v", err)
	}
}

// TestSandboxHidesSecrets — haqiqiy bwrap bilan: sandbox ichidan uy
// papkasidagi fayl ko'rinmasligi, o'z papkasidagi esa ko'rinishi kerak.
func TestSandboxHidesSecrets(t *testing.T) {
	if err := Available(); err != nil {
		t.Skipf("bwrap ishlamaydi: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("uy papkasi aniqlanmadi")
	}
	secret := filepath.Join(home, ".servergo-sandbox-test-secret")
	if err := os.WriteFile(secret, []byte("maxfiy"), 0o600); err != nil {
		t.Skipf("sinov faylini yozib bo'lmadi: %v", err)
	}
	defer os.Remove(secret)

	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "ochiq.txt"), []byte("salom"), 0o600); err != nil {
		t.Fatal(err)
	}

	argv, err := Argv(Spec{Cwd: cwd}, "cat ochiq.txt; cat "+secret+" 2>&1", NoFD, NoFD)
	if err != nil {
		t.Fatal(err)
	}
	// Chiqish kodini tekshirmaymiz: maxfiy fayl ko'rinmasa `cat` 1 qaytaradi —
	// bu aynan kutilgan natija. Muhimi — chiqishning mazmuni.
	out, _ := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	got := string(out)
	if !strings.Contains(got, "salom") {
		t.Errorf("ilova o'z faylini ko'rmadi: %s", got)
	}
	if strings.Contains(got, "maxfiy") {
		t.Errorf("SANDBOX TESHIK — uy papkasidagi fayl o'qildi: %s", got)
	}
}

// TestReadOnlyParentKeepsCwdWritable — monorepo holati: ildiz faqat o'qish
// uchun beriladi (node_modules kerak), lekin ishchi papka uning ichida va
// yoziladigan bo'lib qolishi kerak. bwrap argumentlarni tartib bilan
// qo'llaydi, shuning uchun bu tartibga bog'liq.
func TestReadOnlyParentKeepsCwdWritable(t *testing.T) {
	if err := Available(); err != nil {
		t.Skipf("bwrap ishlamaydi: %v", err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "umumiy.txt"), []byte("ildiz"), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(root, "ilova")
	if err := os.Mkdir(cwd, 0o700); err != nil {
		t.Fatal(err)
	}

	argv, err := Argv(Spec{Cwd: cwd, RO: []string{root}},
		"cat "+filepath.Join(root, "umumiy.txt")+
			"; echo yangi > ./ichki.txt && echo CWD-YOZILDI"+
			"; echo buzdim > "+filepath.Join(root, "umumiy.txt")+" 2>/dev/null && echo RO-TESHIK", NoFD, NoFD)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	got := string(out)
	if !strings.Contains(got, "ildiz") {
		t.Errorf("read-only yo'l o'qilmadi: %s", got)
	}
	if !strings.Contains(got, "CWD-YOZILDI") {
		t.Errorf("ishchi papkaga yozib bo'lmadi (read-only ildiz uni ham yopib qo'ygan): %s", got)
	}
	if strings.Contains(got, "RO-TESHIK") {
		t.Errorf("read-only yo'lga yozib bo'ldi: %s", got)
	}
}

// TestExtraPathAcceptsFile — ba'zi ilovaga faqat bitta fayl kerak
// (masalan monorepo ildizidagi umumiy .env).
func TestExtraPathAcceptsFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, ".env")
	if err := os.WriteFile(f, []byte("A=1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Validate(Spec{Cwd: dir, RO: []string{f}}); err != nil {
		t.Errorf("fayl qo'shimcha yo'l sifatida rad etildi: %v", err)
	}
}

// TestSeccompBlocksEscapeSyscalls — filtr haqiqatan qo'llanadimi: sandbox
// ichida `unshare` yangi namespace ocholmasligi kerak (bu kernel'dan qochib
// chiqishning eng ko'p ishlatiladigan yo'li), oddiy ishlar esa ishlashi kerak.
func TestSeccompBlocksEscapeSyscalls(t *testing.T) {
	if err := Available(); err != nil {
		t.Skipf("bwrap ishlamaydi: %v", err)
	}
	prog := SeccompProgram()
	if len(prog) == 0 {
		t.Skip("bu arxitektura uchun seccomp filtri yo'q")
	}
	if len(prog)%8 != 0 {
		t.Fatalf("filtr uzunligi 8 ga bo'linmadi: %d", len(prog))
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(prog); err != nil {
		t.Fatal(err)
	}
	w.Close()
	defer r.Close()

	cwd := t.TempDir()
	// Filtr stdin orqali beriladi — manager ham shunday qiladi.
	argv, err := Argv(Spec{Cwd: cwd},
		"echo ODDIY-ISH-OK; unshare --mount /bin/true 2>&1 && echo UNSHARE-OTDI", NoFD, 0)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = r
	out, _ := cmd.CombinedOutput()
	got := string(out)

	if !strings.Contains(got, "ODDIY-ISH-OK") {
		t.Fatalf("filtr oddiy ishni ham to'sdi: %s", got)
	}
	if strings.Contains(got, "UNSHARE-OTDI") {
		t.Errorf("unshare() to'silmadi — seccomp qo'llanmagan: %s", got)
	}
}

// TestNetArgvClosesHostByDefault — pasta argumentlari "hamma narsa yopiq"
// holatidan boshlanishi kerak: pasta'ning o'z standarti -T auto, ya'ni
// hostning hamma porti ichkaridan ko'rinardi.
func TestNetArgvClosesHostByDefault(t *testing.T) {
	if err := NetAvailable(); err != nil {
		t.Skipf("pasta yo'q: %v", err)
	}
	line := argvString(t, Spec{Cwd: t.TempDir(), Net: &NetSpec{}})
	for _, want := range []string{"-T none", "--map-host-loopback none", "-U none", "-t auto"} {
		if !strings.Contains(line, want) {
			t.Errorf("%q yo'q: %s", want, line)
		}
	}

	withPorts := argvString(t, Spec{Cwd: t.TempDir(), Net: &NetSpec{HostPorts: []int{5432, 6379}}})
	if !strings.Contains(withPorts, "-T 5432,6379") {
		t.Errorf("ruxsat berilgan portlar o'tmadi: %s", withPorts)
	}
	if _, err := Argv(Spec{Cwd: t.TempDir(), Net: &NetSpec{HostPorts: []int{70000}}}, "true", NoFD, NoFD); err == nil {
		t.Error("noto'g'ri port qabul qilindi")
	}
}

// TestNetIsolationBlocksHostLoopback — eng muhim shart: izolyatsiya
// yoqilganda ilova hostning localhost'idagi xizmatlarga ulanmasligi,
// ruxsat berilgan port esa ishlashi kerak.
func TestNetIsolationBlocksHostLoopback(t *testing.T) {
	if err := Available(); err != nil {
		t.Skipf("bwrap ishlamaydi: %v", err)
	}
	if err := NetAvailable(); err != nil {
		t.Skipf("pasta yo'q: %v", err)
	}

	// Hostda "qo'shni xizmat" rolini o'ynaydigan tinglovchi.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte("qo'shni-xizmat\n"))
			c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port

	resolv, err := EnsureResolvConf(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// pasta tarmoqni sozlab olishi uchun biroz kutamiz, so'ng ulanishni
	// sinaymiz (bash /dev/tcp — qo'shimcha dasturga ehtiyoj yo'q).
	probe := "sleep 1; bash -c 'exec 3<>/dev/tcp/127.0.0.1/" + strconv.Itoa(port) +
		"' 2>/dev/null && echo ULANDI || echo ULANMADI"

	run := func(sp Spec) string {
		argv, err := Argv(sp, probe, NoFD, NoFD)
		if err != nil {
			t.Fatal(err)
		}
		out, _ := exec.Command(argv[0], argv[1:]...).CombinedOutput()
		return string(out)
	}

	blocked := run(Spec{Cwd: t.TempDir(), Net: &NetSpec{ResolvConf: resolv}})
	if !strings.Contains(blocked, "ULANMADI") {
		t.Errorf("TARMOQ TESHIK — ilova hostning localhost'iga ulandi: %s", blocked)
	}

	allowed := run(Spec{Cwd: t.TempDir(), Net: &NetSpec{HostPorts: []int{port}, ResolvConf: resolv}})
	if !strings.Contains(allowed, "ULANDI") {
		t.Errorf("ruxsat berilgan port ishlamadi: %s", allowed)
	}
}

// TestNetIsolationProvidesResolvConf — izolyatsiyada ichkaridagi
// /etc/resolv.conf pasta'ning nomlar serverini ko'rsatishi kerak. Avval
// /run/systemd/resolve papkasining bind'i bu faylni yopib qo'yardi va DNS
// EAI_AGAIN bilan ishlamay qolardi.
func TestNetIsolationProvidesResolvConf(t *testing.T) {
	if err := Available(); err != nil {
		t.Skipf("bwrap ishlamaydi: %v", err)
	}
	if err := NetAvailable(); err != nil {
		t.Skipf("pasta yo'q: %v", err)
	}
	resolv, err := EnsureResolvConf(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	argv, err := Argv(Spec{Cwd: t.TempDir(), Net: &NetSpec{ResolvConf: resolv}},
		"cat /etc/resolv.conf", NoFD, NoFD)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	if !strings.Contains(string(out), netDNSAddr) {
		t.Errorf("ichkaridagi resolv.conf %s ni ko'rsatmadi: %s", netDNSAddr, out)
	}
}

// TestFindNSPicksInnermostNamespace — pasta ham, bwrap ham o'z PID
// namespace'ini yaratadi. FindNS eng ichkaridagisini (ilova turgan joyni)
// qaytarishi kerak: pasta'ningi olinsa yumshoq to'xtatish ishlamaydi, chunki
// SIGTERM ilovaga emas, bwrap'ning tashqi jarayoniga borardi.
func TestFindNSPicksInnermostNamespace(t *testing.T) {
	if err := Available(); err != nil {
		t.Skipf("bwrap ishlamaydi: %v", err)
	}
	if err := NetAvailable(); err != nil {
		t.Skipf("pasta yo'q: %v", err)
	}
	resolv, err := EnsureResolvConf(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	argv, err := Argv(Spec{Cwd: t.TempDir(), Net: &NetSpec{ResolvConf: resolv}},
		"sleep 30", NoFD, NoFD)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	info, err := FindNS(cmd.Process.Pid, 10*time.Second)
	if err != nil {
		t.Fatalf("FindNS: %v", err)
	}
	// Ichki namespace'da kamida ikkita jarayon bo'ladi: bwrap'ning init'i va
	// ilovaning o'zi. pasta'ning namespace'ida esa faqat bitta — shu bilan
	// to'g'ri topilganini bilamiz.
	pids := ProcsInNS(info.PidNS)
	if len(pids) < 2 {
		t.Fatalf("eng ichki namespace emas: %d jarayon topildi (%v)", len(pids), pids)
	}
	// init'dan boshqa jarayonga signal yuborib ko'ramiz (0 — tekshiruv).
	signalled := 0
	for _, pid := range pids {
		if pid == info.ChildPID {
			continue
		}
		if err := syscall.Kill(pid, 0); err == nil {
			signalled++
		}
	}
	if signalled == 0 {
		t.Error("init'dan boshqa jarayon topilmadi — SIGTERM hech kimga yetmaydi")
	}
}
