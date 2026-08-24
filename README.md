# ServerGo

PM2 jarayonlari, tizim xotirasi va Cloudflare tunnellarini bitta oynadan
boshqarish uchun Ubuntu desktop dasturi. Go bilan yozilgan, UI webkit2gtk
nativ oynasida ochiladi.

## Bo'limlar

### Jarayonlar

- **Ro'yxat** — barcha PM2 jarayonlari: ID, nom, holat, CPU, RAM, uptime, restartlar soni, PID, rejim (fork/cluster)
- **Avto-yangilanish** — 1/2/5/10 soniya oralig'ida, o'chirib qo'yish mumkin
- **Boshqaruv** — har bir jarayonni `Start` / `Restart` / `Stop` / `O'chirish`
- **Ommaviy amallar** — filtrga tushgan hamma jarayonni birdan restart yoki stop qilish
- **Tasdiqlash** — `Stop` va `O'chirish` uchun tasdiqlash oynasi
- **Loglar** — stdout va stderr ni dastur ichida ko'rish, pastga avto-ergashish, `pm2 flush`
- **Batafsil panel** — skript yo'li, ishchi papka, interpretator, Node versiyasi, avto-restart/watch, log fayl yo'llari
- **Qidiruv, filtr, saralash** — nom/ID/namespace bo'yicha, holat bo'yicha, istalgan ustun bo'yicha

### Ilovalar

pm2'ga bog'liq bo'lmagan, ServerGo'ning **o'z** jarayon boshqaruvchisi —
istalgan buyruq (`node server.js`, `python3 bot.py`...). Tunnellar bo'limidagi
cloudflared boshqaruvi bilan bir xil naqsh:

- Har bir ilova ServerGo'ning o'z bazasida (`~/.config/servergo/apps/apps.db`)
  saqlanadi — nomi, buyrug'i, ishchi papkasi, **Avtostart** belgisi
- **Avtostart** yoqilgan ilovalar ServerGo ishga tushganda (masalan
  `systemctl --user start servergo`, kompyuter yoqilganda) o'zi ishga tushadi —
  pm2'dagi kabi alohida `pm2 save` qilish shart emas
- **Qulab tushsa avto-restart**: kutilmaganda to'xtasa, 1s/2s/4s oralig'ida
  3 martagacha qayta urinadi, keyin "xato" holatida to'xtaydi
- Run / Stop / Restart / Tahrir / O'chirish, loglar (stdout+stderr, dastur
  ichida ko'rish, fayl sifatida ham saqlanadi)

### RAM

- `/proc/meminfo` dan jonli xotira holati: band, kesh/bufer, bo'sh, mavjud, swap
- Jarayonlar **ilova bo'yicha guruhlanadi** — Firefox'ning o'nlab tab jarayoni yoki
  Electron ilovaning bola jarayonlari bitta qator bo'lib ko'rinadi
- Har bir guruhni **butun daraxti bilan to'xtatish**: avval SIGTERM, 5 soniya
  kutadi, javob bermaganlarga SIGKILL
- Qidiruv va aniq (PSS) rejimi

### Tunnellar

Lokal `localhost:PORT` serverlarni Cloudflare Tunnel orqali o'z domeningizning
subdomenlari ostida internetga chiqaradi. Port yozasiz → subdomen yozasiz →
**Run** bosasiz → `https://subdomen.domeningiz.uz` ochiladi.

- **Domenning o'zi uchun ham** — Subdomen maydonini bo'sh qoldirsangiz (yoki
  `@` yozsangiz), tunnel bevosita `https://domeningiz.uz`ga (subdomensiz)
  ochiladi. Har bir domenning o'zi uchun faqat bitta loyiha bo'lishi mumkin.
- **Sozlash sehrgari** — cloudflared'ni topish, Cloudflare bilan bog'lanish,
  bazaviy domen qo'shish (uch qadam, holati ko'rinib turadi)
- **Bazaviy domenlar** — bir nechtasini qo'shish, orasida almashish va
  ro'yxatdan o'chirish (`− Domen`); domen biror loyihada ishlatilayotgan
  bo'lsa o'chirilmaydi. Bu faqat ServerGo'ning lokal ro'yxati — Cloudflare
  akkauntingizdagi domenga tegmaydi
- **Ko'p domen, alohida avtorizatsiya** — cloudflared'ning sertifikati bir
  vaqtda faqat bitta zonaga (domenga) tegishli bo'ladi. Shu sabab har bir
  YANGI bazaviy domen qo'shilganda (birinchisidan boshqa har biri) brauzer
  qayta ochiladi — o'sha oynada aynan shu domenni tanlang. Bu qadam
  o'tkazib yuborilsa, o'sha domendagi loyihalarning DNS yozuvi (ko'rinishda
  xato bermay) boshqa domenga yozilib qoladi — "Diagnostika" buni topadi.
- **Loyihalar** — har biri o'z named tunneli va o'z `cloudflared` jarayoniga ega,
  shuning uchun bittasini to'xtatish boshqalariga ta'sir qilmaydi
- **Run / Stop / Restart / Tahrir / O'chirish**; o'chirishda Cloudflare tomonidagi
  tunnel ham tozalanadi
- **Avtostart** — dastur ochilganda belgilangan loyihalar o'zi ishga tushadi
- **Loglar** — `cloudflared` chiqishi dastur ichida, pastga avto-ergashish bilan
- **Diagnostika** — "Error 1033" sababini topadi (jarayon, lokal servis, tunnel
  UUID, DNS CNAME tekshiriladi) va **DNS'ni tuzatish** tugmasini taklif qiladi

Talab: [cloudflared](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/)
va nameserver'lari Cloudflare'ga yo'naltirilgan domen. Dastur cloudflared'ni
o'zi qidiradi; topilmasa sozlash sehrgari yo'l ko'rsatadi.

Bu bo'lim [LocalGo](../../localgo) loyihasidan ko'chirilgan: `internal/tunnel/`
paketlari deyarli o'zgarishsiz, Wails bindinglari o'rniga ServerGo ning
token bilan himoyalangan HTTP qatlami ishlatiladi.

#### Dev-serverlarda host cheklovi

Ko'p freymvorklar `localhost` dan boshqa hostdan kelgan so'rovni bloklaydi.
Tunnel ishlab tursa ham brauzerda "Blocked request" / "Invalid Host header"
chiqsa, lokal loyihangizga subdomeningizni qo'shing:

| Freymvork | Sozlama |
|---|---|
| Vite | `server: { allowedHosts: ['.domeningiz.uz'] }` |
| Next.js | `allowedDevOrigins: ['todo.domeningiz.uz']` |
| Django | `ALLOWED_HOSTS = ['.domeningiz.uz', 'localhost']` |
| Laravel | `APP_URL=https://todo.domeningiz.uz` (`.env`) |
| Rails | `config.hosts << '.domeningiz.uz'` |
| webpack-dev-server | `devServer: { allowedHosts: 'all' }` |

#### Foydalanuvchi fayllari

```
~/.config/servergo/
├── servergo.db                 # SQLite: loyihalar va sozlamalar
├── neutral.yml                 # global cloudflared configni neytrallashtirish
├── logs/app.log                # bo'lim loglari
├── logs/<loyiha-id>.log        # cloudflared chiqishi (10 MB da rotatsiya)
└── tunnels/<loyiha-id>/        # config.yml + credentials (0600)

~/.cloudflared/cert.pem         # login natijasi
```

`neutral.yml` nima uchun kerak: `--config` cloudflared'da global bayroq va u
buyruq argumentidan **ustun** turadi. Foydalanuvchida `~/.cloudflared/config.yml`
bo'lsa va unda `tunnel:` yozilgan bo'lsa, `tunnel route dns` buyrug'i DNS
yozuvini o'sha tunnelga yo'naltiradi — argumentdagiga emas. Natijada Error 1033.
Shuning uchun barcha boshqaruv buyruqlariga bo'sh config beriladi.

### VPS Tunnel

Cloudflare Tunnel'ga mustaqil, alohida bo'lim — foydalanuvchining **o'z
VPS'idagi** `servergo-relay` orqali reverse-tunnel. Sabab: Cloudflare Tunnel
so'rovni Cloudflare'ning global edge serveriga olib boradi — O'zbekistondagi
foydalanuvchilar uchun bu qo'shimcha kechikish beradi. O'zbekiston ichidagi
(TAS-IX/UZ-IX) VPS orqali trafik mahalliy internet almashinuv nuqtasidan
o'tadi, xalqaro yo'lga chiqmaydi.

**Qanday ishlaydi**: ServerGo (lokal) VPS'dagi relay'ga chiquvchi TLS ulanish
ochadi (bitta loyiha — bitta ulanish, portlarni ochish shart emas). Relay
`:443`'da jamoatchilik so'rovlarini qabul qiladi, Host header bo'yicha mos
ulanishga (yamux oqimi) yo'naltiradi; lokal tomon esa faqat xom baytlarni
`localhost:PORT`ga ko'chiradi — HTTP semantikasi relay tomonida to'g'ri
bajariladi. Sertifikatlar (jamoatchilik uchun) Let's Encrypt orqali avtomatik
olinadi; control ulanish esa relay ishga tushganda generatsiya qilingan
sertifikatning SHA256 barmoq izi (fingerprint) bilan tasdiqlanadi.

**VPS'da o'rnatish** (bir marta):

```
git clone <shu repo> && cd servergo
sudo RELAY_TOKEN=$(openssl rand -hex 32) ./scripts/install-relay.sh
```

Skript `servergo-relay`ni quradi, systemd service qilib o'rnatadi va
fingerprint'ni ko'rsatadi (`journalctl -u servergo-relay | grep fingerprint`
bilan ham qarash mumkin). So'ng:

1. Firewall'da `80`, `443`, `9443` portlarini oching.
2. DNS panelingizda **wildcard yozuv** qo'shing: `*.sizning-domeningiz.uz →
   VPS_IP` (bir nechta domen/wildcard qo'shish mumkin — Cloudflare bo'limidagi
   kabi, ro'yxatda saqlanadi va ular orasida almashish mumkin).
3. Desktop ilovada **"VPS Tunnel"** bo'limi → **"Relay sozlamalari"**:
   VPS manzili (`IP:9443`), token, fingerprint. So'ng **"+ Domen"** bilan
   yuqoridagi wildcard domeningizni qo'shing.
4. **"+ Yangi loyiha"** — port va subdomen kiritib **Run** bosing.

Subdomen bo'sh qoldirilsa (yoki `@`) — tunnel domenning o'ziga (subdomensiz)
ochiladi, xuddi Cloudflare bo'limidagi kabi. **Diqqat**: wildcard yozuv
(`*.domen`) domenning o'zini QAMRAMAYDI — shu turdagi loyiha uchun DNS
panelida domenning o'ziga ALOHIDA A yozuv ham qo'shishingiz kerak (`domen.uz
→ VPS_IP`, wildcard'dan tashqari).

Ikkala bo'lim (Cloudflare va VPS Tunnel) bir xil bazaviy domenni bo'lisha
oladi — subdomen bandligi ikkalasida ham o'zaro tekshiriladi, shuning uchun
bitta subdomenni ikkalasida ham yaratib qo'yish (va biri boshqasini
sababsiz "yutib yuborishi") mumkin emas.

Buyruq qatoridan:

```
servergo vpstunnel relay <manzil:port> <token> <fingerprint>
servergo vpstunnel add-domain <domen>
servergo vpstunnel create <port> [subdomen] [-n nom] [-d domen] [-s] [-a]
servergo vpstunnel list
```

Fayllar: `~/.config/servergo/vpstunnel/` (Cloudflare bo'limining
`~/.config/servergo/servergo.db`sidan mustaqil).

## Talablar

- Go 1.21+
- `pm2` global o'rnatilgan (`npm i -g pm2`)
- webkit2gtk dev fayllari (cgo uchun):

```bash
make deps
```

Bu `libwebkit2gtk-4.1-dev` va `libgtk-3-dev` ni o'rnatadi (sudo so'raydi).

### webkit2gtk-4.0 shim haqida

`webview_go` cgo direktivasida `webkit2gtk-4.0` ni qattiq yozib qo'ygan
(v0.0.0-20240831120633 — bu uning eng oxirgi holati), Ubuntu 25.10+ da esa
`libwebkit2gtk-4.0-dev` paketi umuman yo'q, faqat 4.1 bor.

Shu sababli `third_party/pkgconfig/webkit2gtk-4.0.pc` shim fayli 4.1 ga
yo'naltiradi va Makefile uni `PKG_CONFIG_PATH` ga qo'shadi. Ikkalasining C API si
bir xil; farqi — 4.1 libsoup3 ga, 4.0 libsoup2 ga bog'langan, biz esa libsoup ni
to'g'ridan-to'g'ri ishlatmaymiz.

**Muhim:** shuning uchun `go build` ni to'g'ridan-to'g'ri emas, `make build`
orqali chaqiring — aks holda `Package 'webkit2gtk-4.0' not found` xatosi chiqadi.
Qo'lda:

```bash
PKG_CONFIG_PATH=./third_party/pkgconfig go build .
```

## Ishga tushirish

```bash
make run
```

Ubuntu ilovalar menyusiga qo'shish:

```bash
make install-desktop
```

Olib tashlash: `make uninstall-desktop`

## Kompyuter yoqilganda avtomatik ishga tushish

Ikki qism mustaqil sozlanadi.

### Tunnellar — systemd user service

```bash
make install-service
```

Bu `~/.config/systemd/user/servergo.service` ni yaratadi, yoqadi va
`loginctl enable-linger` bilan **login'siz, boot'da** ishga tushishini
ta'minlaydi. Tunnel faqat loyihaning **Avtostart** belgisi yoqilgan bo'lsa
ko'tariladi.

```bash
systemctl --user status servergo      # holat
journalctl --user -u servergo -f      # loglar
make uninstall-service                # olib tashlash
```

**Tarmoqni kutish.** systemd *user* manager'ida `network-online.target` mavjud
emas, shuning uchun unga `After=`/`Wants=` yozishning foydasi yo'q. Boot'da demon
tarmoqdan oldin ko'tarilsa, cloudflared ulana olmaydi va manager 3 marta urinib
(1s/2s/4s) taslim bo'ladi — tunnellar o'lik qoladi. Shuning uchun unit'da
`ExecStartPre=scripts/wait-network.sh` bor: u DNS hal bo'lishini 120 soniyagacha
kutadi, kutib bo'lmasa ham 0 bilan chiqadi (demon ishga tushib, sababni
ko'rsatsin).

**Bitta egasi qoidasi.** Demon ham, oyna ham bitta binardan ishlaydi, shuning
uchun ikkalasi bir loyihaga ikkita `cloudflared` ko'tarib yubormasligi kerak.
Buni `~/.config/servergo/servergo.lock` fayli hal qiladi: kim `flock` ni olsa —
o'sha tunnellarning egasi, o'z HTTP manzili va tokenini `daemon.json` ga yozadi.
Qulfni ololmagan nusxa esa o'z serverini umuman ko'tarmaydi va oynani egasining
manziliga yo'naltiradi. Ya'ni demon ishlab turganda oynani ochsangiz, u
demonga **ulanadi** — jadvalda o'sha demon boshqarayotgan tunnellar ko'rinadi.

Qo'shimcha foyda: demon `systemd --user` dan ishga tushgani uchun **unconfined**
bo'ladi, shuning uchun RAM bo'limidan snap ilovalarni to'xtatish ham ishlaydi
(pastdagi AppArmor bo'limiga qarang).

### PM2 jarayonlari

pm2'ning o'z `pm2 startup` (sudo + alohida systemd xizmati) mexanizmi shart
emas — buning o'rniga **ServerGo demoni** (yuqoridagi `make install-service`)
har safar ko'tarilganda `pm2 resurrect` ni o'zi chaqiradi va saqlangan
ro'yxatni tiklaydi. Ya'ni boot'da faqat bitta narsa — ServerGo — ko'tariladi,
qolganini o'zi qiladi.

Kerakli shart bitta: jarayonlaringiz ishga tushirilgach, ro'yxatni saqlang —
aynan shu ro'yxat keyingi ishga tushishda tiklanadi:

```bash
pm2 save
```

Ro'yxatni har o'zgartirganingizda (`pm2 start` / `pm2 delete`) `pm2 save` ni
qayta bajaring, aks holda tiklashda eski ro'yxat qaytadi. Jarayon allaqachon
ishlab tursa, `pm2 resurrect` uni jim o'tkazib yuboradi — xavfsiz.

## Klaviatura

| Tugma | Amal |
|---|---|
| `Ctrl+R` | Darhol yangilash |
| `/` | Qidiruv maydoniga o'tish |
| `Esc` | Panelni yoki tasdiqlash oynasini yopish |
| `Enter` | Tasdiqlash oynasida tasdiqlash |

## Terminal orqali boshqarish

Oynani ochmasdan, `servergo <buyruq>` bilan pm2/RAM/tunnel bo'limlarini
terminaldan boshqarish va kuzatish mumkin. Bu alohida jarayon ko'tarmaydi —
allaqachon ishlab turgan nusxaga (oyna yoki `servergo -daemon`) xuddi
ikkinchi oyna kabi ulanadi, shuning uchun "bitta egasi" qoidasi buzilmaydi.
Hech narsa ishlamayotgan bo'lsa, tushunarli xato bilan to'xtaydi.

`servergo` ni istalgan joydan (`./` siz) chaqirish uchun, bir marta:

```bash
make install-cli   # ~/.local/bin ga bog'lama qo'yadi, sudo shart emas
```

Agar `~/.local/bin` hali `$PATH` da bo'lmasa, buyruq shuni aytadi — yangi
terminal oching yoki `source ~/.profile` bajaring. Olib tashlash:
`make uninstall-cli`.

```bash
servergo help                 # to'liq buyruqlar ro'yxati
servergo status                # qisqacha holat: pm2, RAM, tunnellar

servergo ps                    # pm2 jarayonlari ro'yxati
servergo ps -w                 # jonli kuzatish (2s da yangilanadi, Ctrl+C — chiqish)
servergo ps restart api        # nom yoki id bo'yicha
servergo ps logs api -n 200    # oxirgi 200 qator

servergo ram                   # RAM guruhlari (RSS)
servergo ram -a                # aniq rejim (PSS)
servergo ram kill 12345        # jarayon daraxtini to'xtatish (pid)

servergo apps                                  # ilovalar ro'yxati (pm2'ga bog'liq emas)
servergo apps create bot "node bot.js" -a      # -a: avtostart ham yoqiladi
servergo apps restart bot                      # nom yoki id bo'yicha
servergo apps logs bot

servergo tunnel                       # loyihalar ro'yxati
servergo tunnel create 3000 todo      # port 3000 ni todo.<faol-domen> ga chiqaradi
servergo tunnel create 3000 todo -a   # -a: avtostart ham yoqiladi
servergo tunnel create 3000 todo -f   # -f: DNS band bo'lsa almashtiradi
servergo tunnel create 3000           # subdomensiz — domenning o'zi uchun ('@' ham bo'ladi)
servergo tunnel diagnose todo         # Error 1033 sababini tekshiradi
servergo tunnel fixdns todo           # DNS yozuvini tuzatadi
servergo tunnel domains               # bazaviy domenlar ro'yxati (* — faol)
```

`tunnel create` uchun avval kamida bitta bazaviy domen qo'shilgan va Cloudflare
bilan bog'langan bo'lishi kerak (`servergo tunnel add-domain <domen>` yoki
oynadagi sozlash sehrgari) — bu birinchi sozlash hali ham faqat oynada bo'ladi.

Loyihalar va jarayonlar `id` yoki nom bo'yicha (bir qiymatga mos kelsa)
ko'rsatiladi — to'liq UUID yozish shart emas. Har bir buyruq tavsifi va
to'liq ro'yxat uchun `servergo help`.

## Tuzilishi

```
main.go                  rejimlar: -daemon / oyna (egasi) / oyna (ulanish) / CLI
internal/
  cli/                   "servergo <buyruq>" — terminal orqali boshqarish
  daemon/daemon.go       flock bilan yagona egalik + daemon.json manzili
  apps/service.go        Ilovalar bo'limi mantiqi (pm2'ga bog'liq emas)
  apps/store/            SQLite: ilovalar (~/.config/servergo/apps/apps.db)
  apps/manager/          jarayon boshqaruvi: start/stop, avto-restart, loglar
  pm2/pm2.go             pm2 CLI: jlist, start/stop/restart/delete, flush, ping
  pm2/logs.go            log fayl tail (oxiridan 128 KB)
  sysmon/proc.go         /proc o'qish, meminfo, jarayonlarni guruhlash
  sysmon/kill.go         daraxtni yig'ish, SIGTERM/SIGKILL
  server/server.go       HTTP API
  server/host.go         host metrikalari (loadavg, uptime, uname)
  server/tunnels.go      Tunnellar bo'limining HTTP handlerlari
  tunnel/service.go      Tunnellar bo'limi mantiqi (LocalGo app.go o'rniga)
  tunnel/store/          SQLite: loyihalar, sozlamalar
  tunnel/cf/             cloudflared buyruqlari (login, create, route dns, delete)
  tunnel/manager/        jarayon boshqaruvi: start/stop, avto-restart, health
  tunnel/applog/         bo'lim loglari
web/                     UI — binar ichiga embed qilinadi
  index.html  styles.css  app.js
scripts/install-desktop.sh
scripts/install-service.sh
```

## Xavfsizlik

Dastur ichida HTTP server ishlaydi (UI shu orqali ma'lumot oladi). U:

- faqat `127.0.0.1` da, **tasodifiy portda** tinglaydi
- har bir `/api/` so'rovida bir martalik **tasodifiy token** talab qiladi, token
  oyna ochilganda URL orqali beriladi

Ya'ni bu mashinadagi boshqa dasturlar API ga kira olmaydi.

### RAM bo'limidagi himoyalar

Quyidagilarni to'xtatib bo'lmaydi (tugma o'chirilgan va API ham rad etadi):

- pid 1, dasturning o'zi va uning ajdodlari — bularga desktop rejimida sessiya
  jarayonlari (`gnome-shell`, `systemd --user`) ham kiradi
- **pm2 demoni** — uni o'ldirish barcha pm2 ilovalarini birdaniga yiqitadi.
  Jarayonlarni "Jarayonlar" bo'limidan to'xtatish kerak

`Xwayland`, `sshd`, `mutter` kabi sessiyaga ta'sir qiladigan jarayonlar
ogohlantirish yorlig'ini oladi, lekin bloklanmaydi — qaror foydalanuvchiniki.

Boshqa foydalanuvchining jarayonini o'ldirish uchun root kerak; root'siz ular
ro'yxatda ko'rinadi lekin to'xtatilmaydi.

### AppArmor: snap ilovalari to'xtamasa

Agar snap ilovasi (Firefox, Telegram, Chromium…) "0 tasi yopildi" bo'lsa, sabab
odatda AppArmor signal mediatsiyasi bo'ladi. Snap profili faqat quyidagilardan
signal qabul qiladi:

```
signal (receive) peer=unconfined,
signal (receive) peer=snap.*,
```

Ya'ni pm2-monitor **unconfined** yorlig'i bilan ishlashi kerak. Agar dasturni
o'z AppArmor profiliga ega ilova ichidagi terminaldan ishga tushirsangiz
(masalan `claude-desktop`), bola jarayon o'sha yorliqni meros qilib oladi va
snap'lar uning signalini rad etadi — `kill()` EPERM qaytaradi.

Yorliqni tekshirish:

```bash
cat /proc/self/attr/current
```

`unconfined` chiqishi kerak. Aks holda dasturni oddiy terminaldan yoki Ubuntu
ilovalar menyusidagi yorliqdan ishga tushiring.

Rad etish holatlari kernel logida ko'rinadi:

```bash
journalctl -k --since -10m | grep 'apparmor="DENIED".*signal'
```

## RSS va PSS

Standart rejim **RSS** ni ko'rsatadi — tez, lekin bo'lishilgan kutubxonalar har
bir jarayonda qayta sanaladi, shuning uchun guruhlar yig'indisi tizimdagi
haqiqiy band xotiradan ko'p chiqadi.

"Aniq rejim (PSS)" `/proc/<pid>/smaps_rollup` dan o'qiydi: bo'lishilgan sahifa
uni ishlatayotgan jarayonlar orasida bo'lib beriladi. Aniqroq, lekin har
yangilanishda har bir jarayon uchun qo'shimcha fayl o'qiladi.

## Eslatmalar

- Dastur PM2 ning programmatic API si o'rniga **`pm2` CLI** ni chaqiradi — bu
  PM2 versiyalari o'rtasida barqarorroq ishlaydi.
- Loglar fayl oxiridan maksimum 128 KB o'qiladi, katta log fayllar ham tez ochiladi.
- Amallar joriy foydalanuvchining PM2 daemoni ustida bajariladi.
- Nosozlikni tuzatish uchun `make headless` — oynasiz, faqat HTTP server;
  manzil (token bilan) stdout ga chiqadi.
