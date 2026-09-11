from pathlib import Path


def replace_once(path, old, new):
    p = Path(path)
    text = p.read_text()
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"{path}: expected exactly one match, got {count}: {old!r}")
    p.write_text(text.replace(old, new, 1))


# Telegram display helpers and current-status IP attributes.
p = Path("telegram.go")
text = p.read_text()
marker = "func (t *TelegramManager) statusText() string {"
if text.count(marker) != 1:
    raise SystemExit("telegram.go: statusText marker mismatch")
if "func telegramIPType(v PoolView) string {" in text:
    raise SystemExit("telegram.go: IP display helpers already present")
helpers = '''func telegramIPType(v PoolView) string {
    if s := strings.TrimSpace(v.IPType); s != "" {
        return s
    }
    if strings.TrimSpace(v.ExitIP) != "" {
        return "暂未识别"
    }
    return "-"
}

func telegramExitLabel(v PoolView) string {
    ip := strings.TrimSpace(v.ExitIP)
    if ip == "" {
        return "-"
    }
    return fmt.Sprintf("%s（%s）", ip, telegramIPType(v))
}

'''
text = text.replace(marker, helpers + marker, 1)

status_start = text.index("func (t *TelegramManager) statusText() string {")
status_end = text.index("func (t *TelegramManager) recoveryText() string {", status_start)
status = text[status_start:status_end]
old_status = (
    '\t\tif v.ExitIP != "" {\n'
    '\t\t\tfmt.Fprintf(&b, "出口：%s\\n", v.ExitIP)\n'
    '\t\t} else {\n'
    '\t\t\tb.WriteString("出口：-\\n")\n'
    '\t\t}\n'
)
new_status = (
    '\t\tif v.ExitIP != "" {\n'
    '\t\t\tfmt.Fprintf(&b, "出口：%s\\n", v.ExitIP)\n'
    '\t\t\tfmt.Fprintf(&b, "属性：%s\\n", telegramIPType(v))\n'
    '\t\t\tif strings.TrimSpace(v.IPISP) != "" {\n'
    '\t\t\t\tfmt.Fprintf(&b, "ISP：%s\\n", strings.TrimSpace(v.IPISP))\n'
    '\t\t\t}\n'
    '\t\t} else {\n'
    '\t\t\tb.WriteString("出口：-\\n")\n'
    '\t\t}\n'
)
if status.count(old_status) != 1:
    raise SystemExit(f"telegram.go: status IP block match count={status.count(old_status)}")
status = status.replace(old_status, new_status, 1)
text = text[:status_start] + status + text[status_end:]

# Switch/recovery success: wait briefly for the existing async IP profile,
# then show old/new IP type and the new ISP without triggering extra lookups.
notify_start = text.index("func (t *TelegramManager) notifySwitchSuccess")
notify_end = text.index("func (t *TelegramManager) notifySwitchFailure", notify_start)
notify = text[notify_start:notify_end]
wait_marker = '\t\t\tafter = p.view()\n\t\t}\n\t}\n\tt.mu.RLock()\n'
wait_block = (
    '\t\t\tafter = p.view()\n'
    '\t\t}\n'
    '\t}\n'
    '\tif after.ExitIP != "" && after.IPType == "" {\n'
    '\t\tdeadline := time.Now().Add(3 * time.Second)\n'
    '\t\tfor after.Status == "up" && after.ExitIP != "" && after.IPType == "" && time.Now().Before(deadline) {\n'
    '\t\t\ttime.Sleep(250 * time.Millisecond)\n'
    '\t\t\tnext := p.view()\n'
    '\t\t\tif next.ExitIP != after.ExitIP {\n'
    '\t\t\t\tafter = next\n'
    '\t\t\t\tbreak\n'
    '\t\t\t}\n'
    '\t\t\tafter = next\n'
    '\t\t}\n'
    '\t}\n'
    '\tt.mu.RLock()\n'
)
if notify.count(wait_marker) != 1:
    raise SystemExit(f"telegram.go: notify wait marker count={notify.count(wait_marker)}")
notify = notify.replace(wait_marker, wait_block, 1)

old_msg = (
    '\toldIP := before.ExitIP\n'
    '\tif oldIP == "" {\n'
    '\t\toldIP = "-"\n'
    '\t}\n'
    '\tnewIP := after.ExitIP\n'
    '\tif newIP == "" {\n'
    '\t\tnewIP = "获取中"\n'
    '\t}\n'
    '\tmsg := fmt.Sprintf("%s\\n\\n来源：%s\\n旧出口：%s\\n新出口：%s", title, sourceLabel(after.ActiveSource), oldIP, newIP)\n'
)
new_msg = (
    '\toldIP := telegramExitLabel(before)\n'
    '\tnewIP := telegramExitLabel(after)\n'
    '\tif after.ExitIP == "" {\n'
    '\t\tnewIP = "获取中"\n'
    '\t}\n'
    '\tmsg := fmt.Sprintf("%s\\n\\n来源：%s\\n旧出口：%s\\n新出口：%s", title, sourceLabel(after.ActiveSource), oldIP, newIP)\n'
    '\tif strings.TrimSpace(after.IPISP) != "" {\n'
    '\t\tmsg += "\\nISP：" + strings.TrimSpace(after.IPISP)\n'
    '\t}\n'
)
if notify.count(old_msg) != 1:
    raise SystemExit(f"telegram.go: notify message block count={notify.count(old_msg)}")
notify = notify.replace(old_msg, new_msg, 1)
text = text[:notify_start] + notify + text[notify_end:]
p.write_text(text)

# Telegram formatting regression tests.
p = Path("telegram_test.go")
tests = p.read_text()
if "func TestTelegramExitLabelIncludesIPType" in tests:
    raise SystemExit("telegram_test.go: v1.3.2 tests already present")
tests += '''
func TestTelegramExitLabelIncludesIPType(t *testing.T) {
    v := PoolView{ExitIP: "203.0.113.8", IPType: "住宅/ISP"}
    if got := telegramExitLabel(v); got != "203.0.113.8（住宅/ISP）" {
        t.Fatalf("telegramExitLabel=%q", got)
    }
    v.IPType = ""
    if got := telegramExitLabel(v); got != "203.0.113.8（暂未识别）" {
        t.Fatalf("telegramExitLabel pending=%q", got)
    }
    v.ExitIP = ""
    if got := telegramExitLabel(v); got != "-" {
        t.Fatalf("telegramExitLabel empty=%q", got)
    }
}
'''
p.write_text(tests)

replace_once("main.go", 'appVersion       = "v1.3.1"', 'appVersion       = "v1.3.2"')
replace_once("install.sh", "VERSION=1.3.1", "VERSION=1.3.2")
replace_once("xs5.sh", 'echo "1.3.1"', 'echo "1.3.2"')
Path("VERSION").write_text("1.3.2\n")
Path("RELEASE.md").write_text('''# Xs5 v1.3.2

本版本增强 Telegram 出口通知中的 IP 属性展示，让切换后和当前运行中的出口信息更直观。

- 切换成功与故障恢复成功通知会显示旧出口和新出口的 IP 属性，例如“住宅/ISP”“机房 IP”“移动网络”等。
- 新出口已有 ISP 信息时，会在通知中同步显示 ISP。
- `/status`、启动通知和每日摘要会显示当前出口的 IP 属性与 ISP；启动通知和每日摘要继续复用现有状态汇总逻辑。
- 切换成功后最多额外等待 3 秒获取现有异步 IP 属性结果；不会新增高频 IP 情报查询，也不会阻塞核心切换流程。
- 属性暂时尚未返回时显示“暂未识别”，后续 `/status` 可看到已经完成的识别结果。
- 不改变节点池、健康检查、自动恢复、自学习评分、动态冷却、VPN Gate / Proxio / ProxyScrape 切换逻辑。
- 从旧版本更新不会改变已有 S5 端口、用户名、密码、国家、来源选择和 Telegram 绑定配置。

> IP 属性来自第三方 IP 情报与启发式判断，仅供辅助参考，可能存在误判。
''')
