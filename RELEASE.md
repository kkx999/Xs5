# Xs5 v1.1.0

本版本新增 Telegram 通知与远程控制，并直接复用 Xs5 现有的健康检查、切换、恢复和节点池逻辑；VPN Gate 与 Proxio 使用同一套 Telegram 状态与控制入口。

- 面板新增 Telegram 设置页，只需填写 BotFather 创建的 Bot Token。
- Xs5 自动验证 Bot Token、自动注册机器人命令菜单，无需手工执行 BotFather `/setcommands`。
- 点击“开始绑定”后，管理员只需向机器人发送 `/start`；Xs5 自动取得 Telegram User ID 与 Chat ID，并锁定为唯一管理员。
- Bot Token 保存于 `/etc/xs5/telegram.json`，文件权限为 600；Web API 不返回完整 Token，只显示脱敏值。
- 机器人使用 long polling，不需要开放额外公网端口，也不需要配置 webhook；启动时会主动清理旧 webhook。
- 内置 `/status`、`/switch`、`/check`、`/refresh`、`/recovery`、`/pause`、`/resume`、`/logs`、`/help` 命令，并提供 Telegram Inline Keyboard 按钮操作。
- `/switch` 直接调用 Xs5 现有切换器，固定 S5 地址、端口、用户名和密码保持不变。
- `/check` 从固定 S5 完整链路执行一次普通 HTTPS 健康检测，只检测，不触发自动切换。
- `/pause` 只暂停指定出口的自动健康切换，不主动停止当前线路；`/resume` 恢复自动切换。
- 通知覆盖：服务启动摘要、连续健康检查失败并开始切换、切换/恢复成功、切换失败或候选耗尽、服务器资源异常、节点池刷新失败。
- 通知带防刷屏冷却；常规 30 秒健康检查正常时不会反复发送 Telegram 消息。
- 可选每日运行摘要，默认关闭，每天约 09:00 按服务器本地时间发送。
- Telegram 远程操作使用 User ID + Chat ID 双重校验；未绑定用户即使找到机器人也不能控制 Xs5。
- 保留 v1.0.5 的资源保护、VPN Gate 全局串行切换、子进程回收、两个源统一本机资源错误策略。
- 从旧版本更新不会改变已有 S5 端口、用户名、密码、国家出口和节点来源配置。

> Xs5 使用第三方公开 VPN / SOCKS5 节点。公开节点可能不稳定、不可信或被目标站封禁，请勿通过不受信任的公共出口传输敏感明文数据。
