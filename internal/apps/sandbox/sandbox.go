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
)

// BinName — kerakli tashqi dastur.
const BinName = "bwrap"

// Spec — bitta ilova uchun sandbox parametrlari.
type Spec struct {
	Cwd string   // loyiha papkasi — yozish huquqi bilan ko'rinadi (shart)
	RW  []string // qo'shimcha yo'llar (ma'lumot papkasi, tashqi skript...)
}

// Info — bwrap --info-fd orqali qaytaradigan ma'lumot.
type Info struct {
	ChildPID int    `json:"child-pid"`
	PidNS    uint64 `json:"pid-namespace"`
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
	for _, raw := range sp.RW {
		if _, err := checkRW(raw, home); err != nil {
			return err
		}
	}
	return nil
}

// Argv — bwrap uchun to'liq argument ro'yxati (birinchi element — bwrap yo'li).
// infoFD > 0 bo'lsa, bwrap o'sha deskriptorga Info JSON'ini yozadi.
func Argv(sp Spec, command string, infoFD int) ([]string, error) {
	path, err := exec.LookPath(BinName)
	if err != nil {
		return nil, fmt.Errorf("%s o'rnatilmagan — `sudo apt install bubblewrap`", BinName)
	}
	cwd, err := checkDir(sp.Cwd, "ishchi papka")
	if err != nil {
		return nil, err
	}
	home := homeDir()

	a := []string{
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
	}

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

	// DNS: /etc/resolv.conf Ubuntu'da /run/systemd/resolve ichiga bog'lama.
	a = append(a, "--ro-bind-try", "/run/systemd/resolve", "/run/systemd/resolve")
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
	for _, raw := range sp.RW {
		p, err := checkRW(raw, home)
		if err != nil {
			return nil, err
		}
		a = append(a, "--bind", p, p)
	}
	a = append(a, "--bind", cwd, cwd, "--chdir", cwd)

	if infoFD > 0 {
		a = append(a, "--info-fd", strconv.Itoa(infoFD))
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

// checkRW — qo'shimcha yo'lni tekshiradi. Sandbox'ning ma'nosini yo'q
// qiladigan yo'llarni (uy papkasining o'zi, SSH kalitlari, ServerGo'ning
// tokenlari) ataylab to'sadi — aks holda bitta e'tiborsiz qo'shimcha butun
// himoyani bekor qilardi.
func checkRW(raw, home string) (string, error) {
	p, err := checkDir(raw, "qo'shimcha yo'l")
	if err != nil {
		return "", err
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
