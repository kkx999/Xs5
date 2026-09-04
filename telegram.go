package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	telegramConfigPath = "/etc/xs5/telegram.json"
	telegramBindWindow = 10 * time.Minute
)

type telegramConfig struct {
	Token          string          `json:"token"`
	UserID         int64           `json:"user_id,omitempty"`
	ChatID         int64           `json:"chat_id,omitempty"`
	Enabled        bool            `json:"enabled"`
	RemoteControl  bool            `json:"remote_control"`
	NotifyStart    bool            `json:"notify_start"`
	NotifySwitch   bool            `json:"notify_switch"`
	NotifyFailure  bool            `json:"notify_failure"`
	NotifyRecovery bool            `json:"notify_recovery"`
	NotifyResource bool            `json:"notify_resource"`
	NotifyRefresh  bool            `json:"notify_refresh"`
	DailySummary   bool            `json:"daily_summary"`
	BoundAt        time.Time       `json:"bound_at,omitempty"`
	PausedPools    map[string]bool `json:"paused_pools,omitempty"`
}

func defaultTelegramConfig() telegramConfig {
	return telegramConfig{
		Enabled: true, RemoteControl: true,
		NotifyStart: true, NotifySwitch: true, NotifyFailure: true,
		NotifyRecovery: true, NotifyResource: true, NotifyRefresh: true,
		PausedPools: map[string]bool{},
	}
}

type TelegramManager struct {
	app *App

	mu           sync.RWMutex
	cfg          telegramConfig
	cancel       context.CancelFunc
	bindingUntil time.Time
	botUsername  string
	lastDaily    string
	startedAt    time.Time

	notifyMu sync.Mutex
	lastSend map[string]time.Time
}

func newTelegramManager(app *App) *TelegramManager {
	t := &TelegramManager{app: app, cfg: defaultTelegramConfig(), lastSend: map[string]time.Time{}, startedAt: time.Now()}
	_ = t.load()
	return t
}

func (t *TelegramManager) load() error {
	b, err := os.ReadFile(telegramConfigPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	cfg := defaultTelegramConfig()
	if err := json.Unmarshal(b, &cfg); err != nil {
		return err
	}
	if cfg.PausedPools == nil {
		cfg.PausedPools = map[string]bool{}
	}
	t.mu.Lock()
	t.cfg = cfg
	t.mu.Unlock()
	return nil
}

func (t *TelegramManager) saveLocked() error {
	if t.cfg.PausedPools == nil {
		t.cfg.PausedPools = map[string]bool{}
	}
	if err := os.MkdirAll(filepath.Dir(telegramConfigPath), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(t.cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := telegramConfigPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, telegramConfigPath)
}

func (t *TelegramManager) start() {
	t.restartPolling(true)
}

func (t *TelegramManager) stop() {
	t.mu.Lock()
	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}
	t.mu.Unlock()
}

func (t *TelegramManager) restartPolling(sendStartup bool) {
	t.mu.Lock()
	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}
	token := strings.TrimSpace(t.cfg.Token)
	if token == "" {
		t.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel
	t.mu.Unlock()

	go t.pollLoop(ctx, token)
	go func() {
		info, err := t.validateToken(token)
		if err == nil {
			t.mu.Lock()
			t.botUsername = info.Username
			t.mu.Unlock()
		}
	}()
	go t.registerCommands(token)
	if sendStartup {
		go t.delayedStartupSummary(ctx)
	}
	go t.dailySummaryLoop(ctx)
}

func (t *TelegramManager) delayedStartupSummary(ctx context.Context) {
	tm := time.NewTimer(15 * time.Second)
	defer tm.Stop()
	select {
	case <-ctx.Done():
		return
	case <-tm.C:
		t.notifyStartup()
	}
}

func (t *TelegramManager) dailySummaryLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			t.mu.Lock()
			enabled := t.cfg.Enabled && t.cfg.DailySummary && t.cfg.ChatID != 0
			day := now.Format("2006-01-02")
			if enabled && now.Hour() >= 9 && t.lastDaily != day {
				t.lastDaily = day
				t.mu.Unlock()
				t.sendBound("📊 Xs5 每日运行摘要\n\n"+t.statusText(), nil)
				continue
			}
			t.mu.Unlock()
		}
	}
}

type tgAPIEnvelope struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

func telegramCall(ctx context.Context, token, method string, payload any, out any) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("Telegram Bot Token 为空")
	}
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.telegram.org/bot"+token+"/"+method, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	cli := &http.Client{Timeout: 40 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return errors.New(strings.ReplaceAll(err.Error(), token, "<BOT_TOKEN>"))
	}
	defer resp.Body.Close()
	var env tgAPIEnvelope
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&env); err != nil {
		return err
	}
	if !env.OK {
		desc := strings.ReplaceAll(env.Description, token, "<BOT_TOKEN>")
		if desc == "" {
			desc = fmt.Sprintf("Telegram API HTTP %d", resp.StatusCode)
		}
		return errors.New(desc)
	}
	if out != nil && len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return err
		}
	}
	return nil
}

type tgUser struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}

type tgChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type tgMessage struct {
	MessageID int64   `json:"message_id"`
	From      *tgUser `json:"from"`
	Chat      tgChat  `json:"chat"`
	Text      string  `json:"text"`
}

type tgCallback struct {
	ID      string     `json:"id"`
	From    tgUser     `json:"from"`
	Message *tgMessage `json:"message"`
	Data    string     `json:"data"`
}

type tgUpdate struct {
	UpdateID      int64       `json:"update_id"`
	Message       *tgMessage  `json:"message"`
	CallbackQuery *tgCallback `json:"callback_query"`
}

type tgBotInfo struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type tgButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

type tgMarkup struct {
	InlineKeyboard [][]tgButton `json:"inline_keyboard"`
}

func (t *TelegramManager) validateToken(token string) (tgBotInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	var info tgBotInfo
	err := telegramCall(ctx, token, "getMe", map[string]any{}, &info)
	return info, err
}

func (t *TelegramManager) registerCommands(token string) {
	commands := []map[string]string{
		{"command": "status", "description": "查看全部出口状态"},
		{"command": "switch", "description": "立即切换指定出口"},
		{"command": "check", "description": "手动检测指定出口"},
		{"command": "refresh", "description": "刷新节点池"},
		{"command": "recovery", "description": "查看自动恢复状态"},
		{"command": "pause", "description": "暂停指定出口自动切换"},
		{"command": "resume", "description": "恢复指定出口自动切换"},
		{"command": "logs", "description": "查看最近关键日志"},
		{"command": "help", "description": "查看机器人帮助"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	_ = telegramCall(ctx, token, "deleteWebhook", map[string]any{"drop_pending_updates": false}, nil)
	_ = telegramCall(ctx, token, "setMyCommands", map[string]any{"commands": commands}, nil)
}

func (t *TelegramManager) pollLoop(ctx context.Context, token string) {
	var offset int64
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		var updates []tgUpdate
		err := telegramCall(ctx, token, "getUpdates", map[string]any{
			"offset": offset, "timeout": 25, "allowed_updates": []string{"message", "callback_query"},
		}, &updates)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 15*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		for _, up := range updates {
			if up.UpdateID >= offset {
				offset = up.UpdateID + 1
			}
			t.handleUpdate(token, up)
		}
	}
}

func (t *TelegramManager) handleUpdate(token string, up tgUpdate) {
	if up.Message != nil {
		t.handleMessage(token, up.Message)
		return
	}
	if up.CallbackQuery != nil {
		t.handleCallback(token, up.CallbackQuery)
	}
}

func (t *TelegramManager) isAuthorized(userID, chatID int64) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.cfg.UserID != 0 && t.cfg.ChatID != 0 && t.cfg.UserID == userID && t.cfg.ChatID == chatID
}

func commandName(text string) string {
	f := strings.Fields(strings.TrimSpace(text))
	if len(f) == 0 || !strings.HasPrefix(f[0], "/") {
		return ""
	}
	cmd := strings.TrimPrefix(f[0], "/")
	if i := strings.IndexByte(cmd, '@'); i >= 0 {
		cmd = cmd[:i]
	}
	return strings.ToLower(cmd)
}

func (t *TelegramManager) handleMessage(token string, m *tgMessage) {
	if m == nil || m.From == nil || m.Chat.Type != "private" {
		return
	}
	cmd := commandName(m.Text)
	if cmd == "start" {
		if t.tryBind(token, m) {
			return
		}
	}
	if !t.isAuthorized(m.From.ID, m.Chat.ID) {
		return
	}

	switch cmd {
	case "start", "status":
		t.sendTo(token, m.Chat.ID, t.statusText(), t.mainMenu())
	case "switch":
		t.sendPoolMenu(token, m.Chat.ID, "选择要立即切换的出口：", "sw:", false)
	case "check":
		t.sendPoolMenu(token, m.Chat.ID, "选择要手动检测的出口：", "ck:", false)
	case "refresh":
		t.sendTo(token, m.Chat.ID, "选择要刷新的节点源：", tgMarkup{InlineKeyboard: [][]tgButton{
			{{Text: "VPN Gate", CallbackData: "rf:vpngate"}, {Text: "Proxio", CallbackData: "rf:proxio"}},
			{{Text: "ProxyScrape", CallbackData: "rf:proxyscrape_free"}, {Text: "全部来源", CallbackData: "rf:all"}},
		}})
	case "recovery":
		t.sendTo(token, m.Chat.ID, t.recoveryText(), t.mainMenu())
	case "pause":
		t.sendPoolMenu(token, m.Chat.ID, "选择要暂停自动切换的出口：", "ps:", false)
	case "resume":
		t.sendPoolMenu(token, m.Chat.ID, "选择要恢复自动切换的出口：", "rs:", true)
	case "logs":
		t.sendTo(token, m.Chat.ID, t.logsText(), nil)
	case "help":
		t.sendTo(token, m.Chat.ID, t.helpText(), t.mainMenu())
	default:
		if cmd != "" {
			t.sendTo(token, m.Chat.ID, "未知指令。发送 /help 查看可用功能。", t.mainMenu())
		}
	}
}

func (t *TelegramManager) tryBind(token string, m *tgMessage) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cfg.UserID != 0 || t.cfg.ChatID != 0 {
		return false
	}
	if t.bindingUntil.IsZero() || time.Now().After(t.bindingUntil) {
		return false
	}
	t.cfg.UserID = m.From.ID
	t.cfg.ChatID = m.Chat.ID
	t.cfg.BoundAt = time.Now()
	t.bindingUntil = time.Time{}
	if t.cfg.PausedPools == nil {
		t.cfg.PausedPools = map[string]bool{}
	}
	if err := t.saveLocked(); err != nil {
		return false
	}
	go t.sendTo(token, m.Chat.ID, "✅ Xs5 Telegram 已绑定成功。\n\n你是当前唯一管理员，机器人命令与远程控制已经启用。", t.mainMenu())
	return true
}

func (t *TelegramManager) mainMenu() tgMarkup {
	return tgMarkup{InlineKeyboard: [][]tgButton{
		{{Text: "📊 运行状态", CallbackData: "m:status"}, {Text: "🔄 立即切换", CallbackData: "m:switch"}},
		{{Text: "🩺 健康检测", CallbackData: "m:check"}, {Text: "🌐 刷新节点池", CallbackData: "m:refresh"}},
		{{Text: "♻️ 恢复状态", CallbackData: "m:recovery"}, {Text: "⏸ 暂停 / 恢复", CallbackData: "m:pause"}},
	}}
}

func (t *TelegramManager) handleCallback(token string, q *tgCallback) {
	if q == nil || q.Message == nil || !t.isAuthorized(q.From.ID, q.Message.Chat.ID) {
		return
	}
	t.answerCallback(token, q.ID)
	chatID := q.Message.Chat.ID
	data := q.Data

	t.mu.RLock()
	remote := t.cfg.RemoteControl
	t.mu.RUnlock()

	switch data {
	case "m:status":
		t.sendTo(token, chatID, t.statusText(), t.mainMenu())
	case "m:switch":
		t.sendPoolMenu(token, chatID, "选择要立即切换的出口：", "sw:", false)
	case "m:check":
		t.sendPoolMenu(token, chatID, "选择要手动检测的出口：", "ck:", false)
	case "m:refresh":
		t.sendTo(token, chatID, "选择要刷新的节点源：", tgMarkup{InlineKeyboard: [][]tgButton{
			{{Text: "VPN Gate", CallbackData: "rf:vpngate"}, {Text: "Proxio", CallbackData: "rf:proxio"}},
			{{Text: "ProxyScrape", CallbackData: "rf:proxyscrape_free"}, {Text: "全部来源", CallbackData: "rf:all"}},
		}})
	case "m:recovery":
		t.sendTo(token, chatID, t.recoveryText(), t.mainMenu())
	case "m:pause":
		t.sendTo(token, chatID, "选择操作：", tgMarkup{InlineKeyboard: [][]tgButton{{
			{Text: "⏸ 暂停自动切换", CallbackData: "menu:pause"}, {Text: "▶️ 恢复自动切换", CallbackData: "menu:resume"},
		}}})
	case "menu:pause":
		t.sendPoolMenu(token, chatID, "选择要暂停的出口：", "ps:", false)
	case "menu:resume":
		t.sendPoolMenu(token, chatID, "选择要恢复的出口：", "rs:", true)
	default:
		if strings.HasPrefix(data, "sw:") {
			if !remote {
				t.sendTo(token, chatID, "远程控制已在面板中关闭。", nil)
				return
			}
			id := strings.TrimPrefix(data, "sw:")
			p := t.pool(id)
			if p == nil {
				t.sendTo(token, chatID, "出口不存在。", nil)
				return
			}
			v := p.view()
			if v.Status == "starting" || v.Status == "switching" || v.Status == "restoring" {
				t.sendTo(token, chatID, "⏳ "+poolDisplay(v)+" 当前正在"+v.Status+"，请等待本轮完成后再操作。", nil)
				return
			}
			t.sendTo(token, chatID, "🔄 正在切换 "+poolDisplay(v)+"…\n固定 S5 地址不会改变。", nil)
			p.mu.Lock()
			p.Status = "switching"
			p.Error = ""
			p.mu.Unlock()
			go t.app.switchNext(p, "switching")
			return
		}
		if strings.HasPrefix(data, "ck:") {
			id := strings.TrimPrefix(data, "ck:")
			p := t.pool(id)
			if p == nil {
				t.sendTo(token, chatID, "出口不存在。", nil)
				return
			}
			v := p.view()
			t.sendTo(token, chatID, "🩺 正在检测 "+poolDisplay(v)+"，只检测，不会触发切换。", nil)
			go func() {
				latency, err := probePoolConnectivity(p)
				if err != nil {
					t.sendTo(token, chatID, "❌ "+poolDisplay(v)+" 检测失败\n"+safeTGText(err.Error(), 900), nil)
					return
				}
				t.sendTo(token, chatID, fmt.Sprintf("✅ %s 完整 S5 链路正常\n响应耗时：%d ms", poolDisplay(v), latency), nil)
			}()
			return
		}
		if strings.HasPrefix(data, "rf:") {
			if !remote {
				t.sendTo(token, chatID, "远程控制已在面板中关闭。", nil)
				return
			}
			source := normalizeSource(strings.TrimPrefix(data, "rf:"))
			t.sendTo(token, chatID, "🌐 正在刷新 "+sourceLabel(source)+" 节点池…", nil)
			go func() {
				if source == sourceAll {
					t.sendTo(token, chatID, t.refreshAllSourcesText(), nil)
					return
				}
				if err := t.app.refreshSource(source); err != nil {
					t.sendTo(token, chatID, "❌ "+sourceLabel(source)+" 刷新失败\n"+safeTGText(err.Error(), 900), nil)
					return
				}
				count := t.sourceNodeCount(source)
				t.sendTo(token, chatID, fmt.Sprintf("✅ %s 节点池刷新完成：%d 个候选。", sourceLabel(source), count), nil)
			}()
			return
		}
		if strings.HasPrefix(data, "ps:") {
			if !remote {
				t.sendTo(token, chatID, "远程控制已在面板中关闭。", nil)
				return
			}
			id := strings.TrimPrefix(data, "ps:")
			p := t.pool(id)
			if p == nil {
				t.sendTo(token, chatID, "出口不存在。", nil)
				return
			}
			t.setPoolPaused(id, true)
			cancelAutoRetry(id)
			t.sendTo(token, chatID, "⏸ 已暂停 "+poolDisplay(p.view())+" 的自动健康切换。\n当前线路不会被主动停止，仍可手动检测或立即切换。", nil)
			return
		}
		if strings.HasPrefix(data, "rs:") {
			if !remote {
				t.sendTo(token, chatID, "远程控制已在面板中关闭。", nil)
				return
			}
			id := strings.TrimPrefix(data, "rs:")
			p := t.pool(id)
			if p == nil {
				t.sendTo(token, chatID, "出口不存在。", nil)
				return
			}
			t.setPoolPaused(id, false)
			v := p.view()
			if v.Status != "up" {
				go t.app.switchNext(p, "switching")
			}
			t.sendTo(token, chatID, "▶️ 已恢复 "+poolDisplay(v)+" 的自动健康切换。", nil)
		}
	}
}

func (t *TelegramManager) sourceNodeCount(source string) int {
	t.app.mu.RLock()
	defer t.app.mu.RUnlock()
	count := 0
	for _, n := range t.app.Nodes {
		if n.Source == source {
			count++
		}
	}
	return count
}

func (t *TelegramManager) refreshAllSourcesText() string {
	sources := []string{sourceVPNGate, sourceProxio, sourceProxyScrape}
	type refreshResult struct {
		source string
		count  int
		err    error
	}
	ch := make(chan refreshResult, len(sources))
	for _, source := range sources {
		go func(s string) {
			err := t.app.refreshSource(s)
			count := 0
			if err == nil {
				count = t.sourceNodeCount(s)
			}
			ch <- refreshResult{source: s, count: count, err: err}
		}(source)
	}
	results := map[string]refreshResult{}
	for range sources {
		r := <-ch
		results[r.source] = r
	}

	okCount := 0
	lines := make([]string, 0, len(sources))
	for _, source := range sources {
		r := results[source]
		if r.err != nil {
			lines = append(lines, "❌ "+sourceLabel(source)+"："+safeTGText(r.err.Error(), 260))
			continue
		}
		okCount++
		lines = append(lines, fmt.Sprintf("✅ %s：%d 个候选", sourceLabel(source), r.count))
	}

	title := "✅ 全部来源节点池刷新完成"
	if okCount == 0 {
		title = "❌ 全部来源节点池刷新失败"
	} else if okCount != len(sources) {
		title = "⚠️ 全部来源节点池部分完成"
	}
	return title + "\n\n" + strings.Join(lines, "\n")
}

func (t *TelegramManager) answerCallback(token, id string) {
	if id == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_ = telegramCall(ctx, token, "answerCallbackQuery", map[string]any{"callback_query_id": id}, nil)
}

func (t *TelegramManager) pool(id string) *Pool {
	t.app.mu.RLock()
	defer t.app.mu.RUnlock()
	return t.app.Pools[id]
}

func (t *TelegramManager) poolViews() []PoolView {
	t.app.mu.RLock()
	vals := make([]PoolView, 0, len(t.app.Pools))
	for _, p := range t.app.Pools {
		vals = append(vals, p.view())
	}
	t.app.mu.RUnlock()
	sort.Slice(vals, func(i, j int) bool {
		if vals[i].CountryCode != vals[j].CountryCode {
			return vals[i].CountryCode < vals[j].CountryCode
		}
		return vals[i].Ordinal < vals[j].Ordinal
	})
	return vals
}

func (t *TelegramManager) sendPoolMenu(token string, chatID int64, title, prefix string, onlyPaused bool) {
	views := t.poolViews()
	rows := make([][]tgButton, 0, len(views))
	for _, v := range views {
		paused := t.isPoolPaused(v.ID)
		if onlyPaused && !paused {
			continue
		}
		label := poolDisplay(v)
		if paused {
			label = "⏸ " + label
		}
		rows = append(rows, []tgButton{{Text: label, CallbackData: prefix + v.ID}})
	}
	if len(rows) == 0 {
		t.sendTo(token, chatID, "没有符合条件的出口。", t.mainMenu())
		return
	}
	t.sendTo(token, chatID, title, tgMarkup{InlineKeyboard: rows})
}

func (t *TelegramManager) sendTo(token string, chatID int64, text string, markup any) {
	if chatID == 0 || strings.TrimSpace(token) == "" {
		return
	}
	payload := map[string]any{"chat_id": chatID, "text": safeTGText(text, 3900), "disable_web_page_preview": true}
	if markup != nil {
		payload["reply_markup"] = markup
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	_ = telegramCall(ctx, token, "sendMessage", payload, nil)
}

func (t *TelegramManager) sendBound(text string, markup any) {
	t.mu.RLock()
	token, chatID := t.cfg.Token, t.cfg.ChatID
	enabled := t.cfg.Enabled
	t.mu.RUnlock()
	if !enabled || token == "" || chatID == 0 {
		return
	}
	t.sendTo(token, chatID, text, markup)
}

func safeTGText(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len([]rune(s)) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "…"
}

func flagEmoji(cc string) string {
	cc = strings.ToUpper(strings.TrimSpace(cc))
	if len(cc) != 2 || cc[0] < 'A' || cc[0] > 'Z' || cc[1] < 'A' || cc[1] > 'Z' {
		return "🌐"
	}
	return string([]rune{rune(0x1F1E6) + rune(cc[0]-'A'), rune(0x1F1E6) + rune(cc[1]-'A')})
}

func poolDisplay(v PoolView) string {
	return fmt.Sprintf("%s %s #%d", flagEmoji(v.CountryCode), v.Country, v.Ordinal)
}

func tgStatusIcon(status string) string {
	switch status {
	case "up":
		return "🟢"
	case "starting", "switching", "restoring":
		return "🟡"
	default:
		return "🔴"
	}
}

func (t *TelegramManager) statusText() string {
	views := t.poolViews()
	if len(views) == 0 {
		return "📊 Xs5 运行状态\n\n当前还没有配置出口。"
	}
	up, busy := 0, 0
	var b strings.Builder
	b.WriteString("📊 Xs5 运行状态\n\n")
	for _, v := range views {
		if v.Status == "up" {
			up++
		} else if v.Status == "starting" || v.Status == "switching" || v.Status == "restoring" {
			busy++
		}
		fmt.Fprintf(&b, "%s %s", tgStatusIcon(v.Status), poolDisplay(v))
		if t.isPoolPaused(v.ID) {
			b.WriteString(" · ⏸")
		}
		b.WriteString("\n")
		if v.ActiveSource == "" {
			b.WriteString("来源：-\n")
		} else {
			fmt.Fprintf(&b, "来源：%s\n", sourceLabel(v.ActiveSource))
		}
		if v.ExitIP != "" {
			fmt.Fprintf(&b, "出口：%s\n", v.ExitIP)
		} else {
			b.WriteString("出口：-\n")
		}
		if v.LatencyMS >= 0 {
			fmt.Fprintf(&b, "完整链路：%d ms\n", v.LatencyMS)
		}
		if v.Error != "" && v.Status != "up" {
			fmt.Fprintf(&b, "状态：%s\n", safeTGText(v.Error, 180))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "合计：%d 个出口 · %d 正常 · %d 处理中", len(views), up, busy)
	return safeTGText(b.String(), 3900)
}

func (t *TelegramManager) recoveryText() string {
	views := t.poolViews()
	var b strings.Builder
	b.WriteString("♻️ Xs5 自动恢复状态\n\n")
	shown := 0
	for _, v := range views {
		p := t.pool(v.ID)
		if p == nil {
			continue
		}
		p.mu.Lock()
		cands := append([]Node(nil), p.Candidates...)
		p.mu.Unlock()
		_, cooling, earliest := getCandidateScanState(v.ID).attemptOrder(cands, nil, time.Now())
		if v.Status == "up" && cooling == 0 && !t.isPoolPaused(v.ID) {
			continue
		}
		shown++
		fmt.Fprintf(&b, "%s %s\n", tgStatusIcon(v.Status), poolDisplay(v))
		fmt.Fprintf(&b, "状态：%s", v.Status)
		if t.isPoolPaused(v.ID) {
			b.WriteString(" · 已暂停自动切换")
		}
		b.WriteString("\n")
		fmt.Fprintf(&b, "候选：%d · 冷却：%d", len(cands), cooling)
		if cooling > 0 && !earliest.IsZero() {
			fmt.Fprintf(&b, " · 最早 %s 后恢复", cooldownWait(earliest, time.Now()))
		}
		b.WriteString("\n")
		if v.Error != "" {
			fmt.Fprintf(&b, "%s\n", safeTGText(v.Error, 220))
		}
		b.WriteString("\n")
	}
	if shown == 0 {
		b.WriteString("当前没有处于故障、冷却或暂停状态的出口。")
	}
	return safeTGText(b.String(), 3900)
}

func (t *TelegramManager) logsText() string {
	out, err := exec.Command("journalctl", "-u", "xs5", "-n", "20", "--no-pager", "-o", "short-iso").CombinedOutput()
	if err != nil && len(out) == 0 {
		return "无法读取 Xs5 日志：" + safeTGText(err.Error(), 500)
	}
	text := strings.TrimSpace(string(out))
	t.mu.RLock()
	token := t.cfg.Token
	t.mu.RUnlock()
	if token != "" {
		text = strings.ReplaceAll(text, token, "<BOT_TOKEN>")
	}
	if text == "" {
		text = "暂无日志"
	}
	return "🧾 Xs5 最近日志\n\n" + safeTGText(text, 3400)
}

func (t *TelegramManager) helpText() string {
	return "🤖 Xs5 Telegram 控制\n\n" +
		"/status  查看全部出口状态\n" +
		"/switch  立即切换指定出口\n" +
		"/check  手动检测指定出口，不触发切换\n" +
		"/refresh  刷新 VPN Gate / Proxio / ProxyScrape 节点池\n" +
		"/recovery  查看故障、冷却和恢复状态\n" +
		"/pause  暂停指定出口自动切换\n" +
		"/resume  恢复指定出口自动切换\n" +
		"/logs  查看最近 20 条 Xs5 日志\n" +
		"/help  查看本帮助\n\n" +
		"机器人只接受面板首次绑定的 Telegram 管理员操作。"
}

func (t *TelegramManager) isPoolPaused(id string) bool {
	if t == nil {
		return false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.cfg.PausedPools != nil && t.cfg.PausedPools[id]
}

func (t *TelegramManager) setPoolPaused(id string, paused bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.cfg.PausedPools == nil {
		t.cfg.PausedPools = map[string]bool{}
	}
	if paused {
		t.cfg.PausedPools[id] = true
	} else {
		delete(t.cfg.PausedPools, id)
	}
	_ = t.saveLocked()
	t.mu.Unlock()
}

func (t *TelegramManager) allowNotify(key string, gap time.Duration) bool {
	t.notifyMu.Lock()
	defer t.notifyMu.Unlock()
	now := time.Now()
	if last, ok := t.lastSend[key]; ok && now.Sub(last) < gap {
		return false
	}
	t.lastSend[key] = now
	return true
}

func (t *TelegramManager) notifyStartup() {
	if t == nil {
		return
	}
	t.mu.RLock()
	on := t.cfg.Enabled && t.cfg.NotifyStart && t.cfg.ChatID != 0
	t.mu.RUnlock()
	if !on || !t.allowNotify("startup", time.Minute) {
		return
	}
	t.sendBound("✅ Xs5 已启动\n\n"+strings.TrimPrefix(t.statusText(), "📊 Xs5 运行状态\n\n"), t.mainMenu())
}

func (t *TelegramManager) notifyAutoSwitchStart(p *Pool, cause error) {
	if t == nil || p == nil {
		return
	}
	t.mu.RLock()
	on := t.cfg.Enabled && t.cfg.NotifySwitch && t.cfg.ChatID != 0
	t.mu.RUnlock()
	if !on || !t.allowNotify("switch-start:"+p.ID, 30*time.Second) {
		return
	}
	v := p.view()
	msg := "⚠️ " + poolDisplay(v) + " 连续健康检测失败，正在自动切换。"
	if cause != nil {
		msg += "\n原因：" + safeTGText(cause.Error(), 500)
	}
	t.sendBound(msg, nil)
}

func (t *TelegramManager) notifySwitchSuccess(p *Pool, before PoolView, phase string) {
	if t == nil || p == nil || phase == "restoring" {
		return
	}
	for i := 0; i < 12; i++ {
		v := p.view()
		if v.ExitIP != "" || v.Status != "up" {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	after := p.view()
	if after.Status != "up" {
		return
	}
	if after.ExitIP == "" {
		if ip, err := detectPoolExitIP(p); err == nil {
			changed := false
			p.mu.Lock()
			if p.Status == "up" {
				changed = p.ExitIP != ip
				p.ExitIP = ip
				if changed {
					p.IPType = ""
					p.IPISP = ""
					p.IPASN = ""
					p.IPRisk = ""
				}
			}
			p.mu.Unlock()
			if changed {
				go enrichPoolIPProfile(p, ip)
			}
			after = p.view()
		}
	}
	t.mu.RLock()
	isRecovery := before.Status == "failed" || before.Status == "no-candidates" || before.Status == "restoring"
	on := t.cfg.Enabled && t.cfg.ChatID != 0 && ((isRecovery && t.cfg.NotifyRecovery) || (!isRecovery && t.cfg.NotifySwitch))
	t.mu.RUnlock()
	if !on {
		return
	}
	title := "✅ " + poolDisplay(after) + " 切换成功"
	if isRecovery {
		title = "✅ " + poolDisplay(after) + " 已恢复正常"
	}
	oldIP := before.ExitIP
	if oldIP == "" {
		oldIP = "-"
	}
	newIP := after.ExitIP
	if newIP == "" {
		newIP = "获取中"
	}
	msg := fmt.Sprintf("%s\n\n来源：%s\n旧出口：%s\n新出口：%s", title, sourceLabel(after.ActiveSource), oldIP, newIP)
	if after.NodeLatencyMS >= 0 {
		msg += fmt.Sprintf("\n节点延迟：%d ms", after.NodeLatencyMS)
	}
	if after.LatencyMS >= 0 {
		msg += fmt.Sprintf("\n完整链路：%d ms", after.LatencyMS)
	}
	t.sendBound(msg, nil)
}

func (t *TelegramManager) notifySwitchFailure(p *Pool) {
	if t == nil || p == nil {
		return
	}
	t.mu.RLock()
	on := t.cfg.Enabled && t.cfg.NotifyFailure && t.cfg.ChatID != 0
	t.mu.RUnlock()
	if time.Since(t.startedAt) < 20*time.Second {
		return
	}
	v := p.view()
	if v.Status == "up" {
		return
	}
	if !on || !t.allowNotify("switch-fail:"+p.ID, 2*time.Minute) {
		return
	}
	t.sendBound("❌ "+poolDisplay(v)+" 暂未找到可用出口\n\n"+safeTGText(v.Error, 1200), nil)
}

func (t *TelegramManager) notifyResource(p *Pool, cause error) {
	if t == nil || p == nil {
		return
	}
	t.mu.RLock()
	on := t.cfg.Enabled && t.cfg.NotifyResource && t.cfg.ChatID != 0
	t.mu.RUnlock()
	if !on || !t.allowNotify("resource:"+p.ID, 5*time.Minute) {
		return
	}
	v := p.view()
	msg := "⚠️ Xs5 服务器资源异常\n\n受影响：" + poolDisplay(v) + "\n候选节点：本次不处罚\n系统将在约 30 秒后自动重试。"
	if cause != nil {
		msg += "\n系统错误：" + safeTGText(cause.Error(), 700)
	}
	t.sendBound(msg, nil)
}

func (t *TelegramManager) notifyRefreshFailure(source string, err error) {
	if t == nil || err == nil {
		return
	}
	t.mu.RLock()
	on := t.cfg.Enabled && t.cfg.NotifyRefresh && t.cfg.ChatID != 0
	t.mu.RUnlock()
	if !on || !t.allowNotify("refresh:"+source, 10*time.Minute) {
		return
	}
	t.sendBound("⚠️ "+sourceLabel(source)+" 节点池刷新失败\n"+safeTGText(err.Error(), 900), nil)
}

func (a *App) telegramPage(w http.ResponseWriter, r *http.Request) {
	io.WriteString(w, telegramHTML)
}

func (a *App) telegramStatus(w http.ResponseWriter, r *http.Request) {
	if a.telegram == nil {
		writeJSON(w, 500, map[string]string{"error": "Telegram 模块未初始化"})
		return
	}
	t := a.telegram
	t.mu.RLock()
	cfg := t.cfg
	bindingUntil := t.bindingUntil
	botUsername := t.botUsername
	t.mu.RUnlock()
	writeJSON(w, 200, map[string]any{
		"configured": cfg.Token != "", "token_masked": maskTelegramToken(cfg.Token),
		"bound": cfg.UserID != 0 && cfg.ChatID != 0, "bot_username": botUsername,
		"binding":         !bindingUntil.IsZero() && time.Now().Before(bindingUntil),
		"binding_seconds": maxInt64(0, int64(time.Until(bindingUntil).Seconds())),
		"enabled":         cfg.Enabled, "remote_control": cfg.RemoteControl,
		"notify_start": cfg.NotifyStart, "notify_switch": cfg.NotifySwitch,
		"notify_failure": cfg.NotifyFailure, "notify_recovery": cfg.NotifyRecovery,
		"notify_resource": cfg.NotifyResource, "notify_refresh": cfg.NotifyRefresh,
		"daily_summary": cfg.DailySummary,
	})
}

func (a *App) telegramSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "POST only"})
		return
	}
	t := a.telegram
	if t == nil {
		writeJSON(w, 500, map[string]string{"error": "Telegram 模块未初始化"})
		return
	}
	newToken := strings.TrimSpace(r.FormValue("token"))
	t.mu.RLock()
	oldToken := t.cfg.Token
	t.mu.RUnlock()
	token := oldToken
	if newToken != "" {
		token = newToken
	}
	if token == "" {
		writeJSON(w, 400, map[string]string{"error": "请填写 Bot Token"})
		return
	}
	info, err := t.validateToken(token)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "Bot Token 验证失败：" + err.Error()})
		return
	}

	t.mu.Lock()
	tokenChanged := token != t.cfg.Token
	if tokenChanged {
		t.cfg.UserID = 0
		t.cfg.ChatID = 0
		t.cfg.BoundAt = time.Time{}
		t.bindingUntil = time.Time{}
	}
	t.cfg.Token = token
	t.cfg.Enabled = formBool(r, "enabled")
	t.cfg.RemoteControl = formBool(r, "remote_control")
	t.cfg.NotifyStart = formBool(r, "notify_start")
	t.cfg.NotifySwitch = formBool(r, "notify_switch")
	t.cfg.NotifyFailure = formBool(r, "notify_failure")
	t.cfg.NotifyRecovery = formBool(r, "notify_recovery")
	t.cfg.NotifyResource = formBool(r, "notify_resource")
	t.cfg.NotifyRefresh = formBool(r, "notify_refresh")
	t.cfg.DailySummary = formBool(r, "daily_summary")
	t.botUsername = info.Username
	err = t.saveLocked()
	t.mu.Unlock()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	t.restartPolling(false)
	writeJSON(w, 200, map[string]any{"ok": true, "bot_username": info.Username, "token_changed": tokenChanged})
}

func (a *App) telegramBind(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "POST only"})
		return
	}
	t := a.telegram
	if t == nil {
		writeJSON(w, 500, map[string]string{"error": "Telegram 模块未初始化"})
		return
	}
	t.mu.Lock()
	if t.cfg.Token == "" {
		t.mu.Unlock()
		writeJSON(w, 400, map[string]string{"error": "请先保存 Bot Token"})
		return
	}
	if formBool(r, "reset") {
		t.cfg.UserID = 0
		t.cfg.ChatID = 0
		t.cfg.BoundAt = time.Time{}
	}
	if t.cfg.UserID != 0 || t.cfg.ChatID != 0 {
		t.mu.Unlock()
		writeJSON(w, 409, map[string]string{"error": "机器人已经绑定；如需更换管理员请使用重新绑定"})
		return
	}
	t.bindingUntil = time.Now().Add(telegramBindWindow)
	_ = t.saveLocked()
	username := t.botUsername
	t.mu.Unlock()
	writeJSON(w, 200, map[string]any{"ok": true, "seconds": int(telegramBindWindow.Seconds()), "bot_username": username})
}

func (a *App) telegramTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "POST only"})
		return
	}
	t := a.telegram
	if t == nil {
		writeJSON(w, 500, map[string]string{"error": "Telegram 模块未初始化"})
		return
	}
	t.mu.RLock()
	token, chatID := t.cfg.Token, t.cfg.ChatID
	t.mu.RUnlock()
	if token == "" || chatID == 0 {
		writeJSON(w, 400, map[string]string{"error": "请先完成 Telegram 绑定"})
		return
	}
	t.sendTo(token, chatID, "✅ Xs5 Telegram 测试通知成功。\n当前版本："+appVersion, t.mainMenu())
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) telegramUnbind(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "POST only"})
		return
	}
	t := a.telegram
	if t == nil {
		writeJSON(w, 500, map[string]string{"error": "Telegram 模块未初始化"})
		return
	}
	t.mu.Lock()
	t.cfg.UserID = 0
	t.cfg.ChatID = 0
	t.cfg.BoundAt = time.Time{}
	t.bindingUntil = time.Time{}
	err := t.saveLocked()
	t.mu.Unlock()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func formBool(r *http.Request, key string) bool {
	v := strings.ToLower(strings.TrimSpace(r.FormValue(key)))
	return v == "1" || v == "true" || v == "on" || v == "yes"
}

func maskTelegramToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	if len(token) <= 12 {
		return "********"
	}
	return token[:6] + "****" + token[len(token)-4:]
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func parseInt64(s string) int64 {
	v, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return v
}
