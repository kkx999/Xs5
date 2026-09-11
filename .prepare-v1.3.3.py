from pathlib import Path


def replace_once(path, old, new):
    p = Path(path)
    text = p.read_text()
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"{path}: expected exactly one match, got {count}: {old!r}")
    p.write_text(text.replace(old, new, 1))


# main.go
p = Path("main.go")
text = p.read_text()
if text.count('appVersion       = "v1.3.2"') != 1:
    raise SystemExit("main.go: version marker mismatch")
text = text.replace('appVersion       = "v1.3.2"', 'appVersion       = "v1.3.3"', 1)

old_fields = '''\tLatencyMS      int       `json:"latency_ms"`
\tNodeLatencyMS  int       `json:"node_latency_ms"`
\tIPType         string    `json:"ip_type,omitempty"`
'''
new_fields = '''\tLatencyMS       int       `json:"latency_ms"`
\tNodeLatencyMS   int       `json:"node_latency_ms"`
\tSpeedMbps       float64   `json:"speed_mbps,omitempty"`
\tSpeedMBps       float64   `json:"speed_mb_per_sec,omitempty"`
\tSpeedBytes      int64     `json:"speed_bytes,omitempty"`
\tSpeedDurationMS int64     `json:"speed_duration_ms,omitempty"`
\tSpeedTestedAt   time.Time `json:"speed_tested_at,omitempty"`
\tIPType          string    `json:"ip_type,omitempty"`
'''
if text.count(old_fields) != 1:
    raise SystemExit("main.go: PoolView fields marker mismatch")
text = text.replace(old_fields, new_fields, 1)

old_view = '''func (p *Pool) view() PoolView {
\tp.mu.Lock()
\tdefer p.mu.Unlock()
\treturn PoolView{
\t\tID: p.ID, Ordinal: p.Ordinal, CountryCode: p.CountryCode, Country: p.Country, SourceMode: normalizeSource(p.SourceMode),
\t\tPort: p.Port, User: p.User, Pass: p.Pass, ActiveSource: p.ActiveSource,
\t\tExitIP: p.ExitIP, LatencyMS: p.LatencyMS, NodeLatencyMS: p.NodeLatencyMS, IPType: p.IPType, IPISP: p.IPISP, IPASN: p.IPASN, IPRisk: p.IPRisk, Status: p.Status, LastSwitch: p.LastSwitch,
\t\tFailCount: p.FailCount, Error: p.Error, CandidateCount: len(p.Candidates),
\t}
}
'''
new_view = '''func (p *Pool) view() PoolView {
\tp.mu.Lock()
\tdefer p.mu.Unlock()
\tv := PoolView{
\t\tID: p.ID, Ordinal: p.Ordinal, CountryCode: p.CountryCode, Country: p.Country, SourceMode: normalizeSource(p.SourceMode),
\t\tPort: p.Port, User: p.User, Pass: p.Pass, ActiveSource: p.ActiveSource,
\t\tExitIP: p.ExitIP, LatencyMS: p.LatencyMS, NodeLatencyMS: p.NodeLatencyMS, IPType: p.IPType, IPISP: p.IPISP, IPASN: p.IPASN, IPRisk: p.IPRisk, Status: p.Status, LastSwitch: p.LastSwitch,
\t\tFailCount: p.FailCount, Error: p.Error, CandidateCount: len(p.Candidates),
\t}
\tapplySpeedTestView(p.ID, p.Status, runtimeIdentity{source: p.ActiveSource, ip: p.ActiveIP, port: p.ActivePort}, &v)
\treturn v
}
'''
if text.count(old_view) != 1:
    raise SystemExit("main.go: view function marker mismatch")
text = text.replace(old_view, new_view, 1)

handler_marker = '\tmux.HandleFunc("/api/pool/switch", app.auth(app.switchPool))\n'
if text.count(handler_marker) != 1:
    raise SystemExit("main.go: switch handler marker mismatch")
text = text.replace(handler_marker, handler_marker + '\tmux.HandleFunc("/api/pool/speedtest", app.auth(app.speedTestPool))\n', 1)

delete_marker = '\tdropAutoRetry(id)\n'
if text.count(delete_marker) != 1:
    raise SystemExit("main.go: delete cleanup marker mismatch")
text = text.replace(delete_marker, delete_marker + '\tdropSpeedTestState(id)\n', 1)
p.write_text(text)

# health.go
p = Path("health.go")
text = p.read_text()
marker = '''\tif a.telegram != nil && a.telegram.isPoolPaused(p.ID) {
\t\treturn
\t}

\tdelays := []time.Duration{healthRetryDelayOne, healthRetryDelayTwo}
'''
replacement = '''\tif a.telegram != nil && a.telegram.isPoolPaused(p.ID) {
\t\treturn
\t}
\tif speedTestRunning(p.ID) {
\t\treturn
\t}

\tdelays := []time.Duration{healthRetryDelayOne, healthRetryDelayTwo}
'''
if text.count(marker) != 1:
    raise SystemExit("health.go: health marker mismatch")
p.write_text(text.replace(marker, replacement, 1))

# web.go
p = Path("web.go")
text = p.read_text()
old_vars = "var sourceMode='vpngate',regions={},selectedCountry='',poolCache={},deleteID='',sourcePoolID='';"
new_vars = "var sourceMode='vpngate',regions={},selectedCountry='',poolCache={},deleteID='',sourcePoolID='',speedBusy={};"
if text.count(old_vars) != 1:
    raise SystemExit("web.go: vars marker mismatch")
text = text.replace(old_vars, new_vars, 1)

start = text.index("function cardHTML(p){")
end = text.index("async function loadPools()", start)
card = '''function cardHTML(p){var busy=busyStatus(p.status),testing=!!speedBusy[p.id],addr=s5Host()+':'+p.port;var nodeLatency=(Number.isFinite(p.node_latency_ms)&&p.node_latency_ms>=0?p.node_latency_ms+' ms':'不可测');var response=(Number.isFinite(p.latency_ms)&&p.latency_ms>=0?p.latency_ms+' ms':'-');var speed=(Number.isFinite(p.speed_mbps)&&p.speed_mbps>0?p.speed_mbps.toFixed(1)+' Mbps · '+Number(p.speed_mb_per_sec||0).toFixed(2)+' MB/s':'-');var netinfo=[p.ip_isp,p.ip_asn].filter(Boolean).join(' · ')||'-';return '<article class="card '+(busy?'busy':'')+'" id="card-'+esc(p.id)+'"><div class="card-head"><div><div class="country"><span class="flag">'+flag(p.country_code)+'</span><span class="country-name">'+esc(p.country)+'</span></div><div class="ordinal">'+esc(p.country_code)+' · 出口 '+p.ordinal+'</div></div><span class="badge '+statusClass(p.status)+'">'+statusLabel(p.status)+'</span></div><div class="s5box"><div class="s5meta"><div class="s5label">SOCKS5</div><div class="s5addr">'+esc(addr)+'</div></div><button class="copy" title="复制完整 S5" onclick="copyText(\\''+esc(fullS5(p))+'\\',\\'S5 配置\\')">⧉</button></div><div class="kv"><div class="k">用户名</div><div class="v"><code>'+esc(p.user)+'</code></div><div class="k">密码</div><div class="v"><code>'+esc(p.pass)+'</code></div><div class="k">当前出口</div><div class="v"><code>'+esc(p.exit_ip||'-')+'</code></div><div class="k">IP 属性</div><div class="v">'+ipProfileHTML(p)+'</div><div class="k">ISP / ASN</div><div class="v" title="'+esc(netinfo)+'">'+esc(netinfo)+'</div><div class="k">节点延迟</div><div class="v latency '+nodeLatencyClass(p.node_latency_ms)+'"><code>'+esc(nodeLatency)+'</code></div><div class="k">出口响应</div><div class="v latency '+latencyClass(p.latency_ms)+'"><code>'+esc(response)+'</code></div><div class="k">下载测速</div><div class="v"><code>'+esc(speed)+'</code></div></div><div class="source-row">'+sourceButton(p)+'<span class="source-now">当前：'+esc(sourceName(p.active_source))+'</span></div><div class="meta">候选 '+(p.candidate_count||0)+' · 上次切换 '+esc(when(p.last_switch))+'</div>'+(p.error?'<div class="errorbox">'+esc(p.error)+'</div>':'')+'<div class="card-actions"><button class="smallbtn" '+((p.status!==\'up\'||testing)?\'disabled\':\'\')+' onclick="speedTest(\\''+esc(p.id)+'\\')">'+(testing?'<span class="spinner"></span>测速中':'测速')+'</button><button class="smallbtn" '+(busy?'disabled':'')+' onclick="sw(\\''+esc(p.id)+'\\')">'+(busy?'<span class="spinner"></span>'+statusLabel(p.status):'立即切换')+'</button><button class="smallbtn danger" onclick="askDelete(\\''+esc(p.id)+'\\')">删除</button></div></article>'}
'''
text = text[:start] + card + text[end:]

sw_marker = "async function sw(id){"
if text.count(sw_marker) != 1:
    raise SystemExit("web.go: sw marker mismatch")
speed_js = '''async function speedTest(id){if(speedBusy[id])return;speedBusy[id]=true;loadPools();try{var r=await j('/api/pool/speedtest',{method:'POST',body:new URLSearchParams({id:id})});toast('测速完成：'+Number(r.mbps||0).toFixed(1)+' Mbps','success')}catch(e){toast(e.message,'error')}finally{delete speedBusy[id];await loadPools()}}
'''
text = text.replace(sw_marker, speed_js + sw_marker, 1)
p.write_text(text)

# telegram.go
p = Path("telegram.go")
text = p.read_text()
cmd_marker = '\t\t{"command": "check", "description": "手动检测指定出口"},\n'
if text.count(cmd_marker) != 1:
    raise SystemExit("telegram.go: command marker mismatch")
text = text.replace(cmd_marker, cmd_marker + '\t\t{"command": "speed", "description": "测速当前出口"},\n', 1)

msg_marker = '''\tcase "check":
\t\tt.sendPoolMenu(token, m.Chat.ID, "选择要手动检测的出口：", "ck:", false)
'''
if text.count(msg_marker) != 1:
    raise SystemExit("telegram.go: message check marker mismatch")
text = text.replace(msg_marker, msg_marker + '''\tcase "speed":
\t\tt.sendPoolMenu(token, m.Chat.ID, "选择要测速的当前出口：", "st:", false)
''', 1)

menu_start = text.index("func (t *TelegramManager) mainMenu() tgMarkup {")
menu_end = text.index("func (t *TelegramManager) handleCallback", menu_start)
menu = '''func (t *TelegramManager) mainMenu() tgMarkup {
\treturn tgMarkup{InlineKeyboard: [][]tgButton{
\t\t{{Text: "📊 运行状态", CallbackData: "m:status"}, {Text: "🔄 立即切换", CallbackData: "m:switch"}},
\t\t{{Text: "🩺 健康检测", CallbackData: "m:check"}, {Text: "🚀 出口测速", CallbackData: "m:speed"}},
\t\t{{Text: "🌐 刷新节点池", CallbackData: "m:refresh"}, {Text: "♻️ 恢复状态", CallbackData: "m:recovery"}},
\t\t{{Text: "⏸ 暂停 / 恢复", CallbackData: "m:pause"}},
\t}}
}

'''
text = text[:menu_start] + menu + text[menu_end:]

callback_marker = '''\tcase "m:check":
\t\tt.sendPoolMenu(token, chatID, "选择要手动检测的出口：", "ck:", false)
'''
if text.count(callback_marker) != 1:
    raise SystemExit("telegram.go: callback check marker mismatch")
text = text.replace(callback_marker, callback_marker + '''\tcase "m:speed":
\t\tt.sendPoolMenu(token, chatID, "选择要测速的当前出口：", "st:", false)
''', 1)

rf_marker = '\t\tif strings.HasPrefix(data, "rf:") {\n'
if text.count(rf_marker) != 1:
    raise SystemExit("telegram.go: rf marker mismatch")
speed_cb = '''\t\tif strings.HasPrefix(data, "st:") {
\t\t\tid := strings.TrimPrefix(data, "st:")
\t\t\tp := t.pool(id)
\t\t\tif p == nil {
\t\t\t\tt.sendTo(token, chatID, "出口不存在。", nil)
\t\t\t\treturn
\t\t\t}
\t\t\tv := p.view()
\t\t\tif v.Status != "up" {
\t\t\t\tt.sendTo(token, chatID, "当前出口未处于正常状态，无法测速。", nil)
\t\t\t\treturn
\t\t\t}
\t\t\tt.sendTo(token, chatID, "🚀 正在测速 "+poolDisplay(v)+"…\\n最长约 8 秒，测试流量最多约 50 MB。", nil)
\t\t\tgo func() {
\t\t\t\tresult, err := runPoolSpeedTest(p)
\t\t\t\tif err != nil {
\t\t\t\t\tt.sendTo(token, chatID, "❌ "+poolDisplay(v)+" 测速失败\\n"+safeTGText(err.Error(), 900), nil)
\t\t\t\t\treturn
\t\t\t\t}
\t\t\t\tafter := p.view()
\t\t\t\tmsg := fmt.Sprintf("🚀 %s 测速完成\\n\\n出口：%s\\n下载：%.1f Mbps\\n      %.2f MB/s\\n测试流量：%.1f MB\\n耗时：%.2f 秒", poolDisplay(after), telegramExitLabel(after), result.Mbps, result.MBps, float64(result.Bytes)/1_000_000, float64(result.DurationMS)/1000)
\t\t\t\tt.sendTo(token, chatID, msg, nil)
\t\t\t}()
\t\t\treturn
\t\t}
'''
text = text.replace(rf_marker, speed_cb + rf_marker, 1)

help_marker = '\t\t"/check  手动检测指定出口，不触发切换\\n" +\n'
if text.count(help_marker) != 1:
    raise SystemExit("telegram.go: help marker mismatch")
text = text.replace(help_marker, help_marker + '\t\t"/speed  测速当前出口的实际下载吞吐\\n" +\n', 1)
p.write_text(text)

replace_once("install.sh", "VERSION=1.3.2", "VERSION=1.3.3")
replace_once("xs5.sh", 'echo "1.3.2"', 'echo "1.3.3"')
Path("VERSION").write_text("1.3.3\n")
Path("RELEASE.md").write_text('''# Xs5 v1.3.3

本版本新增当前出口的手动下载测速，并同时接入 Web 面板与 Telegram 控制。

- 每个出口卡片新增“测速”按钮，测速流量真实经过该出口当前固定 SOCKS5 链路。
- Telegram 新增 `/speed` 命令与“🚀 出口测速”按钮，可选择指定出口执行同一套测速。
- 测速采用按时间与流量双重兜底：最长约 8 秒、最多约 50 MB；高速线路会自动传输更多数据，不再使用固定 5 MB 小样本。
- 测试数据直接丢弃，不写入硬盘；结果显示 Mbps、MB/s、实际测试流量和耗时。
- 同一时间全局只允许一个测速任务；同一出口两次测速至少间隔 30 秒，避免误操作持续占用带宽。
- 自动健康检查会避开正在测速的出口，测速失败不会增加 FailCount、不会触发自动切换，也不会写入候选自学习/动态冷却评分。
- 面板只显示当前运行线路对应的最近一次测速结果；出口发生切换后不会继续展示旧线路速度。
- 不改变 VPN Gate / Proxio / ProxyScrape 节点选择、自动恢复、候选排序、Telegram 通知和既有 S5 配置。

> 测速使用公共下载测试端点，结果会受到测速端点、出口拥塞及当时网络状态影响，仅供当前链路吞吐参考。
''')
