package main

import (
	"fmt"
	"sync"
	"time"
)

const failedCandidateCooldown = 5 * time.Minute

type candidateScanState struct {
	mu          sync.Mutex
	cursor      int
	failedUntil map[string]time.Time
}

var candidateScanStates sync.Map

func getCandidateScanState(poolID string) *candidateScanState {
	fresh := &candidateScanState{failedUntil: map[string]time.Time{}}
	actual, _ := candidateScanStates.LoadOrStore(poolID, fresh)
	return actual.(*candidateScanState)
}

func resetCandidateScanState(poolID string) { candidateScanStates.Delete(poolID) }
func dropCandidateScanState(poolID string)  { candidateScanStates.Delete(poolID) }

// attemptOrder 从上次 cursor 继续扫描。5 分钟内刚失败的节点会被跳过；
// 同国家其他出口正在使用的上游节点会被后置，但不会永久跳过。
func (s *candidateScanState) attemptOrder(cands []Node, used map[string]bool, now time.Time) (order []int, cooling int, earliest time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(cands)
	if n == 0 {
		s.cursor = 0
		return nil, 0, time.Time{}
	}
	if s.failedUntil == nil {
		s.failedUntil = map[string]time.Time{}
	}
	if s.cursor < 0 || s.cursor >= n {
		s.cursor = 0
	}
	for key, until := range s.failedUntil {
		if !now.Before(until) {
			delete(s.failedUntil, key)
		}
	}
	deferred := make([]int, 0)
	order = make([]int, 0, n)
	for off := 0; off < n; off++ {
		i := (s.cursor + off) % n
		key := nodeKey(cands[i])
		if until, ok := s.failedUntil[key]; ok && now.Before(until) {
			cooling++
			if earliest.IsZero() || until.Before(earliest) {
				earliest = until
			}
			continue
		}
		if used[key] {
			deferred = append(deferred, i)
		} else {
			order = append(order, i)
		}
	}
	order = append(order, deferred...)
	return order, cooling, earliest
}

// nextRetryDelay 用于无人值守恢复。仍有未冷却候选时立即进入下一轮；
// 如果所有候选都在 5 分钟冷却中，则等待最早一个候选冷却到期。
func (s *candidateScanState) nextRetryDelay(cands []Node, now time.Time) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(cands) == 0 {
		return 0
	}
	if s.failedUntil == nil {
		s.failedUntil = map[string]time.Time{}
	}
	var earliest time.Time
	for _, n := range cands {
		key := nodeKey(n)
		until, cooling := s.failedUntil[key]
		if !cooling || !now.Before(until) {
			if cooling {
				delete(s.failedUntil, key)
			}
			return autoRetryContinueDelay
		}
		if earliest.IsZero() || until.Before(earliest) {
			earliest = until
		}
	}
	if earliest.IsZero() {
		return autoRetryContinueDelay
	}
	d := earliest.Sub(now)
	if d < time.Second {
		d = time.Second
	}
	return d + time.Second
}

func (s *candidateScanState) recordFailure(index, total int, key string, now time.Time) {
	s.recordFailureWithCooldown(index, total, key, now, failedCandidateCooldown)
}

func (s *candidateScanState) recordFailureWithCooldown(index, total int, key string, now time.Time, cooldown time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failedUntil == nil {
		s.failedUntil = map[string]time.Time{}
	}
	if total > 0 {
		s.cursor = (index + 1) % total
	} else {
		s.cursor = 0
	}
	if cooldown <= 0 {
		cooldown = failedCandidateCooldown
	}
	s.failedUntil[key] = now.Add(cooldown)
}

func (s *candidateScanState) recordSuccess(index, total int, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if total > 0 {
		s.cursor = (index + 1) % total
	} else {
		s.cursor = 0
	}
	if s.failedUntil != nil {
		delete(s.failedUntil, key)
	}
}

func (s *candidateScanState) cursorPosition(total int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if total <= 0 {
		return 0
	}
	if s.cursor < 0 || s.cursor >= total {
		s.cursor = 0
	}
	return s.cursor + 1
}

func cooldownWait(until, now time.Time) string {
	if until.IsZero() || !now.Before(until) {
		return "0 秒"
	}
	d := until.Sub(now).Round(time.Second)
	if d < time.Second {
		d = time.Second
	}
	minutes := int(d / time.Minute)
	seconds := int((d % time.Minute) / time.Second)
	if minutes > 0 {
		return fmt.Sprintf("%d 分 %d 秒", minutes, seconds)
	}
	return fmt.Sprintf("%d 秒", seconds)
}
