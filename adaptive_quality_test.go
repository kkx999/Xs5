package main

import (
	"errors"
	"testing"
	"time"
)

func TestAdaptiveCooldownEscalatesConservatively(t *testing.T) {
	if got := adaptiveCooldown("timeout", 1); got != 5*time.Minute {
		t.Fatalf("first timeout cooldown = %v", got)
	}
	if got := adaptiveCooldown("timeout", 2); got != 10*time.Minute {
		t.Fatalf("second timeout cooldown = %v", got)
	}
	if got := adaptiveCooldown("timeout", 3); got != 20*time.Minute {
		t.Fatalf("third timeout cooldown = %v", got)
	}
	if got := adaptiveCooldown("timeout", 9); got != 30*time.Minute {
		t.Fatalf("cooldown must be capped at 30m, got %v", got)
	}
	if got := adaptiveCooldown("local", 9); got != 0 {
		t.Fatalf("local resource pressure must not punish candidate, got %v", got)
	}
}

func TestAdaptiveFailureClassification(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{errors.New("TLS certificate verify failed"), "hard"},
		{errors.New("connect: connection refused"), "hard"},
		{errors.New("context deadline exceeded"), "timeout"},
		{errors.New("lookup example: no such host"), "dns"},
		{errors.New("ordinary connectivity failed"), "generic"},
	}
	for _, tc := range cases {
		if got := classifyAdaptiveFailure(tc.err); got != tc.want {
			t.Fatalf("classify %q = %q want %q", tc.err, got, tc.want)
		}
	}
}

func TestAdaptiveSourcePatternKeepsVPNGateFirst(t *testing.T) {
	// 无样本时必须完全退回现有固定策略。
	adaptiveQuality = adaptiveQualityStore{}
	now := time.Now()
	got := adaptiveSourcePattern("US", now)
	want := []string{sourceVPNGate, sourceProxio, sourceVPNGate, sourceProxyScrape}
	if len(got) != len(want) {
		t.Fatalf("pattern len = %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pattern[%d] = %s want %s", i, got[i], want[i])
		}
	}
}

func TestAdaptiveCandidateScoreNeedsRealSamples(t *testing.T) {
	adaptiveQuality = adaptiveQualityStore{}
	n := Node{Source: sourceProxio, IP: "198.51.100.10", Port: 1080, CountryCode: "US"}
	if got := adaptiveCandidateScore(n, time.Now()); got != 0 {
		t.Fatalf("unknown candidate score = %d want 0", got)
	}
}
