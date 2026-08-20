#!/usr/bin/env bash
# Boot'da tarmoq tayyor bo'lishini kutadi.
#
# Nega kerak: systemd USER manager'ida `network-online.target` mavjud emas,
# shuning uchun unit'dagi After=/Wants= bekor bo'ladi. Demon tarmoqdan oldin
# ko'tarilsa, cloudflared ulana olmaydi va manager 3 marta urinib (1s/2s/4s)
# taslim bo'ladi — natijada boot'dan keyin tunnellar o'lik qoladi.
#
# Har doim 0 bilan chiqamiz: tarmoq baribir ko'tarilmasa ham demon ishga
# tushsin va sababni UI/loglarda ko'rsatsin.
set -u

HOST="${1:-api.cloudflare.com}"
TIMEOUT="${2:-120}"

deadline=$((SECONDS + TIMEOUT))
while [ "$SECONDS" -lt "$deadline" ]; do
  if getent hosts "$HOST" >/dev/null 2>&1; then
    echo "tarmoq tayyor ($HOST hal qilindi, ${SECONDS}s)"
    exit 0
  fi
  sleep 2
done

echo "ogohlantirish: ${TIMEOUT}s ichida $HOST hal qilinmadi — baribir davom etamiz"
exit 0
