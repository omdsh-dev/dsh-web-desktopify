package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestStatePath：状态记录位于工具链目录下。
func TestStatePath(t *testing.T) {
	ws := t.TempDir()
	got := StatePath(ws)
	want := filepath.Join(Dir(ws), stateFile)
	if got != want {
		t.Fatalf("StatePath = %q, want %q", got, want)
	}
}

// TestStateRoundtrip：状态记录可序列化/反序列化（回溯与清理用）。
func TestStateRoundtrip(t *testing.T) {
	state := State{
		InstalledAt: "2026-09-03T12:00:00+08:00",
		Tools:       map[string]string{"tsdown": "v0.22.14\n"},
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var back State
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.InstalledAt != state.InstalledAt || back.Tools["tsdown"] != state.Tools["tsdown"] {
		t.Fatalf("roundtrip 不一致: %+v", back)
	}
}

// TestEnsureReuse：已安装（tsdown 可执行存在）时复用，不重装。
func TestEnsureReuse(t *testing.T) {
	ws := t.TempDir()
	bin := filepath.Join(Dir(ws), "node_modules", ".bin", "tsdown")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := Ensure(ws)
	if err != nil {
		t.Fatal(err)
	}
	if dir != Dir(ws) {
		t.Fatalf("应复用工具链目录 %q，得到 %q", Dir(ws), dir)
	}
}
