#!/usr/bin/env bash
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo '请使用 root 运行'; exit 1; }
DATA_DIR=/var/lib/xs5
CONF_DIR=/etc/xs5
DOMAIN=$(cat "$CONF_DIR/domain" 2>/dev/null || true)

systemctl disable --now xs5 >/dev/null 2>&1 || true
if [[ -n "$DOMAIN" && -x /root/.acme.sh/acme.sh ]]; then
  /root/.acme.sh/acme.sh --remove -d "$DOMAIN" --ecc >/dev/null 2>&1 || true
fi
rm -f /etc/nginx/sites-enabled/xs5.conf /etc/nginx/sites-available/xs5.conf
if command -v nginx >/dev/null 2>&1; then nginx -t >/dev/null 2>&1 && systemctl reload nginx >/dev/null 2>&1 || true; fi
rm -f /etc/systemd/system/xs5.service /usr/local/bin/xs5 /usr/local/bin/xs5d
rm -rf /usr/local/lib/xs5
systemctl daemon-reload

for ns in $(ip netns list 2>/dev/null | awk '{print $1}' | grep -E '^csp31[0-9]{3}$' || true); do ip netns del "$ns" >/dev/null 2>&1 || true; done
for i in $(seq 1 250); do
  ip link del "cspv${i}" >/dev/null 2>&1 || true
  ip link del "cspp${i}" >/dev/null 2>&1 || true
  ip link del "cv${i}" >/dev/null 2>&1 || true
  ip link del "cp${i}" >/dev/null 2>&1 || true
  cidr="10.77.${i}.0/30"
  iptables -t nat -D POSTROUTING -s "$cidr" -j MASQUERADE >/dev/null 2>&1 || true
  iptables -D FORWARD -s "$cidr" -j ACCEPT >/dev/null 2>&1 || true
  iptables -D FORWARD -d "$cidr" -j ACCEPT >/dev/null 2>&1 || true
done
rm -rf "$DATA_DIR" "$CONF_DIR" /var/www/xs5-acme

echo 'Xs5 已卸载。不会卸载系统中可能被其他站点共用的 nginx 或 acme.sh。'
echo '旧版本备份目录 /var/lib/x-s5-pool 或 /var/lib/country-s5-pool 若存在，也不会自动删除。'
