package fsutil

import (
	"os"
	"strings"
	"testing"
)

func TestWithEnv(t *testing.T) {
	env := []string{"HOME=/home/u", "DSH_HOME=/old", "PATH=/usr/bin"}
	got := WithEnv(env, "DSH_HOME", "/new")
	if len(got) != 3 {
		t.Fatalf("长度应为 3，得到 %v", got)
	}
	if got[0] != "HOME=/home/u" || got[1] != "PATH=/usr/bin" || got[2] != "DSH_HOME=/new" {
		t.Fatalf("DSH_HOME 应被替换并追加在末尾，得到 %v", got)
	}
	// 不存在时追加。
	got = WithEnv(env, "NEW_KEY", "v")
	if len(got) != 4 || got[3] != "NEW_KEY=v" {
		t.Fatalf("应追加新键，得到 %v", got)
	}
	// 多对 kvs 一次覆盖。
	got = WithEnv(env, "DSH_HOME", "/new", "GOFLAGS", "-mod=mod")
	if len(got) != 4 || got[2] != "DSH_HOME=/new" || got[3] != "GOFLAGS=-mod=mod" {
		t.Fatalf("多对 kvs 应全部生效，得到 %v", got)
	}
}

func TestPrependPath(t *testing.T) {
	env := []string{"HOME=/home/u", "PATH=/usr/bin:/bin", "DSH_HOME=/old"}
	got := PrependPath(env, "/opt/pnpm")
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
	// 无 PATH 时新建。
	got = PrependPath([]string{"HOME=/home/u"}, "/opt/pnpm")
	if len(got) != 2 || got[1] != "PATH=/opt/pnpm"+string(os.PathListSeparator) {
		t.Fatalf("无 PATH 时应新建，得到 %v", got)
	}
}
