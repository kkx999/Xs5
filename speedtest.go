package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

const (
	speedTestDownloadURL = "https://speed.cloudflare.com/__down?bytes=50000000"
	speedTestMaxBytes    = int64(50_000_000)
	speedTestMaxDuration = 8 * time.Second
	speedTestHTTPTimeout = 15 * time.Second
	speedTestCooldown    = 30 * time.Second
	speedTestMinUseful   = int64(128 * 1024)
)

type SpeedTestResult struct {
	PoolID     string    `json:"pool_id"`
	ExitIP     string    `json:"exit_ip,omitempty"`
	Mbps       float64   `json:"mbps"`
	MBps       float64   `json:"mb_per_sec"`
	Bytes      int64     `json:"bytes"`
	DurationMS int64     `json:"duration_ms"`
	TestedAt   time.Time `json:"tested_at"`
}

type speedTestSnapshot struct {
	runtime runtimeIdentity
	result  SpeedTestResult
}

var (
	speedTestGate      = make(chan struct{}, 1)
	speedTestsInFlight sync.Map
	speedTestStateMu   sync.Mutex
	speedTestLast      = map[string]time.Time{}
	speedTestResults   = map[string]speedTestSnapshot{}
)

func calculateSpeedRates(bytes int64, d time.Duration) (float64, float64) {
	seconds := d.Seconds()
	if bytes <= 0 || seconds <= 0 {
		return 0, 0
	}
	mbps := float64(bytes*8) / seconds / 1_000_000
	mbPerSec := float64(bytes) / seconds / 1_000_000
	return mbps, mbPerSec
}

func speedTestRunning(poolID string) bool {
	_, ok := speedTestsInFlight.Load(poolID)
	return ok
}

func beginSpeedTest(poolID string) error {
	select {
	case speedTestGate <- struct{}{}:
	default:
		return errors.New("当前已有其他出口正在测速，请稍后重试")
	}

	speedTestsInFlight.Store(poolID, struct{}{})
	cleanup := func() {
		speedTestsInFlight.Delete(poolID)
		<-speedTestGate
	}

	// Never overlap a manual throughput test with the automatic health probe for
	// the same pool. This prevents the short bandwidth burst from being mistaken
	// for a health failure.
	if _, running := healthChecksInFlight.Load(poolID); running {
		cleanup()
		return errors.New("当前出口正在健康检测，请稍后重试")
	}

	now := time.Now()
	speedTestStateMu.Lock()
	last := speedTestLast[poolID]
	if !last.IsZero() && now.Sub(last) < speedTestCooldown {
		wait := speedTestCooldown - now.Sub(last)
		speedTestStateMu.Unlock()
		cleanup()
		seconds := int(wait.Round(time.Second) / time.Second)
		if seconds < 1 {
			seconds = 1
		}
		return fmt.Errorf("测速冷却中，请 %d 秒后重试", seconds)
	}
	speedTestLast[poolID] = now
	speedTestStateMu.Unlock()
	return nil
}

func endSpeedTest(poolID string) {
	speedTestsInFlight.Delete(poolID)
	<-speedTestGate
}

func dropSpeedTestState(poolID string) {
	speedTestStateMu.Lock()
	delete(speedTestLast, poolID)
	delete(speedTestResults, poolID)
	speedTestStateMu.Unlock()
}

func applySpeedTestView(poolID, status string, runtime runtimeIdentity, v *PoolView) {
	if v == nil || status != "up" {
		return
	}
	speedTestStateMu.Lock()
	snapshot, ok := speedTestResults[poolID]
	speedTestStateMu.Unlock()
	if !ok || snapshot.runtime != runtime {
		return
	}
	v.SpeedMbps = snapshot.result.Mbps
	v.SpeedMBps = snapshot.result.MBps
	v.SpeedBytes = snapshot.result.Bytes
	v.SpeedDurationMS = snapshot.result.DurationMS
	v.SpeedTestedAt = snapshot.result.TestedAt
}

func runPoolSpeedTest(p *Pool) (SpeedTestResult, error) {
	if p == nil {
		return SpeedTestResult{}, errors.New("出口不存在")
	}
	runtime, ok := currentRuntimeIdentity(p)
	if !ok {
		return SpeedTestResult{}, errors.New("当前出口未处于正常状态，无法测速")
	}
	if err := beginSpeedTest(p.ID); err != nil {
		return SpeedTestResult{}, err
	}
	defer endSpeedTest(p.ID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := localPoolHTTPClient(p, speedTestHTTPTimeout)
	url := fmt.Sprintf("%s&cb=%d", speedTestDownloadURL, time.Now().UnixNano())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return SpeedTestResult{}, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := client.Do(req)
	if err != nil {
		return SpeedTestResult{}, fmt.Errorf("测速连接失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = resp.Body.Close()
		return SpeedTestResult{}, fmt.Errorf("测速服务器返回 HTTP %d", resp.StatusCode)
	}

	started := time.Now()
	timer := time.AfterFunc(speedTestMaxDuration, cancel)
	buf := make([]byte, 64*1024)
	transferred, readErr := io.CopyBuffer(io.Discard, io.LimitReader(resp.Body, speedTestMaxBytes), buf)
	elapsed := time.Since(started)
	timer.Stop()
	_ = resp.Body.Close()

	// Cancellation at the configured time limit is an expected successful stop.
	timedStop := ctx.Err() != nil && elapsed >= speedTestMaxDuration-300*time.Millisecond
	if readErr != nil && !timedStop {
		return SpeedTestResult{}, fmt.Errorf("测速下载中断: %w", readErr)
	}
	if transferred < speedTestMinUseful {
		return SpeedTestResult{}, fmt.Errorf("测速样本过小，仅传输 %.2f MB，请稍后重试", float64(transferred)/1_000_000)
	}
	if elapsed <= 0 {
		return SpeedTestResult{}, errors.New("测速耗时无效")
	}

	p.mu.Lock()
	if p.Status != "up" || p.ActiveSource != runtime.source || p.ActiveIP != runtime.ip || p.ActivePort != runtime.port {
		p.mu.Unlock()
		return SpeedTestResult{}, errors.New("测速期间出口发生变化，本次结果已作废")
	}
	exitIP := p.ExitIP
	p.mu.Unlock()

	mbps, mbPerSec := calculateSpeedRates(transferred, elapsed)
	result := SpeedTestResult{
		PoolID: p.ID, ExitIP: exitIP, Mbps: mbps, MBps: mbPerSec,
		Bytes: transferred, DurationMS: elapsed.Milliseconds(), TestedAt: time.Now(),
	}
	speedTestStateMu.Lock()
	speedTestResults[p.ID] = speedTestSnapshot{runtime: runtime, result: result}
	speedTestStateMu.Unlock()
	return result, nil
}

func (a *App) speedTestPool(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	id := r.FormValue("id")
	a.mu.RLock()
	p := a.Pools[id]
	a.mu.RUnlock()
	if p == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "出口不存在"})
		return
	}
	result, err := runPoolSpeedTest(p)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}
