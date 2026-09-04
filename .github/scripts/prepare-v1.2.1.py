from pathlib import Path

ROOT = Path('.')

def replace(path, old, new):
    p = ROOT / path
    text = p.read_text()
    if old not in text:
        raise SystemExit(f'expected text not found in {path}: {old[:100]!r}')
    p.write_text(text.replace(old, new, 1))

proxyscrape = r'''package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	sourceProxyScrape = "proxyscrape_free"

	proxyScrapeJSON    = "https://cdn.jsdelivr.net/gh/ProxyScrape/free-proxy-list@main/proxies/protocols/socks5/data.json"
	proxyScrapeRawJSON = "https://raw.githubusercontent.com/ProxyScrape/free-proxy-list/main/proxies/protocols/socks5/data.json"

	proxyScrapeCountryLimit       = 30
	proxyScrapeCountryTarget      = 10
	proxyScrapePreferredUptimePct = 60.0
	proxyScrapeFallbackUptimePct  = 40.0
	proxyScrapeFloorUptimePct     = 25.0
	proxyScrapeMaxLatencyMS       = 5000.0
	proxyScrapeMaxCheckAge        = 6 * time.Hour
)

type proxyScrapeEntry struct {
	Protocol      string  `json:"protocol"`
	IP            string  `json:"ip"`
	Port          int     `json:"port"`
	Country       string  `json:"country"`
	CountryCode   string  `json:"country_code"`
	Anonymity     string  `json:"anonymity"`
	UptimePercent float64 `json:"uptime_percent"`
	ASN           string  `json:"asn"`
	ISP           string  `json:"isp"`
	LatencyMS     float64 `json:"latency_ms"`
	LastChecked   float64 `json:"last_checked"`
}

func fetchProxyScrapeNodes() ([]Node, error) {
	entries, endpoint, err := fetchProxyScrapeJSON()
	if err != nil {
		return nil, err
	}
	nodes := parseProxyScrapeEntries(entries, time.Now())
	if len(nodes) == 0 {
		return nil, fmt.Errorf("ProxyScrape 节点经过自适应轻筛选后为空（最低在线率 %.0f%%，%s）", proxyScrapeFloorUptimePct, endpoint)
	}
	return nodes, nil
}

func fetchProxyScrapeJSON() ([]proxyScrapeEntry, string, error) {
	endpoints := []string{proxyScrapeJSON, proxyScrapeRawJSON}
	var errs []string
	for _, endpoint := range endpoints {
		req, err := http.NewRequest(http.MethodGet, endpoint, nil)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		req.Header.Set("User-Agent", "Xs5/"+appVersion)
		req.Header.Set("Accept", "application/json")
		cli := &http.Client{Timeout: 35 * time.Second}
		resp, err := cli.Do(req)
		if err != nil {
			errs = append(errs, endpoint+": "+err.Error())
			continue
		}
		if resp.StatusCode != http.StatusOK {
			status := resp.StatusCode
			resp.Body.Close()
			errs = append(errs, fmt.Sprintf("%s: HTTP %d", endpoint, status))
			continue
		}
		var entries []proxyScrapeEntry
		decErr := json.NewDecoder(io.LimitReader(resp.Body, 12<<20)).Decode(&entries)
		resp.Body.Close()
		if decErr != nil {
			errs = append(errs, endpoint+": 解析 JSON 失败: "+decErr.Error())
			continue
		}
		if len(entries) == 0 {
			errs = append(errs, endpoint+": 返回空列表")
			continue
		}
		return entries, endpoint, nil
	}
	if len(errs) == 0 {
		return nil, "", errors.New("ProxyScrape 没有可用数据入口")
	}
	return nil, "", errors.New(strings.Join(errs, "；"))
}

func parseProxyScrapeEntries(entries []proxyScrapeEntry, now time.Time) []Node {
	byCountry := map[string][]Node{}
	seen := map[string]bool{}
	for _, e := range entries {
		if !strings.EqualFold(strings.TrimSpace(e.Protocol), "socks5") {
			continue
		}
		ip := strings.TrimSpace(e.IP)
		if net.ParseIP(ip) == nil || e.Port < 1 || e.Port > 65535 {
			continue
		}
		cc := strings.ToUpper(strings.TrimSpace(e.CountryCode))
		if len(cc) != 2 {
			cc = countryCodeFromName(e.Country)
		}
		if len(cc) != 2 {
			continue
		}
		// 只在元数据层做轻筛选；25% 是兜底下限，不代表节点已被 Xs5 验证可用。
		if e.UptimePercent < proxyScrapeFloorUptimePct {
			continue
		}
		if e.LatencyMS <= 0 || e.LatencyMS > proxyScrapeMaxLatencyMS {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(e.Anonymity), "transparent") {
			continue
		}
		if e.LastChecked > 0 {
			checked := time.Unix(int64(e.LastChecked), 0)
			if checked.Before(now) && now.Sub(checked) > proxyScrapeMaxCheckAge {
				continue
			}
		}
		endpointKey := net.JoinHostPort(ip, strconv.Itoa(e.Port))
		if seen[endpointKey] {
			continue
		}
		seen[endpointKey] = true
		node := Node{
			Host: endpointKey, IP: ip, Port: e.Port, Source: sourceProxyScrape, Protocol: "socks5",
			Country: displayCountryName(cc, strings.TrimSpace(e.Country)), CountryCode: cc,
			Score: int(e.UptimePercent + 0.5), Ping: int(e.LatencyMS + 0.5), Uptime: e.UptimePercent / 100,
			ISP: strings.TrimSpace(e.ISP), SourceHits: 1,
		}
		byCountry[cc] = append(byCountry[cc], node)
	}

	countries := make([]string, 0, len(byCountry))
	for cc := range byCountry {
		countries = append(countries, cc)
	}
	sortStrings(countries)

	out := make([]Node, 0)
	for _, cc := range countries {
		nodes := selectProxyScrapeCountry(byCountry[cc], now)
		out = append(out, nodes...)
	}
	return out
}

// selectProxyScrapeCountry 先保留所有 >=60% 的高质量候选（最多 30 个）。
// 只有高质量候选不足 10 个时，才依次放宽到 >=40% 和 >=25%，并且只补到 10 个。
// 这避免固定 60% 门槛把整个源筛空，也避免低质量节点大量灌入候选池。
func selectProxyScrapeCountry(nodes []Node, now time.Time) []Node {
	if len(nodes) == 0 {
		return nil
	}
	sortCandidateQuality(nodes, now)
	selected := make([]Node, 0, minInt(proxyScrapeCountryLimit, len(nodes)))
	seen := map[string]bool{}
	addTier := func(minUptimePct float64, target int) {
		for _, n := range nodes {
			if len(selected) >= target || len(selected) >= proxyScrapeCountryLimit {
				return
			}
			if n.Uptime*100+0.0001 < minUptimePct {
				continue
			}
			key := socksEndpointKey(n)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			selected = append(selected, n)
		}
	}

	// 第一层不刻意凑满，只收当前确实达到 60% 的节点，最多 30 个。
	addTier(proxyScrapePreferredUptimePct, proxyScrapeCountryLimit)
	if len(selected) < proxyScrapeCountryTarget {
		addTier(proxyScrapeFallbackUptimePct, proxyScrapeCountryTarget)
	}
	if len(selected) < proxyScrapeCountryTarget {
		addTier(proxyScrapeFloorUptimePct, proxyScrapeCountryTarget)
	}
	return selected
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func sortStrings(v []string) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}
'''
(ROOT / 'proxyscrape.go').write_text(proxyscrape)

proxyscrape_test = r'''package main

import (
	"fmt"
	"testing"
	"time"
)

func psEntry(i int, uptime float64, now time.Time) proxyScrapeEntry {
	return proxyScrapeEntry{
		Protocol: "socks5", IP: fmt.Sprintf("192.0.2.%d", i), Port: 10000 + i,
		Country: "Japan", CountryCode: "JP", Anonymity: "elite",
		UptimePercent: uptime, LatencyMS: float64(50 + i), LastChecked: float64(now.Unix()),
	}
}

func TestProxyScrapeAdaptiveKeepsHighTierOnlyWhenEnough(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	var entries []proxyScrapeEntry
	for i := 1; i <= 12; i++ {
		entries = append(entries, psEntry(i, 70, now))
	}
	for i := 13; i <= 22; i++ {
		entries = append(entries, psEntry(i, 45, now))
	}
	nodes := parseProxyScrapeEntries(entries, now)
	if len(nodes) != 12 {
		t.Fatalf("got %d nodes, want 12 high-tier nodes only", len(nodes))
	}
	for _, n := range nodes {
		if n.Uptime < 0.60 {
			t.Fatalf("unexpected fallback node with uptime %.2f", n.Uptime)
		}
	}
}

func TestProxyScrapeAdaptiveFallsBackOnlyToTarget(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	var entries []proxyScrapeEntry
	for i := 1; i <= 3; i++ {
		entries = append(entries, psEntry(i, 72, now))
	}
	for i := 4; i <= 7; i++ {
		entries = append(entries, psEntry(i, 48, now))
	}
	for i := 8; i <= 12; i++ {
		entries = append(entries, psEntry(i, 30, now))
	}
	entries = append(entries, psEntry(13, 20, now), psEntry(14, 10, now))
	nodes := parseProxyScrapeEntries(entries, now)
	if len(nodes) != proxyScrapeCountryTarget {
		t.Fatalf("got %d nodes, want target %d", len(nodes), proxyScrapeCountryTarget)
	}
	lowTier := 0
	for _, n := range nodes {
		if n.Uptime < 0.25 {
			t.Fatalf("node below floor entered pool: %.2f", n.Uptime)
		}
		if n.Uptime < 0.40 {
			lowTier++
		}
	}
	if lowTier != 3 {
		t.Fatalf("got %d low-tier nodes, want exactly 3 to reach target", lowTier)
	}
}

func TestProxyScrapeAdaptiveStillCapsCountryAtThirty(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	var entries []proxyScrapeEntry
	for i := 1; i <= 40; i++ {
		entries = append(entries, psEntry(i, 85, now))
	}
	nodes := parseProxyScrapeEntries(entries, now)
	if len(nodes) != proxyScrapeCountryLimit {
		t.Fatalf("got %d nodes, want country limit %d", len(nodes), proxyScrapeCountryLimit)
	}
}
'''
(ROOT / 'proxyscrape_test.go').write_text(proxyscrape_test)

# Telegram /refresh command menu: keep the same four choices as the inline menu.
replace('telegram.go',
'''\tcase "refresh":\n\t\tt.sendTo(token, m.Chat.ID, "选择要刷新的节点源：", tgMarkup{InlineKeyboard: [][]tgButton{{\n\t\t\t{Text: "VPN Gate", CallbackData: "rf:vpngate"}, {Text: "Proxio", CallbackData: "rf:proxio"}, {Text: "全部来源", CallbackData: "rf:all"},\n\t\t}}})''',
'''\tcase "refresh":\n\t\tt.sendTo(token, m.Chat.ID, "选择要刷新的节点源：", tgMarkup{InlineKeyboard: [][]tgButton{\n\t\t\t{{Text: "VPN Gate", CallbackData: "rf:vpngate"}, {Text: "Proxio", CallbackData: "rf:proxio"}},\n\t\t\t{{Text: "ProxyScrape", CallbackData: "rf:proxyscrape_free"}, {Text: "全部来源", CallbackData: "rf:all"}},\n\t\t}})''')

old_rf = '''\t\tif strings.HasPrefix(data, "rf:") {\n\t\t\tif !remote {\n\t\t\t\tt.sendTo(token, chatID, "远程控制已在面板中关闭。", nil)\n\t\t\t\treturn\n\t\t\t}\n\t\t\tsource := normalizeSource(strings.TrimPrefix(data, "rf:"))\n\t\t\tt.sendTo(token, chatID, "🌐 正在刷新 "+sourceLabel(source)+" 节点池…", nil)\n\t\t\tgo func() {\n\t\t\t\tif err := t.app.refreshSource(source); err != nil {\n\t\t\t\t\tt.sendTo(token, chatID, "❌ "+sourceLabel(source)+" 刷新失败\\n"+safeTGText(err.Error(), 900), nil)\n\t\t\t\t\treturn\n\t\t\t\t}\n\t\t\t\tt.sendTo(token, chatID, "✅ "+sourceLabel(source)+" 节点池刷新完成。", nil)\n\t\t\t}()\n\t\t\treturn\n\t\t}'''
new_rf = '''\t\tif strings.HasPrefix(data, "rf:") {\n\t\t\tif !remote {\n\t\t\t\tt.sendTo(token, chatID, "远程控制已在面板中关闭。", nil)\n\t\t\t\treturn\n\t\t\t}\n\t\t\tsource := normalizeSource(strings.TrimPrefix(data, "rf:"))\n\t\t\tt.sendTo(token, chatID, "🌐 正在刷新 "+sourceLabel(source)+" 节点池…", nil)\n\t\t\tgo func() {\n\t\t\t\tif source == sourceAll {\n\t\t\t\t\tt.sendTo(token, chatID, t.refreshAllSourcesText(), nil)\n\t\t\t\t\treturn\n\t\t\t\t}\n\t\t\t\tif err := t.app.refreshSource(source); err != nil {\n\t\t\t\t\tt.sendTo(token, chatID, "❌ "+sourceLabel(source)+" 刷新失败\\n"+safeTGText(err.Error(), 900), nil)\n\t\t\t\t\treturn\n\t\t\t\t}\n\t\t\t\tcount := t.sourceNodeCount(source)\n\t\t\t\tt.sendTo(token, chatID, fmt.Sprintf("✅ %s 节点池刷新完成：%d 个候选。", sourceLabel(source), count), nil)\n\t\t\t}()\n\t\t\treturn\n\t\t}'''
replace('telegram.go', old_rf, new_rf)

# Insert helpers before answerCallback.
marker = 'func (t *TelegramManager) answerCallback(token, id string) {'
helpers = r'''func (t *TelegramManager) sourceNodeCount(source string) int {
	t.app.mu.RLock()
	defer t.app.mu.RUnlock()
	count := 0
	for _, n := range t.app.Nodes {
		if n.Source == source {
			count++
		}
	}
	return count
}

func (t *TelegramManager) refreshAllSourcesText() string {
	sources := []string{sourceVPNGate, sourceProxio, sourceProxyScrape}
	type refreshResult struct {
		source string
		count  int
		err    error
	}
	ch := make(chan refreshResult, len(sources))
	for _, source := range sources {
		go func(s string) {
			err := t.app.refreshSource(s)
			count := 0
			if err == nil {
				count = t.sourceNodeCount(s)
			}
			ch <- refreshResult{source: s, count: count, err: err}
		}(source)
	}
	results := map[string]refreshResult{}
	for range sources {
		r := <-ch
		results[r.source] = r
	}

	okCount := 0
	lines := make([]string, 0, len(sources))
	for _, source := range sources {
		r := results[source]
		if r.err != nil {
			lines = append(lines, "❌ "+sourceLabel(source)+"："+safeTGText(r.err.Error(), 260))
			continue
		}
		okCount++
		lines = append(lines, fmt.Sprintf("✅ %s：%d 个候选", sourceLabel(source), r.count))
	}

	title := "✅ 全部来源节点池刷新完成"
	if okCount == 0 {
		title = "❌ 全部来源节点池刷新失败"
	} else if okCount != len(sources) {
		title = "⚠️ 全部来源节点池部分完成"
	}
	return title + "\n\n" + strings.Join(lines, "\n")
}

'''
p = ROOT / 'telegram.go'
text = p.read_text()
if marker not in text:
    raise SystemExit('telegram answerCallback marker missing')
p.write_text(text.replace(marker, helpers + marker, 1))

replace('main.go', 'appVersion       = "v1.2.0"', 'appVersion       = "v1.2.1"')
replace('install.sh', 'VERSION=1.2.0', 'VERSION=1.2.1')
replace('xs5.sh', 'echo "1.2.0"', 'echo "1.2.1"')
(ROOT / 'VERSION').write_text('1.2.1\n')

release = '''# Xs5 v1.2.1\n\n本版本修复 v1.2.0 中 ProxyScrape 固定 60% 在线率门槛可能把当前免费 SOCKS5 列表全部筛空的问题，并修正 Telegram “全部来源”刷新结果的汇报方式。\n\n- ProxyScrape 改为按国家自适应轻筛选：优先保留 `uptime >= 60%` 的候选；若该国家不足 10 个，再按需补充 `>= 40%`，仍不足时最低补充到 `>= 25%`。\n- 低质量候选只用于“补足候选数量”，不会挤掉已经达到 60% 门槛的高质量候选；单国家仍最多保留 30 个。\n- ProxyScrape 仍只在刷新阶段读取来源元数据，不会对候选池逐个执行 TCP、SOCKS5 或 HTTPS 主动探测。\n- 继续过滤无效 IP/端口、非 SOCKS5、透明代理、延迟超过 5 秒和超过 6 小时未检查的记录。\n- 最低 25% 只是候选池兜底门槛，不代表节点已被 Xs5 判定可用；真正切换上线前仍必须通过 Xs5 自己的完整 HTTPS 链路检测。\n- Telegram 点击“全部来源”后，VPN Gate、Proxio、ProxyScrape 会分别刷新并逐源显示成功/失败与当前候选数量；不再出现部分来源失败却先显示“全部来源刷新完成”的误导提示。\n- Telegram `/refresh` 命令菜单补齐 ProxyScrape，与主菜单的刷新入口保持一致。\n- VPN Gate 与 Proxio 的现有筛选、健康检查、切换顺序和资源保护策略不做调整。\n- 保留 v1.2.0 的跨源 `IP:port` 去重、VPN Gate 优先策略和已验证节点缓存。\n- 从旧版本更新不会改变已有 S5 端口、用户名、密码、国家出口、节点来源选择和 Telegram 配置。\n\n> Xs5 使用第三方公开 VPN / SOCKS5 节点。公开节点可能不稳定、不可信或被目标站封禁，请勿通过不受信任的公共出口传输敏感明文数据。\n'''
(ROOT / 'RELEASE.md').write_text(release)
