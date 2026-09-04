package main

import (
	"fmt"
	"log"
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

func resetHealthAfterLocalPressure(p *Pool, id runtimeIdentity) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Status != "up" || p.ActiveSource != id.source || p.ActiveIP != id.ip || p.ActivePort != id.port {
		return false
	}
	p.FailCount = 0
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
	if a.telegram != nil && a.telegram.isPoolPaused(p.ID) {
		return
	}

	delays := []time.Duration{healthRetryDelayOne, healthRetryDelayTwo}
	var lastErr error
	for attempt := 1; attempt <= healthFailureLimit; attempt++ {
		latency, err := probePoolConnectivity(p)
		if err == nil {
			if markConnectivityHealthy(p, id, latency) {
				recordVerifiedRuntime(id, latency)
				adaptiveRecordRuntimeHealthy(p.ID, id, latency, time.Now())
				maybeRefreshPoolExitIP(p, false)
			}
			return
		}

		// 所有来源统一处理：如果失败来自服务器本身资源不足，而不是远端出口，
		// 本轮健康检查直接放弃且不累计 FailCount，避免资源抖动触发错误切换。
		if isLocalResourceError(err) || recentLocalResourcePressure(localPressureHealthWindow) {
			noteLocalResourcePressure()
			if resetHealthAfterLocalPressure(p, id) {
				log.Printf("%s/%s health check paused because of local resource pressure: %v", p.CountryCode, p.ID, err)
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
	adaptiveRecordRuntimeDrop(p.ID, id, lastErr, time.Now())
	p.FailCount = healthFailureLimit
	p.Status = "failed"
	p.Error = fmt.Sprintf("固定 S5 完整链路连续 %d 次无法访问普通 HTTPS，正在自动切换：%v", healthFailureLimit, lastErr)
	p.mu.Unlock()

	// 不再由健康检查提前杀掉 runtime；切换器负责在合适的时机接管。
	if a.telegram != nil {
		go a.telegram.notifyAutoSwitchStart(p, lastErr)
	}
	a.switchNext(p, "switching")
}
