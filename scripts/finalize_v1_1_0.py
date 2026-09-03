from pathlib import Path


def replace(path, old, new, count=1):
    p = Path(path)
    s = p.read_text()
    if old not in s:
        raise SystemExit(f"missing marker in {path}: {old[:140]!r}")
    p.write_text(s.replace(old, new, count))


# Saving Telegram settings must restart polling without pretending the whole Xs5 service restarted.
replace(
    "telegram.go",
    "func (t *TelegramManager) start() {\n\tt.restartPolling()\n}",
    "func (t *TelegramManager) start() {\n\tt.restartPolling(true)\n}",
)
replace(
    "telegram.go",
    "func (t *TelegramManager) restartPolling() {",
    "func (t *TelegramManager) restartPolling(sendStartup bool) {",
)
replace(
    "telegram.go",
    "\tgo t.registerCommands(token)\n\tgo t.delayedStartupSummary(ctx)\n\tgo t.dailySummaryLoop(ctx)",
    "\tgo t.registerCommands(token)\n\tif sendStartup {\n\t\tgo t.delayedStartupSummary(ctx)\n\t}\n\tgo t.dailySummaryLoop(ctx)",
)
replace("telegram.go", "t.restartPolling()\n\twriteJSON", "t.restartPolling(false)\n\twriteJSON")

# Daily summary should be close to 09:00 instead of potentially half an hour late.
replace("telegram.go", "time.NewTicker(30 * time.Minute)", "time.NewTicker(5 * time.Minute)")

# Binding must preserve the user's proactive-notification toggle.
replace("telegram.go", "\tt.cfg.Enabled = true\n\tt.bindingUntil", "\tt.bindingUntil")

# Prevent duplicate Telegram taps from queuing repeated switch operations.
replace(
    "telegram.go",
    "\t\t\tv := p.view()\n\t\t\tt.sendTo(token, chatID, \"🔄 正在切换 \"+poolDisplay(v)+\"…\\n固定 S5 地址不会改变。\", nil)\n\t\t\tp.mu.Lock()",
    "\t\t\tv := p.view()\n\t\t\tif v.Status == \"starting\" || v.Status == \"switching\" || v.Status == \"restoring\" {\n\t\t\t\tt.sendTo(token, chatID, \"⏳ \"+poolDisplay(v)+\" 当前正在\"+v.Status+\"，请等待本轮完成后再操作。\", nil)\n\t\t\t\treturn\n\t\t\t}\n\t\t\tt.sendTo(token, chatID, \"🔄 正在切换 \"+poolDisplay(v)+\"…\\n固定 S5 地址不会改变。\", nil)\n\t\t\tp.mu.Lock()",
)

# A successful switch notification should make a best effort to include the actual new exit IP.
replace(
    "telegram.go",
    "\tafter := p.view()\n\tif after.Status != \"up\" {\n\t\treturn\n\t}\n\tt.mu.RLock()",
    "\tafter := p.view()\n\tif after.Status != \"up\" {\n\t\treturn\n\t}\n\tif after.ExitIP == \"\" {\n\t\tif ip, err := detectPoolExitIP(p); err == nil {\n\t\t\tchanged := false\n\t\t\tp.mu.Lock()\n\t\t\tif p.Status == \"up\" {\n\t\t\t\tchanged = p.ExitIP != ip\n\t\t\t\tp.ExitIP = ip\n\t\t\t\tif changed {\n\t\t\t\t\tp.IPType = \"\"\n\t\t\t\t\tp.IPISP = \"\"\n\t\t\t\t\tp.IPASN = \"\"\n\t\t\t\t\tp.IPRisk = \"\"\n\t\t\t\t}\n\t\t\t}\n\t\t\tp.mu.Unlock()\n\t\t\tif changed {\n\t\t\t\tgo enrichPoolIPProfile(p, ip)\n\t\t\t}\n\t\t\tafter = p.view()\n\t\t}\n\t}\n\tt.mu.RLock()",
)

# Do not send a failure alert when the old runtime was kept healthy (e.g. all alternatives cooling).
replace(
    "telegram.go",
    "\tif time.Since(t.startedAt) < 20*time.Second {\n\t\treturn\n\t}\n\tif !on || !t.allowNotify(\"switch-fail:\"+p.ID, 2*time.Minute) {",
    "\tif time.Since(t.startedAt) < 20*time.Second {\n\t\treturn\n\t}\n\tv := p.view()\n\tif v.Status == \"up\" {\n\t\treturn\n\t}\n\tif !on || !t.allowNotify(\"switch-fail:\"+p.ID, 2*time.Minute) {",
)
replace(
    "telegram.go",
    "\t}\n\tv := p.view()\n\tt.sendBound(\"❌ \"+poolDisplay(v)+\" 暂未找到可用出口\\n\\n\"+safeTGText(v.Error, 1200), nil)\n}\n\nfunc (t *TelegramManager) notifyResource",
    "\t}\n\tt.sendBound(\"❌ \"+poolDisplay(v)+\" 暂未找到可用出口\\n\\n\"+safeTGText(v.Error, 1200), nil)\n}\n\nfunc (t *TelegramManager) notifyResource",
)

# Removing a pool should also remove any persisted Telegram pause marker for that pool.
replace(
    "main.go",
    "\tdropCandidateScanState(id)\n\tdropAutoRetry(id)\n\twriteJSON(w, 200, map[string]string{\"ok\": \"deleted\"})",
    "\tdropCandidateScanState(id)\n\tdropAutoRetry(id)\n\tif a.telegram != nil && a.telegram.isPoolPaused(id) {\n\t\ta.telegram.setPoolPaused(id, false)\n\t}\n\twriteJSON(w, 200, map[string]string{\"ok\": \"deleted\"})",
)
