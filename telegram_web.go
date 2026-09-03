package main

const telegramHTML = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Telegram · X S5 池</title>
<style>
:root{color-scheme:dark;--bg:#0b0f14;--panel:#111720;--panel2:#151c26;--line:#283547;--line2:#334258;--text:#edf3fb;--muted:#8b98aa;--accent:#69a7ff;--green:#69d990;--red:#ff7b82;--yellow:#e7c56d}*{box-sizing:border-box}body{margin:0;background:radial-gradient(circle at 10% -10%,rgba(48,109,194,.13),transparent 32%),var(--bg);color:var(--text);font-family:Inter,ui-sans-serif,-apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif}.app{max-width:920px;margin:0 auto;padding:28px 22px 60px}.head{display:flex;align-items:center;justify-content:space-between;gap:15px;margin-bottom:22px}.brand{display:flex;align-items:center;gap:12px}.logo{width:40px;height:40px;border-radius:12px;display:grid;place-items:center;background:linear-gradient(145deg,#2a6fe9,#79b9ff);font-weight:850;color:#06101e}.title h1{margin:0;font-size:22px}.title p{margin:5px 0 0;color:var(--muted);font-size:12px}.back{height:38px;padding:0 13px;border:1px solid var(--line);border-radius:10px;color:#c9d7e8;background:#111923;text-decoration:none;display:flex;align-items:center;font-size:12px}.back:hover{border-color:#4a627e}.grid{display:grid;grid-template-columns:1.2fr .8fr;gap:14px}.card{background:linear-gradient(180deg,#151c26,#111821);border:1px solid rgba(130,154,187,.18);border-radius:18px;padding:20px;box-shadow:0 15px 50px rgba(0,0,0,.13)}.card h2{font-size:15px;margin:0 0 5px}.sub{font-size:12px;color:var(--muted);line-height:1.65;margin-bottom:18px}.label{display:block;font-size:11px;color:#9cabbf;margin:13px 0 7px}.input{width:100%;height:44px;border:1px solid var(--line);border-radius:11px;background:#0d131b;color:var(--text);padding:0 12px;outline:0;font:inherit;font-size:13px}.input:focus{border-color:#4e8bdd;box-shadow:0 0 0 3px rgba(78,139,221,.1)}.mask{margin-top:6px;color:#647387;font-size:10px}.switchrow{display:flex;align-items:center;justify-content:space-between;gap:18px;padding:12px 0;border-bottom:1px solid rgba(46,60,79,.55)}.switchrow:last-child{border-bottom:0}.switchrow b{font-size:12px}.switchrow small{display:block;color:#728197;font-size:10px;margin-top:3px;line-height:1.4}.toggle{position:relative;width:42px;height:24px;flex:0 0 42px}.toggle input{opacity:0;width:0;height:0}.slider{position:absolute;inset:0;background:#263243;border-radius:999px;cursor:pointer;transition:.2s}.slider:before{content:"";position:absolute;width:18px;height:18px;left:3px;top:3px;border-radius:50%;background:#8796aa;transition:.2s}.toggle input:checked+.slider{background:#326ec5}.toggle input:checked+.slider:before{transform:translateX(18px);background:#dcecff}.actions{display:flex;gap:8px;flex-wrap:wrap;margin-top:18px}.btn{height:40px;padding:0 13px;border:1px solid var(--line2);border-radius:10px;background:#131c27;color:#e4edf8;cursor:pointer;font:inherit;font-size:12px;font-weight:650}.btn:hover{border-color:#4d6887;background:#182431}.btn.primary{border-color:transparent;background:linear-gradient(135deg,#4c8ef2,#70b4ff);color:#06101e}.btn.danger{color:#ff969c}.btn[disabled]{opacity:.55;cursor:wait}.status{border:1px solid #283649;border-radius:13px;background:#0d141d;padding:14px;margin-bottom:14px}.statusline{display:flex;align-items:center;gap:8px;font-size:12px}.dot{width:8px;height:8px;border-radius:50%;background:#647387}.dot.ok{background:var(--green);box-shadow:0 0 14px rgba(105,217,144,.3)}.dot.wait{background:var(--yellow)}.statusmeta{color:#7b899d;font-size:11px;line-height:1.7;margin-top:8px}.steps{counter-reset:s}.step{position:relative;padding:0 0 17px 28px;color:#9ba9bb;font-size:12px;line-height:1.6}.step:before{counter-increment:s;content:counter(s);position:absolute;left:0;top:1px;width:19px;height:19px;border-radius:6px;display:grid;place-items:center;background:#1d2a3b;color:#a9cfff;font-size:10px;font-weight:800}.step code{color:#dce8f8}.note{padding:11px 12px;border-radius:11px;border:1px solid rgba(105,167,255,.18);background:rgba(105,167,255,.06);color:#8fa8c9;font-size:11px;line-height:1.6}.toast{position:fixed;right:18px;top:18px;max-width:360px;padding:11px 13px;border:1px solid #35475e;border-radius:11px;background:#17212d;color:#dfeaf8;font-size:12px;box-shadow:0 18px 55px rgba(0,0,0,.4);opacity:0;transform:translateY(-7px);pointer-events:none;transition:.18s}.toast.show{opacity:1;transform:none}.toast.err{color:#ff9da2;border-color:rgba(255,123,130,.36)}@media(max-width:760px){.grid{grid-template-columns:1fr}.app{padding:20px 14px 45px}.head{align-items:flex-start}.back{white-space:nowrap}.actions .btn{flex:1}}
</style>
</head>
<body>
<div class="app">
  <header class="head"><div class="brand"><div class="logo">TG</div><div class="title"><h1>Telegram</h1><p>通知 · 状态查看 · 远程切换 · 健康检测</p></div></div><a class="back" href="/">← 返回面板</a></header>
  <div class="grid">
    <section class="card">
      <h2>机器人配置</h2><div class="sub">只需要在 @BotFather 创建机器人并填入 Bot Token。Chat ID 和管理员 User ID 由 Xs5 在绑定时自动获取。</div>
      <label class="label">Bot Token</label>
      <input class="input" id="token" type="password" autocomplete="off" placeholder="1234567890:AAxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx">
      <div class="mask" id="tokenMask">尚未配置</div>
      <div class="switchrow"><div><b>Telegram 通知</b><small>关闭后机器人仍可绑定，但不会主动发送状态通知</small></div><label class="toggle"><input id="enabled" type="checkbox"><span class="slider"></span></label></div>
      <div class="switchrow"><div><b>远程控制</b><small>允许管理员通过按钮切换、刷新、暂停和恢复出口</small></div><label class="toggle"><input id="remote" type="checkbox"><span class="slider"></span></label></div>
      <div class="actions"><button class="btn primary" id="saveBtn" onclick="saveConfig()">保存并验证 Token</button><button class="btn" onclick="startBind(false)">开始绑定</button><button class="btn" onclick="testNotify()">测试通知</button></div>
    </section>
    <aside class="card">
      <h2>绑定状态</h2><div class="sub">只接受面板绑定的唯一 Telegram 管理员操作。</div>
      <div class="status"><div class="statusline"><span class="dot" id="dot"></span><b id="state">读取中…</b></div><div class="statusmeta" id="meta">-</div></div>
      <div class="steps"><div class="step">打开 Telegram 的 <code>@BotFather</code>，发送 <code>/newbot</code> 创建机器人。</div><div class="step">把 Bot Token 填到左侧并点击“保存并验证 Token”。</div><div class="step">点击“开始绑定”，然后打开你的机器人发送 <code>/start</code>。</div><div class="step">绑定成功后，<code>/status</code>、<code>/switch</code> 等命令菜单会自动出现。</div></div>
      <div class="actions"><button class="btn" id="openBot" onclick="openBotNow()" disabled>打开机器人</button><button class="btn" onclick="startBind(true)">重新绑定</button><button class="btn danger" onclick="unbind()">解除绑定</button></div>
    </aside>
    <section class="card">
      <h2>通知项目</h2><div class="sub">健康检查本身不会每 30 秒刷屏，只在真正有状态变化时通知。</div>
      <div class="switchrow"><div><b>服务启动</b><small>启动约 15 秒后发送当前出口摘要</small></div><label class="toggle"><input id="nStart" type="checkbox"><span class="slider"></span></label></div>
      <div class="switchrow"><div><b>正在切换 / 切换成功</b><small>连续健康检测失败或手动切换完成时通知</small></div><label class="toggle"><input id="nSwitch" type="checkbox"><span class="slider"></span></label></div>
      <div class="switchrow"><div><b>切换失败 / 候选耗尽</b><small>带上恢复与冷却状态，自动做防刷屏</small></div><label class="toggle"><input id="nFailure" type="checkbox"><span class="slider"></span></label></div>
      <div class="switchrow"><div><b>自动恢复成功</b><small>故障出口重新找到可用线路时通知</small></div><label class="toggle"><input id="nRecovery" type="checkbox"><span class="slider"></span></label></div>
      <div class="switchrow"><div><b>服务器资源异常</b><small>PID、内存、fork 等本机资源压力单独告警</small></div><label class="toggle"><input id="nResource" type="checkbox"><span class="slider"></span></label></div>
      <div class="switchrow"><div><b>节点池刷新失败</b><small>VPN Gate / Proxio 自动刷新异常时通知</small></div><label class="toggle"><input id="nRefresh" type="checkbox"><span class="slider"></span></label></div>
      <div class="switchrow"><div><b>每日运行摘要</b><small>每天约 09:00（服务器本地时间）发送一次，默认关闭</small></div><label class="toggle"><input id="daily" type="checkbox"><span class="slider"></span></label></div>
    </section>
    <aside class="card">
      <h2>机器人已内置命令</h2><div class="sub">无需去 BotFather 手工添加命令，Xs5 会通过 Telegram API 自动注册。</div>
      <div class="note"><code>/status</code> 查看全部出口<br><code>/switch</code> 立即切换<br><code>/check</code> 只检测不切换<br><code>/refresh</code> 刷新节点池<br><code>/recovery</code> 查看恢复与冷却<br><code>/pause</code> 暂停自动切换<br><code>/resume</code> 恢复自动切换<br><code>/logs</code> 最近 20 条日志<br><code>/help</code> 帮助</div>
    </aside>
  </div>
</div>
<div class="toast" id="toast"></div>
<script>
var botUsername='';
function el(id){return document.getElementById(id)}
function toast(s,err){var x=el('toast');x.textContent=s;x.className='toast show'+(err?' err':'');clearTimeout(window._tt);window._tt=setTimeout(function(){x.className='toast'},2800)}
async function api(u,o){var r=await fetch(u,o);if(r.status===401){location='/login';throw new Error('登录已过期')}var d=await r.json().catch(function(){return {}});if(!r.ok)throw new Error(d.error||('HTTP '+r.status));return d}
function bool(v){return v?'1':'0'}
function configBody(){return new URLSearchParams({token:el('token').value.trim(),enabled:bool(el('enabled').checked),remote_control:bool(el('remote').checked),notify_start:bool(el('nStart').checked),notify_switch:bool(el('nSwitch').checked),notify_failure:bool(el('nFailure').checked),notify_recovery:bool(el('nRecovery').checked),notify_resource:bool(el('nResource').checked),notify_refresh:bool(el('nRefresh').checked),daily_summary:bool(el('daily').checked)})}
async function load(){try{var s=await api('/api/telegram/status');el('tokenMask').textContent=s.configured?'已保存：'+s.token_masked:'尚未配置';el('enabled').checked=!!s.enabled;el('remote').checked=!!s.remote_control;el('nStart').checked=!!s.notify_start;el('nSwitch').checked=!!s.notify_switch;el('nFailure').checked=!!s.notify_failure;el('nRecovery').checked=!!s.notify_recovery;el('nResource').checked=!!s.notify_resource;el('nRefresh').checked=!!s.notify_refresh;el('daily').checked=!!s.daily_summary;botUsername=s.bot_username||'';el('openBot').disabled=!botUsername;if(s.bound){el('dot').className='dot ok';el('state').textContent='已绑定';el('meta').textContent=(botUsername?'@'+botUsername+' · ':'')+'管理员已锁定，远程控制受 User ID + Chat ID 双重校验'}else if(s.binding){el('dot').className='dot wait';el('state').textContent='等待 /start';el('meta').textContent='绑定窗口剩余约 '+Math.max(1,s.binding_seconds||0)+' 秒'+(botUsername?' · @'+botUsername:'')}else{el('dot').className='dot';el('state').textContent=s.configured?'未绑定':'未配置';el('meta').textContent=s.configured?'点击“开始绑定”后去机器人发送 /start':'先保存 Bot Token'}}catch(e){toast(e.message,true)}}
async function saveConfig(){var b=el('saveBtn');b.disabled=true;b.textContent='验证中…';try{var d=await api('/api/telegram/save',{method:'POST',body:configBody()});el('token').value='';botUsername=d.bot_username||botUsername;toast('Telegram 配置已保存');await load()}catch(e){toast(e.message,true)}finally{b.disabled=false;b.textContent='保存并验证 Token'}}
async function startBind(reset){try{var d=await api('/api/telegram/bind',{method:'POST',body:new URLSearchParams({reset:reset?'1':'0'})});botUsername=d.bot_username||botUsername;toast(reset?'已开启重新绑定窗口，请发送 /start':'绑定窗口已开启，请发送 /start');await load();if(botUsername)setTimeout(openBotNow,300)}catch(e){toast(e.message,true)}}
async function testNotify(){try{await api('/api/telegram/test',{method:'POST'});toast('测试通知已发送')}catch(e){toast(e.message,true)}}
async function unbind(){if(!confirm('解除 Telegram 管理员绑定？Bot Token 和通知设置会保留。'))return;try{await api('/api/telegram/unbind',{method:'POST'});toast('已解除绑定');await load()}catch(e){toast(e.message,true)}}
function openBotNow(){if(botUsername)window.open('https://t.me/'+botUsername,'_blank')}
load();setInterval(load,5000);
</script>
</body>
</html>
`
