package bundle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/omdsh-dev/dsh-web-desktopify/internal/config"
	"github.com/omdsh-dev/dsh-web-desktopify/internal/sea"
	"github.com/omdsh-dev/dsh-web-desktopify/pkg/shell/appconfig"
)

// testConfig 构造一份完整的工作区配置（窗口用缺省值，字段与
// config.Load 的缺省一致）。
func testConfig() *config.Config {
	return &config.Config{
		Name:    "dsh-test",
		Version: "0.1.0",
		Bundles: []string{"@deepseek-ai/dsh-base"},
		Desktop: config.Desktop{
			ID:      "ai.deepseek.dsh-test",
			DSHHome: "xdg",
			Window:  config.Window{Width: 1280, Height: 800, MinWidth: 800, MinHeight: 600},
		},
	}
}

// loadAppConfig 用壳侧 appconfig.Load 解析（契约验证）。
func loadAppConfig(t *testing.T, binDir string) appconfig.Config {
	t.Helper()
	return appconfig.Load(binDir)
}

// TestSeedIgnored：node_modules 恒忽略；白名单外忽略；白名单内保留。
func TestSeedIgnored(t *testing.T) {
	ignored := SeedIgnored([]string{"dist", "icon.svg"})
	cases := []struct {
		rel   string
		isDir bool
		want  bool
	}{
		{"node_modules", true, true},
		{"node_modules/@deepseek-ai/dsh", true, true},
		{"package.json", false, false},
		{"cordis.patch.yml", false, false},
		{"dist", true, false},
		{"dist/index.js", false, false},
		{"icon.svg", false, false},
		{"target", true, true},
		{".dsh-store", true, true},
		{"pnpm-lock.yaml", false, true},
	}
	for _, c := range cases {
		if got := ignored(c.rel, c.isDir); got != c.want {
			t.Errorf("SeedIgnored(%q, dir=%v) = %v, want %v", c.rel, c.isDir, got, c.want)
		}
	}
}

// TestAssembleLayout：公共布局装配（bin/ + appconfig + 资源 + 种子）。
func TestAssembleLayout(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "package.json"), []byte(`{"name":"dsh-test","version":"0.1.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "cordis.patch.yml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 假 SEA 产物与壳二进制。
	seaDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(seaDir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seaDir, "bin", "dsh"), []byte("sea"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(seaDir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seaDir, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	shellBin := filepath.Join(t.TempDir(), "dsh-shell")
	if err := os.WriteFile(shellBin, []byte("shell"), 0o755); err != nil {
		t.Fatal(err)
	}

	appRoot := t.TempDir()
	binDir, err := assembleLayout(Inputs{
		Workspace: ws,
		Cfg:       testConfig(),
		Sea:       sea.NewOutput(seaDir),
		ShellBin:  shellBin,
		SeedHash:  "h1",
	}, appRoot)
	if err != nil {
		t.Fatal(err)
	}
	shellName, serverName := BinNames()
	for _, name := range []string{shellName, serverName, "appconfig.json"} {
		if _, err := os.Stat(filepath.Join(binDir, name)); err != nil {
			t.Errorf("bin/%s 缺失: %v", name, err)
		}
	}
	// 种子：白名单内容复制 + .seed-hash 指纹。
	seedProfile := filepath.Join(appRoot, "dsh-home", "profiles", "web")
	if _, err := os.Stat(filepath.Join(seedProfile, "package.json")); err != nil {
		t.Errorf("种子 package.json 缺失: %v", err)
	}
	if _, err := os.Stat(filepath.Join(seedProfile, "cordis.patch.yml")); err != nil {
		t.Errorf("种子 cordis.patch.yml 缺失: %v", err)
	}
	hash, err := os.ReadFile(filepath.Join(seedProfile, ".seed-hash"))
	if err != nil || string(hash) != "h1" {
		t.Errorf(".seed-hash 应为 h1（%q, %v）", hash, err)
	}
	// 种子不带 node_modules（运行时从安装闭包解析）。
	if _, err := os.Stat(filepath.Join(seedProfile, "node_modules")); !os.IsNotExist(err) {
		t.Error("种子不应包含 node_modules")
	}
}
