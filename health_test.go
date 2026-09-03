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

func TestHealthyProbeAcceptsExitIPChange(t *testing.T) {
	p := &Pool{
		Status:    "up",
		ExitIP:    "1.1.1.1",
		FailCount: 1,
		LatencyMS: -1,
		IPType:    "住宅/ISP",
		IPISP:     "old isp",
		IPASN:     "AS1",
		IPRisk:    "old risk",
	}
	applied, changed := applyHealthyProbe(p, "1.1.1.1", "2.2.2.2", 123)
	if !applied || !changed {
		t.Fatalf("applied=%v changed=%v; dynamic exit IP should remain healthy", applied, changed)
	}
	if p.Status != "up" || p.ExitIP != "2.2.2.2" || p.FailCount != 0 || p.LatencyMS != 123 {
		t.Fatalf("unexpected pool state: status=%s exit=%s fail=%d latency=%d", p.Status, p.ExitIP, p.FailCount, p.LatencyMS)
	}
	if p.IPType != "" || p.IPISP != "" || p.IPASN != "" || p.IPRisk != "" {
		t.Fatal("IP profile must be cleared when exit IP changes")
	}
}

func TestHealthyProbeKeepsProfileWhenIPUnchanged(t *testing.T) {
	p := &Pool{Status: "up", ExitIP: "1.1.1.1", FailCount: 1, IPType: "机房 IP", IPISP: "isp", IPASN: "AS1"}
	applied, changed := applyHealthyProbe(p, "1.1.1.1", "1.1.1.1", 80)
	if !applied || changed {
		t.Fatalf("applied=%v changed=%v", applied, changed)
	}
	if p.FailCount != 0 || p.LatencyMS != 80 || p.IPType != "机房 IP" || p.IPISP != "isp" || p.IPASN != "AS1" {
		t.Fatal("same-IP successful probe should only refresh health data")
	}
}

func TestHealthyProbeRejectsStaleResult(t *testing.T) {
	p := &Pool{Status: "up", ExitIP: "3.3.3.3", FailCount: 0}
	applied, changed := applyHealthyProbe(p, "1.1.1.1", "2.2.2.2", 50)
	if applied || changed {
		t.Fatalf("stale probe must be ignored: applied=%v changed=%v", applied, changed)
	}
	if p.ExitIP != "3.3.3.3" {
		t.Fatalf("stale probe overwrote current exit: %s", p.ExitIP)
	}
}
