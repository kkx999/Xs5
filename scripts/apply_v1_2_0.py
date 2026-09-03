from pathlib import Path


def must_replace(path, old, new, count=None):
    p = Path(path)
    s = p.read_text()
    found = s.count(old)
    if found == 0:
        raise SystemExit(f"missing pattern in {path}: {old[:120]!r}")
    if count is not None and found != count:
        raise SystemExit(f"unexpected count in {path}: {found} != {count} for {old[:120]!r}")
    s = s.replace(old, new)
    p.write_text(s)

# main.go: version, source identity, status, refresh and runtime routing.
must_replace("main.go", 'appVersion       = "v1.1.0"', 'appVersion       = "v1.2.0"', 1)

must_replace("main.go", '''func normalizeSource(v string) string {
\tswitch strings.ToLower(strings.TrimSpace(v)) {
\tcase sourceProxio, "proxyscrape": // 兼容 v0.1.x 保存的旧来源名
\t\treturn sourceProxio
\tcase sourceAll:
\t\treturn sourceAll
\tdefault:
\t\treturn sourceVPNGate
\t}
}

func sourceLabel(v string) string {
\tswitch normalizeSource(v) {
\tcase sourceProxio:
\t\treturn "Proxio"
\tcase sourceAll:
\t\treturn "全部来源"
\tdefault:
\t\treturn "VPN Gate"
\t}
}''', '''func normalizeSource(v string) string {
\tswitch strings.ToLower(strings.TrimSpace(v)) {
\tcase sourceProxio, "proxyscrape": // 兼容 v0.1.x 曾把 proxyscrape 作为旧来源别名保存的配置
\t\treturn sourceProxio
\tcase sourceProxyScrape, "proxyscrape-free", "pscrape":
\t\treturn sourceProxyScrape
\tcase sourceAll:
\t\treturn sourceAll
\tdefault:
\t\treturn sourceVPNGate
\t}
}

func sourceLabel(v string) string {
\tswitch normalizeSource(v) {
\tcase sourceProxio:
\t\treturn "Proxio"
\tcase sourceProxyScrape:
\t\treturn "ProxyScrape"
\tcase sourceAll:
\t\treturn "全部来源"
\tdefault:
\t\treturn "VPN Gate"
\t}
}''', 1)

must_replace("main.go", '''\tISP         string  `json:"isp,omitempty"`
\tConfig      string  `json:"-"`''', '''\tISP         string  `json:"isp,omitempty"`
\tSourceHits  int     `json:"source_hits,omitempty"` // 同一 SOCKS5 端点被多少个独立来源同时收录
\tConfig      string  `json:"-"`''', 1)

must_replace("main.go", 'counts := map[string]int{sourceVPNGate: 0, sourceProxio: 0}', 'counts := map[string]int{sourceVPNGate: 0, sourceProxio: 0, sourceProxyScrape: 0}', 1)

must_replace("main.go", '''func (a *App) candidatesForLocked(country, sourceMode string) []Node {
\tsourceMode = normalizeSource(sourceMode)
\tvar cands []Node
\tfor _, n := range a.Nodes {
\t\tif n.CountryCode == country && sourceMatches(n.Source, sourceMode) {
\t\t\tcands = append(cands, n)
\t\t}
\t}
\tsortNodes(cands)
\treturn cands
}''', '''func (a *App) candidatesForLocked(country, sourceMode string) []Node {
\treturn buildCandidatePool(a.Nodes, country, normalizeSource(sourceMode), time.Now())
}''', 1)

must_replace("main.go", '''\tfetch := func(source string) result {
\t\tswitch source {
\t\tcase sourceProxio:
\t\t\tnodes, err := fetchProxioNodes()
\t\t\treturn result{source: sourceProxio, nodes: nodes, err: err}
\t\tdefault:
\t\t\tnodes, err := fetchVPNGateNodes()
\t\t\treturn result{source: sourceVPNGate, nodes: nodes, err: err}
\t\t}
\t}

\tvar results []result
\tif selected == sourceAll {
\t\tch := make(chan result, 2)
\t\tgo func() { ch <- fetch(sourceVPNGate) }()
\t\tgo func() { ch <- fetch(sourceProxio) }()
\t\tresults = append(results, <-ch, <-ch)
\t} else {
\t\tresults = append(results, fetch(selected))
\t}''', '''\tfetch := func(source string) result {
\t\tswitch source {
\t\tcase sourceProxio:
\t\t\tnodes, err := fetchProxioNodes()
\t\t\treturn result{source: sourceProxio, nodes: nodes, err: err}
\t\tcase sourceProxyScrape:
\t\t\tnodes, err := fetchProxyScrapeNodes()
\t\t\treturn result{source: sourceProxyScrape, nodes: nodes, err: err}
\t\tdefault:
\t\t\tnodes, err := fetchVPNGateNodes()
\t\t\treturn result{source: sourceVPNGate, nodes: nodes, err: err}
\t\t}
\t}

\tvar results []result
\tif selected == sourceAll {
\t\tch := make(chan result, 3)
\t\tgo func() { ch <- fetch(sourceVPNGate) }()
\t\tgo func() { ch <- fetch(sourceProxio) }()
\t\tgo func() { ch <- fetch(sourceProxyScrape) }()
\t\tresults = append(results, <-ch, <-ch, <-ch)
\t} else {
\t\tresults = append(results, fetch(selected))
\t}''', 1)

# Keep source-internal duplicates independent in storage; cross-source SOCKS5 dedupe happens only in an `all` candidate pool.
must_replace("main.go", '''\t// 同源同 IP:port 去重，避免镜像偶发重复项。
\tseen := map[string]bool{}
\tout := merged[:0]
\tfor _, n := range merged {
\t\tk := nodeKey(n)
\t\tif seen[k] {
\t\t\tcontinue
\t\t}
\t\tseen[k] = true
\t\tout = append(out, n)
\t}''', '''\t// 这里只做源内去重，保留每个来源自己的完整视图；“全部来源”候选池再按 SOCKS5 IP:port 跨源去重。
\tseen := map[string]bool{}
\tout := merged[:0]
\tfor _, n := range merged {
\t\tk := sourceNodeKey(n)
\t\tif seen[k] {
\t\t\tcontinue
\t\t}
\t\tseen[k] = true
\t\tout = append(out, n)
\t}''', 1)

must_replace("main.go", 'req.Header.Set("User-Agent", "Xs5/v1.1.0")', 'req.Header.Set("User-Agent", "Xs5/"+appVersion)', 1)

must_replace("main.go", '''\t\tif err == nil {
\t\t\tstate.recordSuccess(i, len(cands), nodeKey(node))
\t\t\tif a.telegram != nil {''', '''\t\tif err == nil {
\t\t\tstate.recordSuccess(i, len(cands), nodeKey(node))
\t\t\trecordVerifiedCandidate(node, p.view().LatencyMS)
\t\t\tif a.telegram != nil {''', 1)

must_replace("main.go", '''\t\t// 本机资源错误时不要再主动杀掉可能仍能工作的旧 runtime。
\t\t// VPN Gate 失败候选在 activateVPNGate 内部已经自行清理；Proxio 预检失败则保留旧线路继续承载流量。''', '''\t\t// 本机资源错误时不要再主动杀掉可能仍能工作的旧 runtime。
\t\t// VPN Gate 失败候选在 activateVPNGate 内部已经自行清理；SOCKS5 源预检失败则保留旧线路继续承载流量。''', 1)

must_replace("main.go", '''\tswitch node.Source {
\tcase sourceProxio:
\t\t// Proxio 仍先独立验证新上游；接管旧 VPN Gate runtime 时也与 VPN Gate 重操作串行。
\t\treturn a.activateProxio(p, node)
\tdefault:''', '''\tswitch node.Source {
\tcase sourceProxio, sourceProxyScrape:
\t\t// 两个公开 SOCKS5 源都先独立验证新上游；接管旧 VPN Gate runtime 时也与 VPN Gate 重操作串行。
\t\treturn a.activateProxio(p, node)
\tdefault:''', 1)

must_replace("main.go", '''func (a *App) activateProxio(p *Pool, node Node) error {
\tlatency, err := probeProxyConnectivity(node)
\tif err != nil {
\t\treturn fmt.Errorf("Proxio SOCKS5 普通 HTTPS 可用性检测失败: %w", err)
\t}''', '''func (a *App) activateProxio(p *Pool, node Node) error {
\tlatency, err := probeProxyConnectivity(node)
\tif err != nil {
\t\treturn fmt.Errorf("%s SOCKS5 普通 HTTPS 可用性检测失败: %w", sourceLabel(node.Source), err)
\t}''', 1)

must_replace("main.go", '''\tp.ActiveSource = sourceProxio
\tp.ActiveIP = node.IP''', '''\tp.ActiveSource = node.Source
\tp.ActiveIP = node.IP''', 1)

must_replace("main.go", '''\tlog.Printf("%s/%s up: SOCKS5 :%d -> Proxio %s, ordinary HTTPS ok (%dms)", p.CountryCode, p.ID, p.Port, node.Host, latency)''', '''\tlog.Printf("%s/%s up: SOCKS5 :%d -> %s %s, ordinary HTTPS ok (%dms)", p.CountryCode, p.ID, p.Port, sourceLabel(node.Source), node.Host, latency)''', 1)

must_replace("main.go", '''\tswitch source {
\tcase sourceProxio:
\t\treturn probeProxyNode(Node{Source: sourceProxio, IP: ip, Port: port})
\tcase sourceVPNGate:''', '''\tswitch source {
\tcase sourceProxio, sourceProxyScrape:
\t\treturn probeProxyNode(Node{Source: source, IP: ip, Port: port})
\tcase sourceVPNGate:''', 1)

must_replace("main.go", '''\tif node.Source == sourceProxio && node.Port > 0 {
\t\treturn tcpConnectLatency(node.IP, node.Port, 3*time.Second)
\t}''', '''\tif isSOCKSProxySource(node.Source) && node.Port > 0 {
\t\treturn tcpConnectLatency(node.IP, node.Port, 3*time.Second)
\t}''', 1)

# Runtime SOCKS forwarding must treat ProxyScrape exactly like Proxio.
main = Path("main.go").read_text()
if 'if source == sourceProxio {' not in main:
    raise SystemExit("missing SOCKS runtime source check")
main = main.replace('if source == sourceProxio {', 'if isSOCKSProxySource(source) {')
Path("main.go").write_text(main)

# health: successful active-path checks keep the current endpoint recently verified; no extra probing is performed.
must_replace("health.go", '''\t\tif err == nil {
\t\t\tif markConnectivityHealthy(p, id, latency) {
\t\t\t\tmaybeRefreshPoolExitIP(p, false)
\t\t\t}
\t\t\treturn
\t\t}''', '''\t\tif err == nil {
\t\t\tif markConnectivityHealthy(p, id, latency) {
\t\t\t\trecordVerifiedRuntime(id, latency)
\t\t\t\tmaybeRefreshPoolExitIP(p, false)
\t\t\t}
\t\t\treturn
\t\t}''', 1)
must_replace("health.go", '// 两个源统一处理：如果失败来自服务器本身资源不足，而不是远端出口，', '// 所有来源统一处理：如果失败来自服务器本身资源不足，而不是远端出口，', 1)

# Telegram source refresh controls.
must_replace("telegram.go", '''\tcase "m:refresh":
\t\tt.sendTo(token, chatID, "选择要刷新的节点源：", tgMarkup{InlineKeyboard: [][]tgButton{{
\t\t\t{Text: "VPN Gate", CallbackData: "rf:vpngate"}, {Text: "Proxio", CallbackData: "rf:proxio"}, {Text: "全部来源", CallbackData: "rf:all"},
\t\t}}})''', '''\tcase "m:refresh":
\t\tt.sendTo(token, chatID, "选择要刷新的节点源：", tgMarkup{InlineKeyboard: [][]tgButton{
\t\t\t{{Text: "VPN Gate", CallbackData: "rf:vpngate"}, {Text: "Proxio", CallbackData: "rf:proxio"}},
\t\t\t{{Text: "ProxyScrape", CallbackData: "rf:proxyscrape_free"}, {Text: "全部来源", CallbackData: "rf:all"}},
\t\t}})''', 1)
must_replace("telegram.go", '"/refresh  刷新 VPN Gate / Proxio 节点池\\n" +', '"/refresh  刷新 VPN Gate / Proxio / ProxyScrape 节点池\\n" +', 1)

# Web source selector: ProxyScrape is explicit and `all` still starts with VPN Gate by candidate policy.
web = Path("web.go").read_text()
old_defs = "var sourceDefs=[{id:'vpngate',name:'VPN Gate',desc:'OpenVPN 公共节点'},{id:'proxio',name:'Proxio',desc:'质量筛选 SOCKS5'},{id:'all',name:'全部来源',desc:'跨源自动选择'}];"
new_defs = "var sourceDefs=[{id:'vpngate',name:'VPN Gate',desc:'默认优先 · OpenVPN 公共节点'},{id:'proxio',name:'Proxio',desc:'质量筛选 SOCKS5'},{id:'proxyscrape_free',name:'ProxyScrape',desc:'轻筛选 SOCKS5 备用源'},{id:'all',name:'全部来源',desc:'VPN Gate 优先 · 跨源去重'}];"
if old_defs not in web:
    raise SystemExit("missing web sourceDefs")
web = web.replace(old_defs, new_defs)
old_name = "function sourceName(s){return ({vpngate:'VPN Gate',proxio:'Proxio',all:'全部来源',proxyscrape:'Proxio'})[s]||'-'}"
new_name = "function sourceName(s){return ({vpngate:'VPN Gate',proxio:'Proxio',proxyscrape_free:'ProxyScrape',all:'全部来源',proxyscrape:'Proxio'})[s]||'-'}"
if old_name not in web:
    raise SystemExit("missing web sourceName")
web = web.replace(old_name, new_name)
web = web.replace('Proxio 会先按可靠性、在线率和延迟做质量筛选，再进行实际 SOCKS5 出网检测。', 'Proxio 与 ProxyScrape 刷新时只做元数据轻筛选和去重，不会批量真实探测；真正的 SOCKS5 HTTPS 检测只在建立或切换出口时执行。')
Path("web.go").write_text(web)

# README remains timeless/use-focused; only update current capabilities.
readme = Path("README.md").read_text()
readme = readme.replace('VPN Gate / Proxio', 'VPN Gate / Proxio / ProxyScrape')
readme = readme.replace('支持 VPN Gate、Proxio 和全部来源三种候选策略。', '支持 VPN Gate、Proxio、ProxyScrape 和全部来源候选策略；默认仍以 VPN Gate 为首选。')
if '### ProxyScrape' not in readme:
    marker = '''### Proxio

```text
固定 S5 -> 公共 SOCKS5 -> 目标网站
```
'''
    insert = marker + '''\n### ProxyScrape\n\n```text\n固定 S5 -> 公共 SOCKS5 -> 目标网站\n```\n\nProxyScrape 作为补充候选源，只在刷新时做协议、国家、在线率、延迟、时效和重复端点等低成本筛选；候选池不会为了“提前标绿”而批量建立真实代理连接。\n'''
    if marker not in readme:
        raise SystemExit("missing README Proxio section")
    readme = readme.replace(marker, insert)
Path("README.md").write_text(readme)

# Version files.
Path("VERSION").write_text("1.2.0\n")
must_replace("install.sh", 'VERSION=1.1.0', 'VERSION=1.2.0', 1)
must_replace("xs5.sh", 'echo "1.1.0"', 'echo "1.2.0"', 1)

# Current release notes only; no changelog is added to README.
Path("RELEASE.md").write_text('''# Xs5 v1.2.0

本版本新增 ProxyScrape 第三节点源，并把候选池改为“低成本筛选 + 按需真实验证”的结构。默认仍以 VPN Gate 为首选，Proxio 与 ProxyScrape 用于补充稳定性和国家覆盖，不会因为新增来源而对全部候选执行批量网络探测。

- 新增 ProxyScrape 免费 SOCKS5 节点源，使用官方机器可读镜像并保留 CDN / GitHub Raw 双入口。
- ProxyScrape 刷新阶段只解析元数据，不做逐节点 TCP、SOCKS5 或 HTTPS 实测；不会因节点数量增加制造大规模主动探测压力。
- ProxyScrape 仅保留 SOCKS5、合法 IP/端口、有效国家代码，并过滤明显低在线率、过高延迟、透明代理和过旧检查记录。
- ProxyScrape 每个国家最多保留质量排序后的 30 个候选，避免大量低价值节点灌入候选池。
- Proxio 与 ProxyScrape 各自保持源内去重；“全部来源”模式进一步按 SOCKS5 `IP:port` 做跨源去重，同一端点不会因为被两个来源收录而重复尝试。
- 同一个 SOCKS5 端点若同时被多个独立来源收录，会记录多源命中并在同档质量下获得轻微排序优势。
- “全部来源”采用 VPN Gate 优先的加权交错顺序：先尝试 VPN Gate，同时穿插 Proxio 与 ProxyScrape，避免 VPN Gate 连续重型失败把 90 秒切换窗口全部耗尽。
- 新增已验证候选记录：真实建立/健康检测成功过的端点会记录最近成功时间和链路延迟；30 分钟内成功过的候选优先级最高，6 小时内仍保留普通加权，之后自动视为未知。
- 已验证记录只来自 Xs5 实际建立或现有固定 S5 健康检查成功，不会为了填充缓存额外主动探测候选池。
- VPN Gate、Proxio、ProxyScrape 最终上线前仍必须通过 Xs5 自己的普通 HTTPS 完整链路检测；第三方源声称“在线”只用于预筛选。
- ProxyScrape 与 Proxio 使用同一套 SOCKS5 runtime、健康检查、失败冷却、本机资源错误保护和 Telegram 通知/控制逻辑。
- 面板与 Telegram 的节点源选择/刷新入口均已加入 ProxyScrape。
- 保留 v1.1.0 Telegram 通知、远程控制以及 v1.0.5 的 VPN Gate 资源保护机制。
- 从旧版本更新不会改变已有 S5 端口、用户名、密码、国家出口、Telegram 配置和现有节点来源选择。

> Xs5 使用第三方公开 VPN / SOCKS5 节点。公开节点可能不稳定、不可信或被目标站封禁，请勿通过不受信任的公共出口传输敏感明文数据。
''')

# New ProxyScrape source implementation.
Path("proxyscrape.go").write_text(r'''package main

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

    proxyScrapeCountryLimit = 30
    proxyScrapeMinUptimePct = 60.0
    proxyScrapeMaxLatencyMS = 5000.0
    proxyScrapeMaxCheckAge  = 6 * time.Hour
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
        return nil, fmt.Errorf("ProxyScrape 节点经过轻筛选后为空（%s）", endpoint)
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
        if e.UptimePercent < proxyScrapeMinUptimePct {
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
        nodes := byCountry[cc]
        sortCandidateQuality(nodes, now)
        if len(nodes) > proxyScrapeCountryLimit {
            nodes = nodes[:proxyScrapeCountryLimit]
        }
        out = append(out, nodes...)
    }
    return out
}

func sortStrings(v []string) {
    for i := 1; i < len(v); i++ {
        for j := i; j > 0 && v[j] < v[j-1]; j-- {
            v[j], v[j-1] = v[j-1], v[j]
        }
    }
}
''')

# Candidate quality, cross-source dedupe and verified history.
Path("candidate_quality.go").write_text(r'''package main

import (
    "encoding/json"
    "fmt"
    "net"
    "os"
    "path/filepath"
    "sort"
    "strconv"
    "sync"
    "time"
)

const (
    verifiedStrongWindow = 30 * time.Minute
    verifiedUsefulWindow = 6 * time.Hour
    verifiedKeepWindow   = 24 * time.Hour
    verifiedPersistGap   = 5 * time.Minute
)

type verifiedCandidateRecord struct {
    LastSuccess time.Time `json:"last_success"`
    LatencyMS   int       `json:"latency_ms"`
    Successes   int       `json:"successes"`
}

type verifiedCandidateStore struct {
    once        sync.Once
    mu          sync.RWMutex
    records     map[string]verifiedCandidateRecord
    lastPersist time.Time
}

var verifiedCandidates verifiedCandidateStore

func isSOCKSProxySource(source string) bool {
    return source == sourceProxio || source == sourceProxyScrape
}

func sourceNodeKey(n Node) string {
    return fmt.Sprintf("%s|%s|%d|%s", n.Source, n.IP, n.Port, n.Host)
}

func socksEndpointKey(n Node) string {
    if !isSOCKSProxySource(n.Source) || net.ParseIP(n.IP) == nil || n.Port < 1 {
        return ""
    }
    return net.JoinHostPort(n.IP, strconv.Itoa(n.Port))
}

func verificationKey(n Node) string {
    if endpoint := socksEndpointKey(n); endpoint != "" {
        return "socks5|" + endpoint
    }
    if n.Source == sourceVPNGate {
        host, port, proto := openVPNRemote(n.Config)
        return fmt.Sprintf("vpngate|%s|%s|%d|%s", n.IP, host, port, proto)
    }
    return sourceNodeKey(n)
}

func (s *verifiedCandidateStore) load() {
    s.once.Do(func() {
        s.records = map[string]verifiedCandidateRecord{}
        b, err := os.ReadFile(filepath.Join(workDir, "verified_nodes.json"))
        if err != nil {
            return
        }
        var records map[string]verifiedCandidateRecord
        if json.Unmarshal(b, &records) == nil && records != nil {
            s.records = records
        }
    })
}

func (s *verifiedCandidateStore) rank(n Node, now time.Time) (int, int) {
    s.load()
    key := verificationKey(n)
    s.mu.RLock()
    rec, ok := s.records[key]
    s.mu.RUnlock()
    if !ok || rec.LastSuccess.IsZero() || rec.LastSuccess.After(now.Add(time.Minute)) {
        return 0, -1
    }
    age := now.Sub(rec.LastSuccess)
    switch {
    case age <= verifiedStrongWindow:
        return 2, rec.LatencyMS
    case age <= verifiedUsefulWindow:
        return 1, rec.LatencyMS
    default:
        return 0, rec.LatencyMS
    }
}

func (s *verifiedCandidateStore) record(key string, latency int, now time.Time) {
    if key == "" {
        return
    }
    s.load()
    s.mu.Lock()
    rec := s.records[key]
    rec.LastSuccess = now
    if latency >= 0 {
        rec.LatencyMS = latency
    }
    rec.Successes++
    s.records[key] = rec
    for k, old := range s.records {
        if !old.LastSuccess.IsZero() && now.Sub(old.LastSuccess) > verifiedKeepWindow {
            delete(s.records, k)
        }
    }
    shouldPersist := s.lastPersist.IsZero() || now.Sub(s.lastPersist) >= verifiedPersistGap
    if shouldPersist {
        s.lastPersist = now
    }
    snapshot := make(map[string]verifiedCandidateRecord, len(s.records))
    if shouldPersist {
        for k, v := range s.records {
            snapshot[k] = v
        }
    }
    s.mu.Unlock()
    if shouldPersist {
        persistVerifiedCandidates(snapshot)
    }
}

func persistVerifiedCandidates(records map[string]verifiedCandidateRecord) {
    if len(records) == 0 {
        return
    }
    if err := os.MkdirAll(workDir, 0700); err != nil {
        return
    }
    b, err := json.MarshalIndent(records, "", "  ")
    if err != nil {
        return
    }
    path := filepath.Join(workDir, "verified_nodes.json")
    tmp := path + ".tmp"
    if os.WriteFile(tmp, b, 0600) != nil {
        return
    }
    _ = os.Chmod(tmp, 0600)
    _ = os.Rename(tmp, path)
}

func recordVerifiedCandidate(n Node, latency int) {
    verifiedCandidates.record(verificationKey(n), latency, time.Now())
}

func recordVerifiedRuntime(id runtimeIdentity, latency int) {
    n := Node{Source: id.source, IP: id.ip, Port: id.port}
    if id.source == sourceVPNGate {
        // VPN Gate 的运行态没有完整配置指纹；切换成功时已经用完整 Node 记录过。
        return
    }
    verifiedCandidates.record(verificationKey(n), latency, time.Now())
}

func candidateSourcePriority(source string) int {
    switch source {
    case sourceVPNGate:
        return 0
    case sourceProxio:
        return 1
    case sourceProxyScrape:
        return 2
    default:
        return 9
    }
}

func sortCandidateQuality(nodes []Node, now time.Time) {
    sort.SliceStable(nodes, func(i, j int) bool {
        ri, li := verifiedCandidates.rank(nodes[i], now)
        rj, lj := verifiedCandidates.rank(nodes[j], now)
        if ri != rj {
            return ri > rj
        }
        if nodes[i].SourceHits != nodes[j].SourceHits {
            return nodes[i].SourceHits > nodes[j].SourceHits
        }
        if isSOCKSProxySource(nodes[i].Source) && isSOCKSProxySource(nodes[j].Source) {
            if nodes[i].Uptime != nodes[j].Uptime {
                return nodes[i].Uptime > nodes[j].Uptime
            }
            if nodes[i].Score != nodes[j].Score {
                return nodes[i].Score > nodes[j].Score
            }
        }
        if nodes[i].Ping <= 0 && nodes[j].Ping > 0 {
            return false
        }
        if nodes[j].Ping <= 0 && nodes[i].Ping > 0 {
            return true
        }
        if nodes[i].Ping != nodes[j].Ping {
            return nodes[i].Ping < nodes[j].Ping
        }
        if ri > 0 && li >= 0 && lj >= 0 && li != lj {
            return li < lj
        }
        if nodes[i].Speed != nodes[j].Speed {
            return nodes[i].Speed > nodes[j].Speed
        }
        if nodes[i].Score != nodes[j].Score {
            return nodes[i].Score > nodes[j].Score
        }
        if candidateSourcePriority(nodes[i].Source) != candidateSourcePriority(nodes[j].Source) {
            return candidateSourcePriority(nodes[i].Source) < candidateSourcePriority(nodes[j].Source)
        }
        return sourceNodeKey(nodes[i]) < sourceNodeKey(nodes[j])
    })
}

func betterCrossSourceDuplicate(a, b Node, now time.Time) Node {
    // 同一 IP:port 实际是同一个 SOCKS5 端点，优先保留近期真实成功、元数据更稳定的一份。
    ra, _ := verifiedCandidates.rank(a, now)
    rb, _ := verifiedCandidates.rank(b, now)
    if ra != rb {
        if rb > ra {
            return b
        }
        return a
    }
    if b.Uptime != a.Uptime {
        if b.Uptime > a.Uptime {
            return b
        }
        return a
    }
    if b.Score != a.Score {
        if b.Score > a.Score {
            return b
        }
        return a
    }
    if a.Ping <= 0 || (b.Ping > 0 && b.Ping < a.Ping) {
        return b
    }
    if candidateSourcePriority(b.Source) < candidateSourcePriority(a.Source) {
        return b
    }
    return a
}

func buildCandidatePool(all []Node, country, sourceMode string, now time.Time) []Node {
    sourceMode = normalizeSource(sourceMode)
    if sourceMode != sourceAll {
        out := make([]Node, 0)
        for _, n := range all {
            if n.CountryCode == country && n.Source == sourceMode {
                if n.SourceHits == 0 {
                    n.SourceHits = 1
                }
                out = append(out, n)
            }
        }
        sortCandidateQuality(out, now)
        return out
    }

    // VPN Gate 是不同传输架构，不与 SOCKS5 端点按 IP 粗暴合并。
    buckets := map[string][]Node{
        sourceVPNGate: {}, sourceProxio: {}, sourceProxyScrape: {},
    }
    socks := map[string]Node{}
    hits := map[string]map[string]bool{}
    for _, n := range all {
        if n.CountryCode != country {
            continue
        }
        if n.Source == sourceVPNGate {
            if n.SourceHits == 0 {
                n.SourceHits = 1
            }
            buckets[sourceVPNGate] = append(buckets[sourceVPNGate], n)
            continue
        }
        if !isSOCKSProxySource(n.Source) {
            continue
        }
        key := socksEndpointKey(n)
        if key == "" {
            continue
        }
        if hits[key] == nil {
            hits[key] = map[string]bool{}
        }
        hits[key][n.Source] = true
        current, ok := socks[key]
        if !ok {
            socks[key] = n
        } else {
            socks[key] = betterCrossSourceDuplicate(current, n, now)
        }
    }
    for key, n := range socks {
        n.SourceHits = len(hits[key])
        buckets[n.Source] = append(buckets[n.Source], n)
    }
    for source := range buckets {
        sortCandidateQuality(buckets[source], now)
    }

    // VPN Gate 默认优先，但交错 SOCKS5 备用源，避免多个重型 OpenVPN 失败耗尽整轮 90 秒窗口。
    pattern := []string{sourceVPNGate, sourceProxio, sourceVPNGate, sourceProxyScrape}
    positions := map[string]int{}
    total := len(buckets[sourceVPNGate]) + len(buckets[sourceProxio]) + len(buckets[sourceProxyScrape])
    out := make([]Node, 0, total)
    for len(out) < total {
        progressed := false
        for _, source := range pattern {
            pos := positions[source]
            if pos >= len(buckets[source]) {
                continue
            }
            out = append(out, buckets[source][pos])
            positions[source] = pos + 1
            progressed = true
        }
        if !progressed {
            break
        }
    }
    return out
}
''')

Path("candidate_quality_test.go").write_text(r'''package main

import (
    "testing"
    "time"
)

func TestProxyScrapeSourceKeepsLegacyAliasSafe(t *testing.T) {
    if got := normalizeSource("proxyscrape"); got != sourceProxio {
        t.Fatalf("legacy proxyscrape alias=%q want %q", got, sourceProxio)
    }
    if got := normalizeSource("proxyscrape_free"); got != sourceProxyScrape {
        t.Fatalf("new source=%q want %q", got, sourceProxyScrape)
    }
}

func TestAllCandidatePoolCrossSourceDeduplicatesAndStartsVPNGate(t *testing.T) {
    now := time.Unix(1_700_000_000, 0)
    nodes := []Node{
        {Source: sourceVPNGate, Protocol: "openvpn", IP: "192.0.2.10", Host: "vg", CountryCode: "JP", Ping: 80},
        {Source: sourceProxio, Protocol: "socks5", IP: "198.51.100.2", Port: 1080, Host: "198.51.100.2:1080", CountryCode: "JP", Uptime: .8, Score: 80, Ping: 200},
        {Source: sourceProxyScrape, Protocol: "socks5", IP: "198.51.100.2", Port: 1080, Host: "198.51.100.2:1080", CountryCode: "JP", Uptime: .9, Score: 90, Ping: 180},
        {Source: sourceProxyScrape, Protocol: "socks5", IP: "198.51.100.3", Port: 1080, Host: "198.51.100.3:1080", CountryCode: "JP", Uptime: .9, Score: 90, Ping: 220},
    }
    got := buildCandidatePool(nodes, "JP", sourceAll, now)
    if len(got) != 3 {
        t.Fatalf("len=%d want 3: %#v", len(got), got)
    }
    if got[0].Source != sourceVPNGate {
        t.Fatalf("first source=%q want VPN Gate", got[0].Source)
    }
    dup := 0
    for _, n := range got {
        if n.IP == "198.51.100.2" && n.Port == 1080 {
            dup++
            if n.SourceHits != 2 {
                t.Fatalf("source hits=%d want 2", n.SourceHits)
            }
        }
    }
    if dup != 1 {
        t.Fatalf("cross-source endpoint appeared %d times", dup)
    }
}

func TestSpecificSourceDoesNotLoseItsOwnDuplicateEndpoint(t *testing.T) {
    now := time.Unix(1_700_000_000, 0)
    nodes := []Node{
        {Source: sourceProxio, IP: "198.51.100.4", Port: 1080, CountryCode: "US", Uptime: .8},
        {Source: sourceProxyScrape, IP: "198.51.100.4", Port: 1080, CountryCode: "US", Uptime: .9},
    }
    got := buildCandidatePool(nodes, "US", sourceProxyScrape, now)
    if len(got) != 1 || got[0].Source != sourceProxyScrape {
        t.Fatalf("specific source candidates=%#v", got)
    }
}

func TestProxyScrapeLightFilterAndCountryLimit(t *testing.T) {
    now := time.Unix(1_700_000_000, 0)
    entries := make([]proxyScrapeEntry, 0, 40)
    for i := 1; i <= 35; i++ {
        entries = append(entries, proxyScrapeEntry{
            Protocol: "socks5", IP: "203.0.113." + itoaTest(i), Port: 10000 + i,
            Country: "Japan", CountryCode: "JP", Anonymity: "elite",
            UptimePercent: 90, LatencyMS: float64(100 + i), LastChecked: float64(now.Unix()),
        })
    }
    entries = append(entries,
        proxyScrapeEntry{Protocol: "http", IP: "198.51.100.10", Port: 80, CountryCode: "JP", UptimePercent: 99, LatencyMS: 20},
        proxyScrapeEntry{Protocol: "socks5", IP: "198.51.100.11", Port: 1080, CountryCode: "JP", UptimePercent: 20, LatencyMS: 20},
        proxyScrapeEntry{Protocol: "socks5", IP: "198.51.100.12", Port: 1080, CountryCode: "JP", UptimePercent: 99, LatencyMS: 6000},
        proxyScrapeEntry{Protocol: "socks5", IP: "198.51.100.13", Port: 1080, CountryCode: "JP", UptimePercent: 99, LatencyMS: 20, Anonymity: "transparent"},
    )
    got := parseProxyScrapeEntries(entries, now)
    if len(got) != proxyScrapeCountryLimit {
        t.Fatalf("len=%d want country limit %d", len(got), proxyScrapeCountryLimit)
    }
    for _, n := range got {
        if n.Source != sourceProxyScrape || n.Protocol != "socks5" || n.CountryCode != "JP" {
            t.Fatalf("unexpected node %#v", n)
        }
    }
}

func itoaTest(v int) string {
    if v < 10 { return string(rune('0' + v)) }
    return string(rune('0' + v/10)) + string(rune('0' + v%10))
}
''')

print("v1.2.0 patch prepared")
