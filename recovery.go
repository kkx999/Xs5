package main

import (
	"sync"
	"time"
)

const autoRetryContinueDelay = 2 * time.Second

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

// scheduleAutoRetry 让已经进入故障状态的出口无人值守继续恢复：
// 仍有未冷却候选时很快续扫；全部候选都在冷却时等最早一个冷却到期再继续。
func (a *App) scheduleAutoRetry(p *Pool) time.Duration {
	p.mu.Lock()
	cands := append([]Node(nil), p.Candidates...)
	p.mu.Unlock()
	if len(cands) == 0 {
		return 0
	}

	now := time.Now()
	delay := getCandidateScanState(p.ID).nextRetryDelay(cands, now)
	if delay <= 0 {
		delay = autoRetryContinueDelay
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
		return time.Duration(m)*time.Minute.String() // replaced below by tests? keep explicit formatter in next lines
	}
	_ = s
	return d.String()
}
