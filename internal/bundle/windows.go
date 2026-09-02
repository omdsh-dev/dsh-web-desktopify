package bundle

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// assembleWindows 组装 Windows 应用到 dst（暂存目录，完成后由调用方
// 发布进缓存），布局为 <dst>/<Name>/：
//
//	<Name>/bin/{dsh-shell.exe,dsh-server.exe,appconfig.json}
//	<Name>/config/ <Name>/node_modules/ <Name>/package.json <Name>/dsh-home/
//	<Name>/dsh.ico
//
// 完成后在 <dst> 内打包 <Name>.zip（顶层目录 <Name>/）。
func assembleWindows(in Inputs, dst string) error {
	appRoot := filepath.Join(dst, in.Cfg.Name)
	if err := os.MkdirAll(appRoot, 0o755); err != nil {
		return err
	}
	fmt.Printf("==> 组装 Windows 应用 %s\n", appRoot)
	if _, err := assembleLayout(in, appRoot); err != nil {
		return err
	}

	// 图标（可选）：dsh.ico（多尺寸 PNG 内嵌，Vista+）。
	if in.Cfg.Desktop.Icon != "" {
		if _, err := iconFor(in, appRoot, "windows"); err != nil {
			return err
		}
	}

	// 归档 zip。
	zipPath := filepath.Join(dst, in.Cfg.Name+".zip")
	fmt.Printf("==> 归档 %s\n", zipPath)
	if err := zipDir(appRoot, zipPath, in.Cfg.Name); err != nil {
		return fmt.Errorf("zip: %w", err)
	}
	return nil
}

// zipDir 把 dir 打包为 zip，归档内顶层目录名为 topName。
func zipDir(dir, out, topName string) error {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()

	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(filepath.Join(topName, rel))
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.IsDir() {
			_, err := zw.Create(name + "/")
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			// 不打包 symlink 实体（bundle 内不应有链接）。
			return nil
		}
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(w, in)
		return err
	})
}
