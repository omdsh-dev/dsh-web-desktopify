package bundle

import "testing"

// TestSeedAllow：DSH_HOME 种子白名单 = package.json + node_modules +
// package.json 的 files 字段；目录命中含整棵子树；非法条目忽略。
func TestSeedAllow(t *testing.T) {
	allow := SeedAllow([]string{
		"dist",
		"icon.svg",
		"cordis.patch.yml",
	})

	cases := []struct {
		rel   string
		isDir bool
		want  bool
	}{
		// 恒在白名单。
		{"package.json", false, true},
		{"node_modules", true, true},
		{"node_modules/@deepseek-ai/dsh", true, true},
		// files 精确文件。
		{"icon.svg", false, true},
		{"cordis.patch.yml", false, true},
		// files 目录 → 目录本身与子树都保留。
		{"dist", true, true},
		{"dist/index.js", false, true},
		{"dist/chunks/x.js", false, true},
		// 白名单外一律跳过。
		{"target", true, false},
		{"target/dsh-custom/app", false, false},
		{".dsh-store", true, false},
		{".dsh-store/x", false, false},
		{"pnpm-lock.yaml", false, false},
		{"pnpm-workspace.yaml", false, false},
		{".git", true, false},
		{"settings.yaml", false, false},
		{"src/index.ts", false, false},
	}
	for _, c := range cases {
		if got := allow(c.rel, c.isDir); got != c.want {
			t.Errorf("allow(%q, dir=%v) = %v, want %v", c.rel, c.isDir, got, c.want)
		}
	}
}

// TestSeedAllowInvalid：越界 / 绝对路径 / 空条目不进白名单。
func TestSeedAllowInvalid(t *testing.T) {
	allow := SeedAllow([]string{
		"../escape",
		"/abs/path",
		".",
		"",
	})
	for _, rel := range []string{"escape", "abs/path"} {
		if allow(rel, false) {
			t.Errorf("非法条目 %q 不应命中白名单", rel)
		}
	}
	// 合法条目不受影响。
	if !allow("package.json", false) {
		t.Error("package.json 应恒在白名单")
	}
}

// TestSeedAllowNoFiles：无 files 字段时白名单只剩 package.json、
// node_modules 与 cordis.patch.yml（profile patch 层恒必需）。
func TestSeedAllowNoFiles(t *testing.T) {
	allow := SeedAllow(nil)
	for _, rel := range []string{"package.json", "node_modules", "cordis.patch.yml"} {
		if !allow(rel, false) {
			t.Errorf("无 files 时 %q 应恒在白名单", rel)
		}
	}
	for _, rel := range []string{"icon.svg", "dist", "justfile", "target"} {
		if allow(rel, false) {
			t.Errorf("无 files 时 %q 不应在白名单", rel)
		}
	}
}
