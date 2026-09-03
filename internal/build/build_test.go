package build

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testOutput 是测试用的类型化产物。
type testOutput struct{ dir string }

func (o testOutput) Dir() string { return o.dir }

// testStep 构造一个可观测的测试步：run 在暂存目录写入产物并记录调用
// 次数；cache 命中检查 = cache/<digest>/ 目录在不在。
func testStep(id string, deps, needs []Step, run func(dst string) error) *step {
	return &step{id: id, deps: deps, needs: needs, run: run}
}

type step struct {
	id      string
	deps    []Step
	needs   []Step
	run     func(dst string) error
	fp      string // 自身指纹（缺省 "input:"+id）
	calls   int
	mu      chan struct{}
	started chan struct{}
}

func (s *step) ID() string    { return s.id }
func (s *step) Label() string { return s.id }
func (s *step) Deps() []Step  { return s.deps }
func (s *step) Needs() []Step { return s.needs }
func (s *step) Fingerprint() (string, error) {
	if s.fp != "" {
		return s.fp, nil
	}
	return "input:" + s.id, nil
}
func (s *step) Output(dir string) Output { return testOutput{dir: dir} }
func (s *step) Run(dst string, _ map[string]Output) error {
	if s.mu != nil {
		s.mu <- struct{}{}
	}
	if s.started != nil {
		close(s.started)
	}
	s.calls++
	if s.run != nil {
		return s.run(dst)
	}
	return os.WriteFile(filepath.Join(dst, "out"), []byte(s.id), 0o644)
}

// newTestGraph 在独立临时工作区构造 DAG，返回图与工作区（缓存目录在
// 工作区内，复用同一工作区才能验证缓存命中）。
func newTestGraph(t *testing.T, steps ...Step) (*Graph, string) {
	t.Helper()
	ws := t.TempDir()
	g, err := NewGraph(ws, steps...)
	if err != nil {
		t.Fatal(err)
	}
	return g, ws
}

// TestContentAddressableReuse：digest 一致（cache 目录存在）时复用，不再
// 调用 run；digest 变化（cache 目录缺失）时重建。
func TestContentAddressableReuse(t *testing.T) {
	s := testStep("leaf", nil, nil, nil)
	g, ws := newTestGraph(t, s)
	outputs, err := g.Run(false)
	if err != nil {
		t.Fatal(err)
	}
	if s.calls != 1 {
		t.Fatalf("首轮应构建一次，调用 %d 次", s.calls)
	}
	if _, err := os.Stat(filepath.Join(outputs["leaf"].Dir(), "out")); err != nil {
		t.Fatalf("缓存产物内容缺失: %v", err)
	}
	// 第二轮：digest 不变，cache 目录存在 → 复用，零调用。
	g2, err := NewGraph(ws, s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g2.Run(false); err != nil {
		t.Fatal(err)
	}
	if s.calls != 1 {
		t.Fatalf("第二轮应复用，调用 %d 次", s.calls)
	}
}

// TestDependencyPropagation：依赖步重建后其 digest 变化，下游 digest
// 随之变化（cache 目录缺失），即使下游自身输入未变也必然重建。
func TestDependencyPropagation(t *testing.T) {
	dep := testStep("dep", nil, nil, nil)
	leaf := testStep("leaf", []Step{dep}, []Step{dep}, nil)
	g, ws := newTestGraph(t, dep, leaf)
	if _, err := g.Run(false); err != nil {
		t.Fatal(err)
	}
	if dep.calls != 1 || leaf.calls != 1 {
		t.Fatalf("首轮应各构建一次（dep=%d, leaf=%d）", dep.calls, leaf.calls)
	}
	// 第二轮：digest 不变，全部复用。
	g2, err := NewGraph(ws, dep, leaf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g2.Run(false); err != nil {
		t.Fatal(err)
	}
	if dep.calls != 1 || leaf.calls != 1 {
		t.Fatalf("第二轮应零调用（dep=%d, leaf=%d）", dep.calls, leaf.calls)
	}
	// 第三轮：dep 输入变化 → dep 重建 → leaf digest 变化 → leaf 重建。
	dep.fp = "input:dep-v2"
	g3, err := NewGraph(ws, dep, leaf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g3.Run(false); err != nil {
		t.Fatal(err)
	}
	if dep.calls != 2 || leaf.calls != 2 {
		t.Fatalf("第三轮应各重建一次（dep=%d, leaf=%d）", dep.calls, leaf.calls)
	}
}

// TestForceRebuilds：--force 忽略 cache 目录，每次重建并覆盖发布。
func TestForceRebuilds(t *testing.T) {
	s := testStep("leaf", nil, nil, nil)
	g, _ := newTestGraph(t, s)
	if _, err := g.Run(false); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := g.Run(true); err != nil {
			t.Fatal(err)
		}
	}
	if s.calls != 3 {
		t.Fatalf("--force 应每次调用 run，调用 %d 次", s.calls)
	}
}

// TestFailedBuildLeavesNoCache：构建失败时暂存目录被清理，cache 不留
// 半成品。
func TestFailedBuildLeavesNoCache(t *testing.T) {
	s := testStep("fail", nil, nil, func(string) error { return os.ErrPermission })
	ws := t.TempDir()
	g, err := NewGraph(ws, s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Run(false); err == nil {
		t.Fatal("构建失败应返回错误")
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

// TestParallelExecution：仅指纹依赖（Needs 为空）的步与依赖步并行执行
// （依赖步未完成时已开始）；产物依赖（Needs 非空）的步等依赖完成。
func TestParallelExecution(t *testing.T) {
	dep := testStep("dep", nil, nil, nil)
	dep.mu = make(chan struct{}, 1) // 阻塞 dep 的 Run，直到测试放行
	dep.started = make(chan struct{})

	// leaf 仅指纹依赖 dep（Needs 为空）：dep 未完成时 leaf 已开始。
	leaf := testStep("leaf", []Step{dep}, nil, nil)
	leaf.started = make(chan struct{})

	g, _ := newTestGraph(t, dep, leaf)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = g.Run(false)
	}()
	// dep 开始后，leaf 应已开始（仅指纹依赖，无需等产物）。
	<-dep.started
	select {
	case <-leaf.started:
	case <-done:
		t.Fatal("leaf 未在 dep 完成前开始（应并行）")
	}
	// 放行 dep，全部完成。
	<-dep.mu
	<-done
	if leaf.calls != 1 {
		t.Fatalf("leaf 应构建一次，调用 %d 次", leaf.calls)
	}
}

// TestNeedsWaitsForDependency：产物依赖（Needs 非空）的步在依赖完成前
// 不开始。
func TestNeedsWaitsForDependency(t *testing.T) {
	dep := testStep("dep", nil, nil, nil)
	dep.mu = make(chan struct{}, 1) // 阻塞 dep 的 Run
	dep.started = make(chan struct{})

	leaf := testStep("leaf", []Step{dep}, []Step{dep}, nil)
	leaf.started = make(chan struct{})

	ws := t.TempDir()
	g, err := NewGraph(ws, dep, leaf)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = g.Run(false)
	}()
	// dep 开始后，leaf 不应开始（等产物依赖）。
	<-dep.started
	select {
	case <-leaf.started:
		t.Fatal("leaf 不应在 dep 完成前开始（产物依赖）")
	default:
	}
	// 放行 dep，leaf 随后开始并完成。
	<-dep.mu
	<-done
	if leaf.calls != 1 {
		t.Fatalf("leaf 应构建一次，调用 %d 次", leaf.calls)
	}
}

// TestDependencyFailureSkipsDownstream：依赖失败时下游跳过（不执行、
// 不发布），错误已上报。
func TestDependencyFailureSkipsDownstream(t *testing.T) {
	dep := testStep("fail", nil, nil, func(string) error { return os.ErrPermission })
	leaf := testStep("leaf", []Step{dep}, []Step{dep}, nil)
	ws := t.TempDir()
	g, err := NewGraph(ws, dep, leaf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Run(false); err == nil {
		t.Fatal("依赖失败应返回错误")
	}
	if leaf.calls != 0 {
		t.Fatalf("依赖失败时下游不应执行，调用 %d 次", leaf.calls)
	}
}

// TestNeedsMustBeSubsetOfDeps：产物依赖必须是指纹依赖的子集。
func TestNeedsMustBeSubsetOfDeps(t *testing.T) {
	a := testStep("a", nil, nil, nil)
	b := testStep("b", nil, nil, nil)
	bad := testStep("bad", []Step{a}, []Step{b}, nil)
	if _, err := NewGraph(t.TempDir(), a, b, bad); err == nil {
		t.Fatal("Needs 不在 Deps 中应报错")
	}
}

// TestDuplicateID：步 ID 重复报错。
func TestDuplicateID(t *testing.T) {
	s := testStep("dup", nil, nil, nil)
	if _, err := NewGraph(t.TempDir(), s, s); err == nil {
		t.Fatal("重复 ID 应报错")
	}
}

// TestCycle：指纹依赖成环报错。
func TestCycle(t *testing.T) {
	a := testStep("a", nil, nil, nil)
	b := testStep("b", nil, nil, nil)
	a.deps = []Step{b}
	b.deps = []Step{a}
	g, _ := newTestGraph(t, a, b)
	if _, err := g.Run(false); err == nil {
		t.Fatal("依赖环应报错")
	}
}

// TestWriteRecord：每次 Run 写状态记录（build/build-<ts>.json），内容含
// 各步 digest / 复用 / 产物路径；超出保留份数的旧记录被清理。
func TestWriteRecord(t *testing.T) {
	ws := t.TempDir()
	s := testStep("leaf", nil, nil, nil)
	g, err := NewGraph(ws, s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Run(false); err != nil {
		t.Fatal(err)
	}
	// 记录存在且内容正确。
	records := listRecords(t, ws)
	if len(records) != 1 {
		t.Fatalf("应有 1 份记录，得到 %d", len(records))
	}
	raw, err := os.ReadFile(filepath.Join(BuildDir(ws), records[0]))
	if err != nil {
		t.Fatal(err)
	}
	var rec Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Force || rec.Time == "" {
		t.Fatalf("记录头错误: %+v", rec)
	}
	step, ok := rec.Steps["leaf"]
	if !ok {
		t.Fatalf("记录应含 leaf 步: %+v", rec.Steps)
	}
	if step.Reused || step.Digest == "" || step.Output == "" {
		t.Fatalf("leaf 步记录错误: %+v", step)
	}
	if !dirExists(step.Output) {
		t.Fatalf("记录产物路径应存在: %s", step.Output)
	}

	// 第二轮（复用）：记录更新为 Reused=true。
	g2, err := NewGraph(ws, s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g2.Run(false); err != nil {
		t.Fatal(err)
	}
	records = listRecords(t, ws)
	if len(records) != 2 {
		t.Fatalf("应有 2 份记录，得到 %d", len(records))
	}
	raw, err = os.ReadFile(filepath.Join(BuildDir(ws), records[1]))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	if !rec.Steps["leaf"].Reused {
		t.Fatalf("第二轮 leaf 应标记复用: %+v", rec.Steps["leaf"])
	}
}

// TestWriteRecordPrunes：超出 keepRecords 的旧记录被清理。
func TestWriteRecordPrunes(t *testing.T) {
	ws := t.TempDir()
	s := testStep("leaf", nil, nil, nil)
	for i := 0; i < keepRecords+3; i++ {
		g, err := NewGraph(ws, s)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := g.Run(false); err != nil {
			t.Fatal(err)
		}
	}
	records := listRecords(t, ws)
	if len(records) != keepRecords {
		t.Fatalf("应保留 %d 份记录，得到 %d", keepRecords, len(records))
	}
}

// listRecords 返回 build/ 下的状态记录文件名（按时间戳排序）。
func listRecords(t *testing.T, ws string) []string {
	t.Helper()
	entries, err := os.ReadDir(BuildDir(ws))
	if err != nil {
		t.Fatal(err)
	}
	var records []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "build-") && strings.HasSuffix(e.Name(), ".json") {
			records = append(records, e.Name())
		}
	}
	return records
}
