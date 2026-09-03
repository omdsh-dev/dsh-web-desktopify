// Package fsutil 提供构建与运行时共用的文件复制工具。
package fsutil

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/sumdb/dirhash"
)

// CopyFile 复制单个文件并保留可执行位。
func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// CopyDir 递归复制目录（不解引用 symlink，按原样复制链接）。
func CopyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		return CopyFile(path, target)
	})
}

// CopyDirDeref 递归复制目录并把 symlink 解引用为实体（保留可执行位）。
// skip 里的名字（任意层级的文件或目录）整体跳过；ignored 非 nil 时，
// 相对路径（/ 分隔）被其判为忽略的条目也跳过（用于遵循 .gitignore）。
// 若目标目录位于源内部（例如产物写回工作区），复制会跳过目标自身的
// 子树，避免把复制中的内容递归包含进来（不依赖任何目录名约定）。
func CopyDirDeref(src, dst string, skip map[string]bool, ignored func(rel string, isDir bool) bool) error {
	return copyDirDeref(src, dst, skip, ignored, make(map[string]bool))
}

// copyDirDeref 是 CopyDirDeref 的递归实现。seen 记录本次复制已落盘的
// 目录 realpath（全局去重，不随递归返回移除）：同一 realpath 目录
// （pnpm 嵌套安装下同一包版本被多个消费者的 node_modules 分别引用，如
// cordis 同时出现在 cordis-plugin-hmr/-timer/-include 的 node_modules 下）
// 只复制一份。这同时打破 peer 互相链接（cordis ↔ cordis-plugin-include）
// 或工作区 node_modules 指向自身 .dsh-store 的 symlink 环——无限递归的
// 两类根源。跳过后续位置不影响运行时解析：Node 的 node_modules 向上
// 查找会命中已落盘的父级副本。
func copyDirDeref(
	src, dst string,
	skip map[string]bool,
	ignored func(rel string, isDir bool) bool,
	seen map[string]bool,
) error {
	real, err := filepath.EvalSymlinks(src)
	if err != nil {
		real = src
	}
	if seen[real] {
		return nil
	}
	seen[real] = true
	defer delete(seen, real)

	// 目标相对源的前缀（仅当目标在源内部时非空）。
	selfPrefix := ""
	if rel, err := filepath.Rel(src, dst); err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." && !filepath.IsAbs(rel) {
		selfPrefix = filepath.ToSlash(rel)
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		slashRel := filepath.ToSlash(rel)
		if selfPrefix != "" && (slashRel == selfPrefix || strings.HasPrefix(selfPrefix, slashRel+"/")) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// 根目录（rel == "."）是遍历起点，永远不按 skip/ignored 跳过——
		// 否则白名单 ignored（如 SeedAllow 对非白名单返回 true）会把整个
		// 源目录 SkipDir，什么都不复制。
		if slashRel != "." && (skip[filepath.Base(path)] || (ignored != nil && ignored(slashRel, info.IsDir()))) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if !filepath.IsAbs(link) {
				link = filepath.Join(filepath.Dir(path), link)
			}
			return copyDeref(link, target, seen)
		}
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return CopyFile(path, target)
	})
}

// copyDeref 复制一个路径（若为 symlink 则解引用），目标按实体落盘。
// seen 与 copyDirDeref 共享：解引用链上的 realpath 若已复制过则跳过
// （peer 互相链接 cordis ↔ cordis-plugin-include 等），链内 realpath 也
// 去重，防 symlink 直接成环（A → B → A）导致循环空转。
func copyDeref(src, dst string, seen map[string]bool) error {
	cur := src
	chain := make(map[string]bool)
	for {
		info, err := os.Lstat(cur)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			real, err := filepath.EvalSymlinks(cur)
			if err != nil {
				real = cur
			}
			if seen[real] || chain[real] {
				return nil
			}
			chain[real] = true
			link, err := os.Readlink(cur)
			if err != nil {
				return err
			}
			if !filepath.IsAbs(link) {
				link = filepath.Join(filepath.Dir(cur), link)
			}
			cur = link
			continue
		}
		if info.IsDir() {
			return copyDirDeref(cur, dst, nil, nil, seen)
		}
		return CopyFile(cur, dst)
	}
}

// RemoveAll 递归删除目录，带重试：macOS APFS 上删除大目录偶发
// ENOTEMPTY（目录项删除的瞬态竞争），重试可自愈。
func RemoveAll(path string) error {
	var err error
	for range 5 {
		err = os.RemoveAll(path)
		if err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return err
}

// DirHash 计算目录内容的稳定哈希：按相对路径排序后对每个文件
// sha256(相对路径 + 文件内容) 聚合（golang.org/x/mod/sumdb/dirhash 的
// Hash1 算法，与 Go 模块缓存同源）。skip 里的名字（文件或目录，任意层级）
// 排除；ignored 非 nil 时，相对路径（/ 分隔）被其判为忽略的条目也排除
// （用于遵循白名单）。用于构建缓存判断（输入无变化则跳过重新打包）。
func DirHash(root string, skip map[string]bool, ignored func(rel string, isDir bool) bool) (string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		// 根目录（rel == "."）是遍历起点，不按 skip/ignored 跳过。
		if rel != "." && (skip[d.Name()] || (ignored != nil && ignored(filepath.ToSlash(rel), d.IsDir()))) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return "", err
	}
	return dirhash.Hash1(paths, func(p string) (io.ReadCloser, error) {
		return os.Open(filepath.Join(root, p))
	})
}

// FindUp 从 start 起逐级向上查找第一个包含 name 条目的目录（name 为
// 目录或文件均可），返回该目录。找不到时返回空串。用于解析 monorepo
// 内嵌工作区：pnpm-workspace.yaml / node_modules 等可能 hoisted 到祖先。
func FindUp(start, name string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		dir = start
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// WithEnv 返回 env 的副本，其中各 key 的值替换为对应 value（先移除旧
// 条目再追加到末尾）。kvs 按 key, value 成对给出。
func WithEnv(env []string, kvs ...string) []string {
	out := make([]string, 0, len(env)+len(kvs)/2)
	for _, e := range env {
		if i := strings.IndexByte(e, '='); i > 0 {
			dup := false
			for k := 0; k < len(kvs); k += 2 {
				if e[:i] == kvs[k] {
					dup = true
					break
				}
			}
			if dup {
				continue
			}
		}
		out = append(out, e)
	}
	for k := 0; k < len(kvs); k += 2 {
		out = append(out, kvs[k]+"="+kvs[k+1])
	}
	return out
}

// PrependPath 返回 env 的副本，把 dir 放到 PATH 最前（去掉重复条目）。
func PrependPath(env []string, dir string) []string {
	old := ""
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if after, ok := strings.CutPrefix(e, "PATH="); ok {
			old = after
			continue
		}
		out = append(out, e)
	}
	return append(out, "PATH="+dir+string(os.PathListSeparator)+old)
}
