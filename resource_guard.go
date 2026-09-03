package main

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	localResourceRetryDelay   = 30 * time.Second
	localPressureHealthWindow = 20 * time.Second
	proxioCandidateGap        = 300 * time.Millisecond
	vpnGateCandidateGap       = 2 * time.Second
	vpnGateRelayLimit         = 32
	vpnGateRelayQueueWait     = 5 * time.Second
	openVPNReapWait           = 3 * time.Second
)

var (
	vpnGateActivationMu       sync.Mutex
	vpnGateRelaySlots         = make(chan struct{}, vpnGateRelayLimit)
	lastLocalResourcePressure atomic.Int64
)

// isLocalResourceError 识别的是本机资源/进程创建失败，而不是远端节点质量问题。
// 这类错误不能把候选节点加入 5 分钟冷却，否则会把服务器自身的瞬时压力误算到公共节点头上。
func isLocalResourceError(err error) bool {
	if err == nil {
		return false
	}
	v := strings.ToLower(err.Error())
	patterns := []string{
		"resource temporarily unavailable",
		"cannot allocate memory",
		"not enough memory",
		"too many open files",
		"too many processes",
		"cannot create child process",
		"failed to create new os thread",
		"no buffer space available",
		"cannot assign requested address",
	}
	for _, p := range patterns {
		if strings.Contains(v, p) {
			return true
		}
	}
	return false
}

func noteLocalResourcePressure() {
	lastLocalResourcePressure.Store(time.Now().UnixNano())
}

func recentLocalResourcePressure(window time.Duration) bool {
	ns := lastLocalResourcePressure.Load()
	if ns == 0 {
		return false
	}
	return time.Since(time.Unix(0, ns)) <= window
}

func candidateRetryGap(node Node) time.Duration {
	if node.Source == sourceVPNGate {
		return vpnGateCandidateGap
	}
	return proxioCandidateGap
}

func waitCandidateGap(node Node, deadline time.Time) bool {
	d := candidateRetryGap(node)
	if d <= 0 {
		return true
	}
	if !deadline.IsZero() && time.Now().Add(d).After(deadline) {
		return false
	}
	t := time.NewTimer(d)
	defer t.Stop()
	<-t.C
	return true
}

func acquireVPNGateRelaySlot() error {
	t := time.NewTimer(vpnGateRelayQueueWait)
	defer t.Stop()
	select {
	case vpnGateRelaySlots <- struct{}{}:
		return nil
	case <-t.C:
		noteLocalResourcePressure()
		return errors.New("VPN Gate 本机转发进程已达到安全并发上限")
	}
}

func releaseVPNGateRelaySlot() {
	select {
	case <-vpnGateRelaySlots:
	default:
	}
}
