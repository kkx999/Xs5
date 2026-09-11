package main

import (
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

func TestApplySpeedTestViewRequiresSameRuntime(t *testing.T) {
	id := "JP-99"
	runtime := runtimeIdentity{source: sourceVPNGate, ip: "198.51.100.10", port: 443}
	result := SpeedTestResult{PoolID: id, Mbps: 88.8, MBps: 11.1, Bytes: 5_000_000, DurationMS: 450, TestedAt: time.Now()}
	speedTestStateMu.Lock()
	speedTestResults[id] = speedTestSnapshot{runtime: runtime, result: result}
	speedTestStateMu.Unlock()
	defer dropSpeedTestState(id)

	var view PoolView
	applySpeedTestView(id, "up", runtime, &view)
	if view.SpeedMbps != result.Mbps || view.SpeedBytes != result.Bytes {
		t.Fatalf("matching runtime did not expose result: %+v", view)
	}

	var stale PoolView
	applySpeedTestView(id, "up", runtimeIdentity{source: sourceVPNGate, ip: "203.0.113.20", port: 443}, &stale)
	if stale.SpeedMbps != 0 || stale.SpeedBytes != 0 {
		t.Fatalf("stale runtime exposed old speed result: %+v", stale)
	}
}
