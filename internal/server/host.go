package server

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"servergo/internal/sysmon"
)

// Host — yuqori paneldagi umumiy tizim metrikalari.
type Host struct {
	Hostname string     `json:"hostname"`
	Platform string     `json:"platform"`
	CPUs     int        `json:"cpus"`
	LoadAvg  [3]float64 `json:"loadavg"`
	MemTotal uint64     `json:"memTotal"`
	MemUsed  uint64     `json:"memUsed"`
	Uptime   float64    `json:"uptime"`
	// CPUTempC — protsessor harorati (°C). Sensor topilmasa nil — ba'zi
	// muhitlarda (VM, konteyner) mavjud emas.
	CPUTempC  *float64 `json:"cpuTempC,omitempty"`
	DiskTotal uint64   `json:"diskTotal"`
	DiskUsed  uint64   `json:"diskUsed"`
}

func hostMetrics() (Host, error) {
	mem, err := sysmon.ReadMemInfo()
	if err != nil {
		return Host{}, err
	}

	h := Host{
		CPUs:     runtime.NumCPU(),
		LoadAvg:  loadAvg(),
		MemTotal: mem.Total,
		MemUsed:  mem.Used,
		Uptime:   uptimeSeconds(),
		Platform: platform(),
		CPUTempC: cpuTempCelsius(),
	}
	h.Hostname, _ = os.Hostname()
	h.DiskTotal, h.DiskUsed = diskUsage("/")
	return h, nil
}

// diskUsage — `/` bo'limidagi umumiy va band joy (baytlarda).
// Root uchun zaxiralangan joy hisobga olinmaydi (Bavail, Bfree emas) —
// "band" ko'rsatkichi oddiy foydalanuvchi ko'radigan joy bilan mos kelsin.
func diskUsage(path string) (total, used uint64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0
	}
	bsize := uint64(st.Bsize)
	total = st.Blocks * bsize
	free := st.Bavail * bsize
	if free > total {
		return total, 0
	}
	return total, total - free
}

// cpuTempCelsius — protsessor harorati. Avval /sys/class/thermal dagi
// protsessorga tegishli zonani, topilmasa /sys/class/hwmon dagi tanilgan
// CPU sensor drayverini (coretemp — Intel, k10temp/zenpower — AMD) qidiradi.
// Ikkalasi ham /proc kabi hech qanday tashqi buyruq (masalan `sensors`)
// talab qilmaydi.
func cpuTempCelsius() *float64 {
	cpuZoneTypes := []string{"x86_pkg_temp", "cpu-thermal", "cpu_thermal", "soc-thermal", "soc_thermal"}
	zones, _ := filepath.Glob("/sys/class/thermal/thermal_zone*")
	for _, want := range cpuZoneTypes {
		for _, zone := range zones {
			typ := strings.TrimSpace(readFile(filepath.Join(zone, "type")))
			if typ == want {
				if v, ok := readMilliDegrees(filepath.Join(zone, "temp")); ok {
					return &v
				}
			}
		}
	}

	cpuHwmonNames := []string{"coretemp", "k10temp", "zenpower"}
	hwmons, _ := filepath.Glob("/sys/class/hwmon/hwmon*")
	for _, want := range cpuHwmonNames {
		for _, hw := range hwmons {
			name := strings.TrimSpace(readFile(filepath.Join(hw, "name")))
			if name != want {
				continue
			}
			inputs, _ := filepath.Glob(filepath.Join(hw, "temp*_input"))
			for _, in := range inputs {
				if v, ok := readMilliDegrees(in); ok {
					return &v
				}
			}
		}
	}
	return nil
}

func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// readMilliDegrees — kernel bu fayllarga harorat*1000 (millidarajada) yozadi.
func readMilliDegrees(path string) (float64, bool) {
	raw := strings.TrimSpace(readFile(path))
	if raw == "" {
		return 0, false
	}
	milli, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}
	return milli / 1000, true
}

func loadAvg() [3]float64 {
	var out [3]float64
	raw, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return out
	}
	fields := strings.Fields(string(raw))
	for i := 0; i < 3 && i < len(fields); i++ {
		out[i], _ = strconv.ParseFloat(fields[i], 64)
	}
	return out
}

func uptimeSeconds() float64 {
	raw, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(fields[0], 64)
	return v
}

func platform() string {
	var u syscall.Utsname
	if err := syscall.Uname(&u); err != nil {
		return runtime.GOOS
	}
	return charsToString(u.Sysname[:]) + " " + charsToString(u.Release[:])
}

// Utsname maydonlari — nol bilan tugaydigan C massivlari.
func charsToString(ca []int8) string {
	b := make([]byte, 0, len(ca))
	for _, c := range ca {
		if c == 0 {
			break
		}
		b = append(b, byte(c))
	}
	return string(b)
}
