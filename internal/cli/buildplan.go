// 打包链内容寻址缓存 DAG：deploy 闭包 → SEA 后端 → 壳二进制 → 平台
// 组装。产物按输入指纹内容寻址存放：node_modules/.dsh-web-desktopify/
// cache/<step>/<digest>/，命中检查只关心目录在不在；构建先落
// build/<uuid>/，完成后原子 mv 进 cache。依赖传导由 digest 链天然保证：
// 依赖步重建后其 digest 变化，下游 digest 随之变化，必然重建。
package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/omdsh-dev/dsh-web-desktopify/internal/config"
	"github.com/omdsh-dev/dsh-web-desktopify/internal/fsutil"
)

// 内容寻址缓存布局（位于工作区 node_modules/.dsh-web-desktopify/ 下）：
//
//	cache/<step>/<digest>/   每步产物（digest = 输入指纹，命中即目录存在）
//	build/<uuid>/            构建暂存，完成后原子 mv 进 cache（同文件系统）
func cacheDir(ws string) string { return filepath.Join(config.BundleRoot(ws), "cache") }

func buildDir(ws string) string { return filepath.Join(config.BundleRoot(ws), "build") }

// buildStep 描述打包链中的一步。
type buildStep struct {
	id    string // cache 子目录名（cache/<id>/）
	label string // 日志名
	deps  []*buildStep
	input func() (string, error) // 自身输入指纹（不含依赖步）
	run   func(dst string) error // 构建到 dst（暂存目录），完成后由 runStep 发布
	// dg 是该步本次执行的完整输入指纹：sha256(自身输入 + 各依赖步 dg)。
	// 依赖步重建后 dg 自动变化。
	dg string
}

// digest 计算该步完整输入指纹：自身输入 + 各依赖步 dg。
func (s *buildStep) digest() (string, error) {
	in, err := s.input()
	if err != nil {
		return "", err
	}
	h := sha256.New()
	io.WriteString(h, in)
	for _, d := range s.deps {
		h.Write([]byte{0})
		io.WriteString(h, d.dg)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// cachePath 返回该步产物在 cache 中的目录（cache/<id>/<dg>/）。
func (s *buildStep) cachePath(ws string) string {
	return filepath.Join(cacheDir(ws), s.id, s.dg)
}

// runStep 执行一步：cache 中已有该 digest 目录则复用；否则构建到
// build/<uuid>/ 并原子 mv 发布。产物不存在时视为未构建。返回
// （产物路径, 是否复用, error）。
func runStep(ws string, s *buildStep, force bool) (string, bool, error) {
	dg, err := s.digest()
	if err != nil {
		return "", false, fmt.Errorf("%s: 计算指纹: %w", s.label, err)
	}
	s.dg = dg
	cached := s.cachePath(ws)
	if !force && dirExists(cached) {
		fmt.Printf("==> [%s] 复用缓存（%s）\n", s.label, shortHash(dg))
		return cached, true, nil
	}

	// 构建暂存 + 原子发布：cache 与 build 同文件系统（都在
	// node_modules/.dsh-web-desktopify/ 下），os.Rename 是原子的；目标已
	// 存在时覆盖替换。
	if force {
		fmt.Printf("==> [%s] 重建（--force）\n", s.label)
	} else {
		fmt.Printf("==> [%s] 首次构建（%s）\n", s.label, shortHash(dg))
	}
	if err := os.MkdirAll(buildDir(ws), 0o755); err != nil {
		return "", false, fmt.Errorf("%s: 创建 build 目录: %w", s.label, err)
	}
	tmp, err := os.MkdirTemp(buildDir(ws), "step-")
	if err != nil {
		return "", false, fmt.Errorf("%s: 创建暂存目录: %w", s.label, err)
	}
	if err := s.run(tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return "", false, fmt.Errorf("%s: %w", s.label, err)
	}
	if err := commit(ws, s, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return "", false, fmt.Errorf("%s: %w", s.label, err)
	}
	_ = os.RemoveAll(tmp)
	fmt.Printf("==> [%s] 缓存: %s\n", s.label, cached)
	return cached, false, nil
}

// commit 把构建完成的暂存目录原子发布到 cache/<id>/<digest>/。
// 目标已存在（并发同指纹）时直接复用；重试兜底跨设备 rename 失败。
func commit(ws string, s *buildStep, tmp string) error {
	parent := filepath.Dir(s.cachePath(ws))
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	if dirExists(s.cachePath(ws)) {
		return nil
	}
	if err := os.Rename(tmp, s.cachePath(ws)); err == nil {
		return nil
	} else if strings.Contains(err.Error(), "cross-device") {
		// 极端情况下 cache 与 build 不在同一文件系统：复制后删除暂存。
		if cerr := fsutil.CopyDir(tmp, s.cachePath(ws)); cerr != nil {
			return cerr
		}
		return os.RemoveAll(tmp)
	} else {
		return err
	}
}

// filesFingerprint 计算一组工程文件的稳定指纹。缺失文件整体跳过（固定
// 清单场景下"缺失"是常态，如 monorepo 的 lockfile 位于 workspace 根而非
// 工作区）；内容变化影响指纹。
func filesFingerprint(ws string, names []string) (string, error) {
	h := sha256.New()
	for _, n := range names {
		data, err := os.ReadFile(filepath.Join(ws, n))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", err
		}
		io.WriteString(h, n)
		h.Write([]byte{0})
		h.Write(data)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
