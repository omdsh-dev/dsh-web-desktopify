package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// newTestStep 构造一个可观测的测试步：run 在暂存目录写入产物并记录调用
// 次数；cache 命中检查 = cache/<id>/<dg>/ 目录在不在。
func newTestStep(t *testing.T, id string, deps ...*buildStep) (*buildStep, *int) {
	calls := 0
	s := &buildStep{
		id:    id,
		label: id,
		deps:  deps,
		input: func() (string, error) { return "input:" + id, nil },
		run: func(dst string) error {
			calls++
			return os.WriteFile(filepath.Join(dst, "out"), []byte(id), 0o644)
		},
	}
	return s, &calls
}

// runTestStep 在独立临时工作区执行一步（缓存目录即临时工作区
// node_modules/.dsh-web-desktopify/）。
func runTestStep(t *testing.T, ws string, s *buildStep, force bool) (string, bool, error) {
	t.Helper()
	return runStep(ws, s, force)
}

// TestStepContentAddressableReuse：digest 一致（cache 目录存在）时复用，
// 不再调用 run；digest 变化（cache 目录缺失）时重建。
func TestStepContentAddressableReuse(t *testing.T) {
	ws := t.TempDir()
	s, calls := newTestStep(t, "leaf")

	// 首轮：cache 无目录 → 构建 → 发布到 cache/<id>/<dg>/。
	out, reused, err := runTestStep(t, ws, s, false)
	if err != nil {
		t.Fatal(err)
	}
	if reused || *calls != 1 {
		t.Fatalf("首轮应构建一次（reused=%v, calls=%d）", reused, *calls)
	}
	cached := s.cachePath(ws)
	if out != cached {
		t.Fatalf("产物路径应为缓存目录 %s，得到 %s", cached, out)
	}
	if !dirExists(cached) {
		t.Fatalf("产物应发布到缓存 %s", cached)
	}
	if _, err := os.Stat(filepath.Join(cached, "out")); err != nil {
		t.Fatalf("缓存产物内容缺失: %v", err)
	}

	// 第二轮：digest 不变，cache 目录存在 → 复用，零调用。
	_, reused, err = runTestStep(t, ws, s, false)
	if err != nil {
		t.Fatal(err)
	}
	if !reused || *calls != 1 {
		t.Fatalf("第二轮应复用（reused=%v, calls=%d）", reused, *calls)
	}
}

// TestStepDependencyPropagation：依赖步重建后其 digest 变化，下游 digest
// 随之变化（cache 目录缺失），即使下游自身输入未变也必然重建。
func TestStepDependencyPropagation(t *testing.T) {
	ws := t.TempDir()
	dep, depCalls := newTestStep(t, "dep")
	leaf, leafCalls := newTestStep(t, "leaf", dep)

	if _, _, err := runTestStep(t, ws, dep, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runTestStep(t, ws, leaf, false); err != nil {
		t.Fatal(err)
	}
	leafDg1 := leaf.dg

	// 第二轮：digest 不变，全部复用。
	if _, reused, err := runTestStep(t, ws, dep, false); err != nil || !reused {
		t.Fatalf("dep 第二轮应复用: %v", err)
	}
	if _, reused, err := runTestStep(t, ws, leaf, false); err != nil || !reused {
		t.Fatalf("leaf 第二轮应复用: %v", err)
	}
	if *depCalls != 1 || *leafCalls != 1 {
		t.Fatalf("第二轮应零调用（dep=%d, leaf=%d）", *depCalls, *leafCalls)
	}

	// 第三轮：dep 输入变化 → dep 重建 → leaf digest 变化 → leaf 重建。
	dep.input = func() (string, error) { return "input:dep-v2", nil }
	if _, reused, err := runTestStep(t, ws, dep, false); err != nil || reused {
		t.Fatalf("输入变化 dep 应重建: %v", err)
	}
	// leaf digest 在 runStep 内重算（依赖 dep 的新 digest），必然变化。
	if _, reused, err := runTestStep(t, ws, leaf, false); err != nil || reused {
		t.Fatalf("依赖变化 leaf 应重建: %v", err)
	}
	if leaf.dg == leafDg1 {
		t.Fatal("依赖重建后 leaf digest 应变化")
	}
	if *depCalls != 2 || *leafCalls != 2 {
		t.Fatalf("第三轮应各重建一次（dep=%d, leaf=%d）", *depCalls, *leafCalls)
	}
}

// TestStepForceRebuilds：--force 忽略 cache 目录，每次重建并覆盖发布。
func TestStepForceRebuilds(t *testing.T) {
	ws := t.TempDir()
	s, calls := newTestStep(t, "leaf")

	if _, _, err := runTestStep(t, ws, s, false); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, reused, err := runTestStep(t, ws, s, true); err != nil || reused {
			t.Fatalf("--force 应重建: %v", err)
		}
	}
	if *calls != 3 {
		t.Fatalf("--force 应每次调用 run，调用 %d 次", *calls)
	}
}

// TestStepFailedBuildLeavesNoCache：构建失败时暂存目录被清理，cache 不
// 留半成品。
func TestStepFailedBuildLeavesNoCache(t *testing.T) {
	ws := t.TempDir()
	calls := 0
	s := &buildStep{
		id:    "fail",
		label: "fail",
		input: func() (string, error) { return "in", nil },
		run: func(dst string) error {
			calls++
			return os.ErrPermission // 模拟构建失败
		},
	}
	if _, _, err := runTestStep(t, ws, s, false); err == nil {
		t.Fatal("构建失败应返回错误")
	}
	if calls != 1 {
		t.Fatalf("应调用 run 一次，调用 %d 次", calls)
	}
	entries, err := os.ReadDir(filepath.Join(ws, "node_modules", ".dsh-web-desktopify", "build"))
	if err != nil {
		t.Fatalf("build 目录应存在: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("失败后暂存目录应清理，剩余 %v", entries)
	}
	if dirExists(filepath.Join(ws, "node_modules", ".dsh-web-desktopify", "cache", "fail")) {
		t.Fatal("失败后不应发布缓存")
	}
}

// TestFilesFingerprint：缺失文件跳过；内容变化影响指纹。
func TestFilesFingerprint(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	h1, err := filesFingerprint(dir, []string{"a", "missing"})
	if err != nil {
		t.Fatal(err)
	}
	h2, err := filesFingerprint(dir, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatal("缺失文件不应影响指纹")
	}
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	h3, err := filesFingerprint(dir, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if h3 == h1 {
		t.Fatal("内容变化指纹应变化")
	}
}
