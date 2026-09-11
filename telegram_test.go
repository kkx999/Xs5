package main

import (
	"testing"
	"time"
)

func TestCommandName(t *testing.T) {
	cases := map[string]string{
		"/status":           "status",
		"/switch@xs5_test":  "switch",
		" /recovery extra ": "recovery",
		"hello":             "",
	}
	for in, want := range cases {
		if got := commandName(in); got != want {
			t.Fatalf("commandName(%q)=%q want %q", in, got, want)
		}
	}
}

func TestMaskTelegramToken(t *testing.T) {
	got := maskTelegramToken("1234567890:ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	if got == "" || got == "1234567890:ABCDEFGHIJKLMNOPQRSTUVWXYZ" {
		t.Fatalf("token was not masked: %q", got)
	}
	if got[:6] != "123456" {
		t.Fatalf("unexpected mask prefix: %q", got)
	}
}

func TestPoolPausePersistenceState(t *testing.T) {
	m := &TelegramManager{cfg: defaultTelegramConfig(), lastSend: map[string]time.Time{}}
	m.cfg.PausedPools["US-1"] = true
	if !m.isPoolPaused("US-1") {
		t.Fatal("expected pool to be paused")
	}
	if m.isPoolPaused("JP-1") {
		t.Fatal("unexpected paused pool")
	}
}

func TestFlagEmojiFallback(t *testing.T) {
	if flagEmoji("US") == "🌐" {
		t.Fatal("valid country code should produce regional flag")
	}
	if got := flagEmoji("BAD"); got != "🌐" {
		t.Fatalf("invalid country should fall back, got %q", got)
	}
}

func TestTelegramExitLabelIncludesIPType(t *testing.T) {
	v := PoolView{ExitIP: "203.0.113.8", IPType: "住宅/ISP"}
	if got := telegramExitLabel(v); got != "203.0.113.8（住宅/ISP）" {
		t.Fatalf("telegramExitLabel=%q", got)
	}
	v.IPType = ""
	if got := telegramExitLabel(v); got != "203.0.113.8（暂未识别）" {
		t.Fatalf("telegramExitLabel pending=%q", got)
	}
	v.ExitIP = ""
	if got := telegramExitLabel(v); got != "-" {
		t.Fatalf("telegramExitLabel empty=%q", got)
	}
}
