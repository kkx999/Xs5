package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	adaptiveQualityPath       = "/var/lib/xs5/adaptive_quality.json"
	adaptivePersistGap        = 2 * time.Minute
	adaptiveInfluenceWindow   = 7 * 24 * time.Hour
	adaptiveKeepWindow        = 30 * 24 * time.Hour
	adaptiveSourceMinAttempts = 12
)

type adaptiveNodeRecord struct {
	CountryCode         string    `json:"country_code"`
	Source              string    `json:"source"`
	Attempts            int       `json:"attempts"`
	Successes           int       `json:"successes"`
	Failures            int       `json:"failures"`
	Drops               int       `json:"drops"`
	ConsecutiveSuccess  int       `json:"consecutive_success"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	LastAttempt         time.Time `json:"last_attempt,omitempty"`
	LastSuccess         time.Time `json:"last_success,omitempty"`
	LastFailure         time.Time `json:"last_failure,omitempty"`
	AvgLatencyMS        float64   `json:"avg_latency_ms,omitempty"`
	LatencySamples      int       `json:"latency_samples,omitempty"`
	RuntimeSeconds      int64     `json:"runtime_seconds,omitempty"`
	RuntimeSessions     int       `json:"runtime_sessions,omitempty"`
	LongestRuntime      int64     `json:"longest_runtime_seconds,omitempty"`
	LastExitIP          string    `json:"last_exit_ip,omitempty"`
	LastIPType          string    `json:"last_ip_type,omitempty"`
	LastISP             string    `json:"last_isp,omitempty"`
	LastASN             string    `json:"last_asn,omitempty"`
	LastRisk            string    `json:"last_risk,omitempty"`
}

type adaptiveSourceRecord struct {
	CountryCode    string    `json:"country_code"`
	Source         string    `json:"source"`
	Attempts       int       `json:"attempts"`
	Successes      int       `json:"successes"`
	Failures       int       `json:"failures"`
	Drops          int       `json:"drops"`
	RuntimeSeconds int64     `json:"runtime_seconds,omitempty"`
	RuntimeSessions int      `json:"runtime_sessions,omitempty"`
	LastUpdated    time.Time `json:"last_updated,omitempty"`
}

type adaptiveQualityFile struct {
	Nodes   map[string]adaptiveNodeRecord   `json:"nodes"`
	Sources map[string]adaptiveSourceRecord `json:"sources"`
}

type adaptiveRuntimeSession struct {
	NodeKey     string
	CountryCode string
	Source      string
	StartedAt   time.Time
}

type adaptiveQualityStore struct {
	once        sync.Once
	mu          sync.RWMutex
	nodes       map[string]adaptiveNodeRecord
	sources     map[string]adaptiveSourceRecord
	runtimes    map[string]adaptiveRuntimeSession
	lastPersist time.Time
}

var adaptiveQuality adaptiveQualityStore

func adaptiveSourceKey(country, source string) string {
	return strings.ToUpper(strings.TrimSpace(country)) + "|" + normalizeSource(source)
}

func (s *adaptiveQualityStore) load() {
	s.once.Do(func() {
		s.nodes = map[string]adaptiveNodeRecord{}
		s.sources = map[string]adaptiveSourceRecord{}
		s.runtimes = map[string]adaptiveRuntimeSession{}
		b, err := os.ReadFile(adaptiveQualityPath)
		if err != nil {
			return
		}
		var f adaptiveQualityFile
		if json.Unmarshal(b, &f) != nil {
			return
		}
		if f.Nodes != nil {
			s.nodes = f.Nodes
		}
		if f.Sources != nil {
			s.sources = f.Sources
		}
	})
}

func (s *adaptiveQualityStore) rememberNode(n Node) {
	if n.Source == "" || n.CountryCode == "" {
		return
	}
	s.load()
	key := verificationKey(n)
	if key == "" {
		return
	}
	s.mu.Lock()
	rec := s.nodes[key]
	if rec.CountryCode == "" {
		rec.CountryCode = strings.ToUpper(n.CountryCode)
	}
	if rec.Source == "" {
		rec.Source = normalizeSource(n.Source)
	}
	s.nodes[key] = rec
	s.mu.Unlock()
}

func adaptiveRememberCandidates(cands []Node) {
	for _, n := range cands {
		adaptiveQuality.rememberNode(n)
	}
}

func adaptiveEWMA(old float64, samples int, value int) float64 {
	if value < 0 {
		return old
	}
	if samples <= 0 || old <= 0 {
		return float64(value)
	}
	// 最近真实表现权重更高，但不让单次抖动完全覆盖历史。
	return old*0.75 + float64(value)*0.25
}

func (s *adaptiveQualityStore) maybePersistLocked(now time.Time, force bool) adaptiveQualityFile {
	if !force && !s.lastPersist.IsZero() && now.Sub(s.lastPersist) < adaptivePersistGap {
		return adaptiveQualityFile{}
	}
	for key, rec := range s.nodes {
		latest := rec.LastAttempt
		if rec.LastSuccess.After(latest) {
			latest = rec.LastSuccess
		}
		if rec.LastFailure.After(latest) {
			latest = rec.LastFailure
		}
		if !latest.IsZero() && now.Sub(latest) > adaptiveKeepWindow {
			delete(s.nodes, key)
		}
	}
	for key, rec := range s.sources {
		if !rec.LastUpdated.IsZero() && now.Sub(rec.LastUpdated) > adaptiveKeepWindow {
			delete(s.sources, key)
		}
	}
	s.lastPersist = now
	out := adaptiveQualityFile{
		Nodes:   make(map[string]adaptiveNodeRecord, len(s.nodes)),
		Sources: make(map[string]adaptiveSourceRecord, len(s.sources)),
	}
	for k, v := range s.nodes {
		out.Nodes[k] = v
	}
	for k, v := range s.sources {
		out.Sources[k] = v
	}
	return out
}

func persistAdaptiveQuality(f adaptiveQualityFile) {
	if f.Nodes == nil && f.Sources == nil {
		return
	}
	if os.MkdirAll(filepath.Dir(adaptiveQualityPath), 0700) != nil {
		return
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return
	}
	tmp := adaptiveQualityPath + ".tmp"
	if os.WriteFile(tmp, b, 0600) != nil {
		return
	}
	_ = os.Chmod(tmp, 0600)
	_ = os.Rename(tmp, adaptiveQualityPath)
}

func (s *adaptiveQualityStore) recordAttempt(n Node, now time.Time) {
	s.load()
	key := verificationKey(n)
	if key == "" {
		return
	}
	s.mu.Lock()
	rec := s.nodes[key]
	rec.CountryCode = strings.ToUpper(n.CountryCode)
	rec.Source = normalizeSource(n.Source)
	rec.Attempts++
	rec.LastAttempt = now
	s.nodes[key] = rec
	sk := adaptiveSourceKey(rec.CountryCode, rec.Source)
	sr := s.sources[sk]
	sr.CountryCode = rec.CountryCode
	sr.Source = rec.Source
	sr.Attempts++
	sr.LastUpdated = now
	s.sources[sk] = sr
	snapshot := s.maybePersistLocked(now, false)
	s.mu.Unlock()
	persistAdaptiveQuality(snapshot)
}

func adaptiveRecordCandidateAttempt(n Node, now time.Time) {
	adaptiveQuality.recordAttempt(n, now)
}

func (s *adaptiveQualityStore) finishRuntimeLocked(poolID string, now time.Time, dropped bool) {
	session, ok := s.runtimes[poolID]
	if !ok {
		return
	}
	delete(s.runtimes, poolID)
	d := now.Sub(session.StartedAt)
	if d < 0 {
		d = 0
	}
	seconds := int64(d / time.Second)
	rec := s.nodes[session.NodeKey]
	if seconds > 0 {
		rec.RuntimeSeconds += seconds
		rec.RuntimeSessions++
		if seconds > rec.LongestRuntime {
			rec.LongestRuntime = seconds
		}
	}
	if dropped {
		rec.Drops++
	}
	s.nodes[session.NodeKey] = rec
	sk := adaptiveSourceKey(session.CountryCode, session.Source)
	sr := s.sources[sk]
	sr.CountryCode = session.CountryCode
	sr.Source = session.Source
	if seconds > 0 {
		sr.RuntimeSeconds += seconds
		sr.RuntimeSessions++
	}
	if dropped {
		sr.Drops++
	}
	sr.LastUpdated = now
	s.sources[sk] = sr
}

func adaptiveEndRuntime(poolID string, now time.Time, dropped bool) {
	adaptiveQuality.load()
	adaptiveQuality.mu.Lock()
	adaptiveQuality.finishRuntimeLocked(poolID, now, dropped)
	snapshot := adaptiveQuality.maybePersistLocked(now, dropped)
	adaptiveQuality.mu.Unlock()
	persistAdaptiveQuality(snapshot)
}

func (s *adaptiveQualityStore) recordSuccess(poolID string, n Node, latency int, now time.Time) {
	s.load()
	key := verificationKey(n)
	if key == "" {
		return
	}
	s.mu.Lock()
	s.finishRuntimeLocked(poolID, now, false)
	rec := s.nodes[key]
	rec.CountryCode = strings.ToUpper(n.CountryCode)
	rec.Source = normalizeSource(n.Source)
	rec.Successes++
	rec.ConsecutiveSuccess++
	rec.ConsecutiveFailures = 0
	rec.LastSuccess = now
	if latency >= 0 {
		rec.AvgLatencyMS = adaptiveEWMA(rec.AvgLatencyMS, rec.LatencySamples, latency)
		rec.LatencySamples++
	}
	s.nodes[key] = rec
	sk := adaptiveSourceKey(rec.CountryCode, rec.Source)
	sr := s.sources[sk]
	sr.CountryCode = rec.CountryCode
	sr.Source = rec.Source
	sr.Successes++
	sr.LastUpdated = now
	s.sources[sk] = sr
	s.runtimes[poolID] = adaptiveRuntimeSession{NodeKey: key, CountryCode: rec.CountryCode, Source: rec.Source, StartedAt: now}
	snapshot := s.maybePersistLocked(now, false)
	s.mu.Unlock()
	persistAdaptiveQuality(snapshot)
}

func adaptiveRecordCandidateSuccess(poolID string, n Node, latency int, now time.Time) {
	adaptiveQuality.recordSuccess(poolID, n, latency, now)
}

func classifyAdaptiveFailure(err error) string {
	if err == nil {
		return "generic"
	}
	text := strings.ToLower(err.Error())
	if isLocalResourceError(err) {
		return "local"
	}
	switch {
	case strings.Contains(text, "certificate"), strings.Contains(text, "tls"), strings.Contains(text, "x509"):
		return "hard"
	case strings.Contains(text, "handshake"), strings.Contains(text, "protocol"), strings.Contains(text, "认证失败"), strings.Contains(text, "connection refused"), strings.Contains(text, "连接被拒绝"), strings.Contains(text, "openvpn 提前退出"):
		return "hard"
	case strings.Contains(text, "timeout"), strings.Contains(text, "timed out"), strings.Contains(text, "超时"), strings.Contains(text, "deadline"):
		return "timeout"
	case strings.Contains(text, "no such host"), strings.Contains(text, "dns"):
		return "dns"
	default:
		return "generic"
	}
}

func adaptiveCooldown(kind string, consecutive int) time.Duration {
	if kind == "local" {
		return 0
	}
	if consecutive < 1 {
		consecutive = 1
	}
	base := 5 * time.Minute
	if kind == "hard" {
		base = 10 * time.Minute
	}
	switch {
	case consecutive >= 4:
		return 30 * time.Minute
	case consecutive == 3:
		if base < 20*time.Minute {
			return 20 * time.Minute
		}
		return 30 * time.Minute
	case consecutive == 2:
		if base < 10*time.Minute {
			return 10 * time.Minute
		}
		return 20 * time.Minute
	default:
		return base
	}
}

func (s *adaptiveQualityStore) recordFailure(n Node, err error, now time.Time) time.Duration {
	kind := classifyAdaptiveFailure(err)
	if kind == "local" {
		return 0
	}
	s.load()
	key := verificationKey(n)
	if key == "" {
		return failedCandidateCooldown
	}
	s.mu.Lock()
	rec := s.nodes[key]
	rec.CountryCode = strings.ToUpper(n.CountryCode)
	rec.Source = normalizeSource(n.Source)
	rec.Failures++
	rec.ConsecutiveFailures++
	rec.ConsecutiveSuccess = 0
	rec.LastFailure = now
	s.nodes[key] = rec
	sk := adaptiveSourceKey(rec.CountryCode, rec.Source)
	sr := s.sources[sk]
	sr.CountryCode = rec.CountryCode
	sr.Source = rec.Source
	sr.Failures++
	sr.LastUpdated = now
	s.sources[sk] = sr
	cooldown := adaptiveCooldown(kind, rec.ConsecutiveFailures)
	snapshot := s.maybePersistLocked(now, false)
	s.mu.Unlock()
	persistAdaptiveQuality(snapshot)
	return cooldown
}

func adaptiveRecordCandidateFailure(n Node, err error, now time.Time) time.Duration {
	return adaptiveQuality.recordFailure(n, err, now)
}

func (s *adaptiveQualityStore) recordRuntimeHealthy(poolID string, id runtimeIdentity, latency int, now time.Time) {
	s.load()
	s.mu.Lock()
	session, ok := s.runtimes[poolID]
	if !ok {
		// 服务重启后不猜测之前的在线时长；从第一次确认健康开始重新计时。
		for key, rec := range s.nodes {
			if rec.Source != normalizeSource(id.source) {
				continue
			}
			if strings.Contains(key, "|"+id.ip+"|") || strings.Contains(key, "|"+id.ip+":" ) {
				session = adaptiveRuntimeSession{NodeKey: key, CountryCode: rec.CountryCode, Source: rec.Source, StartedAt: now}
				s.runtimes[poolID] = session
				ok = true
				break
			}
		}
	}
	if ok {
		rec := s.nodes[session.NodeKey]
		if latency >= 0 {
			rec.AvgLatencyMS = adaptiveEWMA(rec.AvgLatencyMS, rec.LatencySamples, latency)
			rec.LatencySamples++
			s.nodes[session.NodeKey] = rec
		}
	}
	snapshot := s.maybePersistLocked(now, false)
	s.mu.Unlock()
	persistAdaptiveQuality(snapshot)
}

func adaptiveRecordRuntimeHealthy(poolID string, id runtimeIdentity, latency int, now time.Time) {
	adaptiveQuality.recordRuntimeHealthy(poolID, id, latency, now)
}

func adaptiveRecordRuntimeDrop(poolID string, id runtimeIdentity, err error, now time.Time) {
	_ = id
	_ = err
	adaptiveEndRuntime(poolID, now, true)
}

func adaptiveRecordExitProfile(poolID, ip, ipType, isp, asn, risk string, now time.Time) {
	adaptiveQuality.load()
	adaptiveQuality.mu.Lock()
	session, ok := adaptiveQuality.runtimes[poolID]
	if ok {
		rec := adaptiveQuality.nodes[session.NodeKey]
		rec.LastExitIP = strings.TrimSpace(ip)
		rec.LastIPType = strings.TrimSpace(ipType)
		rec.LastISP = strings.TrimSpace(isp)
		rec.LastASN = strings.TrimSpace(asn)
		rec.LastRisk = strings.TrimSpace(risk)
		adaptiveQuality.nodes[session.NodeKey] = rec
	}
	snapshot := adaptiveQuality.maybePersistLocked(now, false)
	adaptiveQuality.mu.Unlock()
	persistAdaptiveQuality(snapshot)
}

func adaptiveRecordFresh(rec adaptiveNodeRecord, now time.Time) bool {
	latest := rec.LastAttempt
	if rec.LastSuccess.After(latest) {
		latest = rec.LastSuccess
	}
	if rec.LastFailure.After(latest) {
		latest = rec.LastFailure
	}
	return !latest.IsZero() && !latest.After(now.Add(time.Minute)) && now.Sub(latest) <= adaptiveInfluenceWindow
}

// adaptiveCandidateScore 只在有足够真实样本时影响同一来源内部的候选排序。
// 未验证节点返回 0，避免少量样本把新节点永久压住。
func adaptiveCandidateScore(n Node, now time.Time) int {
	adaptiveQuality.load()
	key := verificationKey(n)
	adaptiveQuality.mu.RLock()
	rec, ok := adaptiveQuality.nodes[key]
	adaptiveQuality.mu.RUnlock()
	if !ok || !adaptiveRecordFresh(rec, now) || (rec.Attempts < 3 && rec.RuntimeSessions < 1) {
		return 0
	}
	// 使用带先验的成功率，避免 1/1 这种小样本被当成绝对 100%。
	rate := (float64(rec.Successes) + 2.6) / (float64(rec.Attempts) + 4.0)
	score := int((rate - 0.65) * 100)
	if rec.RuntimeSessions > 0 {
		avgSec := rec.RuntimeSeconds / int64(rec.RuntimeSessions)
		switch {
		case avgSec >= int64(4*time.Hour/time.Second):
			score += 12
		case avgSec >= int64(time.Hour/time.Second):
			score += 8
		case avgSec >= int64(30*time.Minute/time.Second):
			score += 4
		case avgSec > 0 && avgSec < int64(10*time.Minute/time.Second):
			score -= 5
		}
	}
	if !rec.LastSuccess.IsZero() {
		age := now.Sub(rec.LastSuccess)
		if age <= 30*time.Minute {
			score += 8
		} else if age <= 6*time.Hour {
			score += 4
		}
	}
	if rec.ConsecutiveFailures > 0 {
		penalty := rec.ConsecutiveFailures * 5
		if penalty > 20 {
			penalty = 20
		}
		score -= penalty
	}
	if rec.AvgLatencyMS > 0 {
		switch {
		case rec.AvgLatencyMS <= 500:
			score += 3
		case rec.AvgLatencyMS >= 2000:
			score -= 3
		}
	}
	typ := strings.ToLower(rec.LastIPType)
	if strings.Contains(typ, "住宅") || strings.Contains(typ, "residential") || strings.Contains(typ, "isp") {
		score += 2 // 出口属性只占很小权重，可用性和稳定性优先。
	}
	if strings.Contains(strings.ToLower(rec.LastRisk), "代理") || strings.Contains(strings.ToLower(rec.LastRisk), "proxy") {
		score -= 2
	}
	if score > 40 {
		score = 40
	}
	if score < -40 {
		score = -40
	}
	return score
}

func adaptiveSourceScore(country, source string, now time.Time) (int, int) {
	adaptiveQuality.load()
	key := adaptiveSourceKey(country, source)
	adaptiveQuality.mu.RLock()
	rec, ok := adaptiveQuality.sources[key]
	adaptiveQuality.mu.RUnlock()
	if !ok || rec.Attempts < adaptiveSourceMinAttempts || rec.LastUpdated.IsZero() || now.Sub(rec.LastUpdated) > adaptiveInfluenceWindow {
		return 0, rec.Attempts
	}
	rate := (float64(rec.Successes) + 5.2) / (float64(rec.Attempts) + 8.0)
	score := int(rate * 100)
	if rec.RuntimeSessions > 0 {
		avgSec := rec.RuntimeSeconds / int64(rec.RuntimeSessions)
		if avgSec >= int64(time.Hour/time.Second) {
			score += 5
		}
	}
	if rec.Drops > 0 && rec.RuntimeSessions > 0 {
		penalty := rec.Drops * 10 / rec.RuntimeSessions
		if penalty > 10 {
			penalty = 10
		}
		score -= penalty
	}
	return score, rec.Attempts
}

// adaptiveSourcePattern 保留 VPN Gate 第一优先。只有同一国家两个主源都累积足够样本，
// 且 Proxio 明显优于 VPN Gate 时，才把第二个 VPN Gate 槽位让给 Proxio。
func adaptiveSourcePattern(country string, now time.Time) []string {
	base := []string{sourceVPNGate, sourceProxio, sourceVPNGate, sourceProxyScrape}
	vpnScore, vpnN := adaptiveSourceScore(country, sourceVPNGate, now)
	proxioScore, proxioN := adaptiveSourceScore(country, sourceProxio, now)
	if vpnN < adaptiveSourceMinAttempts || proxioN < adaptiveSourceMinAttempts {
		return base
	}
	if proxioScore >= vpnScore+15 {
		return []string{sourceVPNGate, sourceProxio, sourceProxio, sourceVPNGate, sourceProxyScrape}
	}
	return base
}
