#!/usr/bin/env bash
set -euo pipefail

[[ $EUID -eq 0 ]] || { echo '请使用 root 运行'; exit 1; }
ROOT_DIR=$(cd "$(dirname "$0")" && pwd)
DATA_DIR=/var/lib/xs5
CONF_DIR=/etc/xs5
LIB_DIR=/usr/local/lib/xs5
VERSION=1.0.2
FIRST_SETUP=0
MIGRATED=0

risk_notice(){
  [[ -f "$DATA_DIR/.risk_ack" ]] && return 0
  echo
  echo '⚠ Xs5 风险提示'
  echo 'Xs5 会使用第三方公开 VPN / SOCKS5 节点。第三方节点可能失效、限速、记录流量、被网站封禁，IP 属性也可能误判。'
  echo '请勿通过不受信任的公共出口传输敏感明文数据。使用者应自行遵守当地法律及第三方服务条款。'
  echo '程序需要 root 权限并会管理 netns、路由、iptables 与 OpenVPN。建议仅部署在独立 VPS。'
  echo
  if [[ ${XS5_ACCEPT_RISK:-0} == 1 ]]; then return 0; fi
  if [[ ! -t 0 ]]; then
    echo '非交互安装请显式设置 XS5_ACCEPT_RISK=1 后重试。' >&2
    exit 1
  fi
  read -r -p '我已了解上述风险并继续安装？[y/N] ' ans
  [[ "$ans" =~ ^[Yy]$ ]] || { echo '已取消安装。'; exit 0; }
}

mkdir -p "$DATA_DIR" "$CONF_DIR" "$LIB_DIR"
chmod 700 "$DATA_DIR" "$CONF_DIR"
risk_notice
touch "$DATA_DIR/.risk_ack"
chmod 600 "$DATA_DIR/.risk_ack"

echo '[1/7] 安装运行依赖'
apt-get update -qq
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq openvpn curl iproute2 iptables socat iputils-ping ca-certificates jq

echo '[2/7] 准备 Xs5 程序'
BIN="$ROOT_DIR/xs5d"
if [[ ! -x "$BIN" ]]; then
  [[ -f "$ROOT_DIR/main.go" && -f "$ROOT_DIR/go.mod" ]] || { echo '缺少 xs5d 二进制或 Go 源码'; exit 1; }
  if ! command -v go >/dev/null 2>&1; then
    echo '源码安装：正在安装 Go 编译器'
    DEBIAN_FRONTEND=noninteractive apt-get install -y -qq golang-go
  fi
  echo '源码安装：正在编译 xs5d'
  (cd "$ROOT_DIR" && CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o xs5d .)
fi

echo '[3/7] 停止旧服务并迁移配置'
for svc in country-s5-pool x-s5-pool xs5; do systemctl disable --now "$svc" >/dev/null 2>&1 || true; done
if [[ ! -s "$DATA_DIR/password" ]]; then
  for old in /var/lib/x-s5-pool /var/lib/country-s5-pool; do
    if [[ -s "$old/password" ]]; then install -m 600 "$old/password" "$DATA_DIR/password"; MIGRATED=1; break; fi
  done
fi
if [[ ! -s "$DATA_DIR/pools.json" ]]; then
  for old in /var/lib/x-s5-pool /var/lib/country-s5-pool; do
    if [[ -s "$old/pools.json" ]]; then install -m 600 "$old/pools.json" "$DATA_DIR/pools.json"; MIGRATED=1; break; fi
  done
fi

echo '[4/7] 安装服务与管理菜单'
install -m 755 "$BIN" /usr/local/bin/xs5d
install -m 755 "$ROOT_DIR/xs5.sh" /usr/local/bin/xs5
install -m 644 "$ROOT_DIR/xs5.service" /etc/systemd/system/xs5.service
install -m 755 "$ROOT_DIR/uninstall.sh" "$LIB_DIR/uninstall.sh"
printf '%s\n' "$VERSION" > "$CONF_DIR/version"
if [[ ! -s "$CONF_DIR/xs5.env" ]]; then printf 'XS5_LISTEN=:8898\n' > "$CONF_DIR/xs5.env"; fi
chmod 600 "$CONF_DIR/xs5.env"
if [[ ! -f "$CONF_DIR/access_mode" ]]; then FIRST_SETUP=1; printf 'ip\n' > "$CONF_DIR/access_mode"; fi
rm -f /etc/systemd/system/country-s5-pool.service /etc/systemd/system/x-s5-pool.service
rm -f /usr/local/bin/country-s5-pool /usr/local/bin/x-s5-pool

sysctl -qw net.ipv4.ip_forward=1
grep -q '^net.ipv4.ip_forward=1' /etc/sysctl.conf 2>/dev/null || echo 'net.ipv4.ip_forward=1' >> /etc/sysctl.conf

echo '[5/7] 启动 Xs5'
systemctl daemon-reload
systemctl enable xs5 >/dev/null
systemctl restart xs5
sleep 2
if ! systemctl is-active --quiet xs5; then
  echo 'Xs5 启动失败，最近日志：' >&2
  journalctl -u xs5 -n 60 --no-pager >&2 || true
  exit 1
fi

echo '[6/7] 配置面板访问方式'
if [[ $FIRST_SETUP -eq 1 ]]; then
  /usr/local/bin/xs5 --setup-access
else
  echo "保留现有访问模式：$(cat "$CONF_DIR/access_mode" 2>/dev/null || echo ip)"
fi

echo '[7/7] 安装完成'
IP=$(curl -4 -fsS --max-time 5 https://api.ipify.org 2>/dev/null || hostname -I | awk '{print $1}')
PW=$(cat "$DATA_DIR/password" 2>/dev/null || true)
MODE=$(cat "$CONF_DIR/access_mode" 2>/dev/null || echo ip)
DOMAIN=$(cat "$CONF_DIR/domain" 2>/dev/null || true)
echo
[[ $MIGRATED -eq 1 ]] && echo '已迁移旧版本管理密码和出口配置。'
echo "Xs5 v$VERSION"
if [[ "$MODE" == domain-* && -n "$DOMAIN" ]]; then echo "管理面板: https://$DOMAIN"; else echo "管理面板: http://$IP:8898"; fi
echo "管理密码: ${PW:-请执行 cat $DATA_DIR/password}"
echo "管理菜单: xs5"
echo 'S5 地址始终使用服务器公网 IP。'
echo
