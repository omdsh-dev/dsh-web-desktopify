package bundle

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/adrg/xdg"
	"github.com/omdsh-dev/dsh-web-desktopify/internal/config"
	"github.com/omdsh-dev/dsh-web-desktopify/internal/fsutil"
)

// Install 把已打包的应用安装到当前平台：
//
//	macOS   /Applications/<Name>.app（先删旧再拷贝）
//	Linux   xdg.DataHome/<Name>/ + xdg.DataHome/applications/<Name>.desktop
//	Windows %LOCALAPPDATA%\Programs\<Name>\
func Install(appRoot string, cfg *config.Config) error {
	switch runtime.GOOS {
	case "darwin":
		dst := filepath.Join("/Applications", cfg.Name+".app")
		fmt.Printf("==> 安装到 %s\n", dst)
		if err := fsutil.RemoveAll(dst); err != nil {
			return err
		}
		if err := fsutil.CopyDir(appRoot, dst); err != nil {
			return fmt.Errorf("安装到 %s: %w", dst, err)
		}
		fmt.Printf("已安装: %s\n", dst)
		return nil

	case "linux":
		dst := filepath.Join(xdg.DataHome, cfg.Name)
		fmt.Printf("==> 安装到 %s\n", dst)
		if err := fsutil.RemoveAll(dst); err != nil {
			return err
		}
		if err := fsutil.CopyDir(appRoot, dst); err != nil {
			return fmt.Errorf("安装到 %s: %w", dst, err)
		}
		// freedesktop 启动器。
		appsDir := filepath.Join(xdg.DataHome, "applications")
		if err := os.MkdirAll(appsDir, 0o755); err != nil {
			return err
		}
		icon := filepath.Join(dst, "share", "icons", "hicolor", "512x512", "apps", "dsh.png")
		iconLine := ""
		if _, err := os.Stat(icon); err == nil {
			iconLine = "Icon=" + icon + "\n"
		}
		desktop := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=%s
Exec=%s
%sTerminal=false
`, cfg.Name, filepath.Join(dst, "bin", "dsh-shell"), iconLine)
		if err := os.WriteFile(filepath.Join(appsDir, cfg.Name+".desktop"), []byte(desktop), 0o644); err != nil {
			return err
		}
		fmt.Printf("已安装: %s\n", dst)
		return nil

	case "windows":
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			return fmt.Errorf("LOCALAPPDATA 未设置")
		}
		dst := filepath.Join(local, "Programs", cfg.Name)
		fmt.Printf("==> 安装到 %s\n", dst)
		if err := fsutil.RemoveAll(dst); err != nil {
			return err
		}
		if err := fsutil.CopyDir(appRoot, dst); err != nil {
			return fmt.Errorf("安装到 %s: %w", dst, err)
		}
		fmt.Printf("已安装: %s\n", dst)
		return nil

	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}
