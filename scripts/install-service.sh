#!/usr/bin/env bash
# ServerGo tunnellarini kompyuter yoqilganda (login'siz) ishga tushiradi.
# --remove bilan olib tashlanadi.
set -euo pipefail

APP_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$APP_DIR/servergo"
UNIT_DIR="$HOME/.config/systemd/user"
UNIT="$UNIT_DIR/servergo.service"

if [[ "${1:-}" == "--remove" ]]; then
  systemctl --user disable --now servergo.service 2>/dev/null || true
  rm -f "$UNIT"
  systemctl --user daemon-reload
  echo "Servis olib tashlandi: $UNIT"
  echo "Eslatma: linger yoqilgan bo'lsa qo'lda o'chiring — loginctl disable-linger $USER"
  exit 0
fi

if [[ ! -x "$BIN" ]]; then
  echo "Xato: $BIN topilmadi. Avval 'make build' ni bajaring." >&2
  exit 1
fi

# systemd user servisining PATH i juda qisqa (/usr/bin:/bin). cloudflared va pm2
# ko'pincha /usr/local/bin da bo'ladi, shuning uchun to'liq yo'llarni yozamiz.
PM2_BIN="$(command -v pm2 || true)"
CF_BIN="$(command -v cloudflared || true)"
EXTRA_PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:$HOME/.local/bin"

mkdir -p "$UNIT_DIR"
cat > "$UNIT" <<EOF
[Unit]
Description=ServerGo — Cloudflare tunnellarini fonda ushlab turadi
Documentation=file://$APP_DIR/README.md
# Diqqat: systemd USER manager'ida network-online.target mavjud emas, shuning
# uchun unga After=/Wants= yozishning foydasi yo'q. Tarmoqni ExecStartPre kutadi.

[Service]
Type=simple
ExecStartPre=$APP_DIR/scripts/wait-network.sh
ExecStart=$BIN -daemon
Environment=PATH=$EXTRA_PATH
${PM2_BIN:+Environment=PM2_BIN=$PM2_BIN}
Restart=on-failure
RestartSec=5
# Tunnellarni chiroyli yopish uchun: SIGTERM -> StopAll -> chiqish.
KillMode=mixed
TimeoutStopSec=20

[Install]
WantedBy=default.target
EOF

systemctl --user daemon-reload
systemctl --user enable --now servergo.service

echo "Yaratildi: $UNIT"
[[ -n "$CF_BIN" ]] && echo "cloudflared: $CF_BIN" || echo "Ogohlantirish: cloudflared PATH da topilmadi"
echo

# Linger — foydalanuvchi login qilmasa ham user servislari ishlashi uchun.
# Boot'da ishga tushishi aynan shunga bog'liq.
if [[ "$(loginctl show-user "$USER" -p Linger --value 2>/dev/null || echo no)" == "yes" ]]; then
  echo "Linger allaqachon yoqilgan — servis kompyuter yoqilganda ishga tushadi."
elif loginctl enable-linger "$USER" 2>/dev/null; then
  echo "Linger yoqildi — servis endi login'siz, kompyuter yoqilganda ishga tushadi."
else
  echo "DIQQAT: linger yoqilmadi. Usiz servis faqat siz login qilganingizda ishga tushadi."
  echo "Qo'lda yoqish uchun:"
  echo "    sudo loginctl enable-linger $USER"
fi

echo
echo "Holat:   systemctl --user status servergo"
echo "Loglar:  journalctl --user -u servergo -f"
echo
echo "Eslatma: tunnel faqat loyihaning \"Avtostart\" belgisi yoqilgan bo'lsa ishga tushadi."
