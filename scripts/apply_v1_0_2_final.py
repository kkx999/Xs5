from pathlib import Path
import re

main = Path('main.go')
s = main.read_text()

if 'appVersion       = "v1.0.1"' not in s:
    raise SystemExit('main.go is not at v1.0.1 baseline')
s = s.replace('appVersion       = "v1.0.1"', 'appVersion       = "v1.0.2"', 1)
s = s.replace('Xs5/v1.0.1', 'Xs5/v1.0.2')

health_pattern = re.compile(r'func \(a \*App\) healthLoop\(\) \{.*?\n\}\n\nconst switchAttemptWindow', re.S)
health_replacement = '''func (a *App) healthLoop() {
\tticker := time.NewTicker(healthCheckInterval)
\tdefer ticker.Stop()
\tfor range ticker.C {
\t\ta.mu.RLock()
\t\tps := make([]*Pool, 0, len(a.Pools))
\t\tfor _, p := range a.Pools {
\t\t\tps = append(ps, p)
\t\t}
\t\ta.mu.RUnlock()
\t\tfor _, p := range ps {
\t\t\tgo a.checkPoolHealth(p)
\t\t}
\t}
}

const switchAttemptWindow'''
s2, n = health_pattern.subn(health_replacement, s, count=1)
if n != 1:
    raise SystemExit(f'healthLoop replacement failed: {n}')
s = s2

old = '''func (a *App) switchNext(p *Pool, phase string) {
\tp.opMu.Lock()
\tdefer p.opMu.Unlock()
'''
new = '''func (a *App) switchNext(p *Pool, phase string) {
\tcancelAutoRetry(p.ID)
\tp.opMu.Lock()
\tdefer p.opMu.Unlock()
'''
if old not in s:
    raise SystemExit('switchNext header anchor missing')
s = s.replace(old, new, 1)

old = '''\tif len(cands) == 0 {
\t\tp.mu.Lock()
\t\tp.Status = "failed"
\t\tp.Error = "没有可用候选节点"
\t\tp.mu.Unlock()
\t\treturn
\t}
'''
new = '''\tif len(cands) == 0 {
\t\tp.mu.Lock()
\t\tp.Status = "failed"
\t\tp.Error = "没有可用候选节点"
\t\tp.mu.Unlock()
\t\ta.armAutoRecovery(p)
\t\treturn
\t}
'''
if old not in s:
    raise SystemExit('no-candidate anchor missing')
s = s.replace(old, new, 1)

old = '''\t\tp.Error = fmt.Sprintf("当前 %d/%d 个候选均处于 5 分钟冷却中，最早约 %s 后可重新尝试；不会重复检测刚失败的节点", cooling, len(cands), cooldownWait(earliest, time.Now()))
\t\tp.mu.Unlock()
\t\treturn
\t}
'''
new = '''\t\tp.Error = fmt.Sprintf("当前 %d/%d 个候选均处于 5 分钟冷却中，最早约 %s 后可重新尝试；不会重复检测刚失败的节点", cooling, len(cands), cooldownWait(earliest, time.Now()))
\t\tp.mu.Unlock()
\t\ta.armAutoRecovery(p)
\t\treturn
\t}
'''
if old not in s:
    raise SystemExit('all-cooling anchor missing')
s = s.replace(old, new, 1)

s = s.replace('；失败节点冷却 5 分钟；再次点击立即切换将从第 %d/%d 个候选继续', '；失败节点冷却 5 分钟；系统将从第 %d/%d 个候选自动继续', 1)

old = '''\tp.mu.Unlock()
}

func (a *App) activateNode'''
new = '''\tp.mu.Unlock()
\ta.armAutoRecovery(p)
}

func (a *App) activateNode'''
if old not in s:
    raise SystemExit('switchNext tail anchor missing')
s = s.replace(old, new, 1)

old = '''\tdropCandidateScanState(id)
\twriteJSON(w, 200, map[string]string{"ok": "deleted"})'''
new = '''\tdropCandidateScanState(id)
\tdropAutoRetry(id)
\twriteJSON(w, 200, map[string]string{"ok": "deleted"})'''
if old not in s:
    raise SystemExit('delete cleanup anchor missing')
s = s.replace(old, new, 1)

old = '''\t\tif len(p.Candidates) == 0 {
\t\t\tp.mu.Lock()
\t\t\tp.Status = "no-candidates"
\t\t\tp.Error = "没有可用候选节点"
\t\t\tp.mu.Unlock()
\t\t\tcontinue
\t\t}
'''
new = '''\t\tif len(p.Candidates) == 0 {
\t\t\tp.mu.Lock()
\t\t\tp.Status = "no-candidates"
\t\t\tp.Error = "没有可用候选节点"
\t\t\tp.mu.Unlock()
\t\t\ta.armAutoRecovery(p)
\t\t\tcontinue
\t\t}
'''
if old not in s:
    raise SystemExit('restore no-candidate anchor missing')
s = s.replace(old, new, 1)
main.write_text(s)

Path('health.go').write_text(r'''package main

import (
    "fmt"
    "sync"
    "time"
)

const (
    healthCheckInterval     = 30 * time.Second
    healthFailureRetryDelay = 10 * time.Second
)

var healthChecksInFlight sync.Map

func beginHealthCheck(poolID string) bool {
    _, loaded := healthChecksInFlight.LoadOrStore(poolID, struct{}{})
    return !loaded
}

func endHealthCheck(poolID string) {
    healthChecksInFlight.Delete(poolID)
}

func (a *App) checkPoolHealth(p *Pool) {
    if !beginHealthCheck(p.ID) {
        return
    }
    defer endHealthCheck(p.ID)

    p.mu.Lock()
    if p.Status != "up" {
        p.mu.Unlock()
        return
    }
    expected := p.ExitIP
    p.mu.Unlock()

    ip, latency, err := p.probeCurrent()
    if err == nil && ip == expected {
        p.mu.Lock()
        if p.Status == "up" && p.ExitIP == expected {
            p.FailCount = 0
            p.LatencyMS = latency
        }
        p.mu.Unlock()
        return
    }

    p.mu.Lock()
    if p.Status != "up" || p.ExitIP != expected {
        p.mu.Unlock()
        return
    }
    p.FailCount = 1
    p.mu.Unlock()

    timer := time.NewTimer(healthFailureRetryDelay)
    <-timer.C

    p.mu.Lock()
    if p.Status != "up" || p.ExitIP != expected {
        p.mu.Unlock()
        return
    }
    p.mu.Unlock()

    ip, latency, err = p.probeCurrent()
    if err == nil && ip == expected {
        p.mu.Lock()
        if p.Status == "up" && p.ExitIP == expected {
            p.FailCount = 0
            p.LatencyMS = latency
        }
        p.mu.Unlock()
        return
    }

    p.mu.Lock()
    if p.Status != "up" || p.ExitIP != expected {
        p.mu.Unlock()
        return
    }
    p.FailCount = 2
    p.Status = "failed"
    if err != nil {
        p.Error = fmt.Sprintf("健康检查连续两次失败，正在自动切换：%v", err)
    } else {
        p.Error = fmt.Sprintf("健康检查发现出口 IP 从 %s 变为 %s，正在自动切换", expected, ip)
    }
    p.mu.Unlock()

    // 当前出口已确认不可用，先停止旧运行时，避免继续对外提供失效的 S5。
    p.stopRuntime()
    a.switchNext(p, "switching")
}
''')

Path('health_test.go').write_text(r'''package main

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
''')

install = Path('install.sh')
x = install.read_text()
if 'VERSION=1.0.1' not in x:
    raise SystemExit('install.sh version anchor missing')
install.write_text(x.replace('VERSION=1.0.1', 'VERSION=1.0.2', 1))

Path('VERSION').write_text('1.0.2\n')
Path('RELEASE.md').write_text('''# Xs5 v1.0.2

本版本补全无人值守出口恢复，并优化健康检查频率。

- 正常出口每 30 秒进行一次真实出网健康检查，降低不必要的探测请求。
- 第一次健康检查失败后，不等待下一个 30 秒周期，10 秒后快速复检。
- 连续 2 次失败，或出口 IP 异常变化后，自动停止失效出口并切换候选。
- 单轮 90 秒未找到可用节点时，不再停在“故障”等待人工点击。
- 仍有未冷却候选时，约 2 秒后从上次扫描位置自动继续。
- 所有候选都在 5 分钟冷却时，等待最早冷却到期后自动继续。
- 启动时暂时没有候选节点，也会自动等待并继续恢复。
- 删除出口会同步清理自动恢复定时器。
- 保留 v1.0.1 的扫描游标与失败候选 5 分钟冷却机制。
- 更新不会改变现有 S5 端口、用户名、密码和国家出口配置。

> Xs5 使用第三方公开 VPN / SOCKS5 节点。公开节点可能不稳定、不可信或被目标站封禁，请勿通过不受信任的公共出口传输敏感明文数据。
''')
