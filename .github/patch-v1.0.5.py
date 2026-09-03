from pathlib import Path


def replace_once(text, old, new, label):
    if old not in text:
        raise SystemExit(f"anchor missing: {label}")
    return text.replace(old, new, 1)

main = Path('main.go')
s = main.read_text()
s = replace_once(s, 'appVersion       = "v1.0.4"', 'appVersion       = "v1.0.5"', 'app version')
s = s.replace('Xs5/v1.0.4', 'Xs5/v1.0.5')

old_pool = '''\tovpn          *exec.Cmd
\tmu            sync.Mutex
\topMu          sync.Mutex
'''
new_pool = '''\tovpn          *exec.Cmd
\tovpnDone      chan error
\tnsActive      bool
\tmu            sync.Mutex
\topMu          sync.Mutex
'''
s = replace_once(s, old_pool, new_pool, 'Pool runtime fields')

old_switch_vars = '''\tvar lastErr error
\tattempted := 0
\ttimedOut := false
'''
new_switch_vars = '''\tvar lastErr error
\tvar resourceErr error
\tattempted := 0
\ttimedOut := false
'''
s = replace_once(s, old_switch_vars, new_switch_vars, 'switch variables')

old_attempt = '''\t\tnode := cands[i]
\t\tattempted++
\t\tif err := a.activateNode(p, node, phase, deadline); err == nil {
\t\t\tstate.recordSuccess(i, len(cands), nodeKey(node))
\t\t\treturn
\t\t} else {
\t\t\tlastErr = err
\t\t\tstate.recordFailure(i, len(cands), nodeKey(node), time.Now())
\t\t\tlog.Printf("%s/%s candidate %s %s failed (本轮 %d, 候选位置 %d/%d): %v", p.CountryCode, p.ID, sourceLabel(node.Source), node.Host, attempted, i+1, len(cands), err)
\t\t}
\t}

\tp.stopRuntime()
'''
new_attempt = '''\t\tnode := cands[i]
\t\tattempted++
\t\terr := a.activateNode(p, node, phase, deadline)
\t\tif err == nil {
\t\t\tstate.recordSuccess(i, len(cands), nodeKey(node))
\t\t\treturn
\t\t}
\t\tlastErr = err
\t\tif isLocalResourceError(err) {
\t\t\tnoteLocalResourcePressure()
\t\t\tresourceErr = err
\t\t\tlog.Printf("%s/%s candidate %s %s paused by local resource pressure (候选不进入冷却): %v", p.CountryCode, p.ID, sourceLabel(node.Source), node.Host, err)
\t\t\tbreak
\t\t}
\t\tstate.recordFailure(i, len(cands), nodeKey(node), time.Now())
\t\tlog.Printf("%s/%s candidate %s %s failed (本轮 %d, 候选位置 %d/%d): %v", p.CountryCode, p.ID, sourceLabel(node.Source), node.Host, attempted, i+1, len(cands), err)
\t\tif !waitCandidateGap(node, deadline) {
\t\t\ttimedOut = true
\t\t\tbreak
\t\t}
\t}

\tp.stopRuntime()
\tif resourceErr != nil {
\t\tp.mu.Lock()
\t\tp.Status = "failed"
\t\tp.FailCount = 0
\t\tp.mu.Unlock()
\t\ta.armResourceRecovery(p, resourceErr)
\t\treturn
\t}
'''
s = replace_once(s, old_attempt, new_attempt, 'candidate attempt loop')

old_activate_node = '''func (a *App) activateNode(p *Pool, node Node, phase string, operationDeadline time.Time) error {
\tif phase == "" {
\t\tphase = "starting"
\t}
\tp.mu.Lock()
\tp.Status = phase
\tp.Error = ""
\tp.mu.Unlock()

\tswitch node.Source {
\tcase sourceProxio:
\t\t// Proxio 可以先独立验证新上游，验证通过后才接管固定 S5 端口。
\t\treturn a.activateProxio(p, node)
\tdefault:
\t\t// VPN Gate 需要复用该池固定的 netns/网段；自动切换前已经经过三次完整链路失败确认。
\t\tp.stopRuntime()
\t\treturn a.activateVPNGate(p, node, operationDeadline)
\t}
}
'''
new_activate_node = '''func (a *App) activateNode(p *Pool, node Node, phase string, operationDeadline time.Time) error {
\tif phase == "" {
\t\tphase = "starting"
\t}
\tp.mu.Lock()
\tp.Status = phase
\tp.Error = ""
\tp.mu.Unlock()

\tswitch node.Source {
\tcase sourceProxio:
\t\t// Proxio 仍先独立验证新上游；接管旧 VPN Gate runtime 时也与 VPN Gate 重操作串行。
\t\treturn a.activateProxio(p, node)
\tdefault:
\t\t// 所有 VPN Gate 建网、OpenVPN 启动和候选检测全局串行，避免多个池同时 fork 大量系统进程。
\t\tvpnGateActivationMu.Lock()
\t\tdefer vpnGateActivationMu.Unlock()
\t\tp.stopRuntime()
\t\treturn a.activateVPNGate(p, node, operationDeadline)
\t}
}
'''
s = replace_once(s, old_activate_node, new_activate_node, 'activateNode')

old_vpn_start = '''func (a *App) activateVPNGate(p *Pool, node Node, operationDeadline time.Time) error {
\tif err := setupNS(p.ns, p.Port); err != nil {
\t\treturn fmt.Errorf("创建网络隔离失败: %w", err)
\t}
\tcfg := filepath.Join(workDir, p.ns+".ovpn")
'''
new_vpn_start = '''func (a *App) activateVPNGate(p *Pool, node Node, operationDeadline time.Time) error {
\tif err := setupNS(p.ns, p.Port); err != nil {
\t\treturn fmt.Errorf("创建网络隔离失败: %w", err)
\t}
\tp.mu.Lock()
\tp.nsActive = true
\tp.mu.Unlock()
\tactivated := false
\tdefer func() {
\t\tif !activated {
\t\t\tp.stopRuntime()
\t\t}
\t}()
\tcfg := filepath.Join(workDir, p.ns+".ovpn")
'''
s = replace_once(s, old_vpn_start, new_vpn_start, 'activateVPNGate start')

old_openvpn_wait = '''\tp.mu.Lock()
\tp.ovpn = cmd
\tp.mu.Unlock()
\tdone := make(chan error, 1)
\tgo func() { done <- cmd.Wait() }()
'''
new_openvpn_wait = '''\tdone := make(chan error, 1)
\tp.mu.Lock()
\tp.ovpn = cmd
\tp.ovpnDone = done
\tp.mu.Unlock()
\tgo func() {
\t\terr := cmd.Wait()
\t\tdone <- err
\t\tclose(done)
\t}()
'''
s = replace_once(s, old_openvpn_wait, new_openvpn_wait, 'OpenVPN wait channel')

old_vpn_success = '''\tgo serveSOCKS(ln, p)
\tmaybeRefreshPoolExitIP(p, true)
\tlog.Printf("%s/%s up: SOCKS5 :%d -> VPN Gate %s (%s), ordinary HTTPS ok (%dms)", p.CountryCode, p.ID, p.Port, node.Host, node.IP, latency)
\treturn nil
}
'''
new_vpn_success = '''\tgo serveSOCKS(ln, p)
\tmaybeRefreshPoolExitIP(p, true)
\tactivated = true
\tlog.Printf("%s/%s up: SOCKS5 :%d -> VPN Gate %s (%s), ordinary HTTPS ok (%dms)", p.CountryCode, p.ID, p.Port, node.Host, node.IP, latency)
\treturn nil
}
'''
s = replace_once(s, old_vpn_success, new_vpn_success, 'VPN Gate success')

old_proxio_stop = '''\t// 新上游已经验证可用后再停止旧 runtime，尽量缩短切换断流窗口。
\tp.stopRuntime()
\tln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", p.Port))
'''
new_proxio_stop = '''\t// 新上游已经验证可用后再停止旧 runtime。若旧 runtime 是 VPN Gate，
\t// teardown 也进入同一全局串行区，避免与其他池正在创建 netns/OpenVPN 时互相抢系统资源。
\tp.mu.Lock()
\thadVPNRuntime := p.nsActive || p.ovpn != nil || p.ActiveSource == sourceVPNGate
\tp.mu.Unlock()
\tif hadVPNRuntime {
\t\tvpnGateActivationMu.Lock()
\t\tp.stopRuntime()
\t\tvpnGateActivationMu.Unlock()
\t} else {
\t\tp.stopRuntime()
\t}
\tln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", p.Port))
'''
s = replace_once(s, old_proxio_stop, new_proxio_stop, 'Proxio takeover cleanup')

old_stop = '''func (p *Pool) stopRuntime() {
\tp.mu.Lock()
\tif p.ln != nil {
\t\t_ = p.ln.Close()
\t\tp.ln = nil
\t}
\tif p.ovpn != nil && p.ovpn.Process != nil {
\t\t_ = p.ovpn.Process.Kill()
\t\tp.ovpn = nil
\t}
\tp.mu.Unlock()
\tteardownNS(p.ns, p.Port)
}
'''
new_stop = '''func (p *Pool) stopRuntime() {
\tp.mu.Lock()
\tln := p.ln
\tp.ln = nil
\tcmd := p.ovpn
\tdone := p.ovpnDone
\tp.ovpn = nil
\tp.ovpnDone = nil
\thadNS := p.nsActive
\tp.nsActive = false
\tp.mu.Unlock()

\tif ln != nil {
\t\t_ = ln.Close()
\t}
\tif cmd != nil && cmd.Process != nil {
\t\t_ = cmd.Process.Kill()
\t}
\tif done != nil {
\t\tt := time.NewTimer(openVPNReapWait)
\t\tselect {
\t\tcase <-done:
\t\t\tif !t.Stop() {
\t\t\t\t<-t.C
\t\t\t}
\t\tcase <-t.C:
\t\t\tlog.Printf("%s/%s OpenVPN process did not reap within %s", p.CountryCode, p.ID, openVPNReapWait)
\t\t}
\t}
\t// Proxio 没有 network namespace，不再每次切换都白白 fork ip/iptables 做无效清理。
\tif hadNS {
\t\tteardownNS(p.ns, p.Port)
\t}
}
'''
s = replace_once(s, old_stop, new_stop, 'stopRuntime')

old_relay = '''\tcmd := exec.Command("ip", "netns", "exec", ns, "socat", "-", "TCP:"+target)
\tstdIn, _ := cmd.StdinPipe()
\tstdOut, _ := cmd.StdoutPipe()
\tif err := cmd.Start(); err != nil {
\t\t_, _ = c.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
\t\treturn
\t}
\t_, _ = c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
\t_ = c.SetDeadline(time.Time{})
\tgo io.Copy(stdIn, c)
\t_, _ = io.Copy(c, stdOut)
\t_ = cmd.Process.Kill()
}
'''
new_relay = '''\tif err := acquireVPNGateRelaySlot(); err != nil {
\t\t_, _ = c.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
\t\treturn
\t}
\tdefer releaseVPNGateRelaySlot()

\tcmd := exec.Command("ip", "netns", "exec", ns, "socat", "-", "TCP:"+target)
\tstdIn, inErr := cmd.StdinPipe()
\tstdOut, outErr := cmd.StdoutPipe()
\tif inErr != nil || outErr != nil {
\t\t_, _ = c.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
\t\treturn
\t}
\tif err := cmd.Start(); err != nil {
\t\tif isLocalResourceError(err) {
\t\t\tnoteLocalResourcePressure()
\t\t}
\t\t_, _ = c.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
\t\treturn
\t}
\t_, _ = c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
\t_ = c.SetDeadline(time.Time{})
\tgo func() {
\t\t_, _ = io.Copy(stdIn, c)
\t\t_ = stdIn.Close()
\t}()
\t_, _ = io.Copy(c, stdOut)
\t_ = stdIn.Close()
\tif cmd.Process != nil {
\t\t_ = cmd.Process.Kill()
\t}
\t// 每一个 VPN Gate SOCKS 连接都必须 Wait，避免 socat 退出后留下僵尸子进程并最终耗尽 PID。
\t_ = cmd.Wait()
}
'''
s = replace_once(s, old_relay, new_relay, 'VPN Gate relay process cleanup')

main.write_text(s)

# Version-bearing shell files.
install = Path('install.sh')
text = install.read_text()
text = replace_once(text, 'VERSION=1.0.4', 'VERSION=1.0.5', 'install version')
install.write_text(text)

menu = Path('xs5.sh')
text = menu.read_text()
text = replace_once(text, 'echo "1.0.4"', 'echo "1.0.5"', 'menu fallback version')
menu.write_text(text)

Path('VERSION').write_text('1.0.5\n')
Path('RELEASE.md').write_text('''# Xs5 v1.0.5\n\n本版本重点修复长时间运行和多出口切换时可能出现的本机资源耗尽问题。VPN Gate 与 Proxio 继续保持统一的健康判定和候选失败策略，同时对 VPN Gate 的重进程链路增加专门的资源保护。\n\n- 修复 VPN Gate 每个 SOCKS5 连接启动 socat 后未执行 Wait 的问题，避免僵尸子进程持续累积最终触发 `resource temporarily unavailable`。\n- VPN Gate 转发子进程增加全局安全并发上限，防止突发连接同时 fork 过多进程拖垮低配置 VPS。\n- VPN Gate 的 netns/OpenVPN 候选激活全局串行，同一时间只允许一个池执行重型切换流程。\n- OpenVPN 停止后等待子进程真正被回收，再继续后续网络资源清理。\n- Proxio 不再在每次切换时无条件执行 VPN Gate 专用的 netns/iptables 清理，减少大量无意义的系统子进程。\n- VPN Gate 和 Proxio 统一识别本机资源错误，例如 fork 失败、内存不足、文件描述符/PID 耗尽、socket buffer 不足。\n- 本机资源错误不再记为候选节点失败，不进入 5 分钟冷却，也不会错误推进扫描游标；30 秒后从原位置自动恢复。\n- 健康检查遇到本机资源压力时，两个源都不会累计故障次数或触发自动切换。\n- 两个源的候选失败后都加入短暂节流；VPN Gate 留出更长的 2 秒资源回收间隔，Proxio 为 300ms，降低连续扫描峰值。\n- 保留 v1.0.4 的普通 HTTPS 三检测点、固定 S5 完整链路检测、30 秒健康检查与三次失败确认。\n- README 继续只描述项目用途与使用方式，版本更新内容仅放在 GitHub Release Notes。\n- 从旧版本更新不会改变已有 S5 端口、用户名、密码和国家出口配置。\n\n> Xs5 使用第三方公开 VPN / SOCKS5 节点。公开节点可能不稳定、不可信或被目标站封禁，请勿通过不受信任的公共出口传输敏感明文数据。\n''')
