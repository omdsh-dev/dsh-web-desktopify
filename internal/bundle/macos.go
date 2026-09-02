package bundle

import (
	"fmt"
	"os"
	"path/filepath"
)

// assembleMacOS 组装 macOS 应用到 dst（暂存目录，完成后由调用方发布进
// 缓存），布局为 <dst>/Contents/...：
//
//	Contents/MacOS/{dsh-shell,dsh-server,appconfig.json}
//	Contents/Resources/dsh.icns
//	Contents/{config,node_modules,package.json,dsh-home}
//	Contents/Info.plist
func assembleMacOS(in Inputs, dst string) error {
	appRoot := dst
	if err := os.MkdirAll(appRoot, 0o755); err != nil {
		return err
	}
	fmt.Printf("==> 组装 macOS 应用 %s\n", appRoot)
	contents := filepath.Join(appRoot, "Contents")
	binDir := filepath.Join(contents, "MacOS")

	// macOS 布局：bin 在 Contents/MacOS，资源在 Contents/。
	// 复用 assembleLayout 时用临时目录再搬移，避免重复实现。
	if err := os.MkdirAll(contents, 0o755); err != nil {
		return err
	}
	if _, err := assembleLayout(in, contents); err != nil {
		return err
	}
	// assembleLayout 产出的是 bin/；macOS 需要 MacOS/。
	if err := os.Rename(filepath.Join(contents, "bin"), binDir); err != nil {
		return err
	}

	// 图标（可选）。
	resDir := filepath.Join(contents, "Resources")
	if err := os.MkdirAll(resDir, 0o755); err != nil {
		return err
	}
	iconPath := ""
	if in.Cfg.Desktop.Icon != "" {
		p, err := iconFor(in, resDir, "darwin")
		if err != nil {
			return err
		}
		iconPath = p
		// iconset 是 iconutil 的中间产物，icns 生成后清理。
		_ = os.RemoveAll(filepath.Join(resDir, "dsh.iconset"))
	}

	// Info.plist。
	iconKey := ""
	if iconPath != "" {
		iconKey = "        <key>CFBundleIconFile</key>\n            <string>dsh</string>\n"
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
    <dict>
        <key>CFBundlePackageType</key>
            <string>APPL</string>
        <key>CFBundleName</key>
            <string>%s</string>
        <key>CFBundleDisplayName</key>
            <string>%s</string>
        <key>CFBundleExecutable</key>
            <string>dsh-shell</string>
%s        <key>CFBundleIdentifier</key>
            <string>%s</string>
        <key>CFBundleVersion</key>
            <string>%s</string>
        <key>CFBundleShortVersionString</key>
            <string>%s</string>
        <key>LSMinimumSystemVersion</key>
            <string>12.0.0</string>
        <key>NSHighResolutionCapable</key>
            <string>true</string>
    </dict>
</plist>
`, in.Cfg.Name, in.Cfg.Name, iconKey, in.Cfg.Desktop.ID, in.Cfg.Version, in.Cfg.Version)
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(plist), 0o644); err != nil {
		return err
	}

	return nil
}
