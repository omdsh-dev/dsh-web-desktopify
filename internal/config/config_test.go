package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeManifest 在 dir 写入 package.json 并返回路径。
func writeManifest(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLoadDefaults：缺省字段（version / dshHome / window / id）按约定补齐。
func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `{
		"name": "My App",
		"dsh": {"profile": {"bundles": ["@deepseek-ai/dsh-base"]}}
	}`)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != "0.0.1" {
		t.Errorf("version 缺省应为 0.0.1，得到 %q", cfg.Version)
	}
	if cfg.Desktop.DSHHome != "xdg" {
		t.Errorf("dshHome 缺省应为 xdg，得到 %q", cfg.Desktop.DSHHome)
	}
	if cfg.Desktop.Window.Width != 1280 || cfg.Desktop.Window.Height != 800 {
		t.Errorf("window 缺省应为 1280x800，得到 %dx%d", cfg.Desktop.Window.Width, cfg.Desktop.Window.Height)
	}
	if cfg.Desktop.Window.MinWidth != 800 || cfg.Desktop.Window.MinHeight != 600 {
		t.Errorf("min window 缺省应为 800x600，得到 %dx%d", cfg.Desktop.Window.MinWidth, cfg.Desktop.Window.MinHeight)
	}
	// id 由 name 派生：大写转小写、非法字符转 -。
	if cfg.Desktop.ID != "ai.deepseek.my-app" {
		t.Errorf("id 应由 name 派生，得到 %q", cfg.Desktop.ID)
	}
}

// TestLoadExplicit：显式字段原样保留。
func TestLoadExplicit(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `{
		"name": "dsh",
		"version": "0.1.0",
		"files": ["dist", "icon.svg"],
		"dependencies": {"@deepseek-ai/dsh": "0.1.2-rc.1"},
		"dsh": {
			"profile": {"bundles": ["@deepseek-ai/dsh-base", "@deepseek-ai/dsh-web-app"]},
			"desktop": {
				"id": "ai.deepseek.dsh",
				"dshHome": "env",
				"window": {"width": 1000, "height": 700, "minWidth": 600, "minHeight": 400}
			}
		}
	}`)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "dsh" || cfg.Version != "0.1.0" {
		t.Errorf("name/version 解析错误: %+v", cfg)
	}
	if len(cfg.Bundles) != 2 || cfg.Bundles[1] != "@deepseek-ai/dsh-web-app" {
		t.Errorf("bundles 解析错误: %v", cfg.Bundles)
	}
	if len(cfg.Files) != 2 || cfg.Files[0] != "dist" {
		t.Errorf("files 解析错误: %v", cfg.Files)
	}
	if cfg.Dependencies["@deepseek-ai/dsh"] != "0.1.2-rc.1" {
		t.Errorf("dependencies 解析错误: %v", cfg.Dependencies)
	}
	if cfg.Desktop.ID != "ai.deepseek.dsh" || cfg.Desktop.DSHHome != "env" {
		t.Errorf("desktop 解析错误: %+v", cfg.Desktop)
	}
	if cfg.Desktop.Window.Width != 1000 || cfg.Desktop.Window.MinHeight != 400 {
		t.Errorf("window 解析错误: %+v", cfg.Desktop.Window)
	}
}

// TestLoadErrors：缺失 / 非法 / 空 name / 空 bundles 均报错。
func TestLoadErrors(t *testing.T) {
	// 缺失 package.json。
	if _, err := Load(t.TempDir()); err == nil {
		t.Error("缺失 package.json 应报错")
	}
	// 非法 JSON。
	dir := t.TempDir()
	writeManifest(t, dir, `{not json`)
	if _, err := Load(dir); err == nil {
		t.Error("非法 JSON 应报错")
	}
	// 空 name。
	dir = t.TempDir()
	writeManifest(t, dir, `{"dsh": {"profile": {"bundles": ["x"]}}}`)
	if _, err := Load(dir); err == nil {
		t.Error("空 name 应报错")
	}
	// 空 bundles。
	dir = t.TempDir()
	writeManifest(t, dir, `{"name": "x"}`)
	if _, err := Load(dir); err == nil {
		t.Error("空 bundles 应报错")
	}
}

// TestWorkspaceRoot：独立工作区返回自身；monorepo 内嵌工作区返回根。
func TestWorkspaceRoot(t *testing.T) {
	// 独立工作区：无 pnpm-workspace.yaml → 自身。
	ws := t.TempDir()
	if got := WorkspaceRoot(ws); got != ws {
		t.Fatalf("独立工作区应返回自身，得到 %q", got)
	}
	// monorepo：根有 pnpm-workspace.yaml，内嵌工作区向上解析到根。
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pnpm-workspace.yaml"), []byte("packages:\n  - apps/*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(root, "apps", "dsh-custom")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := WorkspaceRoot(inner); got != root {
		t.Fatalf("内嵌工作区应解析到根 %q，得到 %q", root, got)
	}
}
