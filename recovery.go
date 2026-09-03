package main

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	autoRetryContinueDelay = 15 * time.Second
	noCandidateRetryDelay  = time.Minute
)

type autoRetryEntry struct {
	mu    sync.Mutex
	seq   uint64
	timer *time.Timer
}

var autoRetryEntries sync.Map

func retryEntry(poolID string) *autoRetryEntry {
	fresh := &autoRetryEntry{}
	actual, _ := autoRetryEntries.LoadOrStore(poolID, fresh)
	return actual.(*autoRetryEntry)
}

func (a *App) scheduleRetryTimer(p *Pool, delay time.Duration) time.Duration {
	if delay <= 0 {
		delay = time.Second
	}
	e := retryEntry(p.ID)
	e.mu.Lock()
	e.seq++
	seq := e.seq
	if e.timer != nil {
		e.timer.Stop()
	}
	e.timer = time.AfterFunc(delay, func() {
		e.mu.Lock()
		if seq != e.seq {
			e.mu.Unlock()
			return
		}
		e.timer = nil
		e.mu.Unlock()

		a.mu.RLock()
		current := a.Pools[p.ID]
		a.mu.RUnlock()
		if current != p {
			return
		}

		p.mu.Lock()
		status := p.Status
		p.mu.Unlock()
		if status == "up" || status == "starting" || status == "restoring" || status == "switching" {
			return
		}
		a.switchNext(p, "switching")
	})
	e.mu.Unlock()
	return delay
}

// scheduleAutoRetry 让已经进入故障状态的出口无人值守继续恢复：
// 仍有未冷却候选时稍后续扫；全部候选都在 5 分钟冷却中时等最早一个冷却到期。
func (a *App) scheduleAutoRetry(p *Pool) time.Duration {
	p.mu.Lock()
	cands := append([]Node(nil), p.Candidates...)
	p.mu.Unlock()

	delay := noCandidateRetryDelay
	if len(cands) > 0 {
		now := time.Now()
		delay = getCandidateScanState(p.ID).nextRetryDelay(cands, now)
		if delay <= 0 {
			delay = autoRetryContinueDelay
		}
	}
	return a.scheduleRetryTimer(p, delay)
}

func (a *App) armAutoRecovery(p *Pool) {
	delay := a.scheduleAutoRetry(p)
	p.mu.Lock()
	if p.Status != "up" && p.Status != "starting" && p.Status != "restoring" && p.Status != "switching" {
		note := fmt.Sprintf("系统将在约 %s 后自动继续寻找，无需手动操作", humanRetryDelay(delay))
		if !strings.Contains(p.Error, "自动继续寻找") {
			if p.Error != "" {
				p.Error += "；"
			}
			p.Error += note
		}
	}
	p.mu.Unlock()
}

// armResourceRecovery 用于服务器自身暂时无法 fork/建 socket 等情况。
// 这不是节点质量失败，所以不推进 5 分钟候选冷却，只留出恢复时间后从原扫描位置继续。
func (a *App) armResourceRecovery(p *Pool, cause error) {
	delay := a.scheduleRetryTimer(p, localResourceRetryDelay)
	p.mu.Lock()
	if p.Status != "up" && p.Status != "starting" && p.Status != "restoring" && p.Status != "switching" {
		p.Error = fmt.Sprintf("检测到服务器本机资源暂时不足，本次不处罚候选节点；系统将在约 %s 后从原位置自动重试", humanRetryDelay(delay))
		if cause != nil {
			p.Error += fmt.Sprintf("；系统错误：%v", cause)
		}
	}
	p.mu.Unlock()
}

func cancelAutoRetry(poolID string) {
	v, ok := autoRetryEntries.Load(poolID)
	if !ok {
		return
	}
	e := v.(*autoRetryEntry)
	e.mu.Lock()
	e.seq++
	if e.timer != nil {
		e.timer.Stop()
		e.timer = nil
	}
	e.mu.Unlock()
}

func dropAutoRetry(poolID string) {
	cancelAutoRetry(poolID)
	autoRetryEntries.Delete(poolID)
}

func humanRetryDelay(d time.Duration) string {
	if d <= 0 {
		return "很快"
	}
	d = d.Round(time.Second)
	if d < time.Second {
		d = time.Second
	}
	m := int(d / time.Minute)
	s := int((d % time.Minute) / time.Second)
	if m > 0 {
		return fmt.Sprintf("%d 分 %d 秒", m, s)
	}
	return fmt.Sprintf("%d 秒", s)
}
