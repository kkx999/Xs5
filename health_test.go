package main

import (
	"testing"
	"time"
)

func TestHealthCheckTiming(t *testing.T) {
	if healthCheckInterval != 30*time.Second {
		t.Fatalf("healthCheckInterval=%v want 30s", healthCheckInterval)
	}
	if healthFailureRetryDelay != 10*time.Second {
		t.Fatalf("healthFailureRetryDelay=%v want 10s", healthFailureRetryDelay)
	}
}

func TestHealthCheckInFlightGuard(t *testing.T) {
	id := "TEST-HEALTH-GUARD"
	endHealthCheck(id)
	if !beginHealthCheck(id) {
		t.Fatal("first beginHealthCheck should succeed")
	}
	if beginHealthCheck(id) {
		t.Fatal("second beginHealthCheck should be blocked")
	}
	endHealthCheck(id)
	if !beginHealthCheck(id) {
		t.Fatal("beginHealthCheck should succeed after end")
	}
	endHealthCheck(id)
}
