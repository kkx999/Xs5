#!/usr/bin/env bash
set -u

APP_NAME="X S5 池"
CONF_DIR="/etc/xs5"
DATA_DIR="/var/lib/xs5"
ENV_FILE="$CONF_DIR/xs5.env"
MODE_FILE="$CONF_DIR/access_mode"
DOMAIN_FILE="$CONF_DIR/domain"
VERSION_FILE="$CONF_DIR/version"
NGINX_SITE="/etc/nginx/sites-available/xs5.conf"
NGINX_LINK="/etc/nginx/sites-enabled/xs5.conf"
ACME_ROOT="/var/www/xs5-acme"
CERT_DIR="$CONF_DIR/certs"
REPO="kkx999/Xs5"
RAW_INSTALL="https://raw.githubusercontent.com/kkx999/Xs5/main/install-latest.sh"

[[ ${EUID:-$(id -u)} -eq 0 ]] || { echo "请使用 root 运行 xs5"; exit 1; }
mkdir -p "$CONF_DIR" "$DATA_DIR"
chmod 700 "$CONF_DIR" "$DATA_DIR"

version(){ cat "$VERSION_FILE" 2>/dev/null || echo "1.0.3"; }
public_ip(){
  local ip
  ip=$(curl -4 -fsS --max-time 5 https://api.ipify.org 2>/dev/null || true)
  if [[ -z "$ip" ]]; then ip=$(hostname -I 2>/dev/null | awk '{print $1}'); fi
  printf '%s' "$ip"
}
mode(){ cat "$MODE_FILE" 2>/dev/null || echo ip; }
domain(){ cat "$DOMAIN_FILE" 2>/dev/null || true; }
set_listen(){
  local addr="$1"
  printf 'XS5_LISTEN=%s\n' "$addr" > "$ENV_FILE"
  chmod 600 "$ENV_FILE"
}
valid_domain(){ [[ "$1" =~ ^([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z]{2,63}$ ]]; }
need_pkg(){ command -v "$1" >/dev/null 2>&1 || return 0; return 1; }
install_domain_deps(){
  apt-get update -qq
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq nginx curl socat ca-certificates openssl
  mkdir -p "$ACME_ROOT/.well-known/acme-challenge" "$CERT_DIR"
  chmod 700 "$CERT_DIR"
}
install_acme(){
  if [[ ! -x /root/.acme.sh/acme.sh ]]; then
    echo "正在安装 acme.sh ..."
    if [[ -n "${1:-}" ]]; then curl -fsSL https://get.acme.sh | sh -s email="$1" >/dev/null; else curl -fsSL https://get.acme.sh | sh >/dev/null; fi
  fi
  /root/.acme.sh/acme.sh --set-default-ca --server letsencrypt >/dev/null 2>&1 || true
}
write_nginx_http_challenge(){
  local d="$1"
  cat > "$NGINX_SITE" <<NGINX
server {
    listen 80;
    server_name $d;
    root $ACME_ROOT;
    location ^~ /.well-known/acme-challenge/ { try_files \$uri =404; }
    location / { return 200 'Xs5 certificate setup'; add_header Content-Type text/plain; }
}
NGINX
  ln -sf "$NGINX_SITE" "$NGINX_LINK"
  nginx -t >/dev/null
  systemctl enable --now nginx >/dev/null 2>&1
  systemctl reload nginx
}
write_nginx_https(){
  local d="$1" with80="$2"
  if [[ "$with80" == "yes" ]]; then
    cat > "$NGINX_SITE" <<NGINX
server {
    listen 80;
    server_name $d;
    root $ACME_ROOT;
    location ^~ /.well-known/acme-challenge/ { try_files \$uri =404; }
    location / { return 301 https://\$host\$request_uri; }
}

server {
    listen 443 ssl;
    server_name $d;
    ssl_certificate $CERT_DIR/fullchain.pem;
    ssl_certificate_key $CERT_DIR/key.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_session_cache shared:XS5SSL:10m;
    ssl_session_timeout 10m;

    location / {
        proxy_pass http://127.0.0.1:8898;
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
        proxy_read_timeout 90s;
    }
}
NGINX
  else
    cat > "$NGINX_SITE" <<NGINX
server {
    listen 443 ssl;
    server_name $d;
    ssl_certificate $CERT_DIR/fullchain.pem;
    ssl_certificate_key $CERT_DIR/key.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_session_cache shared:XS5SSL:10m;
    ssl_session_timeout 10m;

    location / {
        proxy_pass http://127.0.0.1:8898;
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
        proxy_read_timeout 90s;
    }
}
NGINX
  fi
  ln -sf "$NGINX_SITE" "$NGINX_LINK"
  nginx -t >/dev/null
  systemctl enable --now nginx >/dev/null 2>&1
  systemctl reload nginx
}
install_cert_files(){
  local d="$1"
  mkdir -p "$CERT_DIR"
  /root/.acme.sh/acme.sh --install-cert -d "$d" --ecc \
    --key-file "$CERT_DIR/key.pem" \
    --fullchain-file "$CERT_DIR/fullchain.pem" \
    --reloadcmd "systemctl reload nginx" >/dev/null
  chmod 600 "$CERT_DIR/key.pem"
  chmod 644 "$CERT_DIR/fullchain.pem"
}
configure_domain_http(){
  local d="${1:-}"
  [[ -n "$d" ]] || { read -r -p "请输入面板域名（例如 xs5.example.com）：" d; }
  valid_domain "$d" || { echo "域名格式不正确。"; return 1; }
  install_domain_deps
  local ip resolved
  ip=$(public_ip)
  resolved=$(getent ahostsv4 "$d" 2>/dev/null | awk 'NR==1{print $1}')
  if [[ -z "$resolved" || -z "$ip" || "$resolved" != "$ip" ]]; then
    echo
    echo "HTTP-01 模式要求域名直接解析到本机公网 IPv4。"
    echo "当前服务器 IP：${ip:-未知}"
    echo "当前域名解析：${resolved:-未解析}"
    echo "如果使用 Cloudflare，请先暂时关闭橙云（DNS Only / 灰云），证书签发后再开启。"
    return 1
  fi
  if ss -ltnp '( sport = :80 )' 2>/dev/null | grep -q LISTEN; then
    local owners
    owners=$(ss -ltnp '( sport = :80 )' 2>/dev/null | tail -n +2 || true)
    if [[ -n "$owners" ]] && ! grep -qi nginx <<<"$owners"; then
      echo "80 端口已被其他程序占用，无法使用 HTTP-01："
      echo "$owners"
      echo "请改用 Cloudflare DNS 验证模式。"
      return 1
    fi
  fi
  write_nginx_http_challenge "$d"
  install_acme ""
  echo "正在通过 80 端口申请证书..."
  /root/.acme.sh/acme.sh --issue --server letsencrypt -d "$d" -w "$ACME_ROOT" --keylength ec-256
  install_cert_files "$d"
  write_nginx_https "$d" yes
  set_listen "127.0.0.1:8898"
  printf '%s\n' "$d" > "$DOMAIN_FILE"
  printf 'domain-http\n' > "$MODE_FILE"
  systemctl restart xs5
  echo
  echo "配置完成： https://$d"
  echo "证书会由 acme.sh 自动续期。签发完成后可开启 Cloudflare 橙云 CDN。"
}
configure_domain_dns(){
  local d="${1:-}" token zone_id
  [[ -n "$d" ]] || read -r -p "请输入面板域名（例如 xs5.example.com）：" d
  valid_domain "$d" || { echo "域名格式不正确。"; return 1; }
  echo "Cloudflare Token 建议权限：Zone / DNS / Edit + Zone / Zone / Read，并限制到当前域名。"
  read -r -s -p "请输入 Cloudflare API Token：" token; echo
  [[ -n "$token" ]] || { echo "Token 不能为空。"; return 1; }
  read -r -p "Zone ID（可留空，由 acme.sh 自动查找）：" zone_id
  install_domain_deps
  install_acme ""
  echo "正在通过 Cloudflare DNS-01 申请证书（不需要 80 端口）..."
  if [[ -n "$zone_id" ]]; then
    CF_Token="$token" CF_Zone_ID="$zone_id" /root/.acme.sh/acme.sh --issue --server letsencrypt --dns dns_cf -d "$d" --keylength ec-256
  else
    CF_Token="$token" /root/.acme.sh/acme.sh --issue --server letsencrypt --dns dns_cf -d "$d" --keylength ec-256
  fi
  install_cert_files "$d"
  write_nginx_https "$d" no
  set_listen "127.0.0.1:8898"
  printf '%s\n' "$d" > "$DOMAIN_FILE"
  printf 'domain-dns\n' > "$MODE_FILE"
  systemctl restart xs5
  unset token CF_Token CF_Zone_ID
  echo
  echo "配置完成： https://$d"
  echo "此模式不占用 80 端口；Cloudflare 可保持橙云。"
  echo "acme.sh 会在 root 专用配置中保存 DNS API 凭据用于自动续期。"
}
remove_domain(){
  local d; d=$(domain)
  if [[ -n "$d" && -x /root/.acme.sh/acme.sh ]]; then
    /root/.acme.sh/acme.sh --remove -d "$d" --ecc >/dev/null 2>&1 || true
  fi
  rm -f "$NGINX_LINK" "$NGINX_SITE" "$DOMAIN_FILE"
  if command -v nginx >/dev/null 2>&1; then nginx -t >/dev/null 2>&1 && systemctl reload nginx >/dev/null 2>&1 || true; fi
  set_listen ":8898"
  printf 'ip\n' > "$MODE_FILE"
  systemctl restart xs5
  echo "已恢复 IP 直连模式：http://$(public_ip):8898"
}
setup_access(){
  if [[ ! -t 0 ]]; then
    set_listen ":8898"; printf 'ip\n' > "$MODE_FILE"; systemctl restart xs5
    echo "非交互环境，已使用 IP 直连模式。"
    return 0
  fi
  while true; do
    echo
    echo "请选择面板访问方式："
    echo "  1. 服务器 IP 直连（http://IP:8898）"
    echo "  2. 域名 + HTTPS（80 端口 HTTP-01）"
    echo "  3. 域名 + HTTPS（Cloudflare DNS-01，不占用 80）"
    read -r -p "请选择 [1-3]：" c
    case "$c" in
      1) set_listen ":8898"; printf 'ip\n' > "$MODE_FILE"; rm -f "$DOMAIN_FILE"; systemctl restart xs5; echo "已启用 IP 直连模式。"; return 0;;
      2) configure_domain_http && return 0;;
      3) configure_domain_dns && return 0;;
      *) echo "请输入 1、2 或 3。";;
    esac
  done
}
show_status(){
  echo "服务状态：$(systemctl is-active xs5 2>/dev/null || echo unknown)"
  echo "开机启动：$(systemctl is-enabled xs5 2>/dev/null || echo unknown)"
  echo "版本：v$(version)"
  echo "面板模式：$(mode)"
  echo "监听：$(awk -F= '/^XS5_LISTEN=/{print $2}' "$ENV_FILE" 2>/dev/null || echo ':8898')"
}
show_info(){
  local ip d m; ip=$(public_ip); d=$(domain); m=$(mode)
  echo "版本：v$(version)"
  if [[ "$m" == domain-* && -n "$d" ]]; then echo "面板：https://$d"; else echo "面板：http://$ip:8898"; fi
  echo "服务器 IP：$ip"
  echo
  echo "S5 出口（始终使用服务器 IP）："
  if [[ -s "$DATA_DIR/pools.json" ]] && command -v jq >/dev/null 2>&1; then
    jq -r --arg ip "$ip" '.[] | "  \(.country) · 出口 \(.ordinal // 1)\n  \($ip):\(.port)\n  用户名: \(.user)\n  密码: \(.pass)\n"' "$DATA_DIR/pools.json"
  else
    echo "  暂无已创建出口。"
  fi
}
reset_password(){
  local p1 p2
  echo "留空可自动生成 16 位随机密码。"
  read -r -s -p "新管理密码：" p1; echo
  if [[ -z "$p1" ]]; then
    p1=$(dd if=/dev/urandom bs=1 count=48 2>/dev/null | base64 | tr -dc 'A-Za-z0-9' | cut -c1-16)
  else
    [[ ${#p1} -ge 8 ]] || { echo "密码至少 8 位。"; return 1; }
    read -r -s -p "再次输入：" p2; echo
    [[ "$p1" == "$p2" ]] || { echo "两次密码不一致。"; return 1; }
  fi
  printf '%s\n' "$p1" > "$DATA_DIR/password"
  chmod 600 "$DATA_DIR/password"
  systemctl restart xs5
  echo "管理密码已重置：$p1"
}
update_xs5(){
  echo "正在检查并安装最新版本..."
  bash <(curl -fsSL "$RAW_INSTALL")
}
repair_xs5(){
  echo "将重新安装当前 Latest Release，现有配置会保留。"
  bash <(curl -fsSL "$RAW_INSTALL")
}
uninstall_xs5(){
  read -r -p "确认卸载 Xs5？这会删除所有 S5 配置和管理密码。[y/N] " ans
  [[ "$ans" =~ ^[Yy]$ ]] || { echo "已取消。"; return; }
  if [[ -x /usr/local/lib/xs5/uninstall.sh ]]; then bash /usr/local/lib/xs5/uninstall.sh; else echo "未找到卸载脚本。"; fi
}
menu(){
  while true; do
    clear 2>/dev/null || true
    echo "╭────────────────────────────────╮"
    printf "│          X S5 池 v%-10s │\n" "$(version)"
    echo "╰────────────────────────────────╯"
    echo
    echo "  1. 查看运行状态"
    echo "  2. 查看面板 / S5 连接信息"
    echo "  3. 查看实时日志"
    echo "  4. 重启 Xs5"
    echo "  5. 重置管理密码"
    echo "  6. 配置 / 更换面板域名"
    echo "  7. 移除域名，恢复 IP 访问"
    echo "  8. 更新 Xs5"
    echo "  9. 修复 / 重装"
    echo " 10. 卸载 Xs5"
    echo "  0. 退出"
    echo
    read -r -p "请选择：" c
    case "$c" in
      1) show_status;;
      2) show_info;;
      3) journalctl -u xs5 -f --no-pager;;
      4) systemctl restart xs5 && echo "已重启。";;
      5) reset_password;;
      6) setup_access;;
      7) [[ "$(mode)" == ip ]] && echo "当前已经是 IP 模式。" || remove_domain;;
      8) update_xs5;;
      9) repair_xs5;;
      10) uninstall_xs5; return;;
      0) return;;
      *) echo "无效选择。";;
    esac
    echo
    read -r -p "按 Enter 返回菜单..." _
  done
}

case "${1:-}" in
  --setup-access) setup_access;;
  --domain-http) configure_domain_http "${2:-}";;
  --domain-dns) configure_domain_dns "${2:-}";;
  --remove-domain) remove_domain;;
  --status) show_status;;
  --info) show_info;;
  --reset-password) reset_password;;
  --version) echo "Xs5 v$(version)";;
  "") menu;;
  *) echo "用法: xs5 [--status|--info|--version|--setup-access|--remove-domain]"; exit 2;;
esac
