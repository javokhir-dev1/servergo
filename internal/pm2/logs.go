package pm2

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// maxTail — log faylining oxiridan o'qiladigan maksimal hajm. Bir necha
// gigabaytlik loglar ham shu tufayli bir zumda ochiladi.
const maxTail = 128 * 1024

// LogFile — bitta log faylining oxirgi qismi.
type LogFile struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Text   string `json:"text"`
	Size   int64  `json:"size"`
}

// Logs — jarayonning stdout va stderr loglari.
type Logs struct {
	Name string  `json:"name"`
	Out  LogFile `json:"out"`
	Err  LogFile `json:"err"`
}

// GetLogs — id bo'yicha jarayonni topib, ikkala log faylining oxirgi
// `lines` satrini qaytaradi.
func GetLogs(id, lines int) (*Logs, error) {
	procs, err := List()
	if err != nil {
		return nil, err
	}
	var proc *Proc
	for i := range procs {
		if procs[i].ID == id {
			proc = &procs[i]
			break
		}
	}
	if proc == nil {
		return nil, fmt.Errorf("jarayon topilmadi: %d", id)
	}

	out := tailFile(proc.OutLog)
	errf := tailFile(proc.ErrLog)
	out.Text = lastLines(out.Text, lines)
	errf.Text = lastLines(errf.Text, lines)

	return &Logs{Name: proc.Name, Out: out, Err: errf}, nil
}

func tailFile(path string) LogFile {
	lf := LogFile{Path: path}
	if path == "" {
		return lf
	}

	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return lf
		}
		lf.Text = "Logni o'qib bo'lmadi: " + err.Error()
		return lf
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		lf.Text = "Logni o'qib bo'lmadi: " + err.Error()
		return lf
	}
	lf.Exists = true
	lf.Size = st.Size()
	if st.Size() == 0 {
		return lf
	}

	start := int64(0)
	if st.Size() > maxTail {
		start = st.Size() - maxTail
	}
	buf := make([]byte, st.Size()-start)
	n, err := f.ReadAt(buf, start)
	if err != nil && n == 0 {
		lf.Text = "Logni o'qib bo'lmadi: " + err.Error()
		return lf
	}

	text := string(buf[:n])
	if start > 0 {
		// Kesilgan joyda yarim satr qolib ketmasin.
		if nl := strings.IndexByte(text, '\n'); nl >= 0 {
			text = text[nl+1:]
		}
		text = fmt.Sprintf("… (log %s, faqat oxirgi qismi ko'rsatilyapti)\n%s",
			FormatBytes(st.Size()), text)
	}
	lf.Text = text
	return lf
}

func lastLines(text string, n int) string {
	if text == "" || n <= 0 {
		return text
	}
	lines := strings.Split(text, "\n")
	if len(lines) <= n {
		return text
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// FormatBytes — inson o'qiy oladigan hajm.
func FormatBytes(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	v := float64(n)
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f %s", v, units[i])
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
