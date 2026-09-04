from pathlib import Path


def replace_once(text: str, old: str, new: str) -> str:
    if old not in text:
        raise SystemExit("missing expected snippet: " + old[:120])
    return text.replace(old, new, 1)


p = Path("telegram.go")
s = p.read_text()

s = replace_once(
    s,
    '\tPausedPools    map[string]bool `json:"paused_pools,omitempty"`\n}',
    '\tPausedPools     map[string]bool  `json:"paused_pools,omitempty"`\n\tFailureMessages map[string]int64 `json:"failure_messages,omitempty"`\n}',
)

s = replace_once(
    s,
    '\t\tNotifyRecovery: true, NotifyResource: true, NotifyRefresh: true,\n\t\tPausedPools: map[string]bool{},\n',
    '\t\tNotifyRecovery: true, NotifyResource: true, NotifyRefresh: true,\n\t\tPausedPools: map[string]bool{}, FailureMessages: map[string]int64{},\n',
)

s = replace_once(
    s,
    '\tif cfg.PausedPools == nil {\n\t\tcfg.PausedPools = map[string]bool{}\n\t}\n',
    '\tif cfg.PausedPools == nil {\n\t\tcfg.PausedPools = map[string]bool{}\n\t}\n\tif cfg.FailureMessages == nil {\n\t\tcfg.FailureMessages = map[string]int64{}\n\t}\n',
)

s = replace_once(
    s,
    '\tif t.cfg.PausedPools == nil {\n\t\tt.cfg.PausedPools = map[string]bool{}\n\t}\n\tif err := os.MkdirAll',
    '\tif t.cfg.PausedPools == nil {\n\t\tt.cfg.PausedPools = map[string]bool{}\n\t}\n\tif t.cfg.FailureMessages == nil {\n\t\tt.cfg.FailureMessages = map[string]int64{}\n\t}\n\tif err := os.MkdirAll',
)

old_send = '''func (t *TelegramManager) sendTo(token string, chatID int64, text string, markup any) {
\tif chatID == 0 || strings.TrimSpace(token) == "" {
\t\treturn
\t}
\tpayload := map[string]any{"chat_id": chatID, "text": safeTGText(text, 3900), "disable_web_page_preview": true}
\tif markup != nil {
\t\tpayload["reply_markup"] = markup
\t}
\tctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
\tdefer cancel()
\t_ = telegramCall(ctx, token, "sendMessage", payload, nil)
}
'''
new_send = '''func (t *TelegramManager) sendToMessageID(token string, chatID int64, text string, markup any) (int64, error) {
\tif chatID == 0 || strings.TrimSpace(token) == "" {
\t\treturn 0, errors.New("Telegram 未绑定")
\t}
\tpayload := map[string]any{"chat_id": chatID, "text": safeTGText(text, 3900), "disable_web_page_preview": true}
\tif markup != nil {
\t\tpayload["reply_markup"] = markup
\t}
\tctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
\tdefer cancel()
\tvar sent struct {
\t\tMessageID int64 `json:"message_id"`
\t}
\tif err := telegramCall(ctx, token, "sendMessage", payload, &sent); err != nil {
\t\treturn 0, err
\t}
\treturn sent.MessageID, nil
}

func (t *TelegramManager) sendTo(token string, chatID int64, text string, markup any) {
\t_, _ = t.sendToMessageID(token, chatID, text, markup)
}
'''
s = replace_once(s, old_send, new_send)

marker = 'func safeTGText(s string, max int) string {'
helper = '''func failureMessageKey(poolID string) string {
\treturn "switch-fail:" + poolID
}

func (t *TelegramManager) deleteMessage(token string, chatID, messageID int64) bool {
\tif strings.TrimSpace(token) == "" || chatID == 0 || messageID == 0 {
\t\treturn false
\t}
\tctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
\tdefer cancel()
\treturn telegramCall(ctx, token, "deleteMessage", map[string]any{
\t\t"chat_id": chatID, "message_id": messageID,
\t}, nil) == nil
}

// replaceFailureMessage sends the new failure first, then removes the previous
// message for the same pool. This avoids losing the last known failure if a new
// Telegram send itself fails.
func (t *TelegramManager) replaceFailureMessage(poolID, text string) {
\tkey := failureMessageKey(poolID)
\tt.mu.RLock()
\ttoken, chatID := t.cfg.Token, t.cfg.ChatID
\tenabled := t.cfg.Enabled
\tt.mu.RUnlock()
\tif !enabled || token == "" || chatID == 0 {
\t\treturn
\t}
\tnewID, err := t.sendToMessageID(token, chatID, text, nil)
\tif err != nil || newID == 0 {
\t\treturn
\t}

\tt.mu.Lock()
\tif t.cfg.FailureMessages == nil {
\t\tt.cfg.FailureMessages = map[string]int64{}
\t}
\toldID := t.cfg.FailureMessages[key]
\tt.cfg.FailureMessages[key] = newID
\t_ = t.saveLocked()
\tt.mu.Unlock()

\tif oldID != 0 && oldID != newID {
\t\t_ = t.deleteMessage(token, chatID, oldID)
\t}
}

// clearFailureMessage removes the retained failure notification after this pool
// has recovered. If Telegram temporarily refuses deletion, keep the ID so a
// later recovery can retry instead of forgetting the stale message.
func (t *TelegramManager) clearFailureMessage(poolID string) {
\tkey := failureMessageKey(poolID)
\tt.mu.RLock()
\ttoken, chatID := t.cfg.Token, t.cfg.ChatID
\tmessageID := t.cfg.FailureMessages[key]
\tt.mu.RUnlock()
\tif messageID == 0 {
\t\treturn
\t}
\tif !t.deleteMessage(token, chatID, messageID) {
\t\treturn
\t}
\tt.mu.Lock()
\tif t.cfg.FailureMessages[key] == messageID {
\t\tdelete(t.cfg.FailureMessages, key)
\t\t_ = t.saveLocked()
\t}
\tt.mu.Unlock()
}

'''
if marker not in s:
    raise SystemExit("safeTGText marker missing")
s = s.replace(marker, helper + marker, 1)

s = replace_once(
    s,
    '''func (t *TelegramManager) notifySwitchSuccess(p *Pool, before PoolView, phase string) {
\tif t == nil || p == nil || phase == "restoring" {
\t\treturn
\t}
''',
    '''func (t *TelegramManager) notifySwitchSuccess(p *Pool, before PoolView, phase string) {
\tif t == nil || p == nil {
\t\treturn
\t}
\tif phase == "restoring" {
\t\tif p.view().Status == "up" {
\t\t\tt.clearFailureMessage(p.ID)
\t\t}
\t\treturn
\t}
''',
)

s = replace_once(
    s,
    '\tafter := p.view()\n\tif after.Status != "up" {\n\t\treturn\n\t}\n',
    '\tafter := p.view()\n\tif after.Status != "up" {\n\t\treturn\n\t}\n\tt.clearFailureMessage(p.ID)\n',
)

s = replace_once(
    s,
    '\tt.sendBound("❌ "+poolDisplay(v)+" 暂未找到可用出口\\n\\n"+safeTGText(v.Error, 1200), nil)\n}',
    '\tt.replaceFailureMessage(p.ID, "❌ "+poolDisplay(v)+" 暂未找到可用出口\\n\\n"+safeTGText(v.Error, 1200))\n}',
)

s = replace_once(
    s,
    '\tif tokenChanged {\n\t\tt.cfg.UserID = 0\n\t\tt.cfg.ChatID = 0\n\t\tt.cfg.BoundAt = time.Time{}\n\t\tt.bindingUntil = time.Time{}\n\t}\n',
    '\tif tokenChanged {\n\t\tt.cfg.UserID = 0\n\t\tt.cfg.ChatID = 0\n\t\tt.cfg.BoundAt = time.Time{}\n\t\tt.cfg.FailureMessages = map[string]int64{}\n\t\tt.bindingUntil = time.Time{}\n\t}\n',
)

s = replace_once(
    s,
    '\tif formBool(r, "reset") {\n\t\tt.cfg.UserID = 0\n\t\tt.cfg.ChatID = 0\n\t\tt.cfg.BoundAt = time.Time{}\n\t}\n',
    '\tif formBool(r, "reset") {\n\t\tt.cfg.UserID = 0\n\t\tt.cfg.ChatID = 0\n\t\tt.cfg.BoundAt = time.Time{}\n\t\tt.cfg.FailureMessages = map[string]int64{}\n\t}\n',
)

s = replace_once(
    s,
    '\tt.cfg.UserID = 0\n\tt.cfg.ChatID = 0\n\tt.cfg.BoundAt = time.Time{}\n\tt.bindingUntil = time.Time{}\n\terr := t.saveLocked()\n',
    '\tt.cfg.UserID = 0\n\tt.cfg.ChatID = 0\n\tt.cfg.BoundAt = time.Time{}\n\tt.cfg.FailureMessages = map[string]int64{}\n\tt.bindingUntil = time.Time{}\n\terr := t.saveLocked()\n',
)

p.write_text(s)

p = Path("main.go")
s = p.read_text().replace('appVersion       = "v1.3.0"', 'appVersion       = "v1.3.1"')
if 'appVersion       = "v1.3.1"' not in s:
    raise SystemExit("main.go version patch failed")
p.write_text(s)

Path("VERSION").write_text("1.3.1\n")

p = Path("install.sh")
s = p.read_text().replace("VERSION=1.3.0", "VERSION=1.3.1")
if "VERSION=1.3.1" not in s:
    raise SystemExit("install.sh version patch failed")
p.write_text(s)

p = Path("xs5.sh")
s = p.read_text().replace('echo "1.3.0"', 'echo "1.3.1"')
if 'echo "1.3.1"' not in s:
    raise SystemExit("xs5.sh version patch failed")
p.write_text(s)

Path("RELEASE.md").write_text('''# Xs5 v1.3.1

本版本优化 Telegram 重复故障通知：同一个出口连续出现“暂未找到可用出口”时，只保留最新一条，避免自动恢复期间反复刷屏。

- 每个出口独立保存自己的最新故障通知，不会误删其他国家或其他出口的消息。
- 新故障通知发送成功后，自动删除该出口上一条同类故障通知，因此聊天窗口最终只保留最新状态。
- 出口恢复成功后，自动删除该出口保留的“暂未找到可用出口”通知，再按原有规则发送恢复/切换成功通知。
- Telegram Message ID 持久化在现有 `/etc/xs5/telegram.json` 中，服务重启后仍可继续清理此前保留的故障消息。
- 更换 Bot Token、重新绑定或解绑管理员时会清空旧消息 ID，避免跨机器人或跨聊天误删。
- 不改变节点池、健康检查、自动恢复、自学习评分、动态冷却和 VPN Gate / Proxio 切换逻辑。
- 从旧版本更新不会改变已有 S5 端口、用户名、密码、国家、来源选择和 Telegram 绑定配置。

> Xs5 使用第三方公开 VPN / SOCKS5 节点。公开节点可能不稳定、不可信或被目标站封禁，请勿通过不受信任的公共出口传输敏感明文数据。
''')
