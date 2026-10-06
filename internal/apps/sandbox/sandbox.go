//go:build linux

// Package sandbox — ilovalarni bubblewrap (bwrap) ichida, izolyatsiya qilib
// ishga tushirish.
//
// Muammo: "Ilovalar" bo'limidagi buyruqlar ServerGo bilan bir xil
// foydalanuvchi nomidan, hech qanday cheklovsiz ishlaydi. Loyihalardan biri
// buzilsa (paketda RCE, webhook orqali kod bajarish...), hujumchi butun uy
// papkasini oladi: SSH kalitlari, brauzer profillari, qolgan hamma ilovaning
// `.env` fayllari, ServerGo'ning relay tokeni va cloudflared sertifikati.
// Ya'ni bitta ilovaning teshigi butun kompyuterni va barcha tunnellarni
// beradi.
//
// Yechim: jarayonning "ko'zini bog'lash". Loyiha papkasini ko'chirish bu
// muammoni yechmaydi — jarayon o'sha foydalanuvchi nomidan ishlagani uchun
// papkasi qayerda bo'lishidan qat'i nazar uy papkasini o'qiy oladi. Shuning
// uchun bwrap bilan yangi mount/pid namespace ochamiz:
//
//   - uy papkasi ustiga bo'sh tmpfs — ichidagi hech narsa ko'rinmaydi,
//     faqat ilovaning o'z papkasi bind qilib qaytariladi (yozish huquqi bilan)
//   - /usr va /etc — faqat o'qish uchun (node, python, SSL sertifikatlar,
//     DNS sozlamalari ishlashi uchun kerak)
//   - alohida PID namespace — ilova boshqa jarayonlarni ko'rmaydi, ya'ni
//     /proc/<pid>/environ orqali qo'shnilarining sirlarini o'qiy olmaydi
//   - XDG_RUNTIME_DIR o'rniga bo'sh tmpfs — haqiqiy /run/user ichida
//     systemd --user sokiti bor, u orqali sandbox'dan chiqib ketish mumkin
//
// Tarmoq ataylab ochiq qoldirilgan: ilovalar portga quloq solishi va
// internetga chiqishi kerak (tunnel shunga tayanadi).
package sandbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// BinName — kerakli tashqi dastur.
const BinName = "bwrap"

// NoFD — Argv'ga "bu deskriptor berilmaydi" deb aytish uchun. Nol emas,
// chunki 0 (stdin) haqiqiy deskriptor: seccomp filtri aynan stdin orqali
// beriladi — tarmoq izolyatsiyasida pasta qo'shimcha deskriptorlarning
// hammasini yopadi, stdin esa o'tadi.
const NoFD = -1

// Spec — bitta ilova uchun sandbox parametrlari.
type Spec struct {
	Cwd string   // loyiha papkasi — yozish huquqi bilan ko'rinadi (shart)
	RO  []string // faqat o'qish uchun: monorepo node_modules, site-packages, .env...
	RW  []string // yozish ham mumkin: yuklanmalar papkasi, tashqi baza fayli...
	Net *NetSpec // nil — tarmoq hostniki (izolyatsiya yo'q)
}

// NetSpec — tarmoq izolyatsiyasi: ilovaga o'z tarmoq namespace'i beriladi.
//
// Nega kerak: mount va PID namespace fayl va jarayonlarni yopadi, lekin
// localhost ochiq qolardi — buzilgan ilova 127.0.0.1:5432 ga ulanib qo'shni
// loyihaning bazasini o'qiy olardi, ServerGo'ning o'z API'siga ham yetib
// borardi. Izolyatsiya yoqilganda hostning localhost'i butunlay yopiladi va
// faqat HostPorts ro'yxatidagi portlar ichkaridan ko'rinadi.
//
// Buni pasta (passt paketi) amalga oshiradi: u tarmoq namespace'ini yaratib,
// ichida bwrap'ni ishga tushiradi, tashqi ulanishlarni NAT qilib o'tkazadi va
// ilova tinglagan portlarni hostga qaytaradi.
type NetSpec struct {
	HostPorts  []int  // ichkaridan hostning shu TCP portlariga ruxsat (5432, 6379...)
	ResolvConf string // /etc/resolv.conf o'rniga qo'yiladigan fayl (EnsureResolvConf)
}

// NetBinName — tarmoq izolyatsiyasi uchun kerakli dastur.
const NetBinName = "pasta"

// netDNSAddr — sandbox ichidagi nomlar serveri. Haqiqiy bo'lmagan manzil:
// pasta shu manzilga kelgan 53-port trafigini hostning nomlar serveriga
// uzatadi. Hostdagi /etc/resolv.conf 127.0.0.53 ni ko'rsatadi, lekin u
// tarmoq namespace'i ichida hech kim tinglamaydi — shuning uchun ichkariga
// o'z resolv.conf'imizni qo'yamiz.
const netDNSAddr = "169.254.1.1"

// Info — bwrap --info-fd orqali qaytaradigan ma'lumot.
type Info struct {
	ChildPID int    `json:"child-pid"`
	PidNS    uint64 `json:"pid-namespace"`
}

// NetAvailable — tarmoq izolyatsiyasi uchun pasta bormi.
//
// Diqqat: Ubuntu'da kernel.apparmor_restrict_unprivileged_userns=1, ya'ni
// user namespace yaratish uchun AppArmor profili kerak. pasta'ning profili
// paket bilan /etc/apparmor.d/usr.bin.pasta ga o'rnatiladi va YO'LGA
// bog'langan — shuning uchun binarni boshqa joydan ko'chirib ishlatib
// bo'lmaydi, paket sifatida o'rnatilishi shart.
func NetAvailable() error {
	if _, err := exec.LookPath(NetBinName); err != nil {
		return fmt.Errorf("%s o'rnatilmagan — `sudo apt install passt`", NetBinName)
	}
	return nil
}

// EnsureResolvConf — sandbox ichida ishlatiladigan resolv.conf faylini
// yozadi (bir marta yetarli, mazmuni hamma ilova uchun bir xil).
func EnsureResolvConf(dir string) (string, error) {
	path := filepath.Join(dir, "net-resolv.conf")
	want := []byte("# ServerGo sandbox: pasta nomlar serverini shu manzilda beradi\nnameserver " + netDNSAddr + "\n")
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, want) {
		return path, nil
	}
	if err := os.WriteFile(path, want, 0o644); err != nil {
		return "", fmt.Errorf("resolv.conf yozib bo'lmadi: %w", err)
	}
	return path, nil
}

// hostResolvConf — hostdagi /etc/resolv.conf oxir-oqibat qaysi faylga
// ishora qiladi. Ubuntu'da bu /run/systemd/resolve/stub-resolv.conf ga
// bog'lama; bwrap bog'lamaning o'zi ustiga mount qila olmaydi (/etc faqat
// o'qish uchun ulangan), shuning uchun yakuniy manzilni almashtiramiz.
func hostResolvConf() string {
	const p = "/etc/resolv.conf"
	if target, err := filepath.EvalSymlinks(p); err == nil {
		return target
	}
	return p
}

// Available — bwrap o'rnatilganmi va bu tizimda ishlaydimi.
// Ba'zi distributivlarda unprivileged user namespace o'chirilgan bo'ladi —
// bwrap bor bo'lsa ham ishlamaydi, shuning uchun chindan sinab ko'ramiz.
func Available() error {
	path, err := exec.LookPath(BinName)
	if err != nil {
		return fmt.Errorf("%s o'rnatilmagan — `sudo apt install bubblewrap`", BinName)
	}
	out, err := exec.Command(path, "--ro-bind", "/", "/", "--dev", "/dev", "/bin/true").CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s ishlamadi: %s", BinName, msg)
	}
	return nil
}

// Validate — Spec'ni saqlashdan oldin tekshirish: papkalar bor,
// qo'shimcha yo'llar himoyani bekor qilmaydi. Shunda xato ilovani ishga
// tushirishda emas, formani saqlashda ko'rinadi.
func Validate(sp Spec) error {
	if _, err := checkDir(sp.Cwd, "ishchi papka"); err != nil {
		return err
	}
	home := homeDir()
	for _, raw := range append(append([]string{}, sp.RO...), sp.RW...) {
		if _, err := checkExtra(raw, home); err != nil {
			return err
		}
	}
	if sp.Net != nil {
		if err := NetAvailable(); err != nil {
			return err
		}
		for _, port := range sp.Net.HostPorts {
			if port < 1 || port > 65535 {
				return fmt.Errorf("port chegaradan tashqarida: %d", port)
			}
		}
	}
	return nil
}

// Argv — bwrap uchun to'liq argument ro'yxati (birinchi element — bwrap yo'li).
// infoFD >= 0 bo'lsa, bwrap o'sha deskriptorga Info JSON'ini yozadi.
// seccompFD >= 0 bo'lsa, bwrap o'sha deskriptordan cBPF filtrini o'qiydi
// (qarang: SeccompProgram). Berilmasa NoFD yuboriladi.
func Argv(sp Spec, command string, infoFD, seccompFD int) ([]string, error) {
	path, err := exec.LookPath(BinName)
	if err != nil {
		return nil, fmt.Errorf("%s o'rnatilmagan — `sudo apt install bubblewrap`", BinName)
	}
	cwd, err := checkDir(sp.Cwd, "ishchi papka")
	if err != nil {
		return nil, err
	}
	home := homeDir()

	a := []string{}
	if sp.Net != nil {
		head, err := netArgv(sp.Net)
		if err != nil {
			return nil, err
		}
		// pasta tarmoq namespace'ini yaratib, ichida bwrap'ni ishga tushiradi:
		// teskari tartib (bwrap --unshare-net + pasta --netns) ishlamaydi,
		// chunki pasta bwrap yaratgan user namespace'ga kira olmaydi.
		a = append(a, head...)
	}
	a = append(a,
		path,
		// ServerGo o'lsa sandbox ham o'ladi — yetim jarayon qolmaydi.
		"--die-with-parent",
		"--unshare-pid", "--unshare-uts", "--unshare-ipc", "--unshare-cgroup-try",
		// Terminal orqali hujum (TIOCSTI) va signal guruhlaridan ajratish.
		"--new-session",
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
		"--ro-bind", "/usr", "/usr",
		"--ro-bind", "/etc", "/etc",
	)

	// Ubuntu'da /bin, /lib... — /usr ichiga simvolik bog'lama (merged-usr).
	// Boshqa tizimda haqiqiy papka bo'lishi mumkin, shuning uchun tekshiramiz.
	for _, name := range []string{"bin", "sbin", "lib", "lib32", "lib64", "libx32"} {
		p := "/" + name
		st, err := os.Lstat(p)
		if err != nil {
			continue
		}
		if st.Mode()&os.ModeSymlink != 0 {
			a = append(a, "--symlink", "usr/"+name, p)
		} else if st.IsDir() {
			a = append(a, "--ro-bind", p, p)
		}
	}

	// /var — yozish mumkin, lekin tmpfs: ilova log yozsa yozadi, lekin
	// hostdagi /var ga tegmaydi.
	a = append(a, "--tmpfs", "/var", "--symlink", "../run", "/var/run")

	// DNS. Izolyatsiyasiz: /etc/resolv.conf Ubuntu'da /run/systemd/resolve
	// ichiga bog'lama, shuning uchun o'sha papkani ham beramiz.
	// Izolyatsiyada esa 127.0.0.53 ni ichkarida hech kim tinglamaydi —
	// o'rniga pasta'ning manzilini ko'rsatadigan o'z faylimizni qo'yamiz.
	// Tartib muhim: bu bind /run/systemd/resolve dan KEYIN bo'lishi kerak,
	// aks holda papka bind'i faylimizni yopib qo'yadi.
	if sp.Net == nil {
		a = append(a, "--ro-bind-try", "/run/systemd/resolve", "/run/systemd/resolve")
	} else if sp.Net.ResolvConf != "" {
		a = append(a, "--ro-bind", sp.Net.ResolvConf, hostResolvConf())
	}
	// Mahalliy baza soketlari (TCP ishlatilsa ham, ro'yxat ziyon qilmaydi).
	for _, p := range []string{"/run/postgresql", "/run/mysqld", "/run/redis"} {
		a = append(a, "--bind-try", p, p)
	}

	// XDG_RUNTIME_DIR: haqiqiysini BERMAYMIZ — ichida systemd --user va
	// D-Bus sokitlari bor, ular orqali sandbox'dan chiqib ketish mumkin.
	// O'rniga bo'sh tmpfs beramiz, shunda unga tayanadigan kutubxonalar
	// ham ishlayveradi.
	runtimeDir := "/run/user/" + strconv.Itoa(os.Getuid())
	a = append(a,
		"--tmpfs", runtimeDir,
		"--setenv", "XDG_RUNTIME_DIR", runtimeDir,
		"--unsetenv", "DBUS_SESSION_BUS_ADDRESS",
	)

	// Uy papkasini butunlay yopamiz, so'ng faqat ruxsat berilganlarini
	// qaytaramiz (bwrap argumentlarni tartib bilan qo'llaydi).
	if home != "" {
		a = append(a, "--tmpfs", home)
	}
	// Tartib muhim: avval faqat o'qish, keyin yozish mumkin, oxirida ishchi
	// papka. Shunda ishchi papka o'zidan kattaroq read-only yo'l ichida
	// bo'lsa ham (monorepo ildizi kabi) yoziladigan bo'lib qoladi.
	for _, raw := range sp.RO {
		p, err := checkExtra(raw, home)
		if err != nil {
			return nil, err
		}
		a = append(a, "--ro-bind", p, p)
	}
	for _, raw := range sp.RW {
		p, err := checkExtra(raw, home)
		if err != nil {
			return nil, err
		}
		a = append(a, "--bind", p, p)
	}
	a = append(a, "--bind", cwd, cwd, "--chdir", cwd)

	if infoFD >= 0 {
		a = append(a, "--info-fd", strconv.Itoa(infoFD))
	}
	if seccompFD >= 0 {
		a = append(a, "--seccomp", strconv.Itoa(seccompFD))
	}
	return append(a, "--", "/bin/sh", "-c", command), nil
}

// ParseInfo — bwrap yozgan JSON'ni o'qiydi.
func ParseInfo(r io.Reader) (Info, error) {
	var in Info
	if err := json.NewDecoder(r).Decode(&in); err != nil {
		return in, err
	}
	if in.ChildPID <= 0 {
		return in, errors.New("bwrap child-pid qaytarmadi")
	}
	return in, nil
}

// ProcsInNS — berilgan PID namespace'ga tegishli host PID'lari.
// Sandbox ichidagi jarayonlarni tashqaridan signal yuborish uchun kerak:
// ichkaridagi process-group raqamlari host uchun ma'nosiz.
func ProcsInNS(inode uint64) []int {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	out := []int{}
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue // /proc ichidagi raqam bo'lmagan yozuvlar
		}
		var st syscall.Stat_t
		if err := syscall.Stat("/proc/"+e.Name()+"/ns/pid", &st); err != nil {
			continue
		}
		if st.Ino == inode {
			out = append(out, pid)
		}
	}
	return out
}

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Clean(h)
}

func checkDir(raw, label string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", fmt.Errorf("sandbox uchun %s ko'rsatilishi shart", label)
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%s to'liq yo'l bo'lishi kerak: %s", label, p)
	}
	p = filepath.Clean(p)
	st, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("%s topilmadi: %s", label, p)
	}
	if !st.IsDir() {
		return "", fmt.Errorf("%s papka emas: %s", label, p)
	}
	return p, nil
}

// checkExtra — qo'shimcha yo'lni tekshiradi (papka ham, fayl ham bo'lishi
// mumkin: ba'zi ilovaga faqat bitta `.env` kerak). Sandbox'ning ma'nosini
// yo'q qiladigan yo'llarni (uy papkasining o'zi, SSH kalitlari, ServerGo'ning
// tokenlari) ataylab to'sadi — aks holda bitta e'tiborsiz qo'shimcha butun
// himoyani bekor qilardi.
func checkExtra(raw, home string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", errors.New("qo'shimcha yo'l bo'sh")
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("qo'shimcha yo'l to'liq bo'lishi kerak: %s", p)
	}
	p = filepath.Clean(p)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("qo'shimcha yo'l topilmadi: %s", p)
	}
	forbidden := []string{"/", "/etc", "/usr", "/var", "/run", "/proc", "/sys", "/dev", "/boot", "/root"}
	if home != "" {
		forbidden = append(forbidden,
			home,
			filepath.Join(home, ".ssh"),
			filepath.Join(home, ".gnupg"),
			filepath.Join(home, ".aws"),
			filepath.Join(home, ".cloudflared"),
			filepath.Join(home, ".config", "servergo"),
			filepath.Join(home, ".local", "share", "keyrings"),
		)
	}
	for _, f := range forbidden {
		if p == f {
			return "", fmt.Errorf("bu yo'lni sandbox'ga qo'shib bo'lmaydi (himoya ma'nosiz bo'lib qoladi): %s", p)
		}
	}
	return p, nil
}

// netArgv — pasta argumentlari. Ro'yxat ataylab "hamma narsa yopiq, faqat
// kerakligi ochiq" tartibida:
//
//	-t auto              ilova ichkarida tinglagan portlar hostga qaytariladi
//	                     (tunnel 127.0.0.1:PORT ga ulanadi) — ro'yxat
//	                     dinamik, ilovaning portini qo'lda yozish shart emas
//	--host-lo-to-ns-lo   hostning loopback'idan kelgan ulanish ichkarida ham
//	                     loopback bo'lib ko'rinadi (ilovalar 127.0.0.1 ni
//	                     tinglaydi, shuning uchun shart)
//	-T <portlar>         ichkaridan hostning localhost'iga ruxsat — FAQAT
//	                     ko'rsatilgan portlar (pasta'ning standarti "auto",
//	                     ya'ni hamma port ochiq bo'lardi)
//	-u/-U none           UDP ikki tomonga yopiq (DNS --dns-forward orqali)
//	--map-host-loopback none
//	                     hostga to'g'ridan-to'g'ri chiqish yo'li yopiq
//	--dns-forward        ichkaridagi 169.254.1.1 -> hostning nomlar serveri
func netArgv(ns *NetSpec) ([]string, error) {
	path, err := exec.LookPath(NetBinName)
	if err != nil {
		return nil, fmt.Errorf("%s o'rnatilmagan — `sudo apt install passt`", NetBinName)
	}
	ports := "none"
	if len(ns.HostPorts) > 0 {
		list := make([]string, 0, len(ns.HostPorts))
		for _, p := range ns.HostPorts {
			if p < 1 || p > 65535 {
				return nil, fmt.Errorf("port chegaradan tashqarida: %d", p)
			}
			list = append(list, strconv.Itoa(p))
		}
		ports = strings.Join(list, ",")
	}
	return []string{
		path,
		"--config-net",
		"-f", // fon rejimiga o'tmasin: jarayonni ServerGo boshqaradi
		"-q",
		"-t", "auto",
		"--host-lo-to-ns-lo",
		"-T", ports,
		"-u", "none",
		"-U", "none",
		"--map-host-loopback", "none",
		"--dns-forward", netDNSAddr,
		"--",
	}, nil
}

// FindNS — jarayon daraxtidan sandbox'ning PID namespace'ini topadi.
//
// Tarmoq izolyatsiyasida zanjir pasta -> bwrap -> init bo'ladi, pasta esa
// qo'shimcha deskriptorlarni yopadi — ya'ni bwrap'ning --info-fd'i bizga
// yetib kelmaydi. Shuning uchun avlodlarni kezib, PID namespace'ni o'zimiz
// topamiz.
//
// Diqqat: pasta ham o'z PID namespace'ini yaratadi (buyruq uning ichida
// PID 1 bo'lib ishga tushadi), bwrap esa uning ichida yana bittasini. Bizga
// ENG ICHKARIDAGISI kerak — ilovaning o'zi shunda. Birinchi uchragan
// namespace (pasta'ningi) olinsa, SIGTERM ilovaga emas, bwrap'ning tashqi
// jarayoniga borib, yumshoq to'xtatish ishlamay qolardi.
func FindNS(rootPID int, wait time.Duration) (Info, error) {
	selfNS, err := nsInode(rootPID)
	if err != nil {
		return Info{}, err
	}
	deadline := time.Now().Add(wait)
	for {
		if info, depth := deepestNS(rootPID, selfNS, 0); depth > 0 {
			return info, nil
		}
		if time.Now().After(deadline) {
			return Info{}, errors.New("sandbox PID namespace'i topilmadi")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// deepestNS — eng ko'p namespace o'tishi orqasida turgan jarayonni qaytaradi.
// O'tish sodir bo'lgan jarayon — o'sha namespace'ning init'i (ichkarida PID 1).
func deepestNS(pid int, parentNS uint64, depth int) (Info, int) {
	best, bestDepth := Info{}, 0
	for _, child := range childrenOf(pid) {
		ns, err := nsInode(child)
		if err != nil {
			continue
		}
		d := depth
		cand := best
		candDepth := bestDepth
		if ns != parentNS {
			d++
			if d > candDepth {
				cand, candDepth = Info{ChildPID: child, PidNS: ns}, d
			}
		}
		if sub, subDepth := deepestNS(child, ns, d); subDepth > candDepth {
			cand, candDepth = sub, subDepth
		}
		if candDepth > bestDepth {
			best, bestDepth = cand, candDepth
		}
	}
	return best, bestDepth
}

func childrenOf(pid int) []int {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", pid, pid))
	if err != nil {
		return nil
	}
	out := []int{}
	for _, f := range strings.Fields(string(data)) {
		if n, err := strconv.Atoi(f); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func nsInode(pid int) (uint64, error) {
	var st syscall.Stat_t
	if err := syscall.Stat(fmt.Sprintf("/proc/%d/ns/pid", pid), &st); err != nil {
		return 0, err
	}
	return st.Ino, nil
}
