from pathlib import Path

Path('health.go').write_text(r'''package main

import (
	"fmt"
	"log"
	"sync"
	"time"
)

const (
	healthCheckInterval     = 30 * time.Second
	healthFailureRetryDelay = 10 * time.Second
)

var healthChecksInFlight sync.Map

func beginHealthCheck(poolID string) bool {
	_, loaded := healthChecksInFlight.LoadOrStore(poolID, struct{}{})
	return !loaded
}

func endHealthCheck(poolID string) {
	healthChecksInFlight.Delete(poolID)
}

// applyHealthyProbe 接受任何“真实 HTTPS 出网成功 + 合法公网 IPv4”的结果。
// 公共 VPN / SOCKS5 的 NAT 出口 IP 可能动态变化，因此 IP 变化本身不是故障。
// expectedIP 只用于防止把切换前的慢探测结果覆盖到已经切换的新出口。
func applyHealthyProbe(p *Pool, expectedIP, observedIP string, latency int) (applied bool, changed bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Status != "up" || p.ExitIP != expectedIP {
		return false, false
	}

	changed = observedIP != expectedIP
	p.FailCount = 0
	p.LatencyMS = latency
	p.Error = ""
	if changed {
		p.ExitIP = observedIP
		// 出口 IP 已变化，旧的 IP 属性已经失效，先清空后异步重新识别。
		p.IPType = ""
		p.IPISP = ""
		p.IPASN = ""
		p.IPRisk = ""
	}
	return true, changed
}

func finishHealthyProbe(p *Pool, expectedIP, observedIP string, latency int) bool {
	applied, changed := applyHealthyProbe(p, expectedIP, observedIP, latency)
	if !applied {
		return false
	}
	if changed {
		log.Printf("%s/%s health: exit IP changed %s -> %s; keep current upstream", p.CountryCode, p.ID, expectedIP, observedIP)
		go enrichPoolIPProfile(p, observedIP)
	}
	return true
}

func (a *App) checkPoolHealth(p *Pool) {
	if !beginHealthCheck(p.ID) {
		return
	}
	defer endHealthCheck(p.ID)

	p.mu.Lock()
	if p.Status != "up" {
		p.mu.Unlock()
		return
	}
	expected := p.ExitIP
	p.mu.Unlock()

	// 只要当前出口能完成真实 HTTPS 出网并返回合法 IPv4，就认为健康。
	// 即使公网 IP 与上一次不同，也只更新出口信息，不触发切换。
	ip, latency, err := p.probeCurrent()
	if err == nil {
		finishHealthyProbe(p, expected, ip, latency)
		return
	}

	p.mu.Lock()
	if p.Status != "up" || p.ExitIP != expected {
		p.mu.Unlock()
		return
	}
	p.FailCount = 1
	p.mu.Unlock()

	// 第一次真实出网失败后 10 秒快速复检，避免瞬时抖动造成误切换。
	timer := time.NewTimer(healthFailureRetryDelay)
	<-timer.C

	p.mu.Lock()
	if p.Status != "up" || p.ExitIP != expected {
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()

	ip, latency, err = p.probeCurrent()
	if err == nil {
		finishHealthyProbe(p, expected, ip, latency)
		return
	}

	p.mu.Lock()
	if p.Status != "up" || p.ExitIP != expected {
		p.mu.Unlock()
		return
	}
	p.FailCount = 2
	p.Status = "failed"
	p.Error = fmt.Sprintf("健康检查连续两次真实出网失败，正在自动切换：%v", err)
	p.mu.Unlock()

	// 只有连续两次真实出网失败才停止旧出口并切换。
	p.stopRuntime()
	a.switchNext(p, "switching")
}
''')

Path('health_test.go').write_text(r'''package main

import (
	"testing"
	"time"
)

func TestHealthCheckTiming(t *testing.T) {
	if healthCheckInterval != 30*time.Second {
		t.Fatalf("healthCheckInterval=%v want 30s", healthCheckInterval)
	}
	if healthFailureRetryDelay != 10*time.Second {
		t.Fatalf("healthFailureRetryDelay=%v want 10s", healthFailureRetryDelay)
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
	if !beginHealthCheck(id) {
		t.Fatal("beginHealthCheck should succeed after end")
	}
	endHealthCheck(id)
}

func TestHealthyProbeAcceptsExitIPChange(t *testing.T) {
	p := &Pool{
		Status:    "up",
		ExitIP:    "1.1.1.1",
		FailCount: 1,
		LatencyMS: -1,
		IPType:    "住宅/ISP",
		IPISP:     "old isp",
		IPASN:     "AS1",
		IPRisk:    "old risk",
	}
	applied, changed := applyHealthyProbe(p, "1.1.1.1", "2.2.2.2", 123)
	if !applied || !changed {
		t.Fatalf("applied=%v changed=%v; dynamic exit IP should remain healthy", applied, changed)
	}
	if p.Status != "up" || p.ExitIP != "2.2.2.2" || p.FailCount != 0 || p.LatencyMS != 123 {
		t.Fatalf("unexpected pool state: status=%s exit=%s fail=%d latency=%d", p.Status, p.ExitIP, p.FailCount, p.LatencyMS)
	}
	if p.IPType != "" || p.IPISP != "" || p.IPASN != "" || p.IPRisk != "" {
		t.Fatal("IP profile must be cleared when exit IP changes")
	}
}

func TestHealthyProbeKeepsProfileWhenIPUnchanged(t *testing.T) {
	p := &Pool{Status: "up", ExitIP: "1.1.1.1", FailCount: 1, IPType: "机房 IP", IPISP: "isp", IPASN: "AS1"}
	applied, changed := applyHealthyProbe(p, "1.1.1.1", "1.1.1.1", 80)
	if !applied || changed {
		t.Fatalf("applied=%v changed=%v", applied, changed)
	}
	if p.FailCount != 0 || p.LatencyMS != 80 || p.IPType != "机房 IP" || p.IPISP != "isp" || p.IPASN != "AS1" {
		t.Fatal("same-IP successful probe should only refresh health data")
	}
}

func TestHealthyProbeRejectsStaleResult(t *testing.T) {
	p := &Pool{Status: "up", ExitIP: "3.3.3.3", FailCount: 0}
	applied, changed := applyHealthyProbe(p, "1.1.1.1", "2.2.2.2", 50)
	if applied || changed {
		t.Fatalf("stale probe must be ignored: applied=%v changed=%v", applied, changed)
	}
	if p.ExitIP != "3.3.3.3" {
		t.Fatalf("stale probe overwrote current exit: %s", p.ExitIP)
	}
}
''')

main = Path('main.go')
s = main.read_text()
if 'appVersion       = "v1.0.2"' not in s:
    raise SystemExit('main.go version anchor missing')
s = s.replace('appVersion       = "v1.0.2"', 'appVersion       = "v1.0.3"', 1)
s = s.replace('Xs5/v1.0.2', 'Xs5/v1.0.3')
main.write_text(s)

install = Path('install.sh')
s = install.read_text()
if 'VERSION=1.0.2' not in s:
    raise SystemExit('install.sh version anchor missing')
install.write_text(s.replace('VERSION=1.0.2', 'VERSION=1.0.3', 1))

menu = Path('xs5.sh')
s = menu.read_text()
if 'echo "1.0.2"' not in s:
    raise SystemExit('xs5.sh fallback version anchor missing')
menu.write_text(s.replace('echo "1.0.2"', 'echo "1.0.3"', 1))

readme = Path('README.md')
s = readme.read_text()
s = s.replace('# X S5 池（Xs5）v1.0.1', '# X S5 池（Xs5）v1.0.3', 1)
s = s.replace('xs5-v1.0.1-linux-amd64.tar.gz', 'xs5-v1.0.3-linux-amd64.tar.gz')
s = s.replace('xs5-v1.0.1-linux-arm64.tar.gz', 'xs5-v1.0.3-linux-arm64.tar.gz')
s = s.replace('cd xs5-v1.0.1-linux-amd64', 'cd xs5-v1.0.3-linux-amd64')
s = s.replace('cd Xs5-1.0.1', 'cd Xs5-1.0.3')
marker = '## v1.0.1：候选续跑与失败冷却\n'
current = '''## v1.0.3：动态出口健康检查稳定性修复

- 健康检查不再要求公网出口 IP 与上一次完全一致。
- VPN Gate / Proxio 只要真实 HTTPS 出网成功并取得合法公网 IPv4，就继续保持当前上游。
- 公网出口 IP 动态变化时，面板自动更新新 IP，并重新识别 IP 属性、ISP / ASN，不触发切换。
- 仍保持 30 秒常规检查 + 首次失败 10 秒快速复检；只有连续两次真实出网失败才自动切换。
- 保留 v1.0.2 的三 HTTPS 检测点、TLS 严格验证、5 分钟失败候选冷却和无人值守续扫。

'''
if marker in s and '## v1.0.3：动态出口健康检查稳定性修复' not in s:
    s = s.replace(marker, current + marker, 1)
readme.write_text(s)

Path('VERSION').write_text('1.0.3\n')
Path('RELEASE.md').write_text('''# Xs5 v1.0.3

本版本修复公共出口动态 IP 被健康检查误判为故障的问题，重点提升已连接出口的稳定性。

- 健康检查不再要求出口公网 IP 与建立连接时的 IP 完全一致。
- VPN Gate / Proxio 只要真实 HTTPS 出网成功并返回合法公网 IPv4，就判定当前上游健康。
- 出口 IP 动态变化时保持当前节点，不再自动断开切换。
- 面板会自动更新新的出口 IP，并重新识别 IP 属性、ISP / ASN。
- 使用旧 ExitIP 作为并发保护标记，避免切换前的慢探测结果覆盖已经切换后的新出口。
- 保持 30 秒常规健康检查；第一次真实出网失败后 10 秒快速复检。
- 只有连续两次真实出网失败才停止旧出口并自动切换。
- 保留 v1.0.2 的 3 个 HTTPS 检测点容灾、TLS 严格验证、5 分钟失败节点冷却和 90 秒后无人值守续扫。
- 从 v1.0.0 / v1.0.1 / v1.0.2 更新不会改变现有 S5 端口、用户名、密码和国家出口配置。

> Xs5 使用第三方公开 VPN / SOCKS5 节点。公开节点可能不稳定、不可信或被目标站封禁，请勿通过不受信任的公共出口传输敏感明文数据。
''')
