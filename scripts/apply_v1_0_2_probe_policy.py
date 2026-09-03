from pathlib import Path
import re

main = Path('main.go')
s = main.read_text()

# v1.0.2 保持最终版本号一致。
s = s.replace('appVersion       = "v1.0.1"', 'appVersion       = "v1.0.2"')
s = s.replace('Xs5/v1.0.1', 'Xs5/v1.0.2')

# Proxio 元数据仅作为预筛，最终仍必须经过真实 SOCKS5 + HTTPS + 出口 IP 验证。
replacements = {
    'if hasReliability && reliability < 80 {': 'if hasReliability && reliability < 70 {',
    'if hasUptime && uptime < 0.75 {': 'if hasUptime && uptime < 0.60 {',
    'if hasLatencyS && (latencyS <= 0 || latencyS > 2.5) {': 'if hasLatencyS && (latencyS <= 0 || latencyS > 5.0) {',
    'if total >= 5 && float64(good)/float64(total) < 0.70 {': 'if total >= 5 && float64(good)/float64(total) < 0.60 {',
}
for old, new in replacements.items():
    if old in s:
        s = s.replace(old, new, 1)
    elif new not in s:
        raise SystemExit(f'probe policy anchor missing: {old}')

pattern = re.compile(
    r'func probeVPNGate\(ns string\) \(string, int, error\) \{.*?\n\}\n\nfunc probeProxyNode\(node Node\) \(string, int, error\) \{.*?\n\}\n\n// measureNodeLatency',
    re.S,
)
replacement = r'''var exitProbeEndpoints = []string{
	"https://api.ipify.org",
	"https://checkip.amazonaws.com",
	"https://icanhazip.com",
}

func tlsVerificationFailed(text string) bool {
	v := strings.ToLower(strings.TrimSpace(text))
	return strings.Contains(v, "x509") ||
		strings.Contains(v, "certificate has expired") ||
		strings.Contains(v, "certificate signed by unknown authority") ||
		strings.Contains(v, "ssl certificate problem") ||
		strings.Contains(v, "failed to verify certificate")
}

func parseProbeIP(body []byte) (string, error) {
	ip := strings.TrimSpace(string(body))
	parsed := net.ParseIP(ip)
	if parsed == nil || parsed.To4() == nil {
		return "", fmt.Errorf("出口 IPv4 返回异常: %q", ip)
	}
	return parsed.String(), nil
}

// probeVPNGate 通过 VPN namespace 依次尝试多个 HTTPS 出口检测点。
// 任意一个正常返回合法 IPv4 即认为出口可用；TLS 证书验证异常则立即拒绝该节点。
func probeVPNGate(ns string) (string, int, error) {
	var errs []string
	for _, endpoint := range exitProbeEndpoints {
		start := time.Now()
		cmd := exec.Command("ip", "netns", "exec", ns, "curl", "-4", "-fsS", "--max-time", "6", endpoint)
		out, err := cmd.CombinedOutput()
		latency := int(time.Since(start).Milliseconds())
		if err != nil {
			detail := strings.TrimSpace(string(out))
			if detail == "" {
				detail = err.Error()
			}
			if tlsVerificationFailed(detail) {
				return "", -1, fmt.Errorf("TLS 证书验证失败: %s", detail)
			}
			errs = append(errs, endpoint+": "+detail)
			continue
		}
		ip, parseErr := parseProbeIP(out)
		if parseErr != nil {
			errs = append(errs, endpoint+": "+parseErr.Error())
			continue
		}
		if latency < 1 {
			latency = 1
		}
		return ip, latency, nil
	}
	return "", -1, fmt.Errorf("所有 HTTPS 出口检测点均失败: %s", strings.Join(errs, " | "))
}

// probeProxyNode 对公开 SOCKS5 做真实 CONNECT + HTTPS 检测。
// 多检测点仅用于降低单一检测站不可达造成的误判，不会跳过系统 TLS 验证。
func probeProxyNode(node Node) (string, int, error) {
	if net.ParseIP(node.IP) == nil || node.Port < 1 || node.Port > 65535 {
		return "", -1, errors.New("上游 SOCKS5 地址无效")
	}
	var errs []string
	for _, endpoint := range exitProbeEndpoints {
		client := proxyHTTPClient(node, 8*time.Second)
		start := time.Now()
		resp, err := client.Get(endpoint)
		latency := int(time.Since(start).Milliseconds())
		if err != nil {
			if tlsVerificationFailed(err.Error()) {
				return "", -1, fmt.Errorf("TLS 证书验证失败: %w", err)
			}
			errs = append(errs, endpoint+": "+err.Error())
			continue
		}
		b, readErr := io.ReadAll(io.LimitReader(resp.Body, 128))
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			errs = append(errs, fmt.Sprintf("%s: HTTP %d", endpoint, resp.StatusCode))
			continue
		}
		if readErr != nil {
			errs = append(errs, endpoint+": "+readErr.Error())
			continue
		}
		ip, parseErr := parseProbeIP(b)
		if parseErr != nil {
			errs = append(errs, endpoint+": "+parseErr.Error())
			continue
		}
		if latency < 1 {
			latency = 1
		}
		return ip, latency, nil
	}
	return "", -1, fmt.Errorf("所有 HTTPS 出口检测点均失败: %s", strings.Join(errs, " | "))
}

// measureNodeLatency'''

s2, n = pattern.subn(replacement, s, count=1)
if n != 1:
    # 已经应用过时允许幂等执行。
    if 'var exitProbeEndpoints = []string{' not in s or '所有 HTTPS 出口检测点均失败' not in s:
        raise SystemExit(f'probe function replacement failed: {n}')
else:
    s = s2

main.write_text(s)

Path('probe_policy_test.go').write_text(r'''package main

import (
	"encoding/json"
	"testing"
)

func TestRelaxedProxioPrefilterStillRequiresQuality(t *testing.T) {
	blob := []byte(`[
		{"protocols":["socks5"],"ip":"198.51.100.21","port":1080,"country":"Japan","country_code":"JP","latency_s":4.5,"reliability":72,"uptime":0.65,"last_results":"11100"},
		{"protocols":["socks5"],"ip":"198.51.100.22","port":1080,"country":"Japan","country_code":"JP","latency_s":4.5,"reliability":69,"uptime":0.65,"last_results":"11100"}
	]`)
	var root any
	if err := json.Unmarshal(blob, &root); err != nil {
		t.Fatal(err)
	}
	nodes := parseProxioRows(proxyRows(root))
	if len(nodes) != 1 || nodes[0].IP != "198.51.100.21" {
		t.Fatalf("unexpected relaxed prefilter result: %+v", nodes)
	}
}

func TestTLSVerificationFailureDetection(t *testing.T) {
	bad := []string{
		"tls: failed to verify certificate: x509: certificate has expired",
		"SSL certificate problem: certificate has expired",
		"x509: certificate signed by unknown authority",
	}
	for _, v := range bad {
		if !tlsVerificationFailed(v) {
			t.Fatalf("should classify TLS verification failure: %q", v)
		}
	}
	if tlsVerificationFailed("context deadline exceeded") {
		t.Fatal("timeout must not be classified as TLS verification failure")
	}
}

func TestParseProbeIPv4(t *testing.T) {
	if got, err := parseProbeIP([]byte("203.0.113.7\n")); err != nil || got != "203.0.113.7" {
		t.Fatalf("parseProbeIP=%q err=%v", got, err)
	}
	if _, err := parseProbeIP([]byte("not-an-ip")); err == nil {
		t.Fatal("invalid probe body must fail")
	}
}
''')

Path('VERSION').write_text('1.0.2\n')

release = '''# Xs5 v1.0.2

本版本重点提升公开出口池的无人值守恢复能力，并减少“节点实际可用但被单一检测点误判”的情况。

- 正常出口每 30 秒进行一次真实出网健康检查。
- 第一次健康检查失败后，10 秒后快速复检；连续 2 次失败才自动切换。
- 单轮切换 90 秒未找到可用节点后，系统自动从上次扫描位置继续，无需人工点击。
- 失败候选冷却 5 分钟；全部候选都在冷却时，等待最早冷却到期后自动继续。
- VPN Gate 与 Proxio 的最终出口验证由单一检测点改为 3 个 HTTPS 检测点容灾。
- 任意检测点正常返回合法公网 IPv4即可通过；TLS 证书验证异常仍立即拒绝，不降低安全要求。
- Proxio 元数据预筛适度放宽：可靠度 >= 70%、在线率 >= 60%、来源延迟 <= 5 秒、近期成功率 >= 60%。
- Proxio 最终仍必须真实完成 SOCKS5 CONNECT、HTTPS TLS 验证并取得合法出口 IP，预筛放宽不等于直接信任公开节点。
- 保留同国家多出口避让、扫描游标和自动故障切换逻辑。
- 从 v1.0.0 / v1.0.1 更新不会改变现有 S5 端口、用户名、密码和国家出口配置。

> Xs5 使用第三方公开 VPN / SOCKS5 节点。公开节点可能不稳定、不可信或被目标站封禁，请勿通过不受信任的公共出口传输敏感明文数据。
'''
Path('RELEASE.md').write_text(release)
