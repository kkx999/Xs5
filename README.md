# X S5 池（Xs5）

Xs5 是一个面向 Debian 12 VPS 的公开出口聚合与 SOCKS5 管理工具。它按国家维护一个或多个固定 SOCKS5 地址，后台从 VPN Gate / Proxio 获取候选节点，执行真实出网验证、健康检查和故障切换；上层程序长期使用固定的 `服务器IP:端口`，后端出口发生变化时不需要重新修改 S5 配置。

## Xs5 能做什么

- 按国家创建固定 SOCKS5 出口。
- 同一国家可创建多个独立出口，端口、用户名和密码彼此独立。
- 后端公共节点失效时自动检测并切换，上层 S5 地址保持不变。
- 支持 VPN Gate、Proxio 和全部来源三种候选策略。
- 显示当前出口 IP、节点延迟、出口响应、IP 属性、ISP / ASN 等辅助信息。
- 支持失败候选冷却、候选扫描续跑和无人值守故障恢复。
- Web 面板可创建、删除、切换出口并调整节点来源。
- 支持 Telegram 通知与远程控制：自动绑定管理员、状态查询、手动切换、健康检测、节点池刷新、暂停/恢复和日志查看。
- 提供 `xs5` 管理菜单，用于状态、日志、密码、域名、更新、修复和卸载。
- 面板支持服务器 IP 直连，也支持域名 HTTPS。
- S5 始终使用服务器公网 IP，不依赖面板域名或 CDN。

## 工作方式

### VPN Gate

```text
固定 S5 -> 独立 netns -> OpenVPN -> VPN Gate -> 目标网站
```

### Proxio

```text
固定 S5 -> 公共 SOCKS5 -> 目标网站
```

Xs5 会从当前服务器实际测试候选节点是否能够正常出网。已经启用的出口也会持续进行健康检查；只有确认出口无法正常访问公网后才进入自动切换流程。

## 环境要求

- Debian 12
- amd64 / arm64
- root 权限
- systemd
- VPN Gate 模式需要 `/dev/net/tun`

默认端口：

```text
Web 面板：8898
SOCKS5：31001-31250
```

VPN Gate 模式会使用 Linux network namespace、veth、iptables 和 OpenVPN。

## 一键安装

推荐在全新或已确认端口无冲突的 Debian 12 服务器上执行：

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/kkx999/Xs5/main/install-latest.sh)
```

首次安装会显示风险提示，并选择面板访问方式：

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

## Release 安装包

Release 会提供：

```text
xs5-v<版本>-linux-amd64.tar.gz
xs5-v<版本>-linux-arm64.tar.gz
SHA256SUMS
```

下载与服务器架构对应的压缩包后解压并执行：

```bash
bash install.sh
```

GitHub Release 同时提供自动生成的 `Source code (zip)` 和 `Source code (tar.gz)`。源码目录中执行 `bash install.sh` 时，如果没有预编译 `xs5d`，安装脚本会自动安装 Go 并从源码编译。

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

运行：

```bash
xs5
```

选择 `5`。可以手工设置，也可以留空生成随机密码。

### 更新

运行：

```bash
xs5
```

选择 `8`，程序会安装最新 Release，并尽量保留现有 S5 端口、用户名、密码和出口配置。

## 域名与 HTTPS

域名只用于 Web 管理面板，不用于 SOCKS5 连接。

### 方式 A：HTTP-01

适合服务器的 80/443 端口可用的情况。域名需要直接解析到服务器公网 IPv4。

如果使用 Cloudflare，可在证书签发阶段使用 DNS Only（灰云），签发完成后再根据需要开启橙云。

### 方式 B：Cloudflare DNS-01

不需要开放或占用 80 端口，需要 Cloudflare API Token。

建议 Token 仅授权当前 Zone：

```text
Zone / DNS / Edit
Zone / Zone / Read
```

Cloudflare 可以保持橙云。DNS API 凭据由 acme.sh 保存，用于后续证书续期。

### SOCKS5 不走 CDN

不论面板是否配置域名，S5 始终使用服务器公网 IP，例如：

```text
服务器IP:31001
服务器IP:31002
```

普通 Cloudflare CDN 不代理这些 SOCKS5 TCP 端口。

## 数据与配置

主要目录：

```text
/etc/xs5
/var/lib/xs5
/usr/local/lib/xs5
```

Xs5 会保存固定 S5 的端口、用户名和密码等配置。更新时安装程序会尽量保留已有配置。

## 公共节点说明

VPN Gate 和 Proxio 中的节点均属于第三方公开节点，并非由 Xs5 运营。Xs5 的作用是获取候选、进行实际出网验证、维护固定 S5 地址，并在出口故障后自动寻找新的可用节点。

Xs5 对公开出口的筛选和健康检查不代表节点本身可信，也不代表出口一定属于住宅网络。

## 风险与免责声明

**Xs5 是公开 VPN / SOCKS5 节点的聚合、检测和管理工具。项目本身不提供、运营或控制任何第三方代理节点，也不保证节点的可用性、稳定性、安全性、带宽、匿名性或 IP 类型。**

第三方公开节点可能随时失效、限速、被封禁或改变出口 IP。公开代理节点的运营者理论上可能观察、记录或篡改未经端到端加密的网络流量，因此不要通过不受信任的公共出口传输密码、支付信息、私密文件等敏感明文数据。

面板显示的“住宅/ISP、机房、移动网络、教育/机构、ISP、ASN”等属性来自第三方 IP 情报或启发式判断，可能存在误判，不应作为商业、风控、合规或身份认证依据。**Xs5 不承诺节点是真实家庭宽带，也不提供“住宅 IP”保证。**

使用者应自行确认使用方式符合所在国家/地区法律、第三方服务条款及网络服务商政策。因使用本项目或第三方节点产生的账号封禁、数据泄露、网络中断、经济损失或其他责任，由使用者自行承担。

Xs5 以 root 权限运行，并会创建网络命名空间、路由、iptables 规则及 OpenVPN 进程。与其他项目同机部署时，请确认 `8898`、`31001-31250`、80/443 及相关网络段没有冲突。

## 卸载

推荐运行：

```bash
xs5
```

选择 `10. 卸载 Xs5`。

也可以在源码或解压目录执行：

```bash
bash uninstall.sh
```

卸载 Xs5 不会擅自删除系统中可能被其他网站共用的 nginx 或 acme.sh。
