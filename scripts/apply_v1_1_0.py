from pathlib import Path


def must_replace(path, old, new, count=1):
    p = Path(path)
    s = p.read_text()
    if old not in s:
        raise SystemExit(f"missing marker in {path}: {old[:120]!r}")
    s2 = s.replace(old, new, count)
    p.write_text(s2)


# main.go: wire Telegram into app lifecycle, web API, notifications and release version.
must_replace("main.go", 'appVersion       = "v1.0.5"', 'appVersion       = "v1.1.0"')
must_replace(
    "main.go",
    '\tsessions map[string]time.Time\n}',
    '\tsessions map[string]time.Time\n\ttelegram *TelegramManager\n}',
)
must_replace(
    "main.go",
    '\tif err := app.loadPools(); err != nil {\n\t\tlog.Printf("load pools: %v", err)\n\t}\n\t// 节点源属于外部网络依赖',
    '\tif err := app.loadPools(); err != nil {\n\t\tlog.Printf("load pools: %v", err)\n\t}\n\tapp.telegram = newTelegramManager(app)\n\tapp.telegram.start()\n\t// 节点源属于外部网络依赖',
)
must_replace(
    "main.go",
    '\tmux.HandleFunc("/api/refresh", app.auth(app.refreshNow))\n\tmux.HandleFunc("/", app.auth(app.index))',
    '\tmux.HandleFunc("/api/refresh", app.auth(app.refreshNow))\n'
    '\tmux.HandleFunc("/telegram", app.auth(app.telegramPage))\n'
    '\tmux.HandleFunc("/api/telegram/status", app.auth(app.telegramStatus))\n'
    '\tmux.HandleFunc("/api/telegram/save", app.auth(app.telegramSave))\n'
    '\tmux.HandleFunc("/api/telegram/bind", app.auth(app.telegramBind))\n'
    '\tmux.HandleFunc("/api/telegram/test", app.auth(app.telegramTest))\n'
    '\tmux.HandleFunc("/api/telegram/unbind", app.auth(app.telegramUnbind))\n'
    '\tmux.HandleFunc("/", app.auth(app.index))',
)
must_replace(
    "main.go",
    'func (a *App) shutdown() {\n\ta.mu.RLock()',
    'func (a *App) shutdown() {\n\tif a.telegram != nil {\n\t\ta.telegram.stop()\n\t}\n\ta.mu.RLock()',
)
must_replace(
    "main.go",
    '\tif err := a.refreshSource(selected); err != nil {\n\t\twriteJSON(w, 502, map[string]string{"error": err.Error()})',
    '\tif err := a.refreshSource(selected); err != nil {\n\t\tif a.telegram != nil {\n\t\t\tgo a.telegram.notifyRefreshFailure(selected, err)\n\t\t}\n\t\twriteJSON(w, 502, map[string]string{"error": err.Error()})',
)
must_replace(
    "main.go",
    '\t\tif err := a.refreshSource(sourceAll); err != nil {\n\t\t\tlog.Printf("refresh: %v", err)\n\t\t}',
    '\t\tif err := a.refreshSource(sourceAll); err != nil {\n\t\t\tlog.Printf("refresh: %v", err)\n\t\t\tif a.telegram != nil {\n\t\t\t\tgo a.telegram.notifyRefreshFailure(sourceAll, err)\n\t\t\t}\n\t\t}',
)
must_replace(
    "main.go",
    'func (a *App) switchNext(p *Pool, phase string) {\n\tcancelAutoRetry(p.ID)\n\tp.opMu.Lock()\n\tdefer p.opMu.Unlock()\n\n\tp.mu.Lock()',
    'func (a *App) switchNext(p *Pool, phase string) {\n\tcancelAutoRetry(p.ID)\n\tp.opMu.Lock()\n\tdefer p.opMu.Unlock()\n\n\tbefore := p.view()\n\tp.mu.Lock()',
)
must_replace(
    "main.go",
    '\t\tif err == nil {\n\t\t\tstate.recordSuccess(i, len(cands), nodeKey(node))\n\t\t\treturn\n\t\t}',
    '\t\tif err == nil {\n\t\t\tstate.recordSuccess(i, len(cands), nodeKey(node))\n\t\t\tif a.telegram != nil {\n\t\t\t\tgo a.telegram.notifySwitchSuccess(p, before, phase)\n\t\t\t}\n\t\t\treturn\n\t\t}',
)
# User-Agent literal used by Proxio source fetch.
must_replace("main.go", 'req.Header.Set("User-Agent", "Xs5/v1.0.5")', 'req.Header.Set("User-Agent", "Xs5/v1.1.0")')

# Health: paused pools keep current runtime and skip automatic health failover; notify real auto-switch.
must_replace(
    "health.go",
    '\tid, ok := currentRuntimeIdentity(p)\n\tif !ok {\n\t\treturn\n\t}\n\n\tdelays :=',
    '\tid, ok := currentRuntimeIdentity(p)\n\tif !ok {\n\t\treturn\n\t}\n\tif a.telegram != nil && a.telegram.isPoolPaused(p.ID) {\n\t\treturn\n\t}\n\n\tdelays :=',
)
must_replace(
    "health.go",
    '\t// 不再由健康检查提前杀掉 runtime；切换器负责在合适的时机接管。\n\ta.switchNext(p, "switching")',
    '\t// 不再由健康检查提前杀掉 runtime；切换器负责在合适的时机接管。\n\tif a.telegram != nil {\n\t\tgo a.telegram.notifyAutoSwitchStart(p, lastErr)\n\t}\n\ta.switchNext(p, "switching")',
)

# Recovery: paused pools never resume automatically; all failures/resource pressure can notify once with cooldown.
must_replace(
    "recovery.go",
    '\t\tp.mu.Lock()\n\t\tstatus := p.Status\n\t\tp.mu.Unlock()\n\t\tif status == "up" || status == "starting" || status == "restoring" || status == "switching" {',
    '\t\tif a.telegram != nil && a.telegram.isPoolPaused(p.ID) {\n\t\t\treturn\n\t\t}\n\t\tp.mu.Lock()\n\t\tstatus := p.Status\n\t\tp.mu.Unlock()\n\t\tif status == "up" || status == "starting" || status == "restoring" || status == "switching" {',
)
must_replace(
    "recovery.go",
    '\tp.mu.Unlock()\n}\n\n// armResourceRecovery',
    '\tp.mu.Unlock()\n\tif a.telegram != nil {\n\t\tgo a.telegram.notifySwitchFailure(p)\n\t}\n}\n\n// armResourceRecovery',
)
must_replace(
    "recovery.go",
    '\tp.mu.Unlock()\n}\n\nfunc cancelAutoRetry',
    '\tp.mu.Unlock()\n\tif a.telegram != nil {\n\t\tgo a.telegram.notifyResource(p, cause)\n\t}\n}\n\nfunc cancelAutoRetry',
)

# Telegram manager refinements.
must_replace(
    "telegram.go",
    '\tbotUsername  string\n\tlastDaily    string',
    '\tbotUsername  string\n\tlastDaily    string\n\tstartedAt    time.Time',
)
must_replace(
    "telegram.go",
    't := &TelegramManager{app: app, cfg: defaultTelegramConfig(), lastSend: map[string]time.Time{}}',
    't := &TelegramManager{app: app, cfg: defaultTelegramConfig(), lastSend: map[string]time.Time{}, startedAt: time.Now()}',
)
must_replace(
    "telegram.go",
    '\tgo t.pollLoop(ctx, token)\n\tgo t.registerCommands(token)',
    '\tgo t.pollLoop(ctx, token)\n\tgo func() {\n\t\tinfo, err := t.validateToken(token)\n\t\tif err == nil {\n\t\t\tt.mu.Lock()\n\t\t\tt.botUsername = info.Username\n\t\t\tt.mu.Unlock()\n\t\t}\n\t}()\n\tgo t.registerCommands(token)',
)
must_replace(
    "telegram.go",
    '\tctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)\n\tdefer cancel()\n\t_ = telegramCall(ctx, token, "setMyCommands", map[string]any{"commands": commands}, nil)',
    '\tctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)\n\tdefer cancel()\n\t_ = telegramCall(ctx, token, "deleteWebhook", map[string]any{"drop_pending_updates": false}, nil)\n\t_ = telegramCall(ctx, token, "setMyCommands", map[string]any{"commands": commands}, nil)',
)
must_replace(
    "telegram.go",
    '\t\tfmt.Fprintf(&b, "来源：%s\\n", sourceLabel(v.ActiveSource))',
    '\t\tif v.ActiveSource == "" {\n\t\t\tb.WriteString("来源：-\\n")\n\t\t} else {\n\t\t\tfmt.Fprintf(&b, "来源：%s\\n", sourceLabel(v.ActiveSource))\n\t\t}',
)
must_replace(
    "telegram.go",
    '\tisRecovery := before.Status != "up"',
    '\tisRecovery := before.Status == "failed" || before.Status == "no-candidates" || before.Status == "restoring"',
)
must_replace(
    "telegram.go",
    '\tif !on || !t.allowNotify("switch-fail:"+p.ID, 2*time.Minute) {',
    '\tif time.Since(t.startedAt) < 20*time.Second {\n\t\treturn\n\t}\n\tif !on || !t.allowNotify("switch-fail:"+p.ID, 2*time.Minute) {',
)

# Add Telegram entry to panel header without changing the existing layout model.
must_replace("web.go", '.stat{height:34px;', '.stat{text-decoration:none;height:34px;')
must_replace(
    "web.go",
    '<div class="stats"><div class="stat">出口 <b id="statPools">-</b></div><div class="stat">正常 <b id="statUp">-</b></div><div class="stat">候选节点 <b id="statNodes">-</b></div></div>',
    '<div class="stats"><div class="stat">出口 <b id="statPools">-</b></div><div class="stat">正常 <b id="statUp">-</b></div><div class="stat">候选节点 <b id="statNodes">-</b></div><a class="stat" href="/telegram">✈ Telegram</a></div>',
)
must_replace(
    "web.go",
    '<p>固定 S5 · 国家节点池 · 健康检查 · 自动切换</p>',
    '<p>固定 S5 · 国家节点池 · 健康检查 · 自动切换 · Telegram</p>',
)

# Product README stays timeless: only describe the capability, not version history.
must_replace(
    "README.md",
    '- Web 面板可创建、删除、切换出口并调整节点来源。\n',
    '- Web 面板可创建、删除、切换出口并调整节点来源。\n- 支持 Telegram 通知与远程控制：自动绑定管理员、状态查询、手动切换、健康检测、节点池刷新、暂停/恢复和日志查看。\n',
)

# Version consistency.
Path("VERSION").write_text("1.1.0\n")
for name in ["install.sh", "xs5.sh"]:
    p = Path(name)
    s = p.read_text().replace("1.0.5", "1.1.0")
    p.write_text(s)

# Release Notes only; README remains product documentation.
Path("RELEASE.md").write_text("""# Xs5 v1.1.0

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
""")
