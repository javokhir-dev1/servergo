// Package registry — hostname -> faol yamux sessiyalar pool'i.
// Control paketi (agent uladi) yozadi, proxy paketi (jamoatchilik HTTP so'rovi)
// va autocert HostPolicy o'qiydi.
//
// Bitta hostname uchun BIR NECHTA sessiya bo'ladi: agent har loyiha uchun
// bir nechta mustaqil ulanish ochadi (qarang: internal/vpstunnel/manager,
// connsPerProject). Shu sabab bittasi uzilganda yoki sekinlashganda proxy
// so'rovni qolganlariga yuboradi va tashrifchi uzilishni sezmaydi —
// cloudflared ham xuddi shu tamoyilda ishlaydi.
package registry

import (
	"sort"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

// maxPerHost — bitta hostname uchun saqlanadigan sessiyalarning eng ko'p soni.
// Agent odatda connsPerProject ta ulanish ochadi, lekin qayta ulanish paytida
// eski sessiya hali "o'lganini" bilmasdan ro'yxatda turishi mumkin (yamux
// uzilishni stallTolerance ichida aniqlaydi). Chegara shu vaqtinchalik
// to'planishni cheklaydi: undan oshsa eng eskisi yopiladi.
const maxPerHost = 8

// hostPool — bitta hostname'ning sessiyalari va round-robin hisoblagichi.
type hostPool struct {
	sessions []*yamux.Session
	next     uint64
}

// prune — yopilgan sessiyalarni ro'yxatdan chiqaradi. Registry.mu ushlab
// turilgan holda chaqiriladi.
func (p *hostPool) prune() {
	live := p.sessions[:0]
	for _, s := range p.sessions {
		if !s.IsClosed() {
			live = append(live, s)
		}
	}
	for i := len(live); i < len(p.sessions); i++ {
		p.sessions[i] = nil // GC yopilgan sessiyani yig'ib olsin
	}
	p.sessions = live
}

// knownTTL — hostname oxirgi marta ulangandan keyin shuncha vaqt "tanish"
// bo'lib qoladi, garchi hozir bitta ham sessiya bo'lmasa ham.
//
// Nima uchun kerak: autocert HostPolicy faqat tanish hostname'larga
// sertifikat beradi (aks holda begona SNI skanerlari sertifikat so'ratib
// yuborardi). Lekin faqat "hozir ulangan" shartiga tayansak, agent uzilib
// qayta ulanayotgan bir necha soniyada relay TLS handshake'ni butunlay rad
// etadi — tashrifchi 502 emas, brauzerdagi qo'rqinchli xavfsizlik xatosini
// ko'radi va proxy'dagi kutish oynasi (agentWait) umuman ishga tushmaydi.
// Tanishlik muddati shu teshikni yopadi: sertifikat baribir faqat haqiqatan
// agent ulangan hostname'larga beriladi.
const knownTTL = 10 * time.Minute

type Registry struct {
	// RWMutex emas, oddiy Mutex: o'qish yo'lida ham holat o'zgaradi
	// (round-robin hisoblagichi va yopilganlarni tozalash).
	mu    sync.Mutex
	hosts map[string]*hostPool
	seen  map[string]time.Time // hostname -> oxirgi ulanish vaqti
}

func New() *Registry {
	return &Registry{
		hosts: map[string]*hostPool{},
		seen:  map[string]time.Time{},
	}
}

// Known — hostname hozir faolmi yoki yaqinda (knownTTL ichida) faol bo'lganmi.
// autocert HostPolicy shu orqali qaror qiladi.
func (r *Registry) Known(hostname string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.hosts[hostname]; ok {
		p.prune()
		if len(p.sessions) > 0 {
			return true
		}
	}
	last, ok := r.seen[hostname]
	if !ok {
		return false
	}
	if time.Since(last) > knownTTL {
		delete(r.seen, hostname)
		return false
	}
	return true
}

// Register — sessiyani hostname pool'iga qo'shadi va pool'dagi jami sonni
// qaytaradi. Avvalgi sessiyalar YOPILMAYDI — aynan shu ko'p ulanishli
// ishlashning mohiyati.
func (r *Registry) Register(hostname string, sess *yamux.Session) int {
	r.mu.Lock()
	p, ok := r.hosts[hostname]
	if !ok {
		p = &hostPool{}
		r.hosts[hostname] = p
	}
	p.prune()
	p.sessions = append(p.sessions, sess)
	r.seen[hostname] = time.Now()
	var evicted *yamux.Session
	if len(p.sessions) > maxPerHost {
		evicted = p.sessions[0]
		p.sessions = append(p.sessions[:0], p.sessions[1:]...)
	}
	n := len(p.sessions)
	r.mu.Unlock()

	if evicted != nil {
		_ = evicted.Close()
	}
	return n
}

// Unregister — aynan shu sessiyani pool'dan olib tashlaydi va qolgan sonni
// qaytaradi.
func (r *Registry) Unregister(hostname string, sess *yamux.Session) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.hosts[hostname]
	if !ok {
		return 0
	}
	for i, s := range p.sessions {
		if s == sess {
			p.sessions = append(p.sessions[:i], p.sessions[i+1:]...)
			break
		}
	}
	p.prune()
	// Tanishlik muddati AYNAN shu lahzadan boshlanishi kerak: aks holda
	// soatlab tirik turgan ulanish uzilganda "oxirgi ko'rilgan" vaqti allaqachon
	// eskirgan bo'lib, hostname darhol notanishga aylanardi.
	r.seen[hostname] = time.Now()
	if len(p.sessions) == 0 {
		delete(r.hosts, hostname)
		return 0
	}
	return len(p.sessions)
}

// Sessions — hostname uchun barcha ochiq sessiyalar, round-robin tartibida
// (har chaqiruvda boshlanish nuqtasi siljiydi). Proxy shu ro'yxat bo'yicha
// birinchisidan boshlab urinadi, xato bo'lsa keyingisiga o'tadi.
func (r *Registry) Sessions(hostname string) []*yamux.Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.hosts[hostname]
	if !ok {
		return nil
	}
	p.prune()
	n := uint64(len(p.sessions))
	if n == 0 {
		delete(r.hosts, hostname)
		return nil
	}
	start := p.next % n
	p.next++

	out := make([]*yamux.Session, 0, n)
	for i := uint64(0); i < n; i++ {
		out = append(out, p.sessions[(start+i)%n])
	}
	return out
}

// Lookup — bitta ochiq sessiya (round-robin). autocert HostPolicy va
// "hostname umuman faolmi" tekshiruvlari uchun.
func (r *Registry) Lookup(hostname string) (*yamux.Session, bool) {
	ss := r.Sessions(hostname)
	if len(ss) == 0 {
		return nil, false
	}
	return ss[0], true
}

// lookupPoll — SessionsWait tekshiruvlari orasidagi kutish.
const lookupPoll = 100 * time.Millisecond

// SessionsWait — Sessions kabi, lekin pool bo'sh bo'lsa, timeout ichida
// agent(lar) ulanishini kutadi. Pool joriy qilingandan keyin bu holat
// kamdan-kam uchraydi (barcha ulanishlar bir vaqtda uzilishi kerak), lekin
// relay qayta ishga tushgan paytda hali ham foydali: tashrifchi 502 o'rniga
// biroz kechikish bilan to'g'ri javob oladi. Hech qachon ulanmagan hostname
// uchun timeout to'liq kutiladi, shuning uchun qiymat qisqa bo'lishi kerak.
func (r *Registry) SessionsWait(hostname string, timeout time.Duration) []*yamux.Session {
	if ss := r.Sessions(hostname); len(ss) > 0 {
		return ss
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(lookupPoll)
		if ss := r.Sessions(hostname); len(ss) > 0 {
			return ss
		}
	}
	return nil
}

// PoolHolat — bitta hostname'ning holati (diagnostika uchun).
type PoolHolat struct {
	Hostname  string `json:"hostname"`
	Ulanishlar int   `json:"ulanishlar"`  // pool'dagi ochiq sessiyalar
	Oqimlar    int   `json:"oqimlar"`     // shu sessiyalardagi jami faol oqim
}

// Holat — barcha hostname'lar bo'yicha pool tasviri. Nosozlikni tekshirishda
// eng muhim savolga javob beradi: qaysi hostname'da nechta ulanish qolgan va
// ular ustida nechta so'rov osilib turibdi.
func (r *Registry) Holat() []PoolHolat {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]PoolHolat, 0, len(r.hosts))
	for h, p := range r.hosts {
		p.prune()
		if len(p.sessions) == 0 {
			continue
		}
		oqim := 0
		for _, s := range p.sessions {
			oqim += s.NumStreams()
		}
		out = append(out, PoolHolat{Hostname: h, Ulanishlar: len(p.sessions), Oqimlar: oqim})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Hostname < out[j].Hostname })
	return out
}

// Hostnames — hozir faol barcha hostname'lar (autocert HostPolicy uchun).
func (r *Registry) Hostnames() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.hosts))
	for h, p := range r.hosts {
		p.prune()
		if len(p.sessions) > 0 {
			out = append(out, h)
		}
	}
	return out
}
