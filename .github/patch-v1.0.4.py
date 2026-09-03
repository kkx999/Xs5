from pathlib import Path
import re

connectivity = r'''package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

var connectivityProbeEndpoints = []string{
	"https://www.google.com/generate_204",
	"https://cp.cloudflare.com/generate_204",
	"https://www.apple.com/library/test/success.html",
}

const exitIPRefreshInterval = 5 * time.Minute

var (
	exitIPRefreshInFlight sync.Map
	exitIPRefreshTimes    sync.Map
)

func availabilityHTTPStatusOK(code int) bool {
	return code >= 200 && code < 400
}

// probeProxyConnectivity 只判断上游 SOCKS5 是否能正常访问普通 HTTPS 网站。
// 出口 IP 查询不参与可用性判定。
func probeProxyConnectivity(node Node) (int, error) {
	if net.ParseIP(node.IP) == nil || node.Port < 1 || node.Port > 65535 {
		return -1, errors.New("上游 SOCKS5 地址无效")
	}
	var errs []string
	for _, endpoint := range connectivityProbeEndpoints {
		client := proxyHTTPClient(node, 8*time.Second)
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
		start := time.Now()
		resp, err := client.Get(endpoint)
		latency := int(time.Since(start).Milliseconds())
		if err != nil {
			errs = append(errs, endpoint+": "+err.Error())
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
		_ = resp.Body.Close()
		if !availabilityHTTPStatusOK(resp.StatusCode) {
			errs = append(errs, fmt.Sprintf("%s: HTTP %d", endpoint, resp.StatusCode))
			continue
		}
		if latency < 1 {
			latency = 1
		}
		return latency, nil
	}
	return -1, fmt.Errorf("普通 HTTPS 可用性检测全部失败: %s", strings.Join(errs, " | "))
}

// probeVPNConnectivity 与 Proxio 使用完全相同的普通 HTTPS 判定标准，
// 只是请求从 VPN Gate 的 network namespace 发出。
func probeVPNConnectivity(ns string) (int, error) {
	var errs []string
	for _, endpoint := range connectivityProbeEndpoints {
		start := time.Now()
		cmd := exec.Command("ip", "netns", "exec", ns, "curl", "-4", "-sS", "-o", "/dev/null", "-w", "%{http_code}", "--max-time", "7", endpoint)
		out, err := cmd.CombinedOutput()
		latency := int(time.Since(start).Milliseconds())
		if err != nil {
			detail := strings.TrimSpace(string(out))
			if detail == "" {
				detail = err.Error()
			}
			errs = append(errs, endpoint+": "+detail)
			continue
		}
		code, parseErr := strconv.Atoi(strings.TrimSpace(string(out)))
		if parseErr != nil || !availabilityHTTPStatusOK(code) {
			errs = append(errs, fmt.Sprintf("%s: HTTP %s", endpoint, strings.TrimSpace(string(out))))
			continue
		}
		if latency < 1 {
			latency = 1
		}
		return latency, nil
	}
	return -1, fmt.Errorf("普通 HTTPS 可用性检测全部失败: %s", strings.Join(errs, " | "))
}

func localPoolHTTPClient(p *Pool, timeout time.Duration) *http.Client {
	p.mu.Lock()
	port, user, pass := p.Port, p.User, p.Pass
	p.mu.Unlock()
	upstream := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	tr := &http.Transport{
		Proxy:             nil,
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialAuthenticatedSOCKS5Context(ctx, upstream, address, user, pass)
		},
		TLSHandshakeTimeout: 5 * time.Second,
	}
	return &http.Client{Transport: tr, Timeout: timeout}
}

// probePoolConnectivity 从 127.0.0.1:<固定S5端口> 带真实账号密码走完整链路。
// 因此监听器、认证、上游 SOCKS5/OpenVPN 和真实 HTTPS 出网任一环节坏掉都会被发现。
func probePoolConnectivity(p *Pool) (int, error) {
	var errs []string
	for _, endpoint := range connectivityProbeEndpoints {
		client := localPoolHTTPClient(p, 8*time.Second)
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
		start := time.Now()
		resp, err := client.Get(endpoint)
		latency := int(time.Since(start).Milliseconds())
		if err != nil {
			errs = append(errs, endpoint+": "+err.Error())
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
		_ = resp.Body.Close()
		if !availabilityHTTPStatusOK(resp.StatusCode) {
			errs = append(errs, fmt.Sprintf("%s: HTTP %d", endpoint, resp.StatusCode))
			continue
		}
		if latency < 1 {
			latency = 1
		}
		return latency, nil
	}
	return -1, fmt.Errorf("固定 S5 完整链路检测全部失败: %s", strings.Join(errs, " | "))
}

// detectPoolExitIP 只负责面板的出口 IP 信息，不参与健康判定。
func detectPoolExitIP(p *Pool) (string, error) {
	var errs []string
	for _, endpoint := range exitProbeEndpoints {
		client := localPoolHTTPClient(p, 7*time.Second)
		resp, err := client.Get(endpoint)
		if err != nil {
			errs = append(errs, endpoint+": "+err.Error())
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 128))
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			errs = append(errs, fmt.Sprintf("%s: HTTP %d", endpoint, resp.StatusCode))
			continue
		}
		if readErr != nil {
			errs = append(errs, endpoint+": "+readErr.Error())
			continue
		}
		ip, parseErr := parseProbeIP(body)
		if parseErr != nil {
			errs = append(errs, endpoint+": "+parseErr.Error())
			continue
		}
		return ip, nil
	}
	return "", fmt.Errorf("出口 IP 查询失败: %s", strings.Join(errs, " | "))
}

func maybeRefreshPoolExitIP(p *Pool, force bool) {
	now := time.Now()
	if !force {
		if v, ok := exitIPRefreshTimes.Load(p.ID); ok {
			if last, ok := v.(time.Time); ok && now.Sub(last) < exitIPRefreshInterval {
				return
			}
		}
	}
	if _, loaded := exitIPRefreshInFlight.LoadOrStore(p.ID, struct{}{}); loaded {
		return
	}
	exitIPRefreshTimes.Store(p.ID, now)
	go func() {
		defer exitIPRefreshInFlight.Delete(p.ID)
		ip, err := detectPoolExitIP(p)
		if err != nil {
			return
		}
		p.mu.Lock()
		if p.Status != "up" {
			p.mu.Unlock()
			return
		}
		changed := p.ExitIP != ip
		profileMissing := p.IPType == "" && p.IPISP == "" && p.IPASN == ""
		if changed {
			p.ExitIP = ip
			p.IPType = ""
			p.IPISP = ""
			p.IPASN = ""
			p.IPRisk = ""
		}
		p.mu.Unlock()
		if changed || profileMissing {
			enrichPoolIPProfile(p, ip)
		}
	}()
}

func dialAuthenticatedSOCKS5Context(ctx context.Context, upstream, target, user, pass string) (net.Conn, error) {
	if len(user) == 0 || len(user) > 255 || len(pass) == 0 || len(pass) > 255 {
		return nil, errors.New("本地 SOCKS5 账号或密码长度无效")
	}
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

	if _, err := conn.Write([]byte{5, 1, 2}); err != nil {
		return fail(err)
	}
	var method [2]byte
	if _, err := io.ReadFull(conn, method[:]); err != nil {
		return fail(err)
	}
	if method[0] != 5 || method[1] != 2 {
		return fail(fmt.Errorf("本地 SOCKS5 未选择用户名密码认证"))
	}
	auth := make([]byte, 0, 3+len(user)+len(pass))
	auth = append(auth, 1, byte(len(user)))
	auth = append(auth, []byte(user)...)
	auth = append(auth, byte(len(pass)))
	auth = append(auth, []byte(pass)...)
	if _, err := conn.Write(auth); err != nil {
		return fail(err)
	}
	var authResp [2]byte
	if _, err := io.ReadFull(conn, authResp[:]); err != nil {
		return fail(err)
	}
	if authResp[1] != 0 {
		return fail(errors.New("本地 SOCKS5 认证失败"))
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
		req = append(req, []byte(host)...)
	}
	req = append(req, byte(port>>8), byte(port))
	if _, err := conn.Write(req); err != nil {
		return fail(err)
	}
	var head [4]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		return fail(err)
	}
	if head[0] != 5 || head[1] != 0 {
		return fail(fmt.Errorf("本地 SOCKS5 CONNECT 失败，代码 %d", head[1]))
	}
	skip := 0
	switch head[3] {
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
		return fail(errors.New("本地 SOCKS5 返回未知地址类型"))
	}
	buf := make([]byte, skip+2)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return fail(err)
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}
'''
Path('connectivity.go').write_text(connectivity)

health = r'''package main

import (
	"fmt"
	"sync"
	"time"
)

const (
	healthCheckInterval = 30 * time.Second
	healthRetryDelayOne = 5 * time.Second
	healthRetryDelayTwo = 10 * time.Second
	healthFailureLimit  = 3
)

var healthChecksInFlight sync.Map

func beginHealthCheck(poolID string) bool {
	_, loaded := healthChecksInFlight.LoadOrStore(poolID, struct{}{})
	return !loaded
}

func endHealthCheck(poolID string) {
	healthChecksInFlight.Delete(poolID)
}

type runtimeIdentity struct {
	source string
	ip     string
	port   int
}

func currentRuntimeIdentity(p *Pool) (runtimeIdentity, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Status != "up" {
		return runtimeIdentity{}, false
	}
	return runtimeIdentity{source: p.ActiveSource, ip: p.ActiveIP, port: p.ActivePort}, true
}

func runtimeStillMatches(p *Pool, id runtimeIdentity) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.Status == "up" && p.ActiveSource == id.source && p.ActiveIP == id.ip && p.ActivePort == id.port
}

func markConnectivityHealthy(p *Pool, id runtimeIdentity, latency int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Status != "up" || p.ActiveSource != id.source || p.ActiveIP != id.ip || p.ActivePort != id.port {
		return false
	}
	p.FailCount = 0
	p.LatencyMS = latency
	p.Error = ""
	return true
}

func setHealthFailCount(p *Pool, id runtimeIdentity, count int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Status != "up" || p.ActiveSource != id.source || p.ActiveIP != id.ip || p.ActivePort != id.port {
		return false
	}
	p.FailCount = count
	return true
}

func (a *App) checkPoolHealth(p *Pool) {
	if !beginHealthCheck(p.ID) {
		return
	}
	defer endHealthCheck(p.ID)

	id, ok := currentRuntimeIdentity(p)
	if !ok {
		return
	}

	delays := []time.Duration{healthRetryDelayOne, healthRetryDelayTwo}
	var lastErr error
	for attempt := 1; attempt <= healthFailureLimit; attempt++ {
		latency, err := probePoolConnectivity(p)
		if err == nil {
			if markConnectivityHealthy(p, id, latency) {
				maybeRefreshPoolExitIP(p, false)
			}
			return
		}
		lastErr = err
		if !setHealthFailCount(p, id, attempt) {
			return
		}
		if attempt == healthFailureLimit {
			break
		}
		timer := time.NewTimer(delays[attempt-1])
		<-timer.C
		if !runtimeStillMatches(p, id) {
			return
		}
	}

	p.mu.Lock()
	if p.Status != "up" || p.ActiveSource != id.source || p.ActiveIP != id.ip || p.ActivePort != id.port {
		p.mu.Unlock()
		return
	}
	p.FailCount = healthFailureLimit
	p.Status = "failed"
	p.Error = fmt.Sprintf("固定 S5 完整链路连续 %d 次无法访问普通 HTTPS，正在自动切换：%v", healthFailureLimit, lastErr)
	p.mu.Unlock()

	// 不再由健康检查提前杀掉 runtime；切换器负责在合适的时机接管。
	a.switchNext(p, "switching")
}
'''
Path('health.go').write_text(health)

health_test = r'''package main

import (
	"testing"
	"time"
)

func TestHealthCheckPolicy(t *testing.T) {
	if healthCheckInterval != 30*time.Second {
		t.Fatalf("healthCheckInterval=%v want 30s", healthCheckInterval)
	}
	if healthRetryDelayOne != 5*time.Second || healthRetryDelayTwo != 10*time.Second {
		t.Fatalf("retry delays=%v/%v want 5s/10s", healthRetryDelayOne, healthRetryDelayTwo)
	}
	if healthFailureLimit != 3 {
		t.Fatalf("healthFailureLimit=%d want 3", healthFailureLimit)
	}
}

func TestHealthCheckInFlightGuard(t *testing.T) {
	id := "TEST-HEALTH-GUARD"
	endHealthCheck(id)
	if !beginHealthCheck(id) {
		t.Fatal("first beginHealthCheck should succeed")
	}
	if beginHealthCheck(id) {
		t.Fatal("second beginHealthCheck should be blocked")
	}
	endHealthCheck(id)
}

func TestAvailabilityHTTPStatus(t *testing.T) {
	for _, code := range []int{200, 204, 301, 302, 399} {
		if !availabilityHTTPStatusOK(code) {
			t.Fatalf("HTTP %d should count as reachable", code)
		}
	}
	for _, code := range []int{0, 199, 400, 403, 500} {
		if availabilityHTTPStatusOK(code) {
			t.Fatalf("HTTP %d should not count as reachable", code)
		}
	}
}

func TestConnectivityEndpointsAreOrdinaryHTTPS(t *testing.T) {
	if len(connectivityProbeEndpoints) != 3 {
		t.Fatalf("connectivity endpoints=%d want 3", len(connectivityProbeEndpoints))
	}
	want := map[string]bool{
		"https://www.google.com/generate_204": true,
		"https://cp.cloudflare.com/generate_204": true,
		"https://www.apple.com/library/test/success.html": true,
	}
	for _, endpoint := range connectivityProbeEndpoints {
		if !want[endpoint] {
			t.Fatalf("unexpected connectivity endpoint %q", endpoint)
		}
	}
}

func TestRuntimeIdentityGuard(t *testing.T) {
	p := &Pool{Status: "up", ActiveSource: sourceProxio, ActiveIP: "198.51.100.1", ActivePort: 1080}
	id, ok := currentRuntimeIdentity(p)
	if !ok || !runtimeStillMatches(p, id) {
		t.Fatal("current runtime should match")
	}
	p.mu.Lock()
	p.ActiveIP = "198.51.100.2"
	p.mu.Unlock()
	if runtimeStillMatches(p, id) {
		t.Fatal("stale health result must not apply after runtime changes")
	}
}
'''
Path('health_test.go').write_text(health_test)

main = Path('main.go')
s = main.read_text()
s = s.replace('appVersion       = "v1.0.3"', 'appVersion       = "v1.0.4"', 1)
s = s.replace('Xs5/v1.0.3', 'Xs5/v1.0.4')

old_activate_node = r'''func (a *App) activateNode(p *Pool, node Node, phase string, operationDeadline time.Time) error {
	if phase == "" {
		phase = "starting"
	}
	p.mu.Lock()
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
}'''
new_activate_node = r'''func (a *App) activateNode(p *Pool, node Node, phase string, operationDeadline time.Time) error {
	if phase == "" {
		phase = "starting"
	}
	p.mu.Lock()
	p.Status = phase
	p.Error = ""
	p.mu.Unlock()

	switch node.Source {
	case sourceProxio:
		// Proxio 可以先独立验证新上游，验证通过后才接管固定 S5 端口。
		return a.activateProxio(p, node)
	default:
		// VPN Gate 需要复用该池固定的 netns/网段；自动切换前已经经过三次完整链路失败确认。
		p.stopRuntime()
		return a.activateVPNGate(p, node, operationDeadline)
	}
}'''
if old_activate_node not in s:
    raise SystemExit('activateNode anchor missing')
s = s.replace(old_activate_node, new_activate_node, 1)

vpn_pattern = re.compile(r'func \(a \*App\) activateVPNGate\(p \*Pool, node Node, operationDeadline time\.Time\) error \{.*?\n\}\n\nfunc \(a \*App\) activateProxio', re.S)
vpn_new = r'''func (a *App) activateVPNGate(p *Pool, node Node, operationDeadline time.Time) error {
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
	latency, err := probeVPNConnectivity(p.ns)
	if err != nil {
		return fmt.Errorf("隧道已建立但普通 HTTPS 可用性检测失败: %w; %s", err, tailFile(logPath, 5))
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
	p.ExitIP = ""
	p.LatencyMS = latency
	p.NodeLatencyMS = nodeLatency
	p.IPType = ""
	p.IPISP = ""
	p.IPASN = ""
	p.IPRisk = ""
	p.Status = "up"
	p.Error = ""
	p.LastSwitch = time.Now()
	p.FailCount = 0
	p.mu.Unlock()
	go serveSOCKS(ln, p)
	maybeRefreshPoolExitIP(p, true)
	log.Printf("%s/%s up: SOCKS5 :%d -> VPN Gate %s (%s), ordinary HTTPS ok (%dms)", p.CountryCode, p.ID, p.Port, node.Host, node.IP, latency)
	return nil
}

func (a *App) activateProxio'''
s, n = vpn_pattern.subn(vpn_new, s, count=1)
if n != 1:
    raise SystemExit(f'activateVPNGate replacement count={n}')

proxio_pattern = re.compile(r'func \(a \*App\) activateProxio\(p \*Pool, node Node\) error \{.*?\n\}\n\nfunc tailFile', re.S)
proxio_new = r'''func (a *App) activateProxio(p *Pool, node Node) error {
	latency, err := probeProxyConnectivity(node)
	if err != nil {
		return fmt.Errorf("Proxio SOCKS5 普通 HTTPS 可用性检测失败: %w", err)
	}
	// 新上游已经验证可用后再停止旧 runtime，尽量缩短切换断流窗口。
	p.stopRuntime()
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
	p.ExitIP = ""
	p.LatencyMS = latency
	p.NodeLatencyMS = nodeLatency
	p.IPType = ""
	p.IPISP = ""
	p.IPASN = ""
	p.IPRisk = ""
	p.Status = "up"
	p.Error = ""
	p.LastSwitch = time.Now()
	p.FailCount = 0
	p.mu.Unlock()
	go serveSOCKS(ln, p)
	maybeRefreshPoolExitIP(p, true)
	log.Printf("%s/%s up: SOCKS5 :%d -> Proxio %s, ordinary HTTPS ok (%dms)", p.CountryCode, p.ID, p.Port, node.Host, latency)
	return nil
}

func tailFile'''
s, n = proxio_pattern.subn(proxio_new, s, count=1)
if n != 1:
    raise SystemExit(f'activateProxio replacement count={n}')

main.write_text(s)

recovery = Path('recovery.go')
s = recovery.read_text()
if 'autoRetryContinueDelay = 2 * time.Second' not in s:
    raise SystemExit('recovery delay anchor missing')
s = s.replace('autoRetryContinueDelay = 2 * time.Second', 'autoRetryContinueDelay = 15 * time.Second', 1)
recovery.write_text(s)

install = Path('install.sh')
s = install.read_text()
if 'VERSION=1.0.3' not in s:
    raise SystemExit('install version anchor missing')
install.write_text(s.replace('VERSION=1.0.3', 'VERSION=1.0.4', 1))

menu = Path('xs5.sh')
s = menu.read_text()
if 'echo "1.0.3"' not in s:
    raise SystemExit('menu version anchor missing')
menu.write_text(s.replace('echo "1.0.3"', 'echo "1.0.4"', 1))

Path('VERSION').write_text('1.0.4\n')
Path('RELEASE.md').write_text('''# Xs5 v1.0.4\n\n本版本重构 VPN Gate 与 Proxio 的健康检查与节点可用性判定，重点减少公共出口被误判、误切换和切换后长时间故障的问题。\n\n- VPN Gate 与 Proxio 统一使用普通 HTTPS 可用性标准，不再用出口 IP 查询接口决定节点是否可用。\n- 普通 HTTPS 检测点为 Google generate_204、Cloudflare generate_204、Apple success；任意一个成功即认为网络可用。\n- 已启用出口的健康检查改为从 127.0.0.1 的固定 SOCKS5 端口携带真实账号密码走完整链路，覆盖监听器、认证、上游代理/OpenVPN 和真实出网。\n- 正常状态每 30 秒检查；首次失败 5 秒后复检，第二次失败 10 秒后第三次复检；连续 3 次完整链路失败才自动切换。\n- 健康检查不再提前停止当前 runtime，降低瞬时抖动被放大成断流的概率。\n- 新候选上线也以普通 HTTPS 可用性为准；出口 IP 查询失败不再淘汰本来能够正常上网的候选。\n- 出口 IP / ISP / ASN 改为独立的辅助信息刷新流程，查询失败不会改变节点健康状态。\n- Proxio 新候选会先独立验证可用，再接管固定 S5 端口，缩短切换中断窗口。\n- VPN Gate 与 Proxio 使用相同的健康判定、失败次数和 IP 信息策略；仅底层建立隧道/代理的方式不同。\n- 90 秒扫描轮次失败后，如仍有未冷却候选，自动续扫等待从 2 秒调整为 15 秒；全部候选冷却时仍等待最早冷却到期，无候选时 60 秒后再试。\n- 保留 5 分钟失败候选冷却、扫描游标、同国家出口避让和无人值守恢复。\n- README 继续只描述项目用途与使用方式，版本更新内容仅保留在 GitHub Release Notes。\n- 从旧版本更新不会改变已有 S5 端口、用户名、密码和国家出口配置。\n\n> Xs5 使用第三方公开 VPN / SOCKS5 节点。公开节点可能不稳定、不可信或被目标站封禁，请勿通过不受信任的公共出口传输敏感明文数据。\n''')
