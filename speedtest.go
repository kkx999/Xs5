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
	speedTestUploadURL   = "https://speed.cloudflare.com/__up"
	speedTestMaxBytes    = int64(50_000_000)
	speedTestMaxDuration = 8 * time.Second
	speedTestHTTPTimeout = 15 * time.Second
	speedTestCooldown    = 10 * time.Second
	speedTestMinUseful   = int64(128 * 1024)
)

type SpeedTestResult struct {
	PoolID           string    `json:"pool_id"`
	ExitIP           string    `json:"exit_ip,omitempty"`
	Mbps             float64   `json:"mbps"`
	MBps             float64   `json:"mb_per_sec"`
	Bytes            int64     `json:"bytes"`
	DurationMS       int64     `json:"duration_ms"`
	UploadMbps       float64   `json:"upload_mbps"`
	UploadMBps       float64   `json:"upload_mb_per_sec"`
	UploadBytes      int64     `json:"upload_bytes"`
	UploadDurationMS int64     `json:"upload_duration_ms"`
	TestedAt         time.Time `json:"tested_at"`
}

type speedTestSnapshot struct {
	runtime runtimeIdentity
	result  SpeedTestResult
}

type generatedUploadReader struct {
	mu        sync.Mutex
	remaining int64
	readBytes int64
	startedAt time.Time
}

func newGeneratedUploadReader(limit int64) *generatedUploadReader {
	return &generatedUploadReader{remaining: limit}
}

func (r *generatedUploadReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	n := len(p)
	if int64(n) > r.remaining {
		n = int(r.remaining)
	}
	for i := 0; i < n; i++ {
		p[i] = 0
	}
	if r.startedAt.IsZero() {
		r.startedAt = time.Now()
	}
	r.remaining -= int64(n)
	r.readBytes += int64(n)
	return n, nil
}

func (r *generatedUploadReader) snapshot() (int64, time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.readBytes, r.startedAt
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
	speedTestStateMu.Unlock()
	if !last.IsZero() && now.Sub(last) < speedTestCooldown {
		cleanup()
		wait := speedTestCooldown - now.Sub(last)
		seconds := int(wait.Round(time.Second) / time.Second)
		if seconds < 1 {
			seconds = 1
		}
		return fmt.Errorf("测速冷却中，请 %d 秒后重试", seconds)
	}
	return nil
}

func endSpeedTest(poolID string) {
	speedTestStateMu.Lock()
	speedTestLast[poolID] = time.Now()
	speedTestStateMu.Unlock()
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
	v.UploadSpeedMbps = snapshot.result.UploadMbps
	v.UploadSpeedMBps = snapshot.result.UploadMBps
	v.UploadSpeedBytes = snapshot.result.UploadBytes
	v.UploadSpeedDurationMS = snapshot.result.UploadDurationMS
	v.SpeedTestedAt = snapshot.result.TestedAt
}

func runDownloadSpeedTest(p *Pool) (int64, time.Duration, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := localPoolHTTPClient(p, speedTestHTTPTimeout)
	url := fmt.Sprintf("%s&cb=%d", speedTestDownloadURL, time.Now().UnixNano())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("下载测速连接失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = resp.Body.Close()
		return 0, 0, fmt.Errorf("下载测速服务器返回 HTTP %d", resp.StatusCode)
	}

	started := time.Now()
	timer := time.AfterFunc(speedTestMaxDuration, cancel)
	buf := make([]byte, 64*1024)
	transferred, readErr := io.CopyBuffer(io.Discard, io.LimitReader(resp.Body, speedTestMaxBytes), buf)
	elapsed := time.Since(started)
	timer.Stop()
	_ = resp.Body.Close()

	timedStop := ctx.Err() != nil && elapsed >= speedTestMaxDuration-300*time.Millisecond
	if readErr != nil && !timedStop {
		return 0, 0, fmt.Errorf("下载测速中断: %w", readErr)
	}
	if transferred < speedTestMinUseful {
		return 0, 0, fmt.Errorf("下载测速样本过小，仅传输 %.2f MB，请稍后重试", float64(transferred)/1_000_000)
	}
	if elapsed <= 0 {
		return 0, 0, errors.New("下载测速耗时无效")
	}
	return transferred, elapsed, nil
}

func runUploadSpeedTest(p *Pool) (int64, time.Duration, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := localPoolHTTPClient(p, speedTestHTTPTimeout)
	body := newGeneratedUploadReader(speedTestMaxBytes)
	url := fmt.Sprintf("%s?cb=%d", speedTestUploadURL, time.Now().UnixNano())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Cache-Control", "no-cache")

	timer := time.AfterFunc(speedTestMaxDuration, cancel)
	resp, requestErr := client.Do(req)
	timer.Stop()
	transferred, started := body.snapshot()
	elapsed := time.Duration(0)
	if !started.IsZero() {
		elapsed = time.Since(started)
	}

	timedStop := ctx.Err() != nil && elapsed >= speedTestMaxDuration-300*time.Millisecond
	if requestErr != nil && !timedStop {
		return 0, 0, fmt.Errorf("上传测速中断: %w", requestErr)
	}
	if resp != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
		_ = resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return 0, 0, fmt.Errorf("上传测速服务器返回 HTTP %d", resp.StatusCode)
		}
	}
	if transferred < speedTestMinUseful {
		return 0, 0, fmt.Errorf("上传测速样本过小，仅传输 %.2f MB，请稍后重试", float64(transferred)/1_000_000)
	}
	if elapsed <= 0 {
		return 0, 0, errors.New("上传测速耗时无效")
	}
	return transferred, elapsed, nil
}

func sameSpeedTestRuntime(p *Pool, runtime runtimeIdentity) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Status != "up" || p.ActiveSource != runtime.source || p.ActiveIP != runtime.ip || p.ActivePort != runtime.port {
		return "", false
	}
	return p.ExitIP, true
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

	downloadBytes, downloadDuration, err := runDownloadSpeedTest(p)
	if err != nil {
		return SpeedTestResult{}, err
	}
	if _, ok := sameSpeedTestRuntime(p, runtime); !ok {
		return SpeedTestResult{}, errors.New("下载测速后出口发生变化，本次结果已作废")
	}

	uploadBytes, uploadDuration, err := runUploadSpeedTest(p)
	if err != nil {
		return SpeedTestResult{}, err
	}
	exitIP, ok := sameSpeedTestRuntime(p, runtime)
	if !ok {
		return SpeedTestResult{}, errors.New("测速期间出口发生变化，本次结果已作废")
	}

	downloadMbps, downloadMBps := calculateSpeedRates(downloadBytes, downloadDuration)
	uploadMbps, uploadMBps := calculateSpeedRates(uploadBytes, uploadDuration)
	result := SpeedTestResult{
		PoolID: p.ID, ExitIP: exitIP,
		Mbps: downloadMbps, MBps: downloadMBps, Bytes: downloadBytes, DurationMS: downloadDuration.Milliseconds(),
		UploadMbps: uploadMbps, UploadMBps: uploadMBps, UploadBytes: uploadBytes, UploadDurationMS: uploadDuration.Milliseconds(),
		TestedAt: time.Now(),
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
