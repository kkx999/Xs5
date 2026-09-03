package main

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
