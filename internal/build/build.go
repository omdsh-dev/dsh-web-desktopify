// Package build 提供内容寻址构建 DAG：每步声明输入（自身指纹 + 依赖
// 步），产出类型化产物；产物依赖就绪即并行执行，仅指纹依赖的步可与
// 依赖步并行。
//
// 产物按输入指纹内容寻址存放：node_modules/.dsh-web-desktopify/
// cache/<digest>/，命中检查只关心目录在不在；构建先落
// build/<uuid>/，完成后原子 mv 进 cache。digest 是含依赖链的完整输入
// 指纹（sha256），不同步产物碰撞概率可忽略，无需按步分目录；步与
// digest 的对应关系由状态记录（build-<ts>.json）承担。
//
// 每次 Run 在 build/ 下写一份状态记录 build-<ts>.json（各步 digest /
// 复用 / 产物路径），保留最近 keepRecords 份，便于回溯与清理。
package build

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/omdsh-dev/dsh-web-desktopify/internal/config"
	"github.com/omdsh-dev/dsh-web-desktopify/internal/fsutil"
)

// keepRecords 是 build/ 下保留的状态记录份数（超出删除最旧）。
const keepRecords = 10

// Record 是一次构建的状态记录（build-<ts>.json）。
type Record struct {
	// Time 是构建开始时间（RFC3339）。
	Time string `json:"time"`
	// Force 是否忽略缓存全量重建。
	Force bool `json:"force"`
	// Steps 是各步的执行结果（按 ID）。
	Steps map[string]StepRecord `json:"steps"`
}

// StepRecord 是单步的状态记录。
type StepRecord struct {
	// Digest 是该步本次执行的完整输入指纹。
	Digest string `json:"digest"`
	// Reused 是否命中缓存（未执行构建）。
	Reused bool `json:"reused"`
	// Output 是产物根目录（cache/<digest>/）。
	Output string `json:"output"`
}

// Output 是构建步产物的类型化句柄。
type Output interface {
	// Dir 返回产物根目录（cache/<digest>/）。
	Dir() string
}

// Step 是构建 DAG 中的一步：输入 = 自身指纹 + 依赖步（digest 传导），
// 输出 = 类型化产物。
type Step interface {
	// ID 是步标识（状态记录键名）。
	ID() string
	// Label 是日志名。
	Label() string
	// Deps 返回指纹依赖：参与 digest 计算（digest 传导）。
	Deps() []Step
	// Needs 返回产物依赖：执行前必须完成，Run 的 deps 参数包含其产物。
	// 必须是 Deps 的子集；为空时本步可与依赖步并行。
	Needs() []Step
	// Fingerprint 返回自身输入指纹（不含依赖步）。
	Fingerprint() (string, error)
	// Output 返回该步产物的类型化描述（dir 为产物根目录）。
	Output(dir string) Output
	// Run 构建产物到 dst（暂存目录），完成后由执行器发布到缓存。
	// deps 按 ID 索引产物依赖的 Output。
	Run(dst string, deps map[string]Output) error
}

// Graph 是一次构建的 DAG：按依赖序执行全部步，产物依赖就绪即并行执行。
type Graph struct {
	ws    string
	steps []Step
	byID  map[string]Step
}

// NewGraph 构造 DAG。校验：步 ID 唯一、产物依赖（Needs）必须是指纹
// 依赖（Deps）的子集。依赖环在 Run 时检测。
func NewGraph(ws string, steps ...Step) (*Graph, error) {
	g := &Graph{ws: ws, steps: steps, byID: make(map[string]Step, len(steps))}
	for _, s := range steps {
		if _, dup := g.byID[s.ID()]; dup {
			return nil, fmt.Errorf("重复步 ID %q", s.ID())
		}
		g.byID[s.ID()] = s
		deps := make(map[string]bool, len(s.Deps()))
		for _, d := range s.Deps() {
			deps[d.ID()] = true
		}
		for _, n := range s.Needs() {
			if !deps[n.ID()] {
				return nil, fmt.Errorf("步 %s 的产物依赖 %s 不在指纹依赖中", s.ID(), n.ID())
			}
		}
	}
	return g, nil
}

// Steps 返回全部步（装配顺序）。
func (g *Graph) Steps() []Step { return g.steps }

// Run 执行全部步（force 忽略缓存），返回各步产物（按 ID）。产物依赖
// 就绪即并行执行；仅指纹依赖的步可与依赖步并行。
func (g *Graph) Run(force bool) (map[string]Output, error) {
	order, err := g.topologicalOrder()
	if err != nil {
		return nil, err
	}
	digests := make(map[string]string, len(order))
	for _, s := range order {
		dg, err := g.digest(s, digests)
		if err != nil {
			return nil, fmt.Errorf("%s: 计算指纹: %w", s.Label(), err)
		}
		digests[s.ID()] = dg
	}

	outputs := make(map[string]Output, len(g.steps))
	done := make(map[string]chan struct{}, len(g.steps))
	for _, s := range g.steps {
		done[s.ID()] = make(chan struct{})
	}
	// 状态记录：各步 digest / 复用 / 产物路径（并发收集，Run 末尾落盘）。
	rec := &Record{
		Time:  time.Now().Format(time.RFC3339),
		Force: force,
		Steps: make(map[string]StepRecord, len(g.steps)),
	}
	var mu sync.Mutex
	errCh := make(chan error, len(g.steps))
	var wg sync.WaitGroup
	for _, s := range g.steps {
		wg.Add(1)
		go func(s Step) {
			defer wg.Done()
			defer close(done[s.ID()])
			dg := digests[s.ID()]
			cached := filepath.Join(cacheDir(g.ws), dg)
			if !force && dirExists(cached) {
				fmt.Printf("==> [%s] 复用缓存（%s）\n", s.Label(), ShortHash(dg))
				mu.Lock()
				outputs[s.ID()] = s.Output(cached)
				rec.Steps[s.ID()] = StepRecord{Digest: dg, Reused: true, Output: cached}
				mu.Unlock()
				return
			}
			// 等产物依赖完成（仅指纹依赖的步无需等待，可与依赖步并行）。
			for _, n := range s.Needs() {
				<-done[n.ID()]
			}
			// 依赖失败（产物缺失）则跳过；错误已由 errCh 收集。
			mu.Lock()
			missing := false
			for _, n := range s.Needs() {
				if _, ok := outputs[n.ID()]; !ok {
					missing = true
					break
				}
			}
			mu.Unlock()
			if missing {
				return
			}

			if force {
				fmt.Printf("==> [%s] 重建（--force）\n", s.Label())
			} else {
				fmt.Printf("==> [%s] 首次构建（%s）\n", s.Label(), ShortHash(dg))
			}
			if err := os.MkdirAll(BuildDir(g.ws), 0o755); err != nil {
				errCh <- fmt.Errorf("%s: 创建 build 目录: %w", s.Label(), err)
				return
			}
			tmp, err := os.MkdirTemp(BuildDir(g.ws), "step-")
			if err != nil {
				errCh <- fmt.Errorf("%s: 创建暂存目录: %w", s.Label(), err)
				return
			}
			deps := make(map[string]Output, len(s.Needs()))
			mu.Lock()
			for _, n := range s.Needs() {
				deps[n.ID()] = outputs[n.ID()]
			}
			mu.Unlock()
			if err := s.Run(tmp, deps); err != nil {
				_ = os.RemoveAll(tmp)
				errCh <- fmt.Errorf("%s: %w", s.Label(), err)
				return
			}
			if err := commit(g.ws, dg, tmp); err != nil {
				_ = os.RemoveAll(tmp)
				errCh <- fmt.Errorf("%s: %w", s.Label(), err)
				return
			}
			_ = os.RemoveAll(tmp)
			fmt.Printf("==> [%s] 缓存: %s\n", s.Label(), cached)
			mu.Lock()
			outputs[s.ID()] = s.Output(cached)
			rec.Steps[s.ID()] = StepRecord{Digest: dg, Reused: false, Output: cached}
			mu.Unlock()
		}(s)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return nil, err
		}
	}
	writeRecord(g.ws, rec)
	return outputs, nil
}

// writeRecord 把状态记录写入 build/build-<ts>.json（原子写），并清理
// 超出 keepRecords 的旧记录。
func writeRecord(ws string, rec *Record) {
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return
	}
	dir := BuildDir(ws)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	path := filepath.Join(dir, "build-"+time.Now().Format("20060102-150405.000000000")+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		return
	}
	// 清理最旧记录（按文件名时间戳排序）。
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var records []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "build-") && strings.HasSuffix(e.Name(), ".json") {
			records = append(records, e.Name())
		}
	}
	sort.Strings(records)
	for i := 0; i+keepRecords < len(records); i++ {
		_ = os.Remove(filepath.Join(dir, records[i]))
	}
}

// topologicalOrder 按指纹依赖（Deps）拓扑排序（DFS 后序），检测环。
func (g *Graph) topologicalOrder() ([]Step, error) {
	var order []Step
	visited := make(map[string]bool, len(g.steps))
	gray := make(map[string]bool, len(g.steps))
	var visit func(s Step) error
	visit = func(s Step) error {
		if gray[s.ID()] {
			return fmt.Errorf("依赖环: %s", s.ID())
		}
		if visited[s.ID()] {
			return nil
		}
		gray[s.ID()] = true
		for _, d := range s.Deps() {
			if err := visit(d); err != nil {
				return err
			}
		}
		delete(gray, s.ID())
		visited[s.ID()] = true
		order = append(order, s)
		return nil
	}
	for _, s := range g.steps {
		if err := visit(s); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// digest 计算该步完整输入指纹：自身指纹 + 各指纹依赖步的 dg。
func (g *Graph) digest(s Step, digests map[string]string) (string, error) {
	in, err := s.Fingerprint()
	if err != nil {
		return "", err
	}
	h := sha256.New()
	io.WriteString(h, in)
	for _, d := range s.Deps() {
		h.Write([]byte{0})
		io.WriteString(h, digests[d.ID()])
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// cacheDir 返回内容寻址缓存根（工作区 node_modules/.dsh-web-desktopify/cache）。
func cacheDir(ws string) string { return filepath.Join(config.BundleRoot(ws), "cache") }

// BuildDir 返回构建暂存根（与 cache 同文件系统，保证原子 rename 发布）。
func BuildDir(ws string) string { return filepath.Join(config.BundleRoot(ws), "build") }

// commit 把构建完成的暂存目录原子发布到 cache/<digest>/。
// 目标已存在（并发同指纹）时直接复用；重试兜底跨设备 rename 失败。
func commit(ws, dg, tmp string) error {
	target := filepath.Join(cacheDir(ws), dg)
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	if dirExists(target) {
		return nil
	}
	if err := os.Rename(tmp, target); err == nil {
		return nil
	} else if strings.Contains(err.Error(), "cross-device") {
		// 极端情况下 cache 与 build 不在同一文件系统：复制后删除暂存。
		if cerr := fsutil.CopyDir(tmp, target); cerr != nil {
			return cerr
		}
		return os.RemoveAll(tmp)
	} else {
		return err
	}
}

// ShortHash 截断 hash 为 12 位，便于日志对比（不足 12 位原样返回）。
func ShortHash(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12]
}

// dirExists 报告路径是否为已存在的目录。
func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
