package main

import (
	"testing"
	"time"
)

func testScanNodes() []Node {
	return []Node{
		{Source: sourceProxio, IP: "192.0.2.1", Port: 1001, Host: "a"},
		{Source: sourceProxio, IP: "192.0.2.2", Port: 1002, Host: "b"},
		{Source: sourceProxio, IP: "192.0.2.3", Port: 1003, Host: "c"},
		{Source: sourceProxio, IP: "192.0.2.4", Port: 1004, Host: "d"},
	}
}

func TestCandidateScanContinuesAndCoolsFailures(t *testing.T) {
	nodes := testScanNodes()
	state := &candidateScanState{failedUntil: map[string]time.Time{}}
	now := time.Unix(1000, 0)
	order, cooling, _ := state.attemptOrder(nodes, nil, now)
	if cooling != 0 || len(order) != 4 || order[0] != 0 {
		t.Fatalf("initial order=%v cooling=%d", order, cooling)
	}
	state.recordFailure(0, len(nodes), nodeKey(nodes[0]), now)
	state.recordFailure(1, len(nodes), nodeKey(nodes[1]), now.Add(time.Second))
	order, cooling, _ = state.attemptOrder(nodes, nil, now.Add(2*time.Second))
	if cooling != 2 || len(order) != 2 || order[0] != 2 || order[1] != 3 {
		t.Fatalf("continued order=%v cooling=%d, want [2 3] cooling=2", order, cooling)
	}
	if got := state.cursorPosition(len(nodes)); got != 3 {
		t.Fatalf("cursor position=%d want 3", got)
	}
}

func TestCandidateCooldownExpiresAfterFiveMinutes(t *testing.T) {
	nodes := testScanNodes()
	state := &candidateScanState{failedUntil: map[string]time.Time{}}
	now := time.Unix(2000, 0)
	state.recordFailure(0, len(nodes), nodeKey(nodes[0]), now)
	order, cooling, _ := state.attemptOrder(nodes, nil, now.Add(4*time.Minute+59*time.Second))
	if cooling != 1 || len(order) != 3 {
		t.Fatalf("before expiry order=%v cooling=%d", order, cooling)
	}
	order, cooling, _ = state.attemptOrder(nodes, nil, now.Add(5*time.Minute+time.Second))
	if cooling != 0 || len(order) != 4 {
		t.Fatalf("after expiry order=%v cooling=%d", order, cooling)
	}
}

func TestUsedCandidateIsDeferredWithoutBreakingCursor(t *testing.T) {
	nodes := testScanNodes()
	state := &candidateScanState{cursor: 1, failedUntil: map[string]time.Time{}}
	used := map[string]bool{nodeKey(nodes[1]): true}
	order, _, _ := state.attemptOrder(nodes, used, time.Unix(3000, 0))
	want := []int{2, 3, 0, 1}
	if len(order) != len(want) {
		t.Fatalf("order=%v", order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order=%v want=%v", order, want)
		}
	}
}

func TestNextRetryDelayContinuesWhenUntestedCandidatesRemain(t *testing.T) {
	nodes := testScanNodes()
	state := &candidateScanState{failedUntil: map[string]time.Time{}}
	now := time.Unix(4000, 0)
	state.recordFailure(0, len(nodes), nodeKey(nodes[0]), now)
	if got := state.nextRetryDelay(nodes, now.Add(time.Second)); got != autoRetryContinueDelay {
		t.Fatalf("retry delay=%v want=%v", got, autoRetryContinueDelay)
	}
}

func TestNextRetryDelayWaitsForEarliestCooldown(t *testing.T) {
	nodes := testScanNodes()
	state := &candidateScanState{failedUntil: map[string]time.Time{}}
	now := time.Unix(5000, 0)
	for i, n := range nodes {
		state.recordFailure(i, len(nodes), nodeKey(n), now.Add(time.Duration(i)*time.Second))
	}
	got := state.nextRetryDelay(nodes, now.Add(time.Minute))
	if got < 4*time.Minute || got > 4*time.Minute+2*time.Second {
		t.Fatalf("retry delay=%v want about 4m", got)
	}
}
