package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"
)

const (
	verifiedStrongWindow = 30 * time.Minute
	verifiedUsefulWindow = 6 * time.Hour
	verifiedKeepWindow   = 24 * time.Hour
	verifiedPersistGap   = 5 * time.Minute
)

type verifiedCandidateRecord struct {
	LastSuccess time.Time `json:"last_success"`
	LatencyMS   int       `json:"latency_ms"`
	Successes   int       `json:"successes"`
}

type verifiedCandidateStore struct {
	once        sync.Once
	mu          sync.RWMutex
	records     map[string]verifiedCandidateRecord
	lastPersist time.Time
}

var verifiedCandidates verifiedCandidateStore

func isSOCKSProxySource(source string) bool {
	return source == sourceProxio || source == sourceProxyScrape
}

func sourceNodeKey(n Node) string {
	return fmt.Sprintf("%s|%s|%d|%s", n.Source, n.IP, n.Port, n.Host)
}

func socksEndpointKey(n Node) string {
	if !isSOCKSProxySource(n.Source) || net.ParseIP(n.IP) == nil || n.Port < 1 {
		return ""
	}
	return net.JoinHostPort(n.IP, strconv.Itoa(n.Port))
}

func verificationKey(n Node) string {
	if endpoint := socksEndpointKey(n); endpoint != "" {
		return "socks5|" + endpoint
	}
	if n.Source == sourceVPNGate {
		host, port, proto := openVPNRemote(n.Config)
		return fmt.Sprintf("vpngate|%s|%s|%d|%s", n.IP, host, port, proto)
	}
	return sourceNodeKey(n)
}

func (s *verifiedCandidateStore) load() {
	s.once.Do(func() {
		s.records = map[string]verifiedCandidateRecord{}
		b, err := os.ReadFile(filepath.Join(workDir, "verified_nodes.json"))
		if err != nil {
			return
		}
		var records map[string]verifiedCandidateRecord
		if json.Unmarshal(b, &records) == nil && records != nil {
			s.records = records
		}
	})
}

func (s *verifiedCandidateStore) rank(n Node, now time.Time) (int, int) {
	s.load()
	key := verificationKey(n)
	s.mu.RLock()
	rec, ok := s.records[key]
	s.mu.RUnlock()
	if !ok || rec.LastSuccess.IsZero() || rec.LastSuccess.After(now.Add(time.Minute)) {
		return 0, -1
	}
	age := now.Sub(rec.LastSuccess)
	switch {
	case age <= verifiedStrongWindow:
		return 2, rec.LatencyMS
	case age <= verifiedUsefulWindow:
		return 1, rec.LatencyMS
	default:
		return 0, rec.LatencyMS
	}
}

func (s *verifiedCandidateStore) record(key string, latency int, now time.Time) {
	if key == "" {
		return
	}
	s.load()
	s.mu.Lock()
	rec := s.records[key]
	rec.LastSuccess = now
	if latency >= 0 {
		rec.LatencyMS = latency
	}
	rec.Successes++
	s.records[key] = rec
	for k, old := range s.records {
		if !old.LastSuccess.IsZero() && now.Sub(old.LastSuccess) > verifiedKeepWindow {
			delete(s.records, k)
		}
	}
	shouldPersist := s.lastPersist.IsZero() || now.Sub(s.lastPersist) >= verifiedPersistGap
	if shouldPersist {
		s.lastPersist = now
	}
	snapshot := make(map[string]verifiedCandidateRecord, len(s.records))
	if shouldPersist {
		for k, v := range s.records {
			snapshot[k] = v
		}
	}
	s.mu.Unlock()
	if shouldPersist {
		persistVerifiedCandidates(snapshot)
	}
}

func persistVerifiedCandidates(records map[string]verifiedCandidateRecord) {
	if len(records) == 0 {
		return
	}
	if err := os.MkdirAll(workDir, 0700); err != nil {
		return
	}
	b, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return
	}
	path := filepath.Join(workDir, "verified_nodes.json")
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0600) != nil {
		return
	}
	_ = os.Chmod(tmp, 0600)
	_ = os.Rename(tmp, path)
}

func recordVerifiedCandidate(n Node, latency int) {
	verifiedCandidates.record(verificationKey(n), latency, time.Now())
}

func recordVerifiedRuntime(id runtimeIdentity, latency int) {
	n := Node{Source: id.source, IP: id.ip, Port: id.port}
	if id.source == sourceVPNGate {
		// VPN Gate 的运行态没有完整配置指纹；切换成功时已经用完整 Node 记录过。
		return
	}
	verifiedCandidates.record(verificationKey(n), latency, time.Now())
}

func candidateSourcePriority(source string) int {
	switch source {
	case sourceVPNGate:
		return 0
	case sourceProxio:
		return 1
	case sourceProxyScrape:
		return 2
	default:
		return 9
	}
}

func sortCandidateQuality(nodes []Node, now time.Time) {
	sort.SliceStable(nodes, func(i, j int) bool {
		ri, li := verifiedCandidates.rank(nodes[i], now)
		rj, lj := verifiedCandidates.rank(nodes[j], now)
		if ri != rj {
			return ri > rj
		}
		if nodes[i].SourceHits != nodes[j].SourceHits {
			return nodes[i].SourceHits > nodes[j].SourceHits
		}
		if isSOCKSProxySource(nodes[i].Source) && isSOCKSProxySource(nodes[j].Source) {
			if nodes[i].Uptime != nodes[j].Uptime {
				return nodes[i].Uptime > nodes[j].Uptime
			}
			if nodes[i].Score != nodes[j].Score {
				return nodes[i].Score > nodes[j].Score
			}
		}
		if nodes[i].Ping <= 0 && nodes[j].Ping > 0 {
			return false
		}
		if nodes[j].Ping <= 0 && nodes[i].Ping > 0 {
			return true
		}
		if nodes[i].Ping != nodes[j].Ping {
			return nodes[i].Ping < nodes[j].Ping
		}
		if ri > 0 && li >= 0 && lj >= 0 && li != lj {
			return li < lj
		}
		if nodes[i].Speed != nodes[j].Speed {
			return nodes[i].Speed > nodes[j].Speed
		}
		if nodes[i].Score != nodes[j].Score {
			return nodes[i].Score > nodes[j].Score
		}
		if candidateSourcePriority(nodes[i].Source) != candidateSourcePriority(nodes[j].Source) {
			return candidateSourcePriority(nodes[i].Source) < candidateSourcePriority(nodes[j].Source)
		}
		return sourceNodeKey(nodes[i]) < sourceNodeKey(nodes[j])
	})
}

func betterCrossSourceDuplicate(a, b Node, now time.Time) Node {
	// 同一 IP:port 实际是同一个 SOCKS5 端点，优先保留近期真实成功、元数据更稳定的一份。
	ra, _ := verifiedCandidates.rank(a, now)
	rb, _ := verifiedCandidates.rank(b, now)
	if ra != rb {
		if rb > ra {
			return b
		}
		return a
	}
	if b.Uptime != a.Uptime {
		if b.Uptime > a.Uptime {
			return b
		}
		return a
	}
	if b.Score != a.Score {
		if b.Score > a.Score {
			return b
		}
		return a
	}
	if a.Ping <= 0 || (b.Ping > 0 && b.Ping < a.Ping) {
		return b
	}
	if candidateSourcePriority(b.Source) < candidateSourcePriority(a.Source) {
		return b
	}
	return a
}

func buildCandidatePool(all []Node, country, sourceMode string, now time.Time) []Node {
	sourceMode = normalizeSource(sourceMode)
	if sourceMode != sourceAll {
		out := make([]Node, 0)
		for _, n := range all {
			if n.CountryCode == country && n.Source == sourceMode {
				if n.SourceHits == 0 {
					n.SourceHits = 1
				}
				out = append(out, n)
			}
		}
		sortCandidateQuality(out, now)
		return out
	}

	// VPN Gate 是不同传输架构，不与 SOCKS5 端点按 IP 粗暴合并。
	buckets := map[string][]Node{
		sourceVPNGate: {}, sourceProxio: {}, sourceProxyScrape: {},
	}
	socks := map[string]Node{}
	hits := map[string]map[string]bool{}
	for _, n := range all {
		if n.CountryCode != country {
			continue
		}
		if n.Source == sourceVPNGate {
			if n.SourceHits == 0 {
				n.SourceHits = 1
			}
			buckets[sourceVPNGate] = append(buckets[sourceVPNGate], n)
			continue
		}
		if !isSOCKSProxySource(n.Source) {
			continue
		}
		key := socksEndpointKey(n)
		if key == "" {
			continue
		}
		if hits[key] == nil {
			hits[key] = map[string]bool{}
		}
		hits[key][n.Source] = true
		current, ok := socks[key]
		if !ok {
			socks[key] = n
		} else {
			socks[key] = betterCrossSourceDuplicate(current, n, now)
		}
	}
	for key, n := range socks {
		n.SourceHits = len(hits[key])
		buckets[n.Source] = append(buckets[n.Source], n)
	}
	for source := range buckets {
		sortCandidateQuality(buckets[source], now)
	}

	// VPN Gate 默认优先，但交错 SOCKS5 备用源，避免多个重型 OpenVPN 失败耗尽整轮 90 秒窗口。
	pattern := []string{sourceVPNGate, sourceProxio, sourceVPNGate, sourceProxyScrape}
	positions := map[string]int{}
	total := len(buckets[sourceVPNGate]) + len(buckets[sourceProxio]) + len(buckets[sourceProxyScrape])
	out := make([]Node, 0, total)
	for len(out) < total {
		progressed := false
		for _, source := range pattern {
			pos := positions[source]
			if pos >= len(buckets[source]) {
				continue
			}
			out = append(out, buckets[source][pos])
			positions[source] = pos + 1
			progressed = true
		}
		if !progressed {
			break
		}
	}
	return out
}
