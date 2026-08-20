package sysmon

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// KillResult — bitta daraxtni to'xtatish natijasi.
type KillResult struct {
	Requested int    `json:"requested"`
	Termed    []int  `json:"termed"`   // SIGTERM bilan chiroyli yopilganlar
	Killed    []int  `json:"killed"`   // javob bermagani uchun SIGKILL qilinganlar
	Survived  []int  `json:"survived"` // baribir tirik qolganlar
	Denied    []int  `json:"denied"`   // signal yuborishga ruxsat berilmagan (EPERM)
	Total     int    `json:"total"`    // daraxtdagi umumiy jarayonlar soni
	Reason    string `json:"reason"`   // nima uchun yopilmagani (bo'sh bo'lishi mumkin)
}

var ErrProtected = errors.New("bu jarayonni to'xtatib bo'lmaydi (himoyalangan)")

// CollectTree — pid va uning BARCHA avlodlarini yig'adi (exe'dan qat'i nazar).
// Foydalanuvchi "butun daraxtni o'ldirish"ni tanlagani uchun chegara qo'yilmaydi.
func CollectTree(pid int) ([]int, error) {
	procs, err := ReadProcs()
	if err != nil {
		return nil, err
	}
	if _, ok := procs[pid]; !ok {
		return nil, errors.New("bunday jarayon topilmadi")
	}

	children := make(map[int][]int, len(procs))
	for p, pr := range procs {
		children[pr.PPID] = append(children[pr.PPID], p)
	}

	var out []int
	seen := map[int]bool{}
	stack := []int{pid}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[cur] {
			continue
		}
		seen[cur] = true
		out = append(out, cur)
		stack = append(stack, children[cur]...)
	}
	return out, nil
}

// KillTree — daraxtni to'xtatadi: avval hammasiga SIGTERM, keyin grace
// davomida kutadi, tirik qolganlarga SIGKILL.
func KillTree(pid int, grace time.Duration) (*KillResult, error) {
	if pid <= 1 || pid == os.Getpid() || isAncestorOfSelf(pid) {
		return nil, ErrProtected
	}

	tree, err := CollectTree(pid)
	if err != nil {
		return nil, err
	}

	// Himoyani UI dagi disabled tugmaga qoldirib bo'lmaydi — API ga to'g'ridan
	// to'g'ri so'rov ham kelishi mumkin, shuning uchun shu yerda tekshiramiz.
	// Daraxt ichida himoyalangan jarayon bo'lsa, butun amal bekor qilinadi.
	self := os.Getpid()
	for _, p := range tree {
		if p <= 1 || p == self {
			continue
		}
		if pr := readProc(p); pr != nil && strings.Contains(pr.Cmd, pm2DaemonMarker) {
			return nil, fmt.Errorf("%w: pm2 demoni daraxt ichida (pid %d)", ErrProtected, p)
		}
	}

	// O'zimizni yoki init'ni tasodifan daraxtdan tushib qolganini tekshiramiz.
	filtered := tree[:0]
	for _, p := range tree {
		if p > 1 && p != self {
			filtered = append(filtered, p)
		}
	}
	tree = filtered
	if len(tree) == 0 {
		return nil, ErrProtected
	}

	// CollectTree otani boladan oldin qaytaradi; teskari aylantirsak eng chuqur
	// avlodlar birinchi bo'ladi. Ota oldin o'lsa, bolalar init'ga o'tib
	// daraxtdan chiqib ketishi mumkin — shuning uchun pastdan yuqoriga yopamiz.
	for i, j := 0, len(tree)-1; i < j; i, j = i+1, j-1 {
		tree[i], tree[j] = tree[j], tree[i]
	}

	// Bo'sh slice'lar: nil bo'lsa JSON'da null chiqib, frontend .length da yiqiladi.
	res := &KillResult{
		Requested: pid,
		Total:     len(tree),
		Termed:    []int{},
		Killed:    []int{},
		Survived:  []int{},
		Denied:    []int{},
	}
	denied := map[int]bool{}
	for _, p := range tree {
		err := syscall.Kill(p, syscall.SIGTERM)
		switch {
		case err == nil:
			res.Termed = append(res.Termed, p)
		case errors.Is(err, syscall.ESRCH):
			// jarayon o'zi allaqachon tugagan — muammo emas
		case errors.Is(err, syscall.EPERM):
			denied[p] = true
			res.Denied = append(res.Denied, p)
		}
	}

	// grace davomida 100ms oralatib tekshiramiz.
	deadline := time.Now().Add(grace)
	var alive []int
	for {
		alive = alive[:0]
		for _, p := range tree {
			if processAlive(p) {
				alive = append(alive, p)
			}
		}
		if len(alive) == 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	for _, p := range alive {
		err := syscall.Kill(p, syscall.SIGKILL)
		if err == nil {
			res.Killed = append(res.Killed, p)
			continue
		}
		if errors.Is(err, syscall.EPERM) && !denied[p] {
			denied[p] = true
			res.Denied = append(res.Denied, p)
		}
	}

	time.Sleep(200 * time.Millisecond)
	for _, p := range alive {
		if processAlive(p) {
			res.Survived = append(res.Survived, p)
		}
	}

	// Xatolarni yutib yuborsak, foydalanuvchi "0 tasi yopildi" degan sababsiz
	// xabar oladi. Eng keng tarqalgan sabab — AppArmor signal mediatsiyasi.
	if len(res.Denied) > 0 {
		res.Reason = denyReason(res.Denied[0])
	}
	return res, nil
}

// procLabel — jarayonning AppArmor yorlig'i, masalan "snap.firefox.firefox (enforce)".
// Profil nomi va rejimi alohida qaytariladi.
func procLabel(pid int) (name, mode string) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/attr/current")
	if err != nil {
		return "", ""
	}
	return parseLabel(string(raw))
}

func parseLabel(s string) (name, mode string) {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, " ("); i > 0 && strings.HasSuffix(s, ")") {
		return s[:i], s[i+2 : len(s)-1]
	}
	return s, ""
}

// denyReason — EPERM ning sababini odam o'qiy oladigan qilib tushuntiradi.
func denyReason(pid int) string {
	selfName, _ := procLabel(os.Getpid())
	targetName, targetMode := procLabel(pid)

	// AppArmor signal mediatsiyasi: qabul qiluvchining profili faqat ruxsat
	// berilgan peer yorliqlaridan signal oladi. Bizning yorlig'imiz "unconfined"
	// bo'lmasa, snap ilovalari signalni rad etadi (rejim "unconfined" bo'lsa ham —
	// muhimi yorliq nomi).
	confined := selfName != "" && selfName != "unconfined"
	if strings.HasPrefix(targetName, "snap.") && confined {
		return fmt.Sprintf(
			"AppArmor bloklagan: %q profilidagi jarayon %q yorlig'idan signal qabul qilmaydi. "+
				"ServerGo'ni oddiy terminaldan (unconfined) ishga tushiring — "+
				"'cat /proc/self/attr/current' 'unconfined' ko'rsatishi kerak.",
			targetName, selfName)
	}
	if targetMode == "enforce" || confined {
		return fmt.Sprintf(
			"Ruxsat berilmadi (EPERM). Bizning AppArmor yorlig'imiz: %q, nishonniki: %q.",
			labelOr(selfName), labelOr(targetName))
	}
	return "Ruxsat berilmadi (EPERM) — jarayon boshqa foydalanuvchiga tegishli bo'lishi mumkin."
}

func labelOr(s string) string {
	if s == "" {
		return "unconfined"
	}
	return s
}

// processAlive — jarayon hali ham ishlayaptimi?
// Zombie (Z) holati o'lgan hisoblanadi: u faqat otasi reap qilishini kutyapti,
// xotirani egallamaydi va unga SIGKILL yuborishning ma'nosi yo'q.
func processAlive(pid int) bool {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	end := strings.LastIndexByte(string(raw), ')')
	if end < 0 {
		return false
	}
	fields := strings.Fields(string(raw)[end+1:])
	if len(fields) == 0 {
		return false
	}
	return fields[0] != "Z"
}
