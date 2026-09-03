package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFindUp：从 start 向上查找包含 name 的目录；找不到返回空串。
func TestFindUp(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pnpm-workspace.yaml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(root, "apps", "dsh-custom")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FindUp(inner, "pnpm-workspace.yaml"); got != root {
		t.Fatalf("应向上解析到 %q，得到 %q", root, got)
	}
	// 自身命中。
	if got := FindUp(root, "pnpm-workspace.yaml"); got != root {
		t.Fatalf("自身命中应返回自身，得到 %q", got)
	}
	// 找不到：空串。
	if got := FindUp(t.TempDir(), "no-such-file"); got != "" {
		t.Fatalf("找不到应返回空串，得到 %q", got)
	}
}

// TestCopyDirDerefSkipsSelf：复制到源内部的目标时，目标自身的子树必须被
// 跳过（避免递归自复制），不依赖任何目录名约定。
func TestCopyDirDerefSkipsSelf(t *testing.T) {
	src := t.TempDir()
	// 源里放一个产物目录（模拟 bundle 已生成的 target/）。
	artifact := filepath.Join(src, "target", "dsh-coding", "dsh-coding.app")
	if err := os.MkdirAll(artifact, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifact, "dsh-shell"), []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 源里一个普通文件。
	if err := os.WriteFile(filepath.Join(src, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 目标在源内部（bundle 的 dsh-home 种子场景）。
	dst := filepath.Join(src, "target", "dsh-coding", "dsh-home", "profiles", "web")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CopyDirDeref(src, dst, nil, nil); err != nil {
		t.Fatal(err)
	}

	// 目标里不应出现嵌套的 target 或 dsh-home（自复制被跳过）。
	bad := filepath.Join(dst, "target")
	if _, err := os.Stat(bad); err == nil {
		t.Fatalf("目标不应包含自身子树 %s（递归自复制）", bad)
	}
	// package.json 应正常复制。
	if _, err := os.Stat(filepath.Join(dst, "package.json")); err != nil {
		t.Fatalf("package.json 应被复制: %v", err)
	}
}

// TestCopyDirDerefIgnores：ignored 回调（如 .gitignore）生效。
func TestCopyDirDerefIgnores(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "keep.txt"), []byte("k"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "drop.txt"), []byte("d"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "ignored-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "ignored-dir", "x.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	ignored := func(rel string, isDir bool) bool {
		return rel == "drop.txt" || rel == "ignored-dir"
	}
	dst := t.TempDir()
	if err := CopyDirDeref(src, dst, nil, ignored); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "keep.txt")); err != nil {
		t.Error("keep.txt 应被复制")
	}
	if _, err := os.Stat(filepath.Join(dst, "drop.txt")); err == nil {
		t.Error("drop.txt 不应被复制（ignored）")
	}
	if _, err := os.Stat(filepath.Join(dst, "ignored-dir")); err == nil {
		t.Error("ignored-dir 不应被复制（ignored）")
	}
}

// TestCopyDirDerefBreaksSymlinkCycle：peer 互相链接（cordis ↔
// cordis-plugin-include 之类）在 node_modules 里形成的 symlink 环必须被
// 打破，解引用复制不能无限递归。
func TestCopyDirDerefBreaksSymlinkCycle(t *testing.T) {
	src := t.TempDir()
	// a/pkg 与 b/pkg 互相链接：a/pkg/node_modules/b → b/pkg，
	// b/pkg/node_modules/a → a/pkg（相对链接，模拟 pnpm 嵌套 peer 安装）。
	a := filepath.Join(src, "a", "pkg")
	b := filepath.Join(src, "b", "pkg")
	if err := os.MkdirAll(filepath.Join(a, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(b, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a, "package.json"), []byte(`{"name":"a"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, "package.json"), []byte(`{"name":"b"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	relA, err := filepath.Rel(filepath.Join(b, "node_modules"), a)
	if err != nil {
		t.Fatal(err)
	}
	relB, err := filepath.Rel(filepath.Join(a, "node_modules"), b)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(relA, filepath.Join(b, "node_modules", "a")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(relB, filepath.Join(a, "node_modules", "b")); err != nil {
		t.Fatal(err)
	}

	// 外层 node_modules 引用 a（解引用后进入环）。
	nm := filepath.Join(src, "node_modules")
	if err := os.MkdirAll(nm, 0o755); err != nil {
		t.Fatal(err)
	}
	relPkgA, err := filepath.Rel(nm, a)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(relPkgA, filepath.Join(nm, "a")); err != nil {
		t.Fatal(err)
	}

	dst := t.TempDir()
	if err := CopyDirDeref(nm, dst, nil, nil); err != nil {
		t.Fatalf("解引用复制遇到 symlink 环应终止而非递归: %v", err)
	}
	// a 的内容应落盘（环只跳过回环部分，不吞掉实体）。
	if _, err := os.Stat(filepath.Join(dst, "a", "package.json")); err != nil {
		t.Errorf("a/package.json 应被复制: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "a", "node_modules", "b", "package.json")); err != nil {
		t.Errorf("环内 b/package.json 应被复制（首次进入）: %v", err)
	}
}
