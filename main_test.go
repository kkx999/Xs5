package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"strconv"
	"testing"
	"time"
)

func TestNormalizeSource(t *testing.T) {
	cases := map[string]string{
		"":            sourceVPNGate,
		"VPNGATE":     sourceVPNGate,
		"proxyscrape": sourceProxio,
		"proxio":      sourceProxio,
		"all":         sourceAll,
		"unknown":     sourceVPNGate,
	}
	for in, want := range cases {
		if got := normalizeSource(in); got != want {
			t.Fatalf("normalizeSource(%q)=%q want %q", in, got, want)
		}
	}
}

func TestParseProxioRowsQuality(t *testing.T) {
	blob := []byte(`[
		{"protocols":["socks5"],"ip":"198.51.100.7","port":1080,"country":"Japan","country_code":"JP","latency_s":0.18,"reliability":91,"uptime":0.93,"last_results":"1110111","isp":"Example ISP"},
		{"protocols":["socks5"],"ip":"198.51.100.8","port":1080,"country":"Japan","country_code":"JP","latency_s":0.22,"reliability":30,"uptime":0.95,"last_results":"1111111"},
		{"protocols":["http"],"ip":"198.51.100.9","port":8080,"country":"Japan","country_code":"JP","latency_s":0.10,"reliability":99,"uptime":0.99}
	]`)
	var root any
	if err := json.Unmarshal(blob, &root); err != nil {
		t.Fatal(err)
	}
	rows := proxyRows(root)
	nodes := parseProxioRows(rows)
	if len(nodes) != 1 {
		t.Fatalf("got %d proxio nodes, want 1: %+v", len(nodes), nodes)
	}
	n := nodes[0]
	if n.Source != sourceProxio || n.CountryCode != "JP" || n.Port != 1080 || n.Ping != 180 || n.Score != 91 {
		t.Fatalf("unexpected proxio node: %+v", n)
	}
}

func TestCountryCodeFromName(t *testing.T) {
	if got := countryCodeFromName("Japan"); got != "JP" {
		t.Fatalf("Japan -> %q", got)
	}
	if got := countryCodeFromName("South Korea"); got != "KR" {
		t.Fatalf("South Korea -> %q", got)
	}
}

func TestDialSOCKS5Context(t *testing.T) {
	echoLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echoLn.Close()
	go func() {
		for {
			c, err := echoLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(c)
		}
	}()

	proxyLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer proxyLn.Close()
	go fakeSOCKS5Server(proxyLn)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := dialSOCKS5Context(ctx, proxyLn.Addr().String(), echoLn.Addr().String())
	if err != nil {
		t.Fatalf("dialSOCKS5Context: %v", err)
	}
	defer c.Close()
	msg := []byte("hello")
	if _, err := c.Write(msg); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != string(msg) {
		t.Fatalf("got %q want %q", buf, msg)
	}
}

func fakeSOCKS5Server(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			var h [2]byte
			if _, err := io.ReadFull(c, h[:]); err != nil || h[0] != 5 {
				return
			}
			methods := make([]byte, int(h[1]))
			if _, err := io.ReadFull(c, methods); err != nil {
				return
			}
			_, _ = c.Write([]byte{5, 0})
			var req [4]byte
			if _, err := io.ReadFull(c, req[:]); err != nil || req[0] != 5 || req[1] != 1 {
				return
			}
			var host string
			switch req[3] {
			case 1:
				b := make([]byte, 4)
				if _, err := io.ReadFull(c, b); err != nil {
					return
				}
				host = net.IP(b).String()
			case 3:
				var n [1]byte
				if _, err := io.ReadFull(c, n[:]); err != nil {
					return
				}
				b := make([]byte, int(n[0]))
				if _, err := io.ReadFull(c, b); err != nil {
					return
				}
				host = string(b)
			default:
				return
			}
			var pb [2]byte
			if _, err := io.ReadFull(c, pb[:]); err != nil {
				return
			}
			port := int(pb[0])<<8 | int(pb[1])
			up, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), time.Second)
			if err != nil {
				_, _ = c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
				return
			}
			defer up.Close()
			_, _ = c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
			go io.Copy(up, c)
			_, _ = io.Copy(c, up)
		}(c)
	}
}

func TestMultiplePoolsSameCountryOrdinals(t *testing.T) {
	a := &App{Pools: map[string]*Pool{
		"JP-1": {ID: "JP-1", Ordinal: 1, CountryCode: "JP"},
		"JP-2": {ID: "JP-2", Ordinal: 2, CountryCode: "JP"},
		"KR-1": {ID: "KR-1", Ordinal: 1, CountryCode: "KR"},
	}}
	if got := a.nextPoolOrdinalLocked("JP"); got != 3 {
		t.Fatalf("next JP ordinal=%d want 3", got)
	}
	if got := a.nextPoolOrdinalLocked("KR"); got != 2 {
		t.Fatalf("next KR ordinal=%d want 2", got)
	}
	if got := poolID("jp", 3); got != "JP-3" {
		t.Fatalf("poolID=%q want JP-3", got)
	}
}

func TestPrioritizeUnusedCandidates(t *testing.T) {
	a := Node{Source: sourceVPNGate, IP: "192.0.2.1", Host: "a", CountryCode: "JP"}
	b := Node{Source: sourceVPNGate, IP: "192.0.2.2", Host: "b", CountryCode: "JP"}
	c := Node{Source: sourceVPNGate, IP: "192.0.2.3", Host: "c", CountryCode: "JP"}
	got := prioritizeUnused([]Node{a, b, c}, map[string]bool{nodeKey(a): true})
	if len(got) != 3 || nodeKey(got[0]) != nodeKey(b) || nodeKey(got[1]) != nodeKey(c) || nodeKey(got[2]) != nodeKey(a) {
		t.Fatalf("unexpected order: %+v", got)
	}
}

func TestOpenVPNRemote(t *testing.T) {
	host, port, proto := openVPNRemote("client\nproto tcp\nremote 203.0.113.9 443\n")
	if host != "203.0.113.9" || port != 443 || proto != "tcp" {
		t.Fatalf("unexpected remote: %q %d %q", host, port, proto)
	}
}

func TestProfileFromSignals(t *testing.T) {
	tv, fv := true, false
	p := profileFromSignals("Amazon Technologies", "AS16509 AMAZON-02", "Amazon.com", &tv, &fv, &fv)
	if p.Type != "机房 IP" {
		t.Fatalf("hosting profile=%q", p.Type)
	}
	p = profileFromSignals("Example Broadband ISP", "AS64500 Example", "Example ISP", &fv, &fv, &fv)
	if p.Type != "住宅/ISP" {
		t.Fatalf("isp profile=%q", p.Type)
	}
	p = profileFromSignals("Example Mobile", "AS64501 Example", "Example", &fv, &tv, &fv)
	if p.Type != "移动网络" {
		t.Fatalf("mobile profile=%q", p.Type)
	}
}
