package sysmon

import (
	"bufio"
	"fmt"
	"os"
	"os/user"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

var pageSize = uint64(os.Getpagesize())

// Proc — /proc dan o'qilgan bitta jarayon.
type Proc struct {
	PID  int
	PPID int
	Name string // comm
	Key  string // guruhlash kaliti: exe yo'li (bo'lmasa comm)
	Cmd  string // to'liq cmdline
	User string
	RSS  uint64 // bayt
	PSS  uint64 // bayt, faqat aniq rejimda to'ldiriladi
}

// MemInfo — /proc/meminfo dan umumiy xotira holati (bayt).
type MemInfo struct {
	Total     uint64 `json:"total"`
	Free      uint64 `json:"free"`
	Available uint64 `json:"available"`
	Buffers   uint64 `json:"buffers"`
	Cached    uint64 `json:"cached"`
	Used      uint64 `json:"used"`
	Shared    uint64 `json:"shared"`
	SwapTotal uint64 `json:"swapTotal"`
	SwapUsed  uint64 `json:"swapUsed"`
}

func ReadMemInfo() (MemInfo, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return MemInfo{}, err
	}
	defer f.Close()

	vals := map[string]uint64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Fields(sc.Text())
		if len(parts) < 2 {
			continue
		}
		v, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil {
			continue
		}
		vals[strings.TrimSuffix(parts[0], ":")] = v * 1024 // kB -> bayt
	}
	if err := sc.Err(); err != nil {
		return MemInfo{}, err
	}

	cached := vals["Cached"] + vals["SReclaimable"]
	m := MemInfo{
		Total:     vals["MemTotal"],
		Free:      vals["MemFree"],
		Available: vals["MemAvailable"],
		Buffers:   vals["Buffers"],
		Cached:    cached,
		Shared:    vals["Shmem"],
		SwapTotal: vals["SwapTotal"],
		SwapUsed:  vals["SwapTotal"] - vals["SwapFree"],
	}
	// `free` bilan bir xil hisob: used = total - free - buffers - cache
	if m.Total > m.Free+m.Buffers+cached {
		m.Used = m.Total - m.Free - m.Buffers - cached
	}
	return m, nil
}

var uidNames = map[uint32]string{}

func userName(uid uint32) string {
	if n, ok := uidNames[uid]; ok {
		return n
	}
	n := strconv.FormatUint(uint64(uid), 10)
	if u, err := user.LookupId(n); err == nil {
		n = u.Username
	}
	uidNames[uid] = n
	return n
}

// ReadProcs — barcha foydalanuvchi jarayonlarini o'qiydi (kernel thread'lar tashlab ketiladi).
func ReadProcs() (map[int]*Proc, error) {
	dir, err := os.Open("/proc")
	if err != nil {
		return nil, err
	}
	defer dir.Close()

	names, err := dir.Readdirnames(-1)
	if err != nil {
		return nil, err
	}

	procs := make(map[int]*Proc, len(names))
	for _, name := range names {
		pid, err := strconv.Atoi(name)
		if err != nil {
			continue
		}
		p := readProc(pid)
		if p != nil {
			procs[pid] = p
		}
	}
	return procs, nil
}

func readProc(pid int) *Proc {
	base := "/proc/" + strconv.Itoa(pid)

	// cmdline bo'sh bo'lsa — bu kernel thread, bizga kerak emas.
	raw, err := os.ReadFile(base + "/cmdline")
	if err != nil || len(raw) == 0 {
		return nil
	}
	args := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
	cmd := strings.Join(args, " ")

	statRaw, err := os.ReadFile(base + "/stat")
	if err != nil {
		return nil
	}
	comm, ppid, ok := parseStat(string(statRaw))
	if !ok {
		return nil
	}

	p := &Proc{PID: pid, PPID: ppid, Name: comm, Cmd: cmd}

	// RSS: /proc/<pid>/statm ning 2-maydoni — resident sahifalar soni.
	if statmRaw, err := os.ReadFile(base + "/statm"); err == nil {
		fields := strings.Fields(string(statmRaw))
		if len(fields) >= 2 {
			if pages, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
				p.RSS = pages * pageSize
			}
		}
	}

	// Guruhlash kaliti — bajariluvchi fayl yo'li. Electron/Firefox kabi ko'p
	// jarayonli ilovalarda barcha bola jarayonlar bitta exe'ga ishora qiladi.
	if exe, err := os.Readlink(base + "/exe"); err == nil && exe != "" {
		p.Key = strings.TrimSuffix(exe, " (deleted)")
	} else if len(args) > 0 && args[0] != "" {
		p.Key = args[0]
	} else {
		p.Key = comm
	}

	if fi, err := os.Stat(base); err == nil {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			p.User = userName(st.Uid)
		}
	}
	return p
}

// parseStat — comm qavs ichida bo'lib, o'zida bo'shliq va qavs saqlashi mumkin,
// shuning uchun oxirgi ')' bo'yicha ajratamiz.
func parseStat(s string) (comm string, ppid int, ok bool) {
	start := strings.IndexByte(s, '(')
	end := strings.LastIndexByte(s, ')')
	if start < 0 || end < 0 || end < start {
		return "", 0, false
	}
	comm = s[start+1 : end]
	rest := strings.Fields(s[end+1:])
	// rest[0] = state, rest[1] = ppid
	if len(rest) < 2 {
		return "", 0, false
	}
	ppid, err := strconv.Atoi(rest[1])
	if err != nil {
		return "", 0, false
	}
	return comm, ppid, true
}

// readPSS — smaps_rollup orqali aniq (bo'lishilgan xotira ikki marta
// sanalmaydigan) qiymatni o'qiydi. Ruxsat bo'lmasa 0 qaytaradi.
func readPSS(pid int) uint64 {
	f, err := os.Open("/proc/" + strconv.Itoa(pid) + "/smaps_rollup")
	if err != nil {
		return 0
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "Pss:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		v, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return v * 1024
	}
	return 0
}

// Group — bitta ilova: bir xil exe'ga ega, ota-bola bog'langan jarayonlar to'plami.
type Group struct {
	RootPID int     `json:"rootPid"`
	Name    string  `json:"name"`
	Exe     string  `json:"exe"`
	Cmd     string  `json:"cmd"`
	User    string  `json:"user"`
	Count   int     `json:"count"`
	RSS     uint64  `json:"rss"`
	PSS     uint64  `json:"pss"`
	Percent float64 `json:"percent"`
	PIDs    []int   `json:"pids"`
	Protect bool    `json:"protected"`
	Warn    string  `json:"warn,omitempty"`
}

// BuildGroups — jarayonlarni ilovalar bo'yicha guruhlaydi.
//
// Guruh ildizi: otasi boshqa exe'ga ega (yoki otasi umuman yo'q) bo'lgan jarayon.
// Guruh a'zolari: shu ildizdan boshlab, bir xil exe'ni saqlab turgan avlodlar.
// Boshqa exe'li avlod o'z guruhini boshlaydi, ya'ni RAM ikki marta sanalmaydi.
func BuildGroups(procs map[int]*Proc, total uint64, accurate bool) []Group {
	children := make(map[int][]int, len(procs))
	for pid, p := range procs {
		children[p.PPID] = append(children[p.PPID], pid)
	}

	if accurate {
		for _, p := range procs {
			p.PSS = readPSS(p.PID)
		}
	}

	var groups []Group
	for pid, p := range procs {
		parent, hasParent := procs[p.PPID]
		if hasParent && parent.Key == p.Key {
			continue // bu ildiz emas, guruh ichidagi bola
		}

		g := Group{RootPID: pid, Name: p.Name, Exe: p.Key, Cmd: p.Cmd, User: p.User}

		// Bir xil exe'ni saqlab turgan avlodlarni yig'amiz.
		stack := []int{pid}
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]

			cp := procs[cur]
			g.PIDs = append(g.PIDs, cur)
			g.Count++
			g.RSS += cp.RSS
			g.PSS += cp.PSS

			for _, ch := range children[cur] {
				if procs[ch].Key == cp.Key {
					stack = append(stack, ch)
				}
			}
		}

		g.Protect, g.Warn = classify(pid, p)
		sort.Ints(g.PIDs)
		groups = append(groups, g)
	}

	for i := range groups {
		mem := groups[i].RSS
		if accurate && groups[i].PSS > 0 {
			mem = groups[i].PSS
		}
		if total > 0 {
			groups[i].Percent = float64(mem) / float64(total) * 100
		}
	}

	sort.Slice(groups, func(i, j int) bool {
		a, b := groups[i].RSS, groups[j].RSS
		if accurate && groups[i].PSS > 0 && groups[j].PSS > 0 {
			a, b = groups[i].PSS, groups[j].PSS
		}
		if a != b {
			return a > b
		}
		return groups[i].RootPID < groups[j].RootPID
	})
	return groups
}

// Sessiyani yiqitib yuboradigan jarayonlar — ularni belgilab qo'yamiz.
var riskyNames = map[string]string{
	"systemd":     "tizim jarayoni",
	"gnome-shell": "ish stolini yopadi",
	"gdm":         "sessiyadan chiqarib yuboradi",
	"gnome-sessi": "sessiyani tugatadi",
	"Xwayland":    "grafik server",
	"mutter":      "oyna menejeri",
	"sshd":        "SSH ulanishini uzadi",
	"dbus-daemon": "tizim shinasi",
}

// pm2 demoni o'z jarayon nomiga "God Daemon" ni yozadi, masalan:
//
//	PM2 v7.0.3: God Daemon (/home/server/.pm2)
//
// comm 15 belgida kesiladi, shuning uchun to'liq cmdline bo'yicha qidiramiz.
const pm2DaemonMarker = "God Daemon"

func classify(pid int, p *Proc) (protected bool, warn string) {
	// O'zimizni yoki ajdodimizni o'ldirsak dastur ham birga yiqiladi.
	// Desktop rejimida bu zanjirga sessiya jarayonlari (gnome-shell,
	// systemd --user) ham kiradi, ya'ni ular avtomatik himoyalanadi.
	if pid == 1 || pid == os.Getpid() || isAncestorOfSelf(pid) {
		return true, "dastur o'zi shu daraxtda"
	}
	// pm2 demonini o'ldirish barcha pm2 ilovalarini birdaniga yiqitadi va
	// aynan shu dastur boshqaradigan ro'yxatni yo'q qiladi. Jarayonlarni
	// "Jarayonlar" bo'limidan to'xtatish kerak, bu yerdan emas.
	if strings.Contains(p.Cmd, pm2DaemonMarker) {
		return true, "pm2 demoni — barcha pm2 ilovalari yiqiladi"
	}
	if w, ok := riskyNames[p.Name]; ok {
		return false, w
	}
	return false, ""
}

// isAncestorOfSelf — berilgan pid bizning ajdodimizmi? (o'zimizni o'ldirmaslik uchun)
func isAncestorOfSelf(pid int) bool {
	cur := os.Getpid()
	for i := 0; i < 64 && cur > 1; i++ {
		raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", cur))
		if err != nil {
			return false
		}
		_, ppid, ok := parseStat(string(raw))
		if !ok {
			return false
		}
		if ppid == pid {
			return true
		}
		cur = ppid
	}
	return false
}
