#!/usr/bin/env bash
# ServerGo ni Ubuntu ilovalar menyusiga qo'shadi (--remove bilan olib tashlaydi).
set -euo pipefail

APP_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DESKTOP_DIR="$HOME/.local/share/applications"
DESKTOP_FILE="$DESKTOP_DIR/servergo.desktop"
BIN="$APP_DIR/servergo"

refresh_menu() {
  if command -v update-desktop-database >/dev/null 2>&1; then
    update-desktop-database "$DESKTOP_DIR" >/dev/null 2>&1 || true
  fi
}

if [[ "${1:-}" == "--remove" ]]; then
  if [[ -f "$DESKTOP_FILE" ]]; then
    rm -f "$DESKTOP_FILE"
    refresh_menu
    echo "O'chirildi: $DESKTOP_FILE"
  else
    echo "Desktop yorlig'i topilmadi, hech nima qilinmadi."
  fi
  exit 0
fi

if [[ ! -x "$BIN" ]]; then
  echo "Xato: $BIN topilmadi. Avval 'make build' ni bajaring." >&2
  exit 1
fi

# pm2 ning to'liq yo'lini yorliqqa yozib qo'yamiz — menyudan ishga tushirilganda
# PATH cheklangan bo'ladi va nvm bilan o'rnatilgan pm2 topilmay qolishi mumkin.
PM2_BIN="$(command -v pm2 || true)"
if [[ -z "$PM2_BIN" ]]; then
  echo "Ogohlantirish: pm2 PATH da topilmadi. Yorliq baribir yaratiladi." >&2
  EXEC="$BIN"
else
  EXEC="env PM2_BIN=$PM2_BIN $BIN"
fi

mkdir -p "$DESKTOP_DIR"
cat > "$DESKTOP_FILE" <<EOF
[Desktop Entry]
Type=Application
Version=1.0
Name=ServerGo
GenericName=Server Manager
Comment=PM2 jarayonlari, tizim xotirasi va Cloudflare tunnellarini boshqarish
Exec=$EXEC
Icon=$APP_DIR/assets/icon.svg
Terminal=false
Categories=Development;System;Monitor;
Keywords=pm2;process;monitor;ram;memory;node;cloudflare;tunnel;servergo;
StartupNotify=true
StartupWMClass=servergo
EOF
chmod 644 "$DESKTOP_FILE"
refresh_menu

echo "Yaratildi: $DESKTOP_FILE"
echo "Exec: $EXEC"
echo
echo "Endi 'ServerGo' ni Ubuntu ilovalar menyusidan qidirib toping."
