package main

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
