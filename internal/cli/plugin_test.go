package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// TestCleanupDevWorkspace：dsh 运行期间写入工作区的 pnpm-workspace.yaml
// 在退出时被删除（仅当启动时本来没有）；启动时已有则保留。
func TestCleanupDevWorkspace(t *testing.T) {
	ws := t.TempDir()

	// 启动时没有 → 退出时删除。
	cleanupDevWorkspace(ws, false)
	if _, err := os.Stat(filepath.Join(ws, "pnpm-workspace.yaml")); !os.IsNotExist(err) {
		t.Fatalf("应删除 dsh 写入的 pnpm-workspace.yaml（%v）", err)
	}

	// 启动时已有 → 退出时保留。
	if err := os.WriteFile(filepath.Join(ws, "pnpm-workspace.yaml"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	cleanupDevWorkspace(ws, true)
	raw, err := os.ReadFile(filepath.Join(ws, "pnpm-workspace.yaml"))
	if err != nil || string(raw) != "keep" {
		t.Fatalf("启动时已有的 pnpm-workspace.yaml 应保留（%q, %v）", raw, err)
	}
}
