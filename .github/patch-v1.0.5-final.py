from pathlib import Path


def replace_once(text, old, new, label):
    if old not in text:
        raise SystemExit(f"anchor missing: {label}")
    return text.replace(old, new, 1)

p = Path('main.go')
s = p.read_text()

old = '''\tp.stopRuntime()
\tif resourceErr != nil {
\t\tp.mu.Lock()
\t\tp.Status = "failed"
\t\tp.FailCount = 0
\t\tp.mu.Unlock()
\t\ta.armResourceRecovery(p, resourceErr)
\t\treturn
\t}
\tnextPos := state.cursorPosition(len(cands))
'''
new = '''\tif resourceErr != nil {
\t\t// 本机资源错误时不要再主动杀掉可能仍能工作的旧 runtime。
\t\t// VPN Gate 失败候选在 activateVPNGate 内部已经自行清理；Proxio 预检失败则保留旧线路继续承载流量。
\t\tp.mu.Lock()
\t\tp.Status = "failed"
\t\tp.FailCount = 0
\t\tp.mu.Unlock()
\t\ta.armResourceRecovery(p, resourceErr)
\t\treturn
\t}
\tstopRuntimeSerialized(p)
\tnextPos := state.cursorPosition(len(cands))
'''
s = replace_once(s, old, new, 'resource error cleanup order')

old = '''\tp.mu.Lock()
\thadVPNRuntime := p.nsActive || p.ovpn != nil || p.ActiveSource == sourceVPNGate
\tp.mu.Unlock()
\tif hadVPNRuntime {
\t\tvpnGateActivationMu.Lock()
\t\tp.stopRuntime()
\t\tvpnGateActivationMu.Unlock()
\t} else {
\t\tp.stopRuntime()
\t}
'''
new = '''\tstopRuntimeSerialized(p)
'''
s = replace_once(s, old, new, 'Proxio serialized takeover')

old = '''\tif done != nil {
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
'''
new = '''\tif done != nil {
\t\tt := time.NewTimer(openVPNReapWait)
\t\tselect {
\t\tcase <-done:
\t\t\tt.Stop()
\t\tcase <-t.C:
\t\t\tlog.Printf("%s/%s OpenVPN process did not reap within %s", p.CountryCode, p.ID, openVPNReapWait)
\t\t}
\t}
'''
s = replace_once(s, old, new, 'OpenVPN timer cleanup')

p.write_text(s)
