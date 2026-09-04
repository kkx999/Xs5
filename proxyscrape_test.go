package main

import (
	"fmt"
	"testing"
	"time"
)

func psEntry(i int, uptime float64, now time.Time) proxyScrapeEntry {
	return proxyScrapeEntry{
		Protocol: "socks5", IP: fmt.Sprintf("192.0.2.%d", i), Port: 10000 + i,
		Country: "Japan", CountryCode: "JP", Anonymity: "elite",
		UptimePercent: uptime, LatencyMS: float64(50 + i), LastChecked: float64(now.Unix()),
	}
}

func TestProxyScrapeAdaptiveKeepsHighTierOnlyWhenEnough(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	var entries []proxyScrapeEntry
	for i := 1; i <= 12; i++ {
		entries = append(entries, psEntry(i, 70, now))
	}
	for i := 13; i <= 22; i++ {
		entries = append(entries, psEntry(i, 45, now))
	}
	nodes := parseProxyScrapeEntries(entries, now)
	if len(nodes) != 12 {
		t.Fatalf("got %d nodes, want 12 high-tier nodes only", len(nodes))
	}
	for _, n := range nodes {
		if n.Uptime < 0.60 {
			t.Fatalf("unexpected fallback node with uptime %.2f", n.Uptime)
		}
	}
}

func TestProxyScrapeAdaptiveFallsBackOnlyToTarget(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	var entries []proxyScrapeEntry
	for i := 1; i <= 3; i++ {
		entries = append(entries, psEntry(i, 72, now))
	}
	for i := 4; i <= 7; i++ {
		entries = append(entries, psEntry(i, 48, now))
	}
	for i := 8; i <= 12; i++ {
		entries = append(entries, psEntry(i, 30, now))
	}
	entries = append(entries, psEntry(13, 20, now), psEntry(14, 10, now))
	nodes := parseProxyScrapeEntries(entries, now)
	if len(nodes) != proxyScrapeCountryTarget {
		t.Fatalf("got %d nodes, want target %d", len(nodes), proxyScrapeCountryTarget)
	}
	lowTier := 0
	for _, n := range nodes {
		if n.Uptime < 0.25 {
			t.Fatalf("node below floor entered pool: %.2f", n.Uptime)
		}
		if n.Uptime < 0.40 {
			lowTier++
		}
	}
	if lowTier != 3 {
		t.Fatalf("got %d low-tier nodes, want exactly 3 to reach target", lowTier)
	}
}

func TestProxyScrapeAdaptiveStillCapsCountryAtThirty(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	var entries []proxyScrapeEntry
	for i := 1; i <= 40; i++ {
		entries = append(entries, psEntry(i, 85, now))
	}
	nodes := parseProxyScrapeEntries(entries, now)
	if len(nodes) != proxyScrapeCountryLimit {
		t.Fatalf("got %d nodes, want country limit %d", len(nodes), proxyScrapeCountryLimit)
	}
}
