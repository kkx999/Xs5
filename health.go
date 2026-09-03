package main

import (
	"fmt"
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

	ip, latency, err := p.probeCurrent()
	if err == nil && ip == expected {
		p.mu.Lock()
		if p.Status == "up" && p.ExitIP == expected {
			p.FailCount = 0
			p.LatencyMS = latency
		}
		p.mu.Unlock()
		return
	}

	p.mu.Lock()
	if p.Status != "up" || p.ExitIP != expected {
		p.mu.Unlock()
		return
	}
	p.FailCount = 1
	p.mu.Unlock()

	timer := time.NewTimer(healthFailureRetryDelay)
	<-timer.C

	p.mu.Lock()
	if p.Status != "up" || p.ExitIP != expected {
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()

	ip, latency, err = p.probeCurrent()
	if err == nil && ip == expected {
		p.mu.Lock()
		if p.Status == "up" && p.ExitIP == expected {
			p.FailCount = 0
			p.LatencyMS = latency
		}
		p.mu.Unlock()
		return
	}

	p.mu.Lock()
	if p.Status != "up" || p.ExitIP != expected {
		p.mu.Unlock()
		return
	}
	p.FailCount = 2
	p.Status = "failed"
	if err != nil {
		p.Error = fmt.Sprintf("健康检查连续两次失败，正在自动切换：%v", err)
	} else {
		p.Error = fmt.Sprintf("健康检查发现出口 IP 从 %s 变为 %s，正在自动切换", expected, ip)
	}
	p.mu.Unlock()

	// 当前出口已确认不可用，先停止旧运行时，避免继续对外提供失效的 S5。
	p.stopRuntime()
	a.switchNext(p, "switching")
}
