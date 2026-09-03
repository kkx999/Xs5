package main

import (
	"encoding/json"
	"testing"
)

func TestRelaxedProxioPrefilterStillRequiresQuality(t *testing.T) {
	blob := []byte(`[
		{"protocols":["socks5"],"ip":"198.51.100.21","port":1080,"country":"Japan","country_code":"JP","latency_s":4.5,"reliability":72,"uptime":0.65,"last_results":"11100"},
		{"protocols":["socks5"],"ip":"198.51.100.22","port":1080,"country":"Japan","country_code":"JP","latency_s":4.5,"reliability":69,"uptime":0.65,"last_results":"11100"}
	]`)
	var root any
	if err := json.Unmarshal(blob, &root); err != nil {
		t.Fatal(err)
	}
	nodes := parseProxioRows(proxyRows(root))
	if len(nodes) != 1 || nodes[0].IP != "198.51.100.21" {
		t.Fatalf("unexpected relaxed prefilter result: %+v", nodes)
	}
}

func TestTLSVerificationFailureDetection(t *testing.T) {
	bad := []string{
		"tls: failed to verify certificate: x509: certificate has expired",
		"SSL certificate problem: certificate has expired",
		"x509: certificate signed by unknown authority",
	}
	for _, v := range bad {
		if !tlsVerificationFailed(v) {
			t.Fatalf("should classify TLS verification failure: %q", v)
		}
	}
	if tlsVerificationFailed("context deadline exceeded") {
		t.Fatal("timeout must not be classified as TLS verification failure")
	}
}

func TestParseProbeIPv4(t *testing.T) {
	if got, err := parseProbeIP([]byte("203.0.113.7\n")); err != nil || got != "203.0.113.7" {
		t.Fatalf("parseProbeIP=%q err=%v", got, err)
	}
	if _, err := parseProbeIP([]byte("not-an-ip")); err == nil {
		t.Fatal("invalid probe body must fail")
	}
}
