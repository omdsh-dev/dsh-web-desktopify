package profile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/omdsh-dev/dsh-web-desktopify/internal/config"
)

// TestTemplatePackageJSONLoads：模板 package.json（dev 在非工作区目录
// 兜底创建时生成）必须满足 config.Load 的校验（name 与 bundles 非空）。
func TestTemplatePackageJSONLoads(t *testing.T) {
	data, err := templates.ReadFile("templates/package.json")
	if err != nil {
		t.Fatalf("模板 package.json 缺失: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("模板 package.json 应通过 config.Load: %v", err)
	}
	if cfg.Name == "" || len(cfg.Bundles) == 0 {
		t.Fatalf("模板应声明 name 与 bundles: %+v", cfg)
	}
}

// makePkg 在 nmDir 下创建包目录（支持 @scope/name）与 package.json。
func makePkg(t *testing.T, nmDir, name, version string) {
	t.Helper()
	dir := filepath.Join(nmDir, filepath.FromSlash(name))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"name":"` + name + `","version":"` + version + `"}`)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDshClosureDir：向上查找第一个 node_modules 里有 dsh 主包的目录；
// monorepo 内嵌工作区时 hoisted 到根；找不到返回空串。
func TestDshClosureDir(t *testing.T) {
	root := t.TempDir()
	nm := filepath.Join(root, "node_modules", "@deepseek-ai", "dsh")
	if err := os.MkdirAll(nm, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(root, "apps", "dsh-custom")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := DshClosureDir(inner); got != root {
		t.Fatalf("应向上解析到 %q，得到 %q", root, got)
	}
	if got := DshClosureDir(root); got != root {
		t.Fatalf("自身命中应返回自身，得到 %q", got)
	}
	if got := DshClosureDir(t.TempDir()); got != "" {
		t.Fatalf("找不到应返回空串，得到 %q", got)
	}
}

// TestInstalled：闭包已安装（含向上）为 true，否则 false。
func TestInstalled(t *testing.T) {
	root := t.TempDir()
	nm := filepath.Join(root, "node_modules", "@deepseek-ai", "dsh")
	if err := os.MkdirAll(nm, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(root, "apps", "dsh-custom")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	if !Installed(inner) {
		t.Fatal("内嵌工作区应视为已安装（hoisted 到根）")
	}
	if Installed(t.TempDir()) {
		t.Fatal("无闭包应视为未安装")
	}
}

func TestClosureFingerprint(t *testing.T) {
	dir := t.TempDir()
	nmDir := filepath.Join(dir, "node_modules")
	if err := os.MkdirAll(nmDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 顶层包与 scoped 包、簿记条目混合。
	makePkg(t, nmDir, "commander", "15.0.0")
	makePkg(t, nmDir, "@deepseek-ai/dsh", "0.1.0-rc.6")
	makePkg(t, nmDir, "@deepseek-ai/dsh-base", "0.1.0-rc.6")
	if err := os.MkdirAll(filepath.Join(nmDir, ".bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nmDir, ".modules.yaml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	f1, err := ClosureFingerprint(dir)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	if len(f1) != 64 {
		t.Fatalf("指纹长度异常: %q", f1)
	}

	// 稳定：相同闭包再次计算一致。
	f2, err := ClosureFingerprint(dir)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	if f1 != f2 {
		t.Fatalf("相同闭包指纹不稳定: %s vs %s", f1, f2)
	}

	// 敏感：新增包使指纹变化（模拟 pnpm install 补齐闭包）。
	makePkg(t, nmDir, "ms", "2.1.3")
	f3, err := ClosureFingerprint(dir)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	if f1 == f3 {
		t.Fatalf("闭包变化但指纹未变: %s", f1)
	}

	// 敏感：升级版本使指纹变化。
	makePkg(t, nmDir, "commander", "16.0.0")
	f4, err := ClosureFingerprint(dir)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	if f3 == f4 {
		t.Fatalf("版本升级但指纹未变: %s", f3)
	}

	// node_modules 缺失：空指纹不报错。
	empty := filepath.Join(t.TempDir(), "no-nm")
	f5, err := ClosureFingerprint(empty)
	if err != nil || f5 != "" {
		t.Fatalf("缺失闭包应返回空指纹，得到 %q, %v", f5, err)
	}
}
