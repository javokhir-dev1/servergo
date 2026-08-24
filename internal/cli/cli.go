package cli

import (
	"fmt"
	"os"
)

// Run — "servergo <buyruq> ..." ni bajaradi va chiqish kodini qaytaradi.
// main.go ni chaqiruvchisi shu kodni os.Exit ga beradi.
func Run(args []string) int {
	if len(args) == 0 {
		printUsage()
		return 2
	}
	cmd, rest := args[0], args[1:]

	// "help"/"-h"/"--help" — daemonga ulanishga urinmasdan chiqish tavsifini beradi.
	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		printUsage()
		return 0
	}

	c, err := connect()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	var runErr error
	switch cmd {
	case "status":
		runErr = cmdStatus(c, rest)
	case "ps":
		runErr = cmdPS(c, rest)
	case "ram":
		runErr = cmdRAM(c, rest)
	case "apps":
		runErr = cmdApps(c, rest)
	case "tunnel", "tun":
		runErr = cmdTunnel(c, rest)
	case "vpstunnel", "vtun":
		runErr = cmdVPSTunnel(c, rest)
	case "login":
		runErr = cmdLogin(c, rest)
	case "logout":
		runErr = cmdLogout(c, rest)
	case "sync":
		runErr = cmdSync(c, rest)
	default:
		fmt.Fprintf(os.Stderr, "noma'lum buyruq: %s\n\n", cmd)
		printUsage()
		return 2
	}
	if runErr != nil {
		fmt.Fprintln(os.Stderr, "xato:", runErr)
		return 1
	}
	return 0
}

func printUsage() {
	fmt.Print(`ServerGo — terminal orqali boshqarish

Foydalanish:
  servergo <buyruq> [argumentlar]

Umumiy:
  status                              qisqacha holat: pm2, RAM, tunnellar

Jarayonlar (pm2):
  ps [list]                           ro'yxat (-w — jonli kuzatish)
  ps start   <id|nom>                 ishga tushirish
  ps stop    <id|nom>                 to'xtatish
  ps restart <id|nom>                 qayta ishga tushirish
  ps delete  <id|nom>                 pm2 ro'yxatidan o'chirish
  ps logs    <id|nom> [-n N]          oxirgi N qator log (standart 100)
  ps flush   <id|nom>                 log fayllarini bo'shatish

RAM:
  ram [list] [-a]                     guruhlar bo'yicha (-a — aniq/PSS rejim, -w — jonli)
  ram kill <pid>                      jarayon daraxtini to'xtatish

Ilovalar (pm2'ga bog'liq emas — ServerGo'ning o'z boshqaruvchisi):
  apps [list] [-w]                    ro'yxat (-w — jonli kuzatish)
  apps create <nom> <buyruq...>       masalan: apps create bot "node bot.js" -a
    [-c ishchi-papka] [-a]             -c: ishchi papka (standart: uy papkasi)
                                        -a: avtostart yoqish
  apps start   <id|nom>
  apps stop    <id|nom>
  apps restart <id|nom>
  apps delete  <id|nom>
  apps logs    <id|nom>

Tunnellar:
  tunnel [list]                       loyihalar ro'yxati (-w — jonli kuzatish)
  tunnel create <port> <subdomen>      port'ni internetga chiqarish
    [-n nom] [-d domen] [-s] [-a] [-f]  -n: nom (standart: subdomen)
                                        -d: bazaviy domen (standart: faol domen)
                                        -s: lokal servis https (standart: http)
                                        -a: avtostart yoqish
                                        -f: mavjud DNS yozuvini almashtirish
  tunnel start   <id|nom>
  tunnel stop    <id|nom>
  tunnel restart <id|nom>
  tunnel delete  <id|nom>
  tunnel logs    <id|nom>
  tunnel diagnose <id|nom>            Error 1033 sababini tekshiradi
  tunnel fixdns   <id|nom>            DNS yozuvini tuzatadi
  tunnel domains                      bazaviy domenlar ro'yxati
  tunnel add-domain <domen>
  tunnel remove-domain <domen>
  tunnel active-domain <domen>        faol domenni belgilash

VPS Tunnel (o'z VPS'ingiz orqali, Cloudflare'siz):
  vpstunnel [list]                    loyihalar ro'yxati (-w — jonli kuzatish)
  vpstunnel create <port> <subdomen>   port'ni VPS orqali internetga chiqarish
    [-n nom] [-d domen] [-s] [-a]      -n: nom (standart: subdomen)
                                        -d: bazaviy domen (standart: faol domen)
                                        -s: lokal servis https (standart: http)
                                        -a: avtostart yoqish
                                        subdomen ko'rsatilmasa (yoki '@') —
                                        domenning o'zi uchun (alohida A
                                        yozuv kerak, wildcard yetarli emas)
  vpstunnel start   <id|nom>
  vpstunnel stop    <id|nom>
  vpstunnel restart <id|nom>
  vpstunnel delete  <id|nom>
  vpstunnel logs    <id|nom>
  vpstunnel relay <manzil:port> <token> <fingerprint>
                                       relay ulanishini sozlash (bir marta)
  vpstunnel domains                   bazaviy domenlar ro'yxati
  vpstunnel add-domain <domen>
  vpstunnel remove-domain <domen>
  vpstunnel active-domain <domen>     faol domenni belgilash

Bulutli sinxronizatsiya:
  login <email>                       backend'ga kirish (parol so'raladi)
  login                                joriy holatni ko'rsatish
  logout                               chiqish
  sync [push|pull]                     ikkala tomonga (yoki faqat bittasiga) sync

Eslatma: bu buyruqlar allaqachon ishlab turgan ServerGo nusxasiga (oyna
yoki "servergo -daemon") ulanadi — o'zi alohida jarayon ko'tarmaydi.
`)
}
