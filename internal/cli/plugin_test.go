package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/omdsh-dev/dsh-web-desktopify/internal/config"
)

// TestDevProfileLinked：profile 未初始化时返回 false；依赖全部以 link:
// 形式装好时返回 true；缺依赖或非 link 时返回 false。
func TestDevProfileLinked(t *testing.T) {
	cfg := &config.Config{
		Dependencies: map[string]string{
			"@morlay/better-session": "^0.0.12",
			"@deepseek-ai/dsh":       "0.1.2-rc.1",
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
