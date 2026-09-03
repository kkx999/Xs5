package main

import (
	"testing"
	"time"
)

func TestHealthCheckPolicy(t *testing.T) {
	if healthCheckInterval != 30*time.Second {
		t.Fatalf("healthCheckInterval=%v want 30s", healthCheckInterval)
	}
	if healthRetryDelayOne != 5*time.Second || healthRetryDelayTwo != 10*time.Second {
		t.Fatalf("retry delays=%v/%v want 5s/10s", healthRetryDelayOne, healthRetryDelayTwo)
	}
	if healthFailureLimit != 3 {
		t.Fatalf("healthFailureLimit=%d want 3", healthFailureLimit)
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
}

func TestAvailabilityHTTPStatus(t *testing.T) {
	for _, code := range []int{200, 204, 301, 302, 399} {
		if !availabilityHTTPStatusOK(code) {
			t.Fatalf("HTTP %d should count as reachable", code)
		}
	}
	for _, code := range []int{0, 199, 400, 403, 500} {
		if availabilityHTTPStatusOK(code) {
			t.Fatalf("HTTP %d should not count as reachable", code)
		}
	}
}

func TestConnectivityEndpointsAreOrdinaryHTTPS(t *testing.T) {
	if len(connectivityProbeEndpoints) != 3 {
		t.Fatalf("connectivity endpoints=%d want 3", len(connectivityProbeEndpoints))
	}
	want := map[string]bool{
		"https://www.google.com/generate_204":             true,
		"https://cp.cloudflare.com/generate_204":          true,
		"https://www.apple.com/library/test/success.html": true,
	}
	for _, endpoint := range connectivityProbeEndpoints {
		if !want[endpoint] {
			t.Fatalf("unexpected connectivity endpoint %q", endpoint)
		}
	}
}

func TestRuntimeIdentityGuard(t *testing.T) {
	p := &Pool{Status: "up", ActiveSource: sourceProxio, ActiveIP: "198.51.100.1", ActivePort: 1080}
	id, ok := currentRuntimeIdentity(p)
	if !ok || !runtimeStillMatches(p, id) {
		t.Fatal("current runtime should match")
	}
	p.mu.Lock()
	p.ActiveIP = "198.51.100.2"
	p.mu.Unlock()
	if runtimeStillMatches(p, id) {
		t.Fatal("stale health result must not apply after runtime changes")
	}
}
