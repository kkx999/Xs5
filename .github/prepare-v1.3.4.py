from pathlib import Path


def replace_once(path: str, old: str, new: str) -> None:
    p = Path(path)
    text = p.read_text()
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"{path}: expected exactly one match, got {count}: {old!r}")
    p.write_text(text.replace(old, new, 1))


Path("VERSION").write_text("1.3.4\n")
Path("RELEASE.md").write_text("""# Xs5 v1.3.4

本版本在现有当前出口测速基础上加入真实上传测速，并按使用反馈简化速度展示与重测间隔。

- Web 面板一次测速同时测试当前出口的下载与上传吞吐，均真实经过该出口固定 SOCKS5 链路。
- Telegram `/speed` 与“🚀 出口测速”同步返回下载、上传结果。
- 面板与 Telegram 速度展示统一只显示 `MB/s`，不再同时显示重复的 Mbps 数值；内部仍保留带宽计算字段。
- 下载与上传分别采用按时间与流量双重兜底：每个方向最长约 8 秒、最多约 50 MB；高速线路会自动传输更多测试数据。
- 上传测试数据按流式方式即时生成并发送，不创建大文件，也不会一次性占用 50 MB 内存。
- 同一出口重测冷却由 30 秒调整为 10 秒，并从上一次测速结束后开始计算。
- 同一时间全局仍只允许一个测速任务；自动健康检查继续避让测速中的出口。
- 测速失败不会增加 FailCount、不会触发自动切换，也不会写入候选自学习或动态冷却评分。
- 出口发生切换后不会继续展示旧线路的上下行测速结果。
- 不改变 VPN Gate / Proxio / ProxyScrape 节点选择、自动恢复、候选排序、Telegram 通知和既有 S5 配置。

> 测速使用 Cloudflare 公共测速端点，结果会受到测速端点、出口拥塞及当时网络状态影响，仅供当前链路吞吐参考。
""")

replace_once("main.go", 'appVersion       = "v1.3.3"', 'appVersion       = "v1.3.4"')
replace_once("install.sh", "VERSION=1.3.3", "VERSION=1.3.4")
replace_once(
    "xs5.sh",
    'version(){ cat "$VERSION_FILE" 2>/dev/null || echo "1.3.3"; }',
    'version(){ cat "$VERSION_FILE" 2>/dev/null || echo "1.3.4"; }',
)

replace_once(
    "main.go",
    '\tSpeedTestedAt   time.Time `json:"speed_tested_at,omitempty"`\n',
    '\tUploadSpeedMbps       float64   `json:"upload_speed_mbps,omitempty"`\n'
    '\tUploadSpeedMBps       float64   `json:"upload_speed_mb_per_sec,omitempty"`\n'
    '\tUploadSpeedBytes      int64     `json:"upload_speed_bytes,omitempty"`\n'
    '\tUploadSpeedDurationMS int64     `json:"upload_speed_duration_ms,omitempty"`\n'
    '\tSpeedTestedAt         time.Time `json:"speed_tested_at,omitempty"`\n',
)

replace_once(
    "web.go",
    "var speed=(Number.isFinite(p.speed_mbps)&&p.speed_mbps>0?p.speed_mbps.toFixed(1)+' Mbps · '+Number(p.speed_mb_per_sec||0).toFixed(2)+' MB/s':'-');",
    "var downSpeed=(Number.isFinite(p.speed_mb_per_sec)&&p.speed_mb_per_sec>0?Number(p.speed_mb_per_sec).toFixed(2)+' MB/s':'-');var upSpeed=(Number.isFinite(p.upload_speed_mb_per_sec)&&p.upload_speed_mb_per_sec>0?Number(p.upload_speed_mb_per_sec).toFixed(2)+' MB/s':'-');",
)
replace_once(
    "web.go",
    "<div class=\"k\">下载测速</div><div class=\"v\"><code>'+esc(speed)+'</code></div>",
    "<div class=\"k\">下载速度</div><div class=\"v\"><code>'+esc(downSpeed)+'</code></div><div class=\"k\">上传速度</div><div class=\"v\"><code>'+esc(upSpeed)+'</code></div>",
)
replace_once(
    "web.go",
    "toast('测速完成：'+Number(r.mbps||0).toFixed(1)+' Mbps','success')",
    "toast('测速完成：下载 '+Number(r.mb_per_sec||0).toFixed(2)+' MB/s · 上传 '+Number(r.upload_mb_per_sec||0).toFixed(2)+' MB/s','success')",
)

replace_once(
    "telegram.go",
    't.sendTo(token, chatID, "🚀 正在测速 "+poolDisplay(v)+"…\\n最长约 8 秒，测试流量最多约 50 MB。", nil)',
    't.sendTo(token, chatID, "🚀 正在测速 "+poolDisplay(v)+"…\\n下载/上传各最长约 8 秒、各最多约 50 MB。", nil)',
)
replace_once(
    "telegram.go",
    'msg := fmt.Sprintf("🚀 %s 测速完成\\n\\n出口：%s\\n下载：%.1f Mbps\\n      %.2f MB/s\\n测试流量：%.1f MB\\n耗时：%.2f 秒", poolDisplay(after), telegramExitLabel(after), result.Mbps, result.MBps, float64(result.Bytes)/1_000_000, float64(result.DurationMS)/1000)',
    'msg := fmt.Sprintf("🚀 %s 测速完成\\n\\n出口：%s\\n下载：%.2f MB/s\\n上传：%.2f MB/s\\n下载流量：%.1f MB\\n上传流量：%.1f MB\\n耗时：%.2f 秒", poolDisplay(after), telegramExitLabel(after), result.MBps, result.UploadMBps, float64(result.Bytes)/1_000_000, float64(result.UploadBytes)/1_000_000, float64(result.DurationMS+result.UploadDurationMS)/1000)',
)
