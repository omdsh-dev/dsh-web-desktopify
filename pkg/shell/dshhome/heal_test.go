package dshhome

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// 构造一个迷你 app 闭包：顶层 node_modules 放一个 bundle 包
// @scope/bundle（依赖 @scope/plugin-a），.pnpm/node_modules 放插件实体
// @scope/plugin-a（依赖 peer @scope/plugin-b），再一个插件 @scope/plugin-b。
func makeClosure(t *testing.T) string {
	t.Helper()
	closure := t.TempDir()
	nm := filepath.Join(closure, "node_modules")
	if err := os.MkdirAll(filepath.Join(nm, "@scope"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 顶层 bundle（symlink 指向 .pnpm 实体，模拟 pnpm 布局）。
	store := filepath.Join(closure, "node_modules", ".pnpm", "node_modules")
	writePkg(t, filepath.Join(store, "@scope", "bundle"), map[string]any{
		"name":         "@scope/bundle",
		"version":      "1.0.0",
		"dependencies": map[string]string{"@scope/plugin-a": "^1.0.0"},
	})
	writePkg(t, filepath.Join(store, "@scope", "plugin-a"), map[string]any{
		"name":             "@scope/plugin-a",
		"version":          "1.0.0",
		"peerDependencies": map[string]string{"@scope/plugin-b": "^1.0.0"},
	})
	writePkg(t, filepath.Join(store, "@scope", "plugin-b"), map[string]any{
		"name":    "@scope/plugin-b",
		"version": "1.0.0",
	})
	if err := os.Symlink(filepath.Join(store, "@scope", "bundle"), filepath.Join(nm, "@scope", "bundle")); err != nil {
		t.Fatal(err)
	}
	return closure
}
func writePkg(t *testing.T, dir string, manifest map[string]any) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// makeHome 构造 DSH_HOME：profiles/web/package.json 声明 bundle。
func makeHome(t *testing.T, bundles []string) string {
	t.Helper()
	home := t.TempDir()
	profileDir := filepath.Join(home, "profiles", "web")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"name": "dsh-test",
		"dsh": map[string]any{
			"profile": map[string]any{
				"bundles": bundles,
			},
		},
	}
	writePkg(t, profileDir, manifest)
	return home
}

func TestHealBundleFallback(t *testing.T) {
	closure := makeClosure(t)
	home := makeHome(t, []string{"@scope/bundle"})
	exeDir := filepath.Join(closure, "MacOS")

	if err := HealBundleFallback(home, "web", exeDir); err != nil {
		t.Fatal(err)
	}

	fallback := filepath.Join(home, "profiles", "node_modules")
	// bundle 顶层链接。
	link := filepath.Join(fallback, "@scope", "bundle")
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("bundle 应被链接: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("bundle 应为符号链接")
	}
	// 插件实体也链接了（真实目录可解析）。
	if _, err := os.Stat(filepath.Join(fallback, "@scope", "plugin-a", "package.json")); err != nil {
		t.Fatalf("plugin-a 应可解析: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fallback, "@scope", "plugin-b", "package.json")); err != nil {
		t.Fatalf("plugin-b（peer 依赖）应可解析: %v", err)
	}
}

// 幂等：重复调用不报错、链接目标不变。
func TestHealBundleFallbackIdempotent(t *testing.T) {
	closure := makeClosure(t)
	home := makeHome(t, []string{"@scope/bundle"})
	exeDir := filepath.Join(closure, "MacOS")

	for i := 0; i < 2; i++ {
		if err := HealBundleFallback(home, "web", exeDir); err != nil {
			t.Fatalf("第 %d 次应成功: %v", i+1, err)
		}
	}
	link := filepath.Join(home, "profiles", "node_modules", "@scope", "bundle")
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(closure, "node_modules", "@scope", "bundle"))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("链接目标应不变（%q vs %q）", got, want)
	}
}

// bundle 缺失时静默跳过（不阻断启动，后端会报明确错误）。
func TestHealBundleFallbackMissingBundle(t *testing.T) {
	home := makeHome(t, []string{"@scope/not-installed"})
	closure := t.TempDir()
	if err := os.MkdirAll(filepath.Join(closure, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := HealBundleFallback(home, "web", filepath.Join(closure, "MacOS")); err != nil {
		t.Fatalf("缺失 bundle 应跳过而非报错: %v", err)
	}
}

// 无 bundles 声明：空操作。
func TestHealBundleFallbackNoBundles(t *testing.T) {
	home := makeHome(t, nil)
	closure := makeClosure(t)
	if err := HealBundleFallback(home, "web", filepath.Join(filepath.Dir(closure), "MacOS")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "profiles", "node_modules")); !os.IsNotExist(err) {
		t.Fatalf("无 bundle 不应创建 fallback: %v", err)
	}
}

// 空 DSH_HOME（env 策略）：不操作。
func TestHealBundleFallbackEmptyHome(t *testing.T) {
	if err := HealBundleFallback("", "web", "/tmp/exe"); err != nil {
		t.Fatal(err)
	}
}
