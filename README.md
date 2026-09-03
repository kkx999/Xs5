# X S5 池（Xs5）v1.0.1

Xs5 是一个面向 Debian 12 VPS 的公开出口聚合与 SOCKS5 管理工具。它按国家维护一个或多个固定 SOCKS5 地址，后台从 VPN Gate / Proxio 拉取候选节点，执行真实出网检查、健康检查和故障切换；上层面板只需要长期使用固定的 `服务器IP:端口`。

## 主要功能

- 同一国家可创建多个独立 S5 出口；端口、账号、密码彼此独立。
- 固定 S5 配置，后端节点失效时自动切换，不需要修改上层面板。
- VPN Gate / Proxio / 全部来源三种策略。
- 节点延迟 + 出口响应分开展示。
- 当前出口 IP、IP 属性、ISP / ASN 辅助检测。
- Web 管理面板，非浏览器原生风格的下拉、弹窗、Toast 与状态交互。
- `xs5` 管理菜单：状态、连接信息、日志、重启、重置密码、域名、更新、修复和卸载。
- 面板可选择 IP 直连，也可配置域名 HTTPS。
- HTTPS 支持 80 端口 HTTP-01 或 Cloudflare DNS-01（不占用 80）。
- S5 配置始终显示服务器公网 IP，不随面板域名变化。

## v1.0.1：候选续跑与失败冷却

- 单个出口会记住上一轮扫描位置；90 秒未找到可用节点后，再点“立即切换”会从未扫描的位置继续。
- 检测失败的候选进入 5 分钟冷却，冷却期间不会重复浪费时间检测。
- 同国家其他出口正在使用的节点仍会后置，尽量避免多个出口落到同一上游。
- 切换成功后继续保留下一候选位置，后续手动切换不会立即重复当前节点。

## 环境

- Debian 12
- amd64 / arm64
- root
- systemd
- VPN Gate 模式需要 `/dev/net/tun`

## 一键安装（推荐）

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/kkx999/Xs5/main/install-latest.sh)
```

首次安装会先显示风险提示，然后选择面板访问方式：

```text
1. 服务器 IP 直连（http://IP:8898）
2. 域名 + HTTPS（80 端口 HTTP-01）
3. 域名 + HTTPS（Cloudflare DNS-01，不占用 80）
```

安装完成后输入：

```bash
xs5
```

即可进入管理菜单。

## Release 预编译包安装

下载与服务器架构对应的文件：

```text
xs5-v1.0.1-linux-amd64.tar.gz
xs5-v1.0.1-linux-arm64.tar.gz
SHA256SUMS
```

然后：

```bash
tar -xzf xs5-v1.0.1-linux-amd64.tar.gz
cd xs5-v1.0.1-linux-amd64
bash install.sh
```

Release 同时提供 GitHub 自动生成的 `Source code (zip)` 和 `Source code (tar.gz)`。

## 源码安装

下载 GitHub Release 中的 `Source code (tar.gz)`，解压后：

```bash
cd Xs5-1.0.1
bash install.sh
```

如果目录里没有预编译 `xs5d`，安装脚本会自动安装 Go 并从源码编译。

也可以直接：

```bash
git clone https://github.com/kkx999/Xs5.git
cd Xs5
bash install.sh
```

## xs5 管理菜单

```text
1. 查看运行状态
2. 查看面板 / S5 连接信息
3. 查看实时日志
4. 重启 Xs5
5. 重置管理密码
6. 配置 / 更换面板域名
7. 移除域名，恢复 IP 访问
8. 更新 Xs5
9. 修复 / 重装
10. 卸载 Xs5
0. 退出
```

### 重置管理密码

```bash
xs5
```

选择 `5`。可以手工设置，也可以直接留空生成随机密码。重置后服务会重启，旧登录会话失效。

## 域名与 HTTPS

### 方式 A：80 端口 HTTP-01

适合 80/443 可用的服务器。不需要 Cloudflare API Token。

安装或菜单中选择 HTTP-01 后，域名必须直接解析到服务器公网 IPv4。若使用 Cloudflare，建议申请证书时先切为 **DNS Only（灰云）**，证书签发完成后再开启橙云 CDN。

面板：

```text
https://xs5.example.com
```

### 方式 B：Cloudflare DNS-01

不需要开放或占用 80 端口。需要 Cloudflare API Token。

建议 Token 只授予当前 Zone：

```text
Zone / DNS / Edit
Zone / Zone / Read
```

Cloudflare 可以保持橙云。acme.sh 会在 root 专用配置中保存 DNS API 凭据以完成自动续期。

### S5 不走 CDN

不论面板是否使用域名，S5 始终输出服务器公网 IP，例如：

```text
服务器IP:31001
服务器IP:31002
```

普通 Cloudflare CDN 不代理这些 SOCKS5 TCP 端口，因此 Xs5 不会把面板域名写进 S5 配置。

## 升级旧版本

v1.0.1 会自动尝试迁移：

```text
/var/lib/x-s5-pool
/var/lib/country-s5-pool
```

已有管理密码、`pools.json`、S5 端口、用户名和密码会尽量保留。正式版数据目录为：

```text
/var/lib/xs5
```

旧目录不会自动删除，方便人工回退和核对。

## 数据源

### VPN Gate

```text
固定 S5 -> 独立 netns -> OpenVPN -> VPN Gate -> 目标网站
```

### Proxio

```text
固定 S5 -> 公共 SOCKS5 -> 目标网站
```

Xs5 会再次执行实际出网验证和健康检查，但公开节点本身仍然不受本项目控制。

## 风险与免责声明

**Xs5 是公开 VPN / SOCKS5 节点的聚合、检测和管理工具。项目本身不提供、运营或控制任何第三方代理节点，也不保证节点的可用性、稳定性、安全性、带宽、匿名性或 IP 类型。**

VPN Gate、Proxio 等第三方来源中的节点可能随时失效、限速、被封禁或改变出口 IP。公开代理节点的运营者理论上可能观察、记录或篡改未经端到端加密的网络流量，因此不要通过不受信任的公共出口传输密码、支付信息、私密文件等敏感明文数据。

面板显示的“住宅/ISP、机房、移动网络、教育/机构、ISP、ASN”等属性来自第三方 IP 情报或启发式判断，可能存在误判，不应作为商业、风控、合规或身份认证依据。**Xs5 不承诺节点是真实家庭宽带，也不提供“住宅 IP”保证。**

使用者应自行确认使用方式符合所在国家/地区法律、第三方服务条款及网络服务商政策。因使用本项目或第三方节点产生的账号封禁、数据泄露、网络中断、经济损失或其他责任，由使用者自行承担。

Xs5 以 root 权限运行，并会创建网络命名空间、路由、iptables 规则及 OpenVPN 进程。建议只部署在独立 VPS，并妥善保护管理密码和 SOCKS5 凭据。

## 卸载

推荐：

```bash
xs5
```

选择 `10. 卸载 Xs5`。

也可以在源码/解压目录执行：

```bash
bash uninstall.sh
```

卸载 Xs5 不会擅自删除系统中可能被其他网站共用的 nginx 或 acme.sh。
