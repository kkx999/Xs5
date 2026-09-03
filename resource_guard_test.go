package main

import (
	"errors"
	"testing"
	"time"
)

func TestLocalResourceErrorClassification(t *testing.T) {
	bad := []string{
		"fork/exec /usr/sbin/ip: resource temporarily unavailable",
		"dial tcp: socket: too many open files",
		"openvpn: cannot allocate memory",
		"connect: no buffer space available",
	}
	for _, msg := range bad {
		if !isLocalResourceError(errors.New(msg)) {
			t.Fatalf("should classify local resource pressure: %q", msg)
		}
	}
	good := []string{
		"context deadline exceeded",
		"connection refused",
		"tls: failed to verify certificate",
		"OpenVPN AUTH_FAILED",
	}
	for _, msg := range good {
		if isLocalResourceError(errors.New(msg)) {
			t.Fatalf("must not classify remote/network failure as local resource pressure: %q", msg)
		}
	}
}

func TestCandidateRetryGapAppliesToBothSources(t *testing.T) {
	if got := candidateRetryGap(Node{Source: sourceProxio}); got != proxioCandidateGap || got <= 0 {
		t.Fatalf("Proxio gap=%v want=%v", got, proxioCandidateGap)
	}
	if got := candidateRetryGap(Node{Source: sourceVPNGate}); got != vpnGateCandidateGap || got <= proxioCandidateGap {
		t.Fatalf("VPN Gate gap=%v want=%v", got, vpnGateCandidateGap)
	}
}

func TestResourceRecoveryDelayIsBounded(t *testing.T) {
	if localResourceRetryDelay < 15*time.Second || localResourceRetryDelay > time.Minute {
		t.Fatalf("localResourceRetryDelay=%v", localResourceRetryDelay)
	}
	if vpnGateRelayLimit < 8 || vpnGateRelayLimit > 128 {
		t.Fatalf("vpnGateRelayLimit=%d", vpnGateRelayLimit)
	}
}
