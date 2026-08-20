# servergo-backend

Markaziy backend: foydalanuvchi hisobiga bog'langan holda `servergo`ning
`apps`/`projects` (tunnel) metadata'sini va Cloudflare tunnel credentials'ini
saqlaydi, shunda boshqa PC'da login qilganda ular sync bo'ladi.

Bu — asosiy `servergo` binary'sidan mustaqil, alohida Go moduli va alohida
deploy qilinadigan xizmat.

## Sozlash

Muhit o'zgaruvchilari (`.env` fayl yaratib yoki to'g'ridan-to'g'ri export qilib
berish mumkin — `.env` `.gitignore`da):

```
DATABASE_URL=postgres://servergo:<PAROL>@localhost:5432/servergo_backend?sslmode=disable
SERVERGO_ENC_KEY=<32 bayt, hex, masalan `openssl rand -hex 32`>
LISTEN_ADDR=127.0.0.1:8095   # ixtiyoriy, standart shu
```

Postgres role/database (bir martalik sozlash, `postgres` superuser orqali):

```sql
CREATE ROLE servergo WITH LOGIN PASSWORD '<PAROL>';
CREATE DATABASE servergo_backend OWNER servergo;
```

## Ishga tushirish

```
go build -o servergo-backend ./cmd/servergo-backend
DATABASE_URL=... SERVERGO_ENC_KEY=... ./servergo-backend
```

Birinchi ishga tushishda migratsiya (`internal/db/migrations/*.sql`) avtomatik
qo'llanadi.

## API

Barcha javoblar `{"ok": bool, "error": string, "data": ...}` formatida
(lokal servergo CLI-daemon protokoli bilan bir xil konventsiya).

| Method | Path                        | Auth | Vazifa |
|--------|-----------------------------|------|--------|
| POST   | /api/auth/register          | yo'q | `{email,password}` |
| POST   | /api/auth/login              | yo'q | `{email,password,device_name}` → `{token}` |
| POST   | /api/auth/logout             | ha   | joriy tokenni bekor qiladi |
| GET    | /api/apps                    | ha   | sync qilingan apps ro'yxati |
| PUT    | /api/apps/{local_id}         | ha   | upsert |
| DELETE | /api/apps/{local_id}         | ha   | o'chirish |
| GET    | /api/projects                | ha   | tunnel loyihalari (tunnel_secret deshifrlab qaytadi) |
| PUT    | /api/projects/{local_id}     | ha   | upsert — `tunnel_secret` bo'sh bo'lsa avvalgisi saqlanib qoladi |
| DELETE | /api/projects/{local_id}     | ha   | o'chirish |
| GET    | /api/domains/{domain}/cert   | ha   | domen uchun saqlangan `cert.pem` |
| PUT    | /api/domains/{domain}/cert   | ha   | `cert.pem` tarkibini shifrlab saqlaydi |

`Authorization: Bearer <token>` header talab qilinadi (register/login'dan tashqari).

## Xavfsizlik

- Parollar bcrypt bilan xeshlanadi.
- Bearer token'lar tasodifiy 32 bayt, bazada faqat sha256 xeshi saqlanadi.
- `tunnel_secret` (Cloudflare tunnel credentials) va domen `cert.pem` tarkibi
  bazada AES-256-GCM bilan shifrlangan holda saqlanadi (`SERVERGO_ENC_KEY`).

## Keyingi bosqich

CLI'ga `servergo login`/`logout`/`sync` buyruqlarini ulash — bu backend ustiga
qurilgan, alohida ish sifatida rejalashtirilgan.
