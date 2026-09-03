package main

import (
	"testing"
	"time"
)

func TestProxyScrapeSourceKeepsLegacyAliasSafe(t *testing.T) {
	if got := normalizeSource("proxyscrape"); got != sourceProxio {
		t.Fatalf("legacy proxyscrape alias=%q want %q", got, sourceProxio)
	}
	if got := normalizeSource("proxyscrape_free"); got != sourceProxyScrape {
		t.Fatalf("new source=%q want %q", got, sourceProxyScrape)
	}
}

func TestAllCandidatePoolCrossSourceDeduplicatesAndStartsVPNGate(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	nodes := []Node{
		{Source: sourceVPNGate, Protocol: "openvpn", IP: "192.0.2.10", Host: "vg", CountryCode: "JP", Ping: 80},
		{Source: sourceProxio, Protocol: "socks5", IP: "198.51.100.2", Port: 1080, Host: "198.51.100.2:1080", CountryCode: "JP", Uptime: .8, Score: 80, Ping: 200},
		{Source: sourceProxyScrape, Protocol: "socks5", IP: "198.51.100.2", Port: 1080, Host: "198.51.100.2:1080", CountryCode: "JP", Uptime: .9, Score: 90, Ping: 180},
		{Source: sourceProxyScrape, Protocol: "socks5", IP: "198.51.100.3", Port: 1080, Host: "198.51.100.3:1080", CountryCode: "JP", Uptime: .9, Score: 90, Ping: 220},
	}
	got := buildCandidatePool(nodes, "JP", sourceAll, now)
	if len(got) != 3 {
		t.Fatalf("len=%d want 3: %#v", len(got), got)
	}
	if got[0].Source != sourceVPNGate {
		t.Fatalf("first source=%q want VPN Gate", got[0].Source)
	}
	dup := 0
	for _, n := range got {
		if n.IP == "198.51.100.2" && n.Port == 1080 {
			dup++
			if n.SourceHits != 2 {
				t.Fatalf("source hits=%d want 2", n.SourceHits)
			}
		}
	}
	if dup != 1 {
		t.Fatalf("cross-source endpoint appeared %d times", dup)
	}
}

func TestSpecificSourceDoesNotLoseItsOwnDuplicateEndpoint(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	nodes := []Node{
		{Source: sourceProxio, IP: "198.51.100.4", Port: 1080, CountryCode: "US", Uptime: .8},
		{Source: sourceProxyScrape, IP: "198.51.100.4", Port: 1080, CountryCode: "US", Uptime: .9},
	}
	got := buildCandidatePool(nodes, "US", sourceProxyScrape, now)
	if len(got) != 1 || got[0].Source != sourceProxyScrape {
		t.Fatalf("specific source candidates=%#v", got)
	}
}

func TestProxyScrapeLightFilterAndCountryLimit(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	entries := make([]proxyScrapeEntry, 0, 40)
	for i := 1; i <= 35; i++ {
		entries = append(entries, proxyScrapeEntry{
			Protocol: "socks5", IP: "203.0.113." + itoaTest(i), Port: 10000 + i,
			Country: "Japan", CountryCode: "JP", Anonymity: "elite",
			UptimePercent: 90, LatencyMS: float64(100 + i), LastChecked: float64(now.Unix()),
		})
	}
	entries = append(entries,
		proxyScrapeEntry{Protocol: "http", IP: "198.51.100.10", Port: 80, CountryCode: "JP", UptimePercent: 99, LatencyMS: 20},
		proxyScrapeEntry{Protocol: "socks5", IP: "198.51.100.11", Port: 1080, CountryCode: "JP", UptimePercent: 20, LatencyMS: 20},
		proxyScrapeEntry{Protocol: "socks5", IP: "198.51.100.12", Port: 1080, CountryCode: "JP", UptimePercent: 99, LatencyMS: 6000},
		proxyScrapeEntry{Protocol: "socks5", IP: "198.51.100.13", Port: 1080, CountryCode: "JP", UptimePercent: 99, LatencyMS: 20, Anonymity: "transparent"},
	)
	got := parseProxyScrapeEntries(entries, now)
	if len(got) != proxyScrapeCountryLimit {
		t.Fatalf("len=%d want country limit %d", len(got), proxyScrapeCountryLimit)
	}
	for _, n := range got {
		if n.Source != sourceProxyScrape || n.Protocol != "socks5" || n.CountryCode != "JP" {
			t.Fatalf("unexpected node %#v", n)
		}
	}
}

func itoaTest(v int) string {
	if v < 10 {
		return string(rune('0' + v))
	}
	return string(rune('0'+v/10)) + string(rune('0'+v%10))
}
