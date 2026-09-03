package sea

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFixDeployWorkspace：pnpm deploy 生成的 workspace 配置被修正——
// allowBuilds 占位符改 true，补 minimumReleaseAge / autoInstallPeers /
// nodeLinker；已有字段不重复追加。
func TestFixDeployWorkspace(t *testing.T) {
	dir := t.TempDir()
	wsFile := filepath.Join(dir, "pnpm-workspace.yaml")
	raw := `packages:
  - .
onlyBuiltDependencies:
  - esbuild
  - sharp
allowBuilds:
  esbuild: set this to true or false
  sharp: set this to true or false
`
	if err := os.WriteFile(wsFile, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fixDeployWorkspace(wsFile); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(wsFile)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	for _, want := range []string{
		"esbuild: true",
		"sharp: true",
		"minimumReleaseAge: 0",
		"autoInstallPeers: true",
		"nodeLinker: hoisted",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("修正后应含 %q，得到:\n%s", want, s)
		}
	}
	// 幂等：二次修正不重复追加。
	if err := fixDeployWorkspace(wsFile); err != nil {
		t.Fatal(err)
	}
	got2, _ := os.ReadFile(wsFile)
	if strings.Count(string(got2), "minimumReleaseAge") != 1 {
		t.Errorf("二次修正不应重复追加字段:\n%s", got2)
	}
}

// TestFixDeployWorkspaceMissing：文件缺失时报错。
func TestFixDeployWorkspaceMissing(t *testing.T) {
	if err := fixDeployWorkspace(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("缺失文件应报错")
	}
}

// TestWriteBridge：dsh-bridge 伪包写入闭包（package.json + index.cjs）。
func TestWriteBridge(t *testing.T) {
	nmDir := t.TempDir()
	if err := writeBridge(nmDir); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(nmDir, bridgeName)
	for _, name := range []string{"package.json", "index.cjs"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s 缺失: %v", name, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"type": "commonjs"`) {
		t.Errorf("桥应为 CJS 模块: %s", raw)
	}
}

func TestCheckBareImports(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sea-entry.mjs")

	// 全 node: 导入：通过。
	good := `import { createRequire } from "node:module";
import { Command } from "node:os";
const x = await import("node:fs");
`
	if err := os.WriteFile(path, []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkBareImports(path); err != nil {
		t.Fatalf("全 node: 导入不应报错: %v", err)
	}

	// 含裸导入（闭包缺包时 bundler 保留）：报错并列出。
	bad := `import { Command, CommanderError } from "commander";
import "side-effect-pkg";
import { readFile } from "node:fs";
`
	if err := os.WriteFile(path, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	err := checkBareImports(path)
	if err == nil {
		t.Fatal("裸导入应报错")
	}
	for _, want := range []string{"commander", "side-effect-pkg"} {
		if !contains(err.Error(), want) {
			t.Errorf("报错应提及 %s，得到: %v", want, err)
		}
	}

	// 动态 import() 的裸 specifier 报错；CJS require() 走 createRequire
	// 外部解析（原生模块外置），不视为缺陷。
	dyn := `const m = await import("dynamic-pkg");
const c = require("cjs-pkg");
const ok = await import("node:fs");
`
	if err := os.WriteFile(path, []byte(dyn), 0o644); err != nil {
		t.Fatal(err)
	}
	err = checkBareImports(path)
	if err == nil {
		t.Fatal("动态 import 裸导入应报错")
	}
	if !contains(err.Error(), "dynamic-pkg") {
		t.Errorf("报错应提及 dynamic-pkg，得到: %v", err)
	}
	if contains(err.Error(), "cjs-pkg") {
		t.Errorf("require() 不应报错，得到: %v", err)
	}

	// 重复裸导入去重。
	dup := `import { a } from "foo";
export { b } from "foo";
`
	if err := os.WriteFile(path, []byte(dup), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkBareImports(path); err == nil {
		t.Fatal("裸导入应报错")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
