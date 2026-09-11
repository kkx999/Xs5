package main

import (
	"context"
	"io"
	"math"
	"testing"
	"time"
)

func TestCalculateSpeedRates(t *testing.T) {
	mbps, mbPerSec := calculateSpeedRates(10_000_000, 2*time.Second)
	if math.Abs(mbps-40) > 0.001 {
		t.Fatalf("mbps=%f want 40", mbps)
	}
	if math.Abs(mbPerSec-5) > 0.001 {
		t.Fatalf("MB/s=%f want 5", mbPerSec)
	}
}

func TestGeneratedUploadReaderStreamsExactLimit(t *testing.T) {
	const limit = int64(256*1024 + 17)
	r := newGeneratedUploadReader(limit)
	n, err := io.Copy(io.Discard, r)
	if err != nil {
		t.Fatal(err)
	}
	if n != limit {
		t.Fatalf("streamed=%d want %d", n, limit)
	}
	got, started := r.snapshot()
	if got != limit || started.IsZero() {
		t.Fatalf("snapshot=(%d,%v) want (%d,non-zero)", got, started, limit)
	}
}

func TestSpeedTestCooldownIsTenSeconds(t *testing.T) {
	if speedTestCooldown != 10*time.Second {
		t.Fatalf("cooldown=%s want 10s", speedTestCooldown)
	}
}

func TestSpeedTestTimeLimitReachedDistinguishesDeadlineFromCancel(t *testing.T) {
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer deadlineCancel()
	<-deadlineCtx.Done()
	if !speedTestTimeLimitReached(deadlineCtx) {
		t.Fatal("deadline expiration must be treated as the normal speed-test time limit")
	}

	cancelCtx, cancelOnly := context.WithCancel(context.Background())
	cancelOnly()
	if speedTestTimeLimitReached(cancelCtx) {
		t.Fatal("plain context cancellation must not be treated as the speed-test time limit")
	}
}

func TestApplySpeedTestViewRequiresSameRuntime(t *testing.T) {
	id := "JP-99"
	runtime := runtimeIdentity{source: sourceVPNGate, ip: "198.51.100.10", port: 443}
	result := SpeedTestResult{
		PoolID: id,
		Mbps:   88.8, MBps: 11.1, Bytes: 5_000_000, DurationMS: 450,
		UploadMbps: 40, UploadMBps: 5, UploadBytes: 4_000_000, UploadDurationMS: 800,
		TestedAt: time.Now(),
	}
	speedTestStateMu.Lock()
	speedTestResults[id] = speedTestSnapshot{runtime: runtime, result: result}
	speedTestStateMu.Unlock()
	defer dropSpeedTestState(id)

	var view PoolView
	applySpeedTestView(id, "up", runtime, &view)
	if view.SpeedMBps != result.MBps || view.SpeedBytes != result.Bytes {
		t.Fatalf("matching runtime did not expose download result: %+v", view)
	}
	if view.UploadSpeedMBps != result.UploadMBps || view.UploadSpeedBytes != result.UploadBytes {
		t.Fatalf("matching runtime did not expose upload result: %+v", view)
	}

	var stale PoolView
	applySpeedTestView(id, "up", runtimeIdentity{source: sourceVPNGate, ip: "203.0.113.20", port: 443}, &stale)
	if stale.SpeedMBps != 0 || stale.SpeedBytes != 0 || stale.UploadSpeedMBps != 0 || stale.UploadSpeedBytes != 0 {
		t.Fatalf("stale runtime exposed old speed result: %+v", stale)
	}
}
