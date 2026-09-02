package bundle

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// assembleLinux 组装 Linux 应用到 dst（暂存目录，完成后由调用方发布进
// 缓存），布局为 <dst>/<Name>/：
//
//	<Name>/bin/{dsh-shell,dsh-server,appconfig.json}
//	<Name>/config/ <Name>/node_modules/ <Name>/package.json <Name>/dsh-home/
//	<Name>/share/icons/hicolor/（16–512 + scalable SVG）
//
// 完成后在 <dst> 内打包 <Name>.tar.gz（顶层目录 <Name>/）。
func assembleLinux(in Inputs, dst string) error {
	appRoot := filepath.Join(dst, in.Cfg.Name)
	if err := os.MkdirAll(appRoot, 0o755); err != nil {
		return err
	}
	fmt.Printf("==> 组装 Linux 应用 %s\n", appRoot)
	if _, err := assembleLayout(in, appRoot); err != nil {
		return err
	}

	// 图标（可选）：share/icons/hicolor/。
	if in.Cfg.Desktop.Icon != "" {
		iconsRoot := filepath.Join(appRoot, "share", "icons")
		if err := os.MkdirAll(iconsRoot, 0o755); err != nil {
			return err
		}
		if _, err := iconFor(in, iconsRoot, "linux"); err != nil {
			return err
		}
	}

	// 归档 tar.gz。
	tarPath := filepath.Join(dst, in.Cfg.Name+".tar.gz")
	fmt.Printf("==> 归档 %s\n", tarPath)
	if err := tarGz(appRoot, tarPath, in.Cfg.Name); err != nil {
		return fmt.Errorf("tar.gz: %w", err)
	}
	return nil
}

// tarGz 把 dir 打包为 tar.gz，归档内顶层目录名为 topName。
func tarGz(dir, out, topName string) error {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

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
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = name
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(tw, in)
		return err
	})
}
