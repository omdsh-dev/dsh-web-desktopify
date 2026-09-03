package appconfig

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDefault：缺省配置为 1280x800、profile web、DSH_HOME xdg。
func TestDefault(t *testing.T) {
	c := Default()
	if c.Name != "dsh-desktop" || c.Profile != "web" || c.DSHHome != "xdg" {
		t.Fatalf("缺省配置错误: %+v", c)
	}
	if c.Window.Width != 1280 || c.Window.Height != 800 || c.Window.MinWidth != 800 || c.Window.MinHeight != 600 {
		t.Fatalf("缺省窗口几何错误: %+v", c.Window)
	}
}

// TestLoadMissing：appconfig.json 缺失时回退默认值。
func TestLoadMissing(t *testing.T) {
	cfg := Load(t.TempDir())
	if cfg.Name != "dsh-desktop" || cfg.Profile != "web" {
		t.Fatalf("缺失时应回退默认值: %+v", cfg)
	}
}

// TestLoadCorrupt：appconfig.json 非法时回退默认值（不崩溃）。
func TestLoadCorrupt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "appconfig.json"), []byte("{oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Load(dir)
	if cfg.Name != "dsh-desktop" {
		t.Fatalf("非法配置应回退默认值: %+v", cfg)
	}
}

// TestLoadPartial：部分字段缺失时按默认值补齐。
func TestLoadPartial(t *testing.T) {
	dir := t.TempDir()
	raw := `{"name": "dsh", "id": "ai.deepseek.dsh", "version": "0.1.0"}`
	if err := os.WriteFile(filepath.Join(dir, "appconfig.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Load(dir)
	if cfg.Name != "dsh" || cfg.ID != "ai.deepseek.dsh" || cfg.Version != "0.1.0" {
		t.Fatalf("显式字段应保留: %+v", cfg)
	}
	if cfg.Profile != "web" || cfg.DSHHome != "xdg" {
		t.Fatalf("缺失字段应补齐: %+v", cfg)
	}
	if cfg.Window.Width != 1280 || cfg.Window.Height != 800 {
		t.Fatalf("缺失窗口应补齐: %+v", cfg.Window)
	}
}

// TestLoadFull：完整配置原样解析。
func TestLoadFull(t *testing.T) {
	dir := t.TempDir()
	raw := `{
		"name": "dsh",
		"id": "ai.deepseek.dsh",
		"version": "0.1.0",
		"window": {"width": 1000, "height": 700, "minWidth": 600, "minHeight": 400},
		"profile": "web",
		"dshHome": "env"
	}`
	if err := os.WriteFile(filepath.Join(dir, "appconfig.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Load(dir)
	if cfg.Window.Width != 1000 || cfg.Window.MinHeight != 400 || cfg.DSHHome != "env" {
		t.Fatalf("完整配置解析错误: %+v", cfg)
	}
}
