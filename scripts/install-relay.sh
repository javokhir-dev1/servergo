#!/usr/bin/env bash
# VPS'da ishga tushiriladi: servergo-relay'ni quradi va systemd system
# service sifatida o'rnatadi (80/443 portlarini bog'lash uchun root kerak).
#
# Foydalanish (VPS'da, ushbu repo bilan):
#   sudo RELAY_TOKEN=$(openssl rand -hex 32) ./scripts/install-relay.sh
#
# --remove bilan olib tashlanadi:
#   sudo ./scripts/install-relay.sh --remove
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RELAY_DIR="$REPO_DIR/relay"
BIN="/usr/local/bin/servergo-relay"
UNIT="/etc/systemd/system/servergo-relay.service"
CERT_DIR="/var/lib/servergo-relay/certs"
ENV_FILE="/etc/servergo-relay.env"

if [[ "${1:-}" == "--remove" ]]; then
  systemctl disable --now servergo-relay.service 2>/dev/null || true
  rm -f "$UNIT" "$BIN"
  systemctl daemon-reload
  echo "Olib tashlandi: $UNIT, $BIN"
  echo "Eslatma: sertifikatlar/fingerprint saqlanib qoldi ($CERT_DIR) — qo'lda o'chirilmadi."
  exit 0
fi

if [[ $EUID -ne 0 ]]; then
  echo "Xato: root kerak (sudo bilan ishga tushiring)." >&2
  exit 1
fi

if ! command -v go >/dev/null 2>&1; then
  echo "Xato: 'go' topilmadi. Go 1.21+ o'rnating (https://go.dev/dl/) yoki" >&2
  echo "binary'ni boshqa mashinada quring: GOOS=linux GOARCH=amd64 go build -o servergo-relay ./relay/cmd/servergo-relay" >&2
  echo "va shu VPS'ga $BIN sifatida ko'chiring, so'ng bu skriptni qayta bajaring." >&2
  exit 1
fi

if [[ -z "${RELAY_TOKEN:-}" ]]; then
  echo "Xato: RELAY_TOKEN muhit o'zgaruvchisi kerak." >&2
  echo "Masalan: sudo RELAY_TOKEN=\$(openssl rand -hex 32) $0" >&2
  exit 1
fi

echo "Qurilmoqda: $RELAY_DIR"
( cd "$RELAY_DIR" && go build -trimpath -ldflags="-s -w" -o "$BIN" ./cmd/servergo-relay )
chmod 755 "$BIN"

mkdir -p "$CERT_DIR"
chmod 700 "$CERT_DIR"

cat > "$ENV_FILE" <<EOF
RELAY_TOKEN=$RELAY_TOKEN
RELAY_CONTROL_ADDR=:9443
RELAY_HTTP_ADDR=:80
RELAY_HTTPS_ADDR=:443
RELAY_CERT_DIR=$CERT_DIR
EOF
chmod 600 "$ENV_FILE"

cat > "$UNIT" <<EOF
[Unit]
Description=ServerGo Relay — VPS orqali reverse-tunnel
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=$ENV_FILE
ExecStart=$BIN
Restart=on-failure
RestartSec=3
# 80/443 ni root bo'lmagan holatda ham bog'lash mumkin bo'lishi uchun
# (xohlasangiz User=servergo qo'shib, shu capability bilan cheklashingiz mumkin).
AmbientCapabilities=CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now servergo-relay.service

echo
echo "O'rnatildi va ishga tushdi."
echo "Fingerprint'ni ko'rish uchun:"
echo "    journalctl -u servergo-relay -n 20 --no-pager | grep fingerprint"
echo
echo "Keyingi qadamlar:"
echo "  1) Firewall: 80, 443, 9443 portlarini oching (masalan: sudo ufw allow 80,443,9443/tcp)"
echo "  2) DNS panelingizda wildcard yozuv qo'shing: *.sizning-domeningiz.uz -> shu VPS IP"
echo "  3) ServerGo desktop ilovasida \"VPS Tunnel\" bo'limi -> \"Relay sozlamalari\":"
echo "     - Manzil:      <VPS_IP>:9443"
echo "     - Token:       (RELAY_TOKEN sifatida yuqorida bergan qiymatingiz)"
echo "     - Fingerprint: (yuqoridagi journalctl chiqishidan)"
echo "     - Wildcard domen: sizning-domeningiz.uz"
echo
echo "Holat:  systemctl status servergo-relay"
echo "Loglar: journalctl -u servergo-relay -f"
