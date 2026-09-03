package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omdsh-dev/dsh-web-desktopify/internal/config"
)

func TestWithEnv(t *testing.T) {
	env := []string{"HOME=/home/u", "DSH_HOME=/old", "PATH=/usr/bin"}
	got := withEnv(env, "DSH_HOME", "/new")
	if len(got) != 3 {
		t.Fatalf("长度应为 3，得到 %v", got)
	}
	if got[0] != "HOME=/home/u" || got[1] != "PATH=/usr/bin" || got[2] != "DSH_HOME=/new" {
		t.Fatalf("DSH_HOME 应被替换并追加在末尾，得到 %v", got)
	}
	// 不存在时追加。
	got = withEnv(env, "NEW_KEY", "v")
	if len(got) != 4 || got[3] != "NEW_KEY=v" {
		t.Fatalf("应追加新键，得到 %v", got)
	}
}

func TestPrependPath(t *testing.T) {
	env := []string{"HOME=/home/u", "PATH=/usr/bin:/bin", "DSH_HOME=/old"}
	got := prependPath(env, "/opt/pnpm")
	var path string
	for _, e := range got {
		if strings.HasPrefix(e, "PATH=") {
			path = e
		}
	}
	if path != "PATH=/opt/pnpm"+string(os.PathListSeparator)+"/usr/bin:/bin" {
		t.Fatalf("PATH 应前置 /opt/pnpm 且只保留一个条目，得到 %q", path)
	}
	if len(got) != 3 {
		t.Fatalf("其他条目应保留，得到 %v", got)
	}
}

// TestDevProfileLinked：profile 未初始化时返回 false；依赖全部以 link:
// 形式装好时返回 true；缺依赖或非 link 时返回 false。
func TestDevProfileLinked(t *testing.T) {
	cfg := &config.Config{
		Dependencies: map[string]string{
			"@morlay/better-session": "^0.0.12",
			"@deepseek-ai/dsh":        "0.1.2-rc.1",
		},
	}
	dir := t.TempDir()

	// 未初始化（package.json 缺失）→ false。
	if linked, err := devProfileLinked(dir, cfg); err != nil || linked {
		t.Fatalf("未初始化应返回 false（%v, %v）", linked, err)
	}

	// 全部 link: → true。
	manifest := `{"dependencies": {
		"@morlay/better-session": "link:/ws",
		"@deepseek-ai/dsh": "link:/ws"
	}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if linked, err := devProfileLinked(dir, cfg); err != nil || !linked {
		t.Fatalf("全部 link 应返回 true（%v, %v）", linked, err)
	}

	// 缺一个依赖 → false。
	manifest = `{"dependencies": {"@morlay/better-session": "link:/ws"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if linked, err := devProfileLinked(dir, cfg); err != nil || linked {
		t.Fatalf("缺依赖应返回 false（%v, %v）", linked, err)
	}

	// 非 link spec → false。
	manifest = `{"dependencies": {
		"@morlay/better-session": "^0.0.12",
		"@deepseek-ai/dsh": "link:/ws"
	}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if linked, err := devProfileLinked(dir, cfg); err != nil || linked {
		t.Fatalf("非 link spec 应返回 false（%v, %v）", linked, err)
	}
}
