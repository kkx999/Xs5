package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	appName          = "X S5 池"
	appVersion       = "v1.0.0"
	defaultListen    = ":8898"
	workDir          = "/var/lib/xs5"
	vpngateCSV       = "https://www.vpngate.net/api/iphone/"
	proxioJSON       = "https://proxio.io/download?format=json&type=socks5&limit=20000"
	proxioMirrorJSON = "https://cdn.jsdelivr.net/gh/proxio-io/proxy-list@main/all.json"
	proxioRawJSON    = "https://raw.githubusercontent.com/proxio-io/proxy-list/main/all.json"

	sourceVPNGate = "vpngate"
	sourceProxio  = "proxio"
	sourceAll     = "all"
)

func normalizeSource(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case sourceProxio, "proxyscrape": // 兼容 v0.1.x 保存的旧来源名
		return sourceProxio
	case sourceAll:
		return sourceAll
	default:
		return sourceVPNGate
	}
}

func sourceLabel(v string) string {
	switch normalizeSource(v) {
	case sourceProxio:
		return "Proxio"
	case sourceAll:
		return "全部来源"
	default:
		return "VPN Gate"
	}
}

func displayCountryName(code, fallback string) string {
	names := map[string]string{
		"JP": "日本", "KR": "韩国", "US": "美国", "TW": "台湾", "SG": "新加坡", "HK": "香港",
		"TH": "泰国", "VN": "越南", "MY": "马来西亚", "ID": "印度尼西亚", "PH": "菲律宾",
		"IN": "印度", "AU": "澳大利亚", "NZ": "新西兰", "CA": "加拿大", "MX": "墨西哥",
		"BR": "巴西", "AR": "阿根廷", "CL": "智利", "CO": "哥伦比亚",
		"GB": "英国", "DE": "德国", "FR": "法国", "NL": "荷兰", "IT": "意大利", "ES": "西班牙",
		"SE": "瑞典", "NO": "挪威", "FI": "芬兰", "DK": "丹麦", "CH": "瑞士", "AT": "奥地利",
		"PL": "波兰", "CZ": "捷克", "RO": "罗马尼亚", "BG": "保加利亚", "UA": "乌克兰",
		"TR": "土耳其", "IL": "以色列", "AE": "阿联酋", "SA": "沙特阿拉伯", "ZA": "南非",
	}
	if v := names[code]; v != "" {
		return v
	}
	if fallback != "" {
		return fallback
	}
	return code
}

type Node struct {
	Host        string  `json:"host"`
	IP          string  `json:"ip"`
	Port        int     `json:"port,omitempty"`
	Source      string  `json:"source"`
	Protocol    string  `json:"protocol"`
	Country     string  `json:"country"`
	CountryCode string  `json:"country_code"`
	Score       int     `json:"score"`
	Ping        int     `json:"ping"`
	Speed       int64   `json:"speed"`
	Sessions    int     `json:"sessions"`
	Uptime      float64 `json:"uptime,omitempty"`
	ISP         string  `json:"isp,omitempty"`
	Config      string  `json:"-"`
}

func nodeKey(n Node) string {
	return fmt.Sprintf("%s|%s|%d|%s", n.Source, n.IP, n.Port, n.Host)
}

type Pool struct {
	ID            string    `json:"id"`
	Ordinal       int       `json:"ordinal"`
	CountryCode   string    `json:"country_code"`
	Country       string    `json:"country"`
	SourceMode    string    `json:"source_mode"`
	Port          int       `json:"port"`
	User          string    `json:"user"`
	Pass          string    `json:"pass"`
	ActiveSource  string    `json:"active_source"`
	ActiveIP      string    `json:"active_ip"`
	ActivePort    int       `json:"active_port,omitempty"`
	ActiveHost    string    `json:"active_host"`
	ExitIP        string    `json:"exit_ip"`
	LatencyMS     int       `json:"latency_ms"`      // 实际通过当前出口访问外网的完整响应耗时；-1=尚未测得
	NodeLatencyMS int       `json:"node_latency_ms"` // 面板到当前上游节点的建连/RTT；-1=不可测
	IPType        string    `json:"ip_type,omitempty"`
	IPISP         string    `json:"ip_isp,omitempty"`
	IPASN         string    `json:"ip_asn,omitempty"`
	IPRisk        string    `json:"ip_risk,omitempty"`
	Status        string    `json:"status"`
	LastSwitch    time.Time `json:"last_switch"`
	FailCount     int       `json:"fail_count"`
	Error         string    `json:"error,omitempty"`
	Candidates    []Node    `json:"candidates"`
	ns            string
	ln            net.Listener
	ovpn          *exec.Cmd
	mu            sync.Mutex
	opMu          sync.Mutex
}

type PoolView struct {
	ID             string    `json:"id"`
	Ordinal        int       `json:"ordinal"`
	CountryCode    string    `json:"country_code"`
	Country        string    `json:"country"`
	SourceMode     string    `json:"source_mode"`
	Port           int       `json:"port"`
	User           string    `json:"user"`
	Pass           string    `json:"pass"`
	ActiveSource   string    `json:"active_source"`
	ExitIP         string    `json:"exit_ip"`
	LatencyMS      int       `json:"latency_ms"`
	NodeLatencyMS  int       `json:"node_latency_ms"`
	IPType         string    `json:"ip_type,omitempty"`
	IPISP          string    `json:"ip_isp,omitempty"`
	IPASN          string    `json:"ip_asn,omitempty"`
	IPRisk         string    `json:"ip_risk,omitempty"`
	Status         string    `json:"status"`
	LastSwitch     time.Time `json:"last_switch"`
	FailCount      int       `json:"fail_count"`
	Error          string    `json:"error,omitempty"`
	CandidateCount int       `json:"candidate_count"`
}

func (p *Pool) view() PoolView {
	p.mu.Lock()
	defer p.mu.Unlock()
	return PoolView{
		ID: p.ID, Ordinal: p.Ordinal, CountryCode: p.CountryCode, Country: p.Country, SourceMode: normalizeSource(p.SourceMode),
		Port: p.Port, User: p.User, Pass: p.Pass, ActiveSource: p.ActiveSource,
		ExitIP: p.ExitIP, LatencyMS: p.LatencyMS, NodeLatencyMS: p.NodeLatencyMS, IPType: p.IPType, IPISP: p.IPISP, IPASN: p.IPASN, IPRisk: p.IPRisk, Status: p.Status, LastSwitch: p.LastSwitch,
		FailCount: p.FailCount, Error: p.Error, CandidateCount: len(p.Candidates),
	}
}

type App struct {
	mu       sync.RWMutex
	Pools    map[string]*Pool `json:"pools"`
	Nodes    []Node           `json:"nodes"`
	Password string           `json:"-"`
	ServerIP string           `json:"-"`
	sessions map[string]time.Time
}

func main() {
	if os.Geteuid() != 0 {
		log.Fatal("需要 root 权限")
	}
	_ = os.MkdirAll(workDir, 0700)
	app := &App{Pools: map[string]*Pool{}, sessions: map[string]time.Time{}}
	app.ServerIP = detectPublicIPv4()
	app.Password = loadOrCreateSecret(filepath.Join(workDir, "password"), 12)
	if err := app.loadPools(); err != nil {
		log.Printf("load pools: %v", err)
	}
	// 节点源属于外部网络依赖，不阻塞 Web 面板启动。刷新完成后再恢复已有出口。
	go func() {
		if err := app.refreshSource(sourceAll); err != nil {
			log.Printf("initial refresh: %v", err)
		}
		app.restorePools()
	}()
	go app.refreshLoop()
	go app.healthLoop()

	mux := http.NewServeMux()
	mux.HandleFunc("/login", app.login)
	mux.HandleFunc("/api/status", app.auth(app.status))
	mux.HandleFunc("/api/regions", app.auth(app.regions))
	mux.HandleFunc("/api/pools", app.auth(app.pools))
	mux.HandleFunc("/api/pool/create", app.auth(app.createPool))
	mux.HandleFunc("/api/pool/delete", app.auth(app.deletePool))
	mux.HandleFunc("/api/pool/switch", app.auth(app.switchPool))
	mux.HandleFunc("/api/pool/source", app.auth(app.setPoolSource))
	mux.HandleFunc("/api/refresh", app.auth(app.refreshNow))
	mux.HandleFunc("/", app.auth(app.index))

	listenAddr := strings.TrimSpace(os.Getenv("XS5_LISTEN"))
	if listenAddr == "" {
		listenAddr = defaultListen
	}
	srv := &http.Server{Addr: listenAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		log.Printf("%s 正在停止并清理运行中的网络资源", appName)
		app.shutdown()
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	log.Printf("%s %s listening on %s", appName, appVersion, listenAddr)
	log.Printf("管理密码已加载，文件：%s", filepath.Join(workDir, "password"))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func detectPublicIPv4() string {
	client := &http.Client{Timeout: 4 * time.Second}
	if resp, err := client.Get("https://api.ipify.org"); err == nil {
		defer resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if b, err := io.ReadAll(io.LimitReader(resp.Body, 128)); err == nil {
				if ip := net.ParseIP(strings.TrimSpace(string(b))); ip != nil && ip.To4() != nil {
					return ip.String()
				}
			}
		}
	}
	addrs, _ := net.InterfaceAddrs()
	for _, addr := range addrs {
		ip, _, err := net.ParseCIDR(addr.String())
		if err == nil && ip.To4() != nil && !ip.IsLoopback() {
			return ip.String()
		}
	}
	return ""
}

func (a *App) shutdown() {
	a.mu.RLock()
	ps := make([]*Pool, 0, len(a.Pools))
	for _, p := range a.Pools {
		ps = append(ps, p)
	}
	a.mu.RUnlock()
	for _, p := range ps {
		p.stopRuntime()
	}
}

func (a *App) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("csp_session"); err == nil {
			a.mu.RLock()
			exp, ok := a.sessions[c.Value]
			a.mu.RUnlock()
			if ok && time.Now().Before(exp) {
				next(w, r)
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeJSON(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		io.WriteString(w, loginHTML)
	}
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		io.WriteString(w, loginHTML)
		return
	}
	if r.FormValue("password") != a.Password {
		writeJSON(w, 401, map[string]string{"error": "密码错误"})
		return
	}
	tok := randomHex(24)
	a.mu.Lock()
	a.sessions[tok] = time.Now().Add(12 * time.Hour)
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "csp_session", Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 43200})
	writeJSON(w, 200, map[string]string{"ok": "ok"})
}

func (a *App) index(w http.ResponseWriter, r *http.Request) { io.WriteString(w, indexHTML) }
func (a *App) status(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	counts := map[string]int{sourceVPNGate: 0, sourceProxio: 0}
	views := make([]PoolView, 0, len(a.Pools))
	for _, n := range a.Nodes {
		counts[n.Source]++
	}
	for _, p := range a.Pools {
		views = append(views, p.view())
	}
	poolCount := len(a.Pools)
	nodeCount := len(a.Nodes)
	a.mu.RUnlock()
	states := map[string]int{"up": 0, "busy": 0, "failed": 0}
	for _, p := range views {
		switch p.Status {
		case "up":
			states["up"]++
		case "starting", "switching", "restoring":
			states["busy"]++
		default:
			states["failed"]++
		}
	}
	writeJSON(w, 200, map[string]any{
		"name": appName, "version": appVersion, "pools": poolCount, "nodes": nodeCount,
		"sources": counts, "states": states, "server_ip": a.ServerIP,
	})
}

func sourceMatches(nodeSource, selected string) bool {
	selected = normalizeSource(selected)
	return selected == sourceAll || nodeSource == selected
}

func (a *App) regions(w http.ResponseWriter, r *http.Request) {
	selected := normalizeSource(r.URL.Query().Get("source"))
	a.mu.RLock()
	defer a.mu.RUnlock()
	m := map[string]map[string]any{}
	for _, n := range a.Nodes {
		if !sourceMatches(n.Source, selected) {
			continue
		}
		v := m[n.CountryCode]
		if v == nil {
			v = map[string]any{"country": n.Country, "count": 0}
			m[n.CountryCode] = v
		}
		v["count"] = v["count"].(int) + 1
	}
	writeJSON(w, 200, m)
}

func (a *App) pools(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	items := make(map[string]PoolView, len(a.Pools))
	for id, p := range a.Pools {
		items[id] = p.view()
	}
	a.mu.RUnlock()
	writeJSON(w, 200, items)
}

func (a *App) candidatesForLocked(country, sourceMode string) []Node {
	sourceMode = normalizeSource(sourceMode)
	var cands []Node
	for _, n := range a.Nodes {
		if n.CountryCode == country && sourceMatches(n.Source, sourceMode) {
			cands = append(cands, n)
		}
	}
	sortNodes(cands)
	return cands
}

func (a *App) rebuildCandidatesLocked() {
	for _, p := range a.Pools {
		cands := a.candidatesForLocked(p.CountryCode, p.SourceMode)
		p.mu.Lock()
		p.Candidates = cands
		p.mu.Unlock()
	}
}

func poolID(country string, ordinal int) string {
	return fmt.Sprintf("%s-%d", strings.ToUpper(strings.TrimSpace(country)), ordinal)
}

func (a *App) nextPoolOrdinalLocked(country string) int {
	maxOrdinal := 0
	for _, p := range a.Pools {
		if p.CountryCode == country && p.Ordinal > maxOrdinal {
			maxOrdinal = p.Ordinal
		}
	}
	return maxOrdinal + 1
}

func (a *App) poolByRequest(r *http.Request) *Pool {
	id := strings.TrimSpace(r.FormValue("id"))
	a.mu.RLock()
	defer a.mu.RUnlock()
	if id != "" {
		return a.Pools[id]
	}
	// 兼容旧前端/手工请求：只有该国家恰好一个出口时才按国家找到。
	cc := strings.ToUpper(strings.TrimSpace(r.FormValue("country")))
	if cc == "" {
		return nil
	}
	var found *Pool
	for _, p := range a.Pools {
		if p.CountryCode != cc {
			continue
		}
		if found != nil {
			return nil
		}
		found = p
	}
	return found
}

func (a *App) usedNodeKeys(country, excludeID string) map[string]bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	used := map[string]bool{}
	for id, other := range a.Pools {
		if id == excludeID || other.CountryCode != country {
			continue
		}
		other.mu.Lock()
		if other.Status == "up" && other.ActiveSource != "" {
			used[fmt.Sprintf("%s|%s|%d|%s", other.ActiveSource, other.ActiveIP, other.ActivePort, other.ActiveHost)] = true
		}
		other.mu.Unlock()
	}
	return used
}

func prioritizeUnused(cands []Node, used map[string]bool) []Node {
	if len(used) == 0 {
		return cands
	}
	out := make([]Node, 0, len(cands))
	for _, n := range cands {
		if !used[nodeKey(n)] {
			out = append(out, n)
		}
	}
	for _, n := range cands {
		if used[nodeKey(n)] {
			out = append(out, n)
		}
	}
	return out
}

func (a *App) createPool(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "POST only"})
		return
	}
	cc := strings.ToUpper(strings.TrimSpace(r.FormValue("country")))
	sourceMode := normalizeSource(r.FormValue("source"))
	if cc == "" {
		writeJSON(w, 400, map[string]string{"error": "country required"})
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	cands := a.candidatesForLocked(cc, sourceMode)
	if len(cands) == 0 {
		writeJSON(w, 404, map[string]string{"error": "当前节点源没有这个国家的可用候选"})
		return
	}
	country := cands[0].Country
	port, err := a.freePoolPortLocked()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	ordinal := a.nextPoolOrdinalLocked(cc)
	id := poolID(cc, ordinal)
	p := &Pool{
		ID: id, Ordinal: ordinal, CountryCode: cc, Country: country, SourceMode: sourceMode,
		Port: port, User: "fo" + randomHex(3), Pass: randomHex(8),
		Candidates: cands, LatencyMS: -1, NodeLatencyMS: -1, Status: "starting", ns: fmt.Sprintf("csp%d", port),
	}
	a.Pools[id] = p
	_ = a.savePoolsLocked()
	go a.switchNext(p, "starting")
	writeJSON(w, 200, p.view())
}

func (a *App) deletePool(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "POST only"})
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	if id == "" {
		writeJSON(w, 400, map[string]string{"error": "id required"})
		return
	}
	a.mu.Lock()
	p := a.Pools[id]
	delete(a.Pools, id)
	_ = a.savePoolsLocked()
	a.mu.Unlock()
	if p != nil {
		p.stop()
	}
	writeJSON(w, 200, map[string]string{"ok": "deleted"})
}

func (a *App) switchPool(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "POST only"})
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	a.mu.RLock()
	p := a.Pools[id]
	a.mu.RUnlock()
	if p == nil {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	p.mu.Lock()
	p.Status = "switching"
	p.Error = ""
	p.mu.Unlock()
	go a.switchNext(p, "switching")
	writeJSON(w, 200, map[string]string{"ok": "switching"})
}

func (a *App) setPoolSource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "POST only"})
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	sourceMode := normalizeSource(r.FormValue("source"))
	a.mu.Lock()
	p := a.Pools[id]
	if p == nil {
		a.mu.Unlock()
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	cands := a.candidatesForLocked(p.CountryCode, sourceMode)
	if len(cands) == 0 {
		a.mu.Unlock()
		writeJSON(w, 404, map[string]string{"error": "当前节点源没有这个国家的可用候选"})
		return
	}
	p.mu.Lock()
	p.SourceMode = sourceMode
	p.Candidates = cands
	p.Status = "switching"
	p.Error = ""
	p.mu.Unlock()
	if err := a.savePoolsLocked(); err != nil {
		a.mu.Unlock()
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	a.mu.Unlock()
	go a.switchNext(p, "switching")
	writeJSON(w, 200, map[string]string{"ok": "switching"})
}

func (a *App) refreshNow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "POST only"})
		return
	}
	selected := normalizeSource(r.FormValue("source"))
	if err := a.refreshSource(selected); err != nil {
		writeJSON(w, 502, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "refreshed", "source": selected})
}

func (a *App) refreshLoop() {
	for {
		time.Sleep(15 * time.Minute)
		if err := a.refreshSource(sourceAll); err != nil {
			log.Printf("refresh: %v", err)
		}
	}
}

func (a *App) refreshSource(selected string) error {
	selected = normalizeSource(selected)
	type result struct {
		source string
		nodes  []Node
		err    error
	}
	fetch := func(source string) result {
		switch source {
		case sourceProxio:
			nodes, err := fetchProxioNodes()
			return result{source: sourceProxio, nodes: nodes, err: err}
		default:
			nodes, err := fetchVPNGateNodes()
			return result{source: sourceVPNGate, nodes: nodes, err: err}
		}
	}

	var results []result
	if selected == sourceAll {
		ch := make(chan result, 2)
		go func() { ch <- fetch(sourceVPNGate) }()
		go func() { ch <- fetch(sourceProxio) }()
		results = append(results, <-ch, <-ch)
	} else {
		results = append(results, fetch(selected))
	}

	succeeded := 0
	var errs []string
	for _, res := range results {
		if res.err != nil {
			errs = append(errs, sourceLabel(res.source)+": "+res.err.Error())
			continue
		}
		a.replaceSourceNodes(res.source, res.nodes)
		succeeded++
	}
	if succeeded == 0 && len(errs) > 0 {
		return errors.New(strings.Join(errs, "；"))
	}
	for _, e := range errs {
		log.Printf("refresh partial failure: %s", e)
	}
	return nil
}

func (a *App) replaceSourceNodes(source string, fresh []Node) {
	a.mu.Lock()
	merged := make([]Node, 0, len(a.Nodes)+len(fresh))
	for _, n := range a.Nodes {
		if n.Source != source {
			merged = append(merged, n)
		}
	}
	merged = append(merged, fresh...)
	// 同源同 IP:port 去重，避免镜像偶发重复项。
	seen := map[string]bool{}
	out := merged[:0]
	for _, n := range merged {
		k := nodeKey(n)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, n)
	}
	sortNodes(out)
	a.Nodes = out
	a.rebuildCandidatesLocked()
	countrySet := map[string]struct{}{}
	for _, n := range fresh {
		countrySet[n.CountryCode] = struct{}{}
	}
	a.mu.Unlock()
	log.Printf("%s refreshed: %d nodes, %d countries", sourceLabel(source), len(fresh), len(countrySet))
}

func fetchVPNGateNodes() ([]Node, error) {
	cli := &http.Client{Timeout: 20 * time.Second}
	resp, err := cli.Get(vpngateCSV)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	rd := csv.NewReader(bufio.NewReader(resp.Body))
	rd.FieldsPerRecord = -1
	var nodes []Node
	for {
		rec, err := rd.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			continue
		}
		if len(rec) < 15 || strings.HasPrefix(rec[0], "*") {
			continue
		}
		ping, _ := strconv.Atoi(rec[3])
		speed, _ := strconv.ParseInt(rec[4], 10, 64)
		ses, _ := strconv.Atoi(rec[7])
		score, _ := strconv.Atoi(rec[2])
		raw, err := base64.StdEncoding.DecodeString(rec[14])
		if err != nil {
			continue
		}
		cc := strings.ToUpper(strings.TrimSpace(rec[6]))
		if len(cc) != 2 || net.ParseIP(strings.TrimSpace(rec[1])) == nil {
			continue
		}
		country := displayCountryName(cc, strings.TrimSpace(rec[5]))
		nodes = append(nodes, Node{
			Host: rec[0], IP: strings.TrimSpace(rec[1]), Source: sourceVPNGate, Protocol: "openvpn",
			Country: country, CountryCode: cc, Score: score, Ping: ping, Speed: speed, Sessions: ses, Config: string(raw),
		})
	}
	sortNodes(nodes)
	return nodes, nil
}

// Proxio 公共列表的 all.json 字段会随数据版本增减。这里用宽松解析，
// 兼容顶层数组以及 {"proxies": [...]} 两种结构，并仅取我们关心的字段。
func fetchProxioNodes() ([]Node, error) {
	root, endpoint, err := fetchProxioJSON()
	if err != nil {
		return nil, err
	}
	rows := proxyRows(root)
	if len(rows) == 0 {
		return nil, fmt.Errorf("Proxio 返回的数据中没有代理条目（%s）", endpoint)
	}

	nodes := parseProxioRows(rows)
	if len(nodes) == 0 {
		return nil, errors.New("Proxio 节点经过质量门槛后为空")
	}
	return nodes, nil
}

func parseProxioRows(rows []map[string]any) []Node {
	var nodes []Node
	for _, row := range rows {
		protocols := stringSliceAny(row["protocols"])
		if len(protocols) == 0 {
			if p := stringAny(row, "protocol", "type"); p != "" {
				protocols = []string{p}
			}
		}
		isSOCKS5 := false
		for _, p := range protocols {
			if strings.EqualFold(strings.TrimSpace(p), "socks5") {
				isSOCKS5 = true
				break
			}
		}
		if !isSOCKS5 {
			continue
		}

		ip := strings.TrimSpace(stringAny(row, "ip", "host", "address"))
		port, ok := intAny(row, "port")
		if net.ParseIP(ip) == nil || !ok || port < 1 || port > 65535 {
			continue
		}

		countryRaw := strings.TrimSpace(stringAny(row, "country", "country_name", "countryName"))
		cc := strings.ToUpper(strings.TrimSpace(stringAny(row, "country_code", "countryCode", "cc", "iso_code", "iso")))
		if len(cc) != 2 {
			cc = countryCodeFromName(countryRaw)
		}
		if len(cc) != 2 {
			continue
		}

		reliability, hasReliability := floatAny(row, "reliability", "score")
		uptime, hasUptime := floatAny(row, "uptime", "uptime_ratio", "uptime_percent")
		if hasUptime && uptime > 1 && uptime <= 100 {
			uptime /= 100
		}
		latencyS, hasLatencyS := floatAny(row, "latency_s", "latency")
		if latencyMS, ok := floatAny(row, "latency_ms"); ok {
			latencyS = latencyMS / 1000
			hasLatencyS = true
		}
		lastResults := strings.TrimSpace(stringAny(row, "last_results", "results"))
		anonymity := strings.ToLower(strings.TrimSpace(stringAny(row, "anonymity")))

		// 公共代理质量门槛：宁愿数量少一些，也不把明显不稳定的条目灌入候选池。
		// Proxio 的 reliability 为 0-100、uptime 为 0-1、latency_s 为秒。
		if !hasReliability && !hasUptime {
			continue
		}
		if hasReliability && reliability < 80 {
			continue
		}
		if hasUptime && uptime < 0.75 {
			continue
		}
		if hasLatencyS && (latencyS <= 0 || latencyS > 2.5) {
			continue
		}
		if anonymity == "transparent" {
			continue
		}
		if len(lastResults) >= 5 {
			total, good := 0, 0
			for _, c := range lastResults {
				if c == '0' || c == '1' {
					total++
					if c == '1' {
						good++
					}
				}
			}
			if total >= 5 && float64(good)/float64(total) < 0.70 {
				continue
			}
		}

		ping := 0
		if hasLatencyS {
			ping = int(latencyS*1000 + 0.5)
		}
		score := 0
		if hasReliability {
			score = int(reliability + 0.5)
		} else if hasUptime {
			score = int(uptime*100 + 0.5)
		}
		country := displayCountryName(cc, countryRaw)
		nodes = append(nodes, Node{
			Host: net.JoinHostPort(ip, strconv.Itoa(port)), IP: ip, Port: port,
			Source: sourceProxio, Protocol: "socks5", Country: country, CountryCode: cc,
			Score: score, Ping: ping, Uptime: uptime, ISP: strings.TrimSpace(stringAny(row, "isp", "org", "organization")),
		})
	}
	sortNodes(nodes)
	return nodes
}

func fetchProxioJSON() (any, string, error) {
	urls := []string{proxioJSON, proxioMirrorJSON, proxioRawJSON}
	var errs []string
	for _, endpoint := range urls {
		cli := &http.Client{Timeout: 45 * time.Second}
		req, err := http.NewRequest(http.MethodGet, endpoint, nil)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		req.Header.Set("User-Agent", "Xs5/v1.0.0")
		req.Header.Set("Accept", "application/json")
		resp, err := cli.Do(req)
		if err != nil {
			errs = append(errs, endpoint+": "+err.Error())
			continue
		}
		var root any
		if resp.StatusCode == http.StatusOK {
			decErr := json.NewDecoder(io.LimitReader(resp.Body, 20<<20)).Decode(&root)
			resp.Body.Close()
			if decErr == nil {
				return root, endpoint, nil
			}
			errs = append(errs, endpoint+": 解析 JSON 失败: "+decErr.Error())
			continue
		}
		status := resp.StatusCode
		resp.Body.Close()
		errs = append(errs, fmt.Sprintf("%s: HTTP %d", endpoint, status))
	}
	return nil, "", errors.New(strings.Join(errs, "；"))
}

func proxyRows(root any) []map[string]any {
	var raw []any
	switch v := root.(type) {
	case []any:
		raw = v
	case map[string]any:
		for _, k := range []string{"proxies", "data", "items"} {
			if x, ok := v[k].([]any); ok {
				raw = x
				break
			}
		}
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func stringAny(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			switch x := v.(type) {
			case string:
				return x
			case json.Number:
				return x.String()
			}
		}
	}
	return ""
}

func stringSliceAny(v any) []string {
	switch x := v.(type) {
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return x
	case string:
		if x == "" {
			return nil
		}
		parts := strings.FieldsFunc(x, func(r rune) bool { return r == ',' || r == ';' || r == ' ' })
		return parts
	default:
		return nil
	}
}

func floatAny(m map[string]any, keys ...string) (float64, bool) {
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		switch x := v.(type) {
		case float64:
			return x, true
		case json.Number:
			f, err := x.Float64()
			return f, err == nil
		case string:
			f, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(x, "%")), 64)
			return f, err == nil
		}
	}
	return 0, false
}

func intAny(m map[string]any, keys ...string) (int, bool) {
	f, ok := floatAny(m, keys...)
	if !ok {
		return 0, false
	}
	return int(f + 0.5), true
}

func countryCodeFromName(name string) string {
	key := strings.ToLower(strings.TrimSpace(name))
	key = strings.ReplaceAll(key, ".", "")
	aliases := map[string]string{
		"japan": "JP", "south korea": "KR", "korea, republic of": "KR", "republic of korea": "KR", "united states": "US", "united states of america": "US",
		"taiwan": "TW", "singapore": "SG", "hong kong": "HK", "thailand": "TH", "vietnam": "VN", "viet nam": "VN", "malaysia": "MY", "indonesia": "ID",
		"philippines": "PH", "india": "IN", "australia": "AU", "new zealand": "NZ", "canada": "CA", "mexico": "MX", "brazil": "BR", "argentina": "AR",
		"chile": "CL", "colombia": "CO", "united kingdom": "GB", "great britain": "GB", "germany": "DE", "france": "FR", "netherlands": "NL", "italy": "IT",
		"spain": "ES", "sweden": "SE", "norway": "NO", "finland": "FI", "denmark": "DK", "switzerland": "CH", "austria": "AT", "poland": "PL", "czechia": "CZ",
		"czech republic": "CZ", "romania": "RO", "bulgaria": "BG", "ukraine": "UA", "turkey": "TR", "türkiye": "TR", "israel": "IL", "united arab emirates": "AE",
		"saudi arabia": "SA", "south africa": "ZA", "russia": "RU", "russian federation": "RU", "china": "CN", "bangladesh": "BD", "pakistan": "PK", "nepal": "NP",
		"sri lanka": "LK", "cambodia": "KH", "laos": "LA", "myanmar": "MM", "mongolia": "MN", "kazakhstan": "KZ", "uzbekistan": "UZ", "georgia": "GE",
		"greece": "GR", "portugal": "PT", "belgium": "BE", "ireland": "IE", "iceland": "IS", "luxembourg": "LU", "slovakia": "SK", "slovenia": "SI",
		"croatia": "HR", "serbia": "RS", "hungary": "HU", "lithuania": "LT", "latvia": "LV", "estonia": "EE", "moldova": "MD", "cyprus": "CY",
		"egypt": "EG", "morocco": "MA", "tunisia": "TN", "algeria": "DZ", "kenya": "KE", "nigeria": "NG", "ghana": "GH", "ethiopia": "ET",
		"peru": "PE", "ecuador": "EC", "venezuela": "VE", "uruguay": "UY", "paraguay": "PY", "bolivia": "BO", "costa rica": "CR", "panama": "PA",
		"dominican republic": "DO", "puerto rico": "PR", "guatemala": "GT", "el salvador": "SV", "honduras": "HN", "nicaragua": "NI",
	}
	return aliases[key]
}

func sortNodes(n []Node) {
	sort.SliceStable(n, func(i, j int) bool {
		// 有延迟数据的排在前面，随后按延迟、在线率、速度排序。
		if n[i].Ping <= 0 && n[j].Ping > 0 {
			return false
		}
		if n[j].Ping <= 0 && n[i].Ping > 0 {
			return true
		}
		if n[i].Ping != n[j].Ping {
			return n[i].Ping < n[j].Ping
		}
		if n[i].Uptime != n[j].Uptime {
			return n[i].Uptime > n[j].Uptime
		}
		if n[i].Speed != n[j].Speed {
			return n[i].Speed > n[j].Speed
		}
		return nodeKey(n[i]) < nodeKey(n[j])
	})
}

func (a *App) healthLoop() {
	for {
		time.Sleep(12 * time.Second)
		a.mu.RLock()
		ps := make([]*Pool, 0, len(a.Pools))
		for _, p := range a.Pools {
			ps = append(ps, p)
		}
		a.mu.RUnlock()
		for _, p := range ps {
			p.mu.Lock()
			if p.Status != "up" {
				p.mu.Unlock()
				continue
			}
			expected := p.ExitIP
			p.mu.Unlock()

			ip, latency, err := p.probeCurrent()
			p.mu.Lock()
			if err != nil || ip != expected {
				p.FailCount++
			} else {
				p.FailCount = 0
				p.LatencyMS = latency
			}
			fails := p.FailCount
			p.mu.Unlock()
			if fails >= 2 {
				go a.switchNext(p, "switching")
			}
		}
	}
}

const switchAttemptWindow = 90 * time.Second

func (a *App) switchNext(p *Pool, phase string) {
	p.opMu.Lock()
	defer p.opMu.Unlock()

	p.mu.Lock()
	cands := append([]Node(nil), p.Candidates...)
	p.mu.Unlock()
	cands = prioritizeUnused(cands, a.usedNodeKeys(p.CountryCode, p.ID))
	p.mu.Lock()
	p.Candidates = append([]Node(nil), cands...)
	curKey := fmt.Sprintf("%s|%s|%d|%s", p.ActiveSource, p.ActiveIP, p.ActivePort, p.ActiveHost)
	if phase == "" {
		phase = "switching"
	}
	p.Status = phase
	p.Error = ""
	p.LatencyMS = -1
	p.NodeLatencyMS = -1
	p.IPType = ""
	p.IPISP = ""
	p.IPASN = ""
	p.IPRisk = ""
	p.mu.Unlock()
	if len(cands) == 0 {
		p.mu.Lock()
		p.Status = "failed"
		p.Error = "没有可用候选节点"
		p.mu.Unlock()
		return
	}
	idx := 0
	for i, n := range cands {
		if nodeKey(n) == curKey {
			idx = i + 1
			break
		}
	}

	deadline := time.Now().Add(switchAttemptWindow)
	var lastErr error
	attempted := 0
	timedOut := false
	for off := 0; off < len(cands); off++ {
		if time.Now().After(deadline) {
			timedOut = true
			break
		}
		i := (idx + off) % len(cands)
		attempted++
		if err := a.activate(p, i, phase, deadline); err == nil {
			return
		} else {
			lastErr = err
			log.Printf("%s/%s candidate %s %s failed (%d/%d): %v", p.CountryCode, p.ID, sourceLabel(cands[i].Source), cands[i].Host, attempted, len(cands), err)
		}
	}
	p.stopRuntime()
	p.mu.Lock()
	p.Status = "failed"
	switch {
	case timedOut || time.Now().After(deadline):
		if lastErr != nil {
			p.Error = fmt.Sprintf("90 秒内已尝试 %d/%d 个候选，仍未找到可用节点；最后错误：%v", attempted, len(cands), lastErr)
		} else {
			p.Error = fmt.Sprintf("90 秒内已尝试 %d/%d 个候选，仍未找到可用节点", attempted, len(cands))
		}
	case attempted >= len(cands):
		if lastErr != nil {
			p.Error = fmt.Sprintf("已尝试全部 %d 个候选，均不可用；最后错误：%v", attempted, lastErr)
		} else {
			p.Error = fmt.Sprintf("已尝试全部 %d 个候选，均不可用", attempted)
		}
	case lastErr != nil:
		p.Error = lastErr.Error()
	default:
		p.Error = "候选节点均不可用"
	}
	p.mu.Unlock()
}

func (a *App) activate(p *Pool, idx int, phase string, operationDeadline time.Time) error {
	p.mu.Lock()
	if idx >= len(p.Candidates) {
		p.mu.Unlock()
		return errors.New("candidate out of range")
	}
	node := p.Candidates[idx]
	if phase == "" {
		phase = "starting"
	}
	p.Status = phase
	p.Error = ""
	p.mu.Unlock()

	p.stopRuntime()
	switch node.Source {
	case sourceProxio:
		return a.activateProxio(p, node)
	default:
		return a.activateVPNGate(p, node, operationDeadline)
	}
}

func (a *App) activateVPNGate(p *Pool, node Node, operationDeadline time.Time) error {
	if err := setupNS(p.ns, p.Port); err != nil {
		return fmt.Errorf("创建网络隔离失败: %w", err)
	}
	cfg := filepath.Join(workDir, p.ns+".ovpn")
	if err := os.WriteFile(cfg, []byte(node.Config), 0600); err != nil {
		return fmt.Errorf("写 OpenVPN 配置失败: %w", err)
	}
	authPath := filepath.Join(workDir, "auth.txt")
	if err := os.WriteFile(authPath, []byte("vpn\nvpn\n"), 0600); err != nil {
		return fmt.Errorf("写 OpenVPN 认证文件失败: %w", err)
	}
	logPath := filepath.Join(workDir, p.ns+".openvpn.log")
	_ = os.Remove(logPath)
	cmd := exec.Command("ip", "netns", "exec", p.ns, "openvpn",
		"--config", cfg,
		"--auth-user-pass", authPath,
		"--auth-nocache",
		"--dev", "tun0",
		"--connect-timeout", "15",
		"--connect-retry-max", "1",
		"--data-ciphers", "AES-128-CBC:AES-256-GCM:AES-128-GCM:CHACHA20-POLY1305",
		"--data-ciphers-fallback", "AES-128-CBC",
		"--verb", "3",
		"--log", logPath,
	)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 OpenVPN 失败: %w", err)
	}
	p.mu.Lock()
	p.ovpn = cmd
	p.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	readyDeadline := time.Now().Add(35 * time.Second)
	if !operationDeadline.IsZero() && operationDeadline.Before(readyDeadline) {
		readyDeadline = operationDeadline
	}
	ready := false
	for time.Now().Before(readyDeadline) {
		select {
		case err := <-done:
			return fmt.Errorf("OpenVPN 提前退出: %v; %s", err, tailFile(logPath, 8))
		default:
		}
		if out, err := exec.Command("ip", "netns", "exec", p.ns, "ip", "-4", "addr", "show", "tun0").Output(); err == nil && strings.Contains(string(out), "inet ") {
			ready = true
			break
		}
		time.Sleep(time.Second)
	}
	if !ready {
		if !operationDeadline.IsZero() && !time.Now().Before(operationDeadline) {
			return fmt.Errorf("本轮切换时间已到，等待 tun0 未完成; %s", tailFile(logPath, 8))
		}
		return fmt.Errorf("等待 tun0 就绪超时; %s", tailFile(logPath, 8))
	}
	exit, latency, err := probeVPNGate(p.ns)
	if err != nil {
		return fmt.Errorf("隧道已建立但出口检测失败: %w; %s", err, tailFile(logPath, 5))
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", p.Port))
	if err != nil {
		return fmt.Errorf("监听 SOCKS5 端口 %d 失败: %w", p.Port, err)
	}
	nodeLatency := measureNodeLatency(node)
	p.mu.Lock()
	p.ln = ln
	p.ActiveSource = sourceVPNGate
	p.ActiveIP = node.IP
	p.ActivePort = 0
	p.ActiveHost = node.Host
	p.ExitIP = exit
	p.LatencyMS = latency
	p.NodeLatencyMS = nodeLatency
	p.Status = "up"
	p.Error = ""
	p.LastSwitch = time.Now()
	p.FailCount = 0
	p.mu.Unlock()
	go serveSOCKS(ln, p)
	go enrichPoolIPProfile(p, exit)
	log.Printf("%s/%s up: SOCKS5 :%d -> VPN Gate %s (%s) -> %s (%dms)", p.CountryCode, p.ID, p.Port, node.Host, node.IP, exit, latency)
	return nil
}

func (a *App) activateProxio(p *Pool, node Node) error {
	exit, latency, err := probeProxyNode(node)
	if err != nil {
		return fmt.Errorf("Proxio SOCKS5 检测失败: %w", err)
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", p.Port))
	if err != nil {
		return fmt.Errorf("监听 SOCKS5 端口 %d 失败: %w", p.Port, err)
	}
	nodeLatency := measureNodeLatency(node)
	p.mu.Lock()
	p.ln = ln
	p.ActiveSource = sourceProxio
	p.ActiveIP = node.IP
	p.ActivePort = node.Port
	p.ActiveHost = node.Host
	p.ExitIP = exit
	p.LatencyMS = latency
	p.NodeLatencyMS = nodeLatency
	p.Status = "up"
	p.Error = ""
	p.LastSwitch = time.Now()
	p.FailCount = 0
	p.mu.Unlock()
	go serveSOCKS(ln, p)
	go enrichPoolIPProfile(p, exit)
	log.Printf("%s/%s up: SOCKS5 :%d -> Proxio %s -> %s (%dms)", p.CountryCode, p.ID, p.Port, node.Host, exit, latency)
	return nil
}

func tailFile(path string, lines int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "无 OpenVPN 日志"
	}
	parts := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return strings.Join(parts, " | ")
}

func (p *Pool) probeCurrent() (string, int, error) {
	p.mu.Lock()
	source := p.ActiveSource
	ip := p.ActiveIP
	port := p.ActivePort
	ns := p.ns
	p.mu.Unlock()
	switch source {
	case sourceProxio:
		return probeProxyNode(Node{Source: sourceProxio, IP: ip, Port: port})
	case sourceVPNGate:
		return probeVPNGate(ns)
	default:
		return "", -1, errors.New("当前没有活动出口")
	}
}

// probeVPNGate 测的是实际通过 VPN 隧道访问外网的耗时，不再依赖 ICMP ping。
func probeVPNGate(ns string) (string, int, error) {
	start := time.Now()
	out, err := exec.Command("ip", "netns", "exec", ns, "curl", "-4", "-fsS", "--max-time", "8", "https://api.ipify.org").Output()
	latency := int(time.Since(start).Milliseconds())
	if err != nil {
		return "", -1, err
	}
	s := strings.TrimSpace(string(out))
	if net.ParseIP(s) == nil {
		return "", -1, fmt.Errorf("出口 IP 返回异常: %q", s)
	}
	if latency < 1 {
		latency = 1
	}
	return s, latency, nil
}

func probeProxyNode(node Node) (string, int, error) {
	if net.ParseIP(node.IP) == nil || node.Port < 1 || node.Port > 65535 {
		return "", -1, errors.New("上游 SOCKS5 地址无效")
	}
	client := proxyHTTPClient(node, 10*time.Second)
	start := time.Now()
	resp, err := client.Get("https://api.ipify.org")
	latency := int(time.Since(start).Milliseconds())
	if err != nil {
		return "", -1, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", -1, fmt.Errorf("出口检测 HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 128))
	if err != nil {
		return "", -1, err
	}
	ip := strings.TrimSpace(string(b))
	if net.ParseIP(ip) == nil {
		return "", -1, fmt.Errorf("出口 IP 返回异常: %q", ip)
	}
	if latency < 1 {
		latency = 1
	}
	return ip, latency, nil
}

// measureNodeLatency 测的是面板服务器到上游节点本身的延迟，而不是完整出口 HTTP 请求。
// Proxio 使用 TCP 建连；VPN Gate 的 TCP 配置优先测 OpenVPN remote 端口，UDP 配置则尝试 ICMP。
func measureNodeLatency(node Node) int {
	if net.ParseIP(node.IP) == nil {
		return -1
	}
	if node.Source == sourceProxio && node.Port > 0 {
		return tcpConnectLatency(node.IP, node.Port, 3*time.Second)
	}
	if node.Source == sourceVPNGate {
		host, port, proto := openVPNRemote(node.Config)
		if host == "" {
			host = node.IP
		}
		if port > 0 && strings.HasPrefix(strings.ToLower(proto), "tcp") {
			if ms := tcpConnectLatency(host, port, 3*time.Second); ms >= 0 {
				return ms
			}
		}
		return icmpLatency(node.IP)
	}
	return -1
}

func tcpConnectLatency(host string, port int, timeout time.Duration) int {
	if port < 1 || port > 65535 {
		return -1
	}
	start := time.Now()
	c, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), timeout)
	if err != nil {
		return -1
	}
	_ = c.Close()
	ms := int(time.Since(start).Milliseconds())
	if ms < 1 {
		ms = 1
	}
	return ms
}

func openVPNRemote(cfg string) (host string, port int, proto string) {
	for _, line := range strings.Split(cfg, "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) == 0 || strings.HasPrefix(f[0], "#") || strings.HasPrefix(f[0], ";") {
			continue
		}
		switch strings.ToLower(f[0]) {
		case "proto":
			if len(f) > 1 {
				proto = strings.ToLower(f[1])
			}
		case "remote":
			if len(f) >= 3 && host == "" {
				host = f[1]
				port, _ = strconv.Atoi(f[2])
				if len(f) >= 4 {
					proto = strings.ToLower(f[3])
				}
			}
		}
	}
	return
}

func icmpLatency(ip string) int {
	out, err := exec.Command("ping", "-n", "-c", "1", "-W", "2", ip).CombinedOutput()
	if err != nil {
		return -1
	}
	t := string(out)
	i := strings.Index(t, "time=")
	if i < 0 {
		return -1
	}
	v := t[i+5:]
	j := strings.IndexAny(v, " ms\r\n")
	if j > 0 {
		v = v[:j]
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return -1
	}
	ms := int(f + 0.5)
	if ms < 1 {
		ms = 1
	}
	return ms
}

type ipProfile struct {
	Type string
	ISP  string
	ASN  string
	Risk string
}

// enrichPoolIPProfile 只在出口建立/切换成功后执行，避免健康检查高频消耗第三方查询额度。
func enrichPoolIPProfile(p *Pool, ip string) {
	prof, err := lookupIPProfile(ip)
	if err != nil {
		log.Printf("IP 属性检测 %s 失败: %v", ip, err)
		return
	}
	p.mu.Lock()
	if p.ExitIP == ip { // 防止慢查询把已经切换后的新出口覆盖掉
		p.IPType = prof.Type
		p.IPISP = prof.ISP
		p.IPASN = prof.ASN
		p.IPRisk = prof.Risk
	}
	p.mu.Unlock()
}

// lookupIPProfile 使用 ip-api 的公开查询字段识别 hosting/mobile/proxy，失败时退回 ipwho.is 获取 ASN/ISP，
// 再以组织名称做保守分类。结果是辅助判断，不应视为绝对准确的住宅证明。
func lookupIPProfile(ip string) (ipProfile, error) {
	if net.ParseIP(ip) == nil {
		return ipProfile{}, errors.New("IP 无效")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	url := "http://ip-api.com/json/" + ip + "?fields=status,message,query,isp,org,as,asname,mobile,proxy,hosting"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "Xs5/"+appVersion)
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		defer resp.Body.Close()
		var v struct {
			Status  string `json:"status"`
			Message string `json:"message"`
			ISP     string `json:"isp"`
			Org     string `json:"org"`
			AS      string `json:"as"`
			ASName  string `json:"asname"`
			Mobile  *bool  `json:"mobile"`
			Proxy   *bool  `json:"proxy"`
			Hosting *bool  `json:"hosting"`
		}
		if resp.StatusCode == 200 && json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&v) == nil && v.Status == "success" {
			prof := profileFromSignals(v.ISP, firstNonEmpty(v.AS, v.ASName), v.Org, v.Hosting, v.Mobile, v.Proxy)
			return prof, nil
		}
	}
	return lookupIPProfileFallback(ip)
}

func lookupIPProfileFallback(ip string) (ipProfile, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://ipwho.is/"+ip, nil)
	req.Header.Set("User-Agent", "Xs5/"+appVersion)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ipProfile{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ipProfile{}, fmt.Errorf("IP 属性 HTTP %d", resp.StatusCode)
	}
	var v struct {
		Success    bool `json:"success"`
		Connection struct {
			ASN int    `json:"asn"`
			Org string `json:"org"`
			ISP string `json:"isp"`
		} `json:"connection"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&v); err != nil {
		return ipProfile{}, err
	}
	asn := ""
	if v.Connection.ASN > 0 {
		asn = fmt.Sprintf("AS%d", v.Connection.ASN)
	}
	if v.Connection.Org != "" {
		asn = strings.TrimSpace(asn + " " + v.Connection.Org)
	}
	return profileFromSignals(v.Connection.ISP, asn, v.Connection.Org, nil, nil, nil), nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func profileFromSignals(isp, asn, org string, hosting, mobile, proxy *bool) ipProfile {
	text := strings.ToLower(strings.Join([]string{isp, asn, org}, " "))
	typeName := "未知"
	if hosting != nil && *hosting {
		typeName = "机房 IP"
	} else if mobile != nil && *mobile {
		typeName = "移动网络"
	} else if containsAny(text, "amazon", "aws", "azure", "microsoft", "google cloud", "digitalocean", "linode", "akamai", "cloudflare", "leaseweb", "hetzner", "ovh", "vultr", "choopa", "contabo", "alibaba cloud", "aliyun", "tencent cloud", "huawei cloud", "hosting", "datacenter", "data center", "server") {
		typeName = "机房 IP"
	} else if containsAny(text, "university", "college", "education", "academy", "school") {
		typeName = "教育/机构"
	} else if hosting != nil && !*hosting {
		typeName = "住宅/ISP"
	} else if isp != "" || org != "" {
		typeName = "ISP/未知"
	}
	risk := ""
	if proxy != nil && *proxy {
		risk = "代理标记"
	}
	return ipProfile{Type: typeName, ISP: firstNonEmpty(isp, org), ASN: asn, Risk: risk}
}

func containsAny(s string, vals ...string) bool {
	for _, v := range vals {
		if strings.Contains(s, v) {
			return true
		}
	}
	return false
}

func proxyHTTPClient(node Node, timeout time.Duration) *http.Client {
	upstream := net.JoinHostPort(node.IP, strconv.Itoa(node.Port))
	tr := &http.Transport{
		Proxy:             nil,
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialSOCKS5Context(ctx, upstream, address)
		},
		TLSHandshakeTimeout: 5 * time.Second,
	}
	return &http.Client{Transport: tr, Timeout: timeout}
}

func dialSOCKS5Context(ctx context.Context, upstream, target string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 8 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", upstream)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (net.Conn, error) {
		_ = conn.Close()
		return nil, err
	}
	deadline := time.Now().Add(10 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)

	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		return fail(err)
	}
	var method [2]byte
	if _, err := io.ReadFull(conn, method[:]); err != nil {
		return fail(err)
	}
	if method[0] != 5 || method[1] != 0 {
		return fail(fmt.Errorf("上游 SOCKS5 不支持免认证连接"))
	}

	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		return fail(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fail(errors.New("目标端口无效"))
	}
	req := []byte{5, 1, 0}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			req = append(req, 1)
			req = append(req, v4...)
		} else {
			req = append(req, 4)
			req = append(req, ip.To16()...)
		}
	} else {
		if len(host) == 0 || len(host) > 255 {
			return fail(errors.New("目标域名长度无效"))
		}
		req = append(req, 3, byte(len(host)))
		req = append(req, host...)
	}
	req = append(req, byte(port>>8), byte(port))
	if _, err := conn.Write(req); err != nil {
		return fail(err)
	}
	var hdr [4]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		return fail(err)
	}
	if hdr[0] != 5 || hdr[1] != 0 {
		return fail(fmt.Errorf("上游 SOCKS5 CONNECT 失败，代码 %d", hdr[1]))
	}
	var skip int
	switch hdr[3] {
	case 1:
		skip = 4
	case 4:
		skip = 16
	case 3:
		var n [1]byte
		if _, err := io.ReadFull(conn, n[:]); err != nil {
			return fail(err)
		}
		skip = int(n[0])
	default:
		return fail(errors.New("上游 SOCKS5 返回未知地址类型"))
	}
	buf := make([]byte, skip+2)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return fail(err)
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func (p *Pool) stopRuntime() {
	p.mu.Lock()
	if p.ln != nil {
		_ = p.ln.Close()
		p.ln = nil
	}
	if p.ovpn != nil && p.ovpn.Process != nil {
		_ = p.ovpn.Process.Kill()
		p.ovpn = nil
	}
	p.mu.Unlock()
	teardownNS(p.ns, p.Port)
}
func (p *Pool) stop() { p.stopRuntime(); p.mu.Lock(); p.Status = "stopped"; p.mu.Unlock() }

func setupNS(ns string, port int) (err error) {
	teardownNS(ns, port)
	slot := port - 31000
	if slot < 1 || slot > 250 {
		return fmt.Errorf("invalid pool port %d", port)
	}
	sub := fmt.Sprintf("10.77.%d", slot)
	// v0.1.4 及更早使用 cvN/cpN，名称太短且异常中断时容易留下孤儿 veth。
	// 新版本改用项目专属前缀；teardownNS 同时兼容清理旧命名。
	v := fmt.Sprintf("cspv%d", slot)
	peer := fmt.Sprintf("cspp%d", slot)

	// 任意一步失败都再清理一次，避免下一候选重试时被半成品 veth/netns 卡住。
	ok := false
	defer func() {
		if !ok {
			teardownNS(ns, port)
		}
	}()

	if err = runCmd("ip", "netns", "add", ns); err != nil {
		return err
	}
	if err = runCmd("ip", "netns", "exec", ns, "ip", "link", "set", "lo", "up"); err != nil {
		return err
	}
	if err = runCmd("ip", "link", "add", v, "type", "veth", "peer", "name", peer); err != nil {
		return err
	}
	if err = runCmd("ip", "link", "set", peer, "netns", ns); err != nil {
		return err
	}
	if err = runCmd("ip", "addr", "add", sub+".1/30", "dev", v); err != nil {
		return err
	}
	if err = runCmd("ip", "link", "set", v, "up"); err != nil {
		return err
	}
	if err = runCmd("ip", "netns", "exec", ns, "ip", "addr", "add", sub+".2/30", "dev", peer); err != nil {
		return err
	}
	if err = runCmd("ip", "netns", "exec", ns, "ip", "link", "set", peer, "up"); err != nil {
		return err
	}
	if err = runCmd("ip", "netns", "exec", ns, "ip", "route", "add", "default", "via", sub+".1"); err != nil {
		return err
	}
	if err = os.MkdirAll("/etc/netns/"+ns, 0755); err != nil {
		return fmt.Errorf("创建 netns DNS 目录失败: %w", err)
	}
	if err = os.WriteFile("/etc/netns/"+ns+"/resolv.conf", []byte("nameserver 1.1.1.1\n"), 0644); err != nil {
		return fmt.Errorf("写 netns DNS 配置失败: %w", err)
	}
	_ = exec.Command("sysctl", "-qw", "net.ipv4.ip_forward=1").Run()
	cidr := sub + ".0/30"
	ensureIpt("nat", "POSTROUTING", "-s", cidr, "-j", "MASQUERADE")
	ensureIpt("filter", "FORWARD", "-s", cidr, "-j", "ACCEPT")
	ensureIpt("filter", "FORWARD", "-d", cidr, "-j", "ACCEPT")
	ok = true
	return nil
}
func ensureIpt(table, chain string, spec ...string) {
	check := append([]string{"-t", table, "-C", chain}, spec...)
	if exec.Command("iptables", check...).Run() == nil {
		return
	}
	add := append([]string{"-t", table, "-I", chain, "1"}, spec...)
	_ = exec.Command("iptables", add...).Run()
}
func teardownLegacyCountryNS(country string) {
	ns := "csp" + strings.ToLower(strings.TrimSpace(country))
	if ns == "csp" {
		return
	}
	_ = exec.Command("ip", "netns", "del", ns).Run()
	_ = os.RemoveAll("/etc/netns/" + ns)
}

func teardownNS(ns string, port int) {
	// 先删 namespace。若 peer 已经被移入 namespace，删除 namespace 会一并回收 peer。
	_ = exec.Command("ip", "netns", "del", ns).Run()
	slot := port - 31000
	if slot < 1 || slot > 250 {
		_ = os.RemoveAll("/etc/netns/" + ns)
		return
	}

	// 再显式删除宿主侧 veth。之前版本只删 netns，宿主 cvN 可能残留，
	// 下一次 ip link add 就会报 RTNETLINK answers: File exists。
	// 同时清理 v0.1.4 及更早的 cvN/cpN 旧命名，完成一次性迁移。
	for _, dev := range []string{
		fmt.Sprintf("cspv%d", slot),
		fmt.Sprintf("cspp%d", slot),
		fmt.Sprintf("cv%d", slot),
		fmt.Sprintf("cp%d", slot),
	} {
		_ = exec.Command("ip", "link", "del", dev).Run()
	}

	sub := fmt.Sprintf("10.77.%d", slot)
	cidr := sub + ".0/30"
	_ = exec.Command("iptables", "-t", "nat", "-D", "POSTROUTING", "-s", cidr, "-j", "MASQUERADE").Run()
	_ = exec.Command("iptables", "-t", "filter", "-D", "FORWARD", "-s", cidr, "-j", "ACCEPT").Run()
	_ = exec.Command("iptables", "-t", "filter", "-D", "FORWARD", "-d", cidr, "-j", "ACCEPT").Run()
	_ = os.RemoveAll("/etc/netns/" + ns)
}

func run(n string, args ...string) { _ = exec.Command(n, args...).Run() }

func runCmd(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func serveSOCKS(ln net.Listener, p *Pool) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go handleSOCKS(c, p)
	}
}
func handleSOCKS(c net.Conn, p *Pool) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	b := make([]byte, 2)
	if _, e := io.ReadFull(c, b); e != nil {
		return
	}
	ms := make([]byte, int(b[1]))
	if _, e := io.ReadFull(c, ms); e != nil {
		return
	}
	_, _ = c.Write([]byte{5, 2})
	if _, e := io.ReadFull(c, b); e != nil {
		return
	}
	if b[0] != 1 {
		return
	}
	u := make([]byte, int(b[1]))
	io.ReadFull(c, u)
	q := make([]byte, 1)
	io.ReadFull(c, q)
	pw := make([]byte, int(q[0]))
	io.ReadFull(c, pw)
	p.mu.Lock()
	ok := string(u) == p.User && string(pw) == p.Pass
	p.mu.Unlock()
	if !ok {
		c.Write([]byte{1, 1})
		return
	}
	c.Write([]byte{1, 0})
	h := make([]byte, 4)
	io.ReadFull(c, h)
	if h[0] != 5 || h[1] != 1 {
		return
	}
	var host string
	switch h[3] {
	case 1:
		x := make([]byte, 4)
		io.ReadFull(c, x)
		host = net.IP(x).String()
	case 3:
		io.ReadFull(c, q)
		x := make([]byte, int(q[0]))
		io.ReadFull(c, x)
		host = string(x)
	default:
		return
	}
	pr := make([]byte, 2)
	if _, err := io.ReadFull(c, pr); err != nil {
		return
	}
	port := int(pr[0])<<8 | int(pr[1])
	target := net.JoinHostPort(host, strconv.Itoa(port))

	p.mu.Lock()
	source := p.ActiveSource
	upIP := p.ActiveIP
	upPort := p.ActivePort
	ns := p.ns
	p.mu.Unlock()

	if source == sourceProxio {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		upstream, err := dialSOCKS5Context(ctx, net.JoinHostPort(upIP, strconv.Itoa(upPort)), target)
		cancel()
		if err != nil {
			_, _ = c.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
			return
		}
		defer upstream.Close()
		_, _ = c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
		_ = c.SetDeadline(time.Time{})
		go io.Copy(upstream, c)
		_, _ = io.Copy(c, upstream)
		return
	}

	cmd := exec.Command("ip", "netns", "exec", ns, "socat", "-", "TCP:"+target)
	stdIn, _ := cmd.StdinPipe()
	stdOut, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		_, _ = c.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	_, _ = c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	_ = c.SetDeadline(time.Time{})
	go io.Copy(stdIn, c)
	_, _ = io.Copy(c, stdOut)
	_ = cmd.Process.Kill()
}

func (a *App) freePoolPortLocked() (int, error) {
	used := make(map[int]bool, len(a.Pools))
	for _, pool := range a.Pools {
		used[pool.Port] = true
	}
	return findFreePoolPort(used)
}

func findFreePoolPort(used map[int]bool) (int, error) {
	for port := 31001; port <= 31250; port++ {
		if used[port] {
			continue
		}
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			continue
		}
		_ = ln.Close()
		return port, nil
	}
	return 0, errors.New("没有可用的 SOCKS5 端口")
}

type persistedPool struct {
	ID          string `json:"id,omitempty"`
	Ordinal     int    `json:"ordinal,omitempty"`
	CountryCode string `json:"country_code"`
	Country     string `json:"country"`
	SourceMode  string `json:"source_mode,omitempty"`
	Port        int    `json:"port"`
	User        string `json:"user"`
	Pass        string `json:"pass"`
}

func (a *App) savePoolsLocked() error {
	items := make([]persistedPool, 0, len(a.Pools))
	for _, p := range a.Pools {
		items = append(items, persistedPool{ID: p.ID, Ordinal: p.Ordinal, CountryCode: p.CountryCode, Country: p.Country, SourceMode: normalizeSource(p.SourceMode), Port: p.Port, User: p.User, Pass: p.Pass})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CountryCode != items[j].CountryCode {
			return items[i].CountryCode < items[j].CountryCode
		}
		return items[i].Ordinal < items[j].Ordinal
	})
	b, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(workDir, "pools.json.tmp")
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(workDir, "pools.json"))
}

func (a *App) loadPools() error {
	b, err := os.ReadFile(filepath.Join(workDir, "pools.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var items []persistedPool
	if err := json.Unmarshal(b, &items); err != nil {
		return err
	}
	usedPorts := map[int]bool{}
	usedIDs := map[string]bool{}
	nextOrdinal := map[string]int{}
	migrated := false
	for _, it := range items {
		it.CountryCode = strings.ToUpper(strings.TrimSpace(it.CountryCode))
		if it.CountryCode == "" || it.User == "" || it.Pass == "" {
			continue
		}
		if it.Port < 31001 || it.Port > 31250 || usedPorts[it.Port] {
			port, perr := findFreePoolPort(usedPorts)
			if perr != nil {
				return perr
			}
			log.Printf("migrate %s SOCKS5 port: %d -> %d", it.CountryCode, it.Port, port)
			it.Port = port
			migrated = true
		}
		usedPorts[it.Port] = true

		ordinal := it.Ordinal
		if ordinal < 1 {
			ordinal = nextOrdinal[it.CountryCode] + 1
			migrated = true
		}
		for usedIDs[poolID(it.CountryCode, ordinal)] {
			ordinal++
			migrated = true
		}
		if ordinal > nextOrdinal[it.CountryCode] {
			nextOrdinal[it.CountryCode] = ordinal
		}
		id := strings.TrimSpace(it.ID)
		if id == "" || usedIDs[id] || id != poolID(it.CountryCode, ordinal) {
			id = poolID(it.CountryCode, ordinal)
			migrated = true
		}
		usedIDs[id] = true

		// v0.1.7 及更早按国家名创建 netns（例如 cspjp），多出口版本改为按端口隔离。
		// 升级时顺手清理旧 namespace，避免留下无主网络对象。
		teardownLegacyCountryNS(it.CountryCode)
		sourceMode := normalizeSource(it.SourceMode)
		if strings.ToLower(strings.TrimSpace(it.SourceMode)) == "proxyscrape" {
			migrated = true
		}
		cands := a.candidatesForLocked(it.CountryCode, sourceMode)
		a.Pools[id] = &Pool{
			ID: id, Ordinal: ordinal, CountryCode: it.CountryCode, Country: it.Country,
			SourceMode: sourceMode, Port: it.Port, User: it.User, Pass: it.Pass,
			Candidates: cands, LatencyMS: -1, NodeLatencyMS: -1, Status: "restoring", ns: fmt.Sprintf("csp%d", it.Port),
		}
	}
	if migrated {
		return a.savePoolsLocked()
	}
	return nil
}

func (a *App) restorePools() {
	time.Sleep(500 * time.Millisecond)
	a.mu.RLock()
	ps := make([]*Pool, 0, len(a.Pools))
	for _, p := range a.Pools {
		ps = append(ps, p)
	}
	a.mu.RUnlock()
	for _, p := range ps {
		if len(p.Candidates) == 0 {
			p.mu.Lock()
			p.Status = "no-candidates"
			p.Error = "没有可用候选节点"
			p.mu.Unlock()
			continue
		}
		go a.switchNext(p, "restoring")
	}
}

func loadOrCreateSecret(path string, n int) string {
	if b, e := os.ReadFile(path); e == nil {
		return strings.TrimSpace(string(b))
	}
	s := randomHex(n)
	_ = os.WriteFile(path, []byte(s+"\n"), 0600)
	return s
}
func randomHex(n int) string { b := make([]byte, n); _, _ = rand.Read(b); return hex.EncodeToString(b) }
func writeJSON(w http.ResponseWriter, c int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(c)
	_ = json.NewEncoder(w).Encode(v)
}
