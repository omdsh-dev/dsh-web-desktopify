package bundle

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	xdraw "golang.org/x/image/draw"
)

// 图标源为工作区 SVG 或 PNG，全部尺寸由 Go 的 image 库生成（SVG 用
// oksvg/rasterx 光栅化，缩放用 x/image/draw）。macOS icns 用系统 iconutil。

// iconSize 是图标基准边长（各平台尺寸都由它缩放而来）。
const iconSize = 1024

// loadIcon1024 读取工作区图标源并统一为 1024x1024 白底 PNG 字节。
func loadIcon1024(in Inputs) ([]byte, error) {
	path := filepath.Join(in.Workspace, in.Cfg.Desktop.Icon)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("icon 源缺失 %s: %w", path, err)
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".svg":
		return renderSVG1024(data)
	default:
		return resizePNG(data, iconSize)
	}
}

// renderSVG1024 用 oksvg/rasterx 把 SVG 渲染为 1024x1024 白底 PNG。
// currentColor 预替换为黑色（oksvg 不解析该 CSS 值）。
func renderSVG1024(data []byte) ([]byte, error) {
	svg := strings.ReplaceAll(string(data), `fill="currentColor"`, `fill="#000000"`)
	icon, err := oksvg.ReadIconStream(bytes.NewReader([]byte(svg)), oksvg.WarnErrorMode)
	if err != nil {
		return nil, fmt.Errorf("解析 SVG: %w", err)
	}
	vb := icon.ViewBox
	scale := math.Min(float64(iconSize)/vb.W, float64(iconSize)/vb.H)
	w, h := vb.W*scale, vb.H*scale
	icon.SetTarget((float64(iconSize)-w)/2, (float64(iconSize)-h)/2, w, h)

	rgba := image.NewRGBA(image.Rect(0, 0, iconSize, iconSize))
	draw.Draw(rgba, rgba.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	scanner := rasterx.NewScannerGV(iconSize, iconSize, rgba, rgba.Bounds())
	r := rasterx.NewDasher(iconSize, iconSize, scanner)
	icon.Draw(r, 1.0)

	var buf bytes.Buffer
	if err := png.Encode(&buf, rgba); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// resizePNG 把 PNG 数据缩放到 size x size。
func resizePNG(data []byte, size int) ([]byte, error) {
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	xdraw.NearestNeighbor.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Over, nil)
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeScaledPng 把 1024 PNG 数据缩放到 size 并写出。
func writeScaledPng(path string, data []byte, size int) error {
	out, err := resizePNG(data, size)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

// makeIconset 生成 macOS iconset（16–512 @1x/@2x）。
func makeIconset(data []byte, iconset string) error {
	if err := os.MkdirAll(iconset, 0o755); err != nil {
		return err
	}
	for _, s := range []int{16, 32, 128, 256, 512} {
		if err := writeScaledPng(filepath.Join(iconset, fmt.Sprintf("icon_%dx%d.png", s, s)), data, s); err != nil {
			return err
		}
		if err := writeScaledPng(filepath.Join(iconset, fmt.Sprintf("icon_%dx%d@2x.png", s, s)), data, s*2); err != nil {
			return err
		}
	}
	return nil
}

// makeIcns 用系统 iconutil 把 iconset 打包为 icns。
func makeIcns(iconset, out string) error {
	cmd := exec.Command("iconutil", "-c", "icns", iconset, "-o", out)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fmt.Printf("==> exec: %s\n", cmd.String())
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("iconutil: %w", err)
	}
	return nil
}

// makeHicolor 生成 freedesktop hicolor 多尺寸图标集（16–512）。
func makeHicolor(data []byte, iconsRoot, iconName string) error {
	for _, s := range []int{16, 22, 24, 32, 48, 64, 128, 256, 512} {
		dir := filepath.Join(iconsRoot, "hicolor", fmt.Sprintf("%dx%d", s, s), "apps")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := writeScaledPng(filepath.Join(dir, iconName+".png"), data, s); err != nil {
			return err
		}
	}
	return nil
}

// makeIco 组装多尺寸 PNG 内嵌的 ICO（Vista+ 支持 PNG 压缩条目）。
func makeIco(data []byte, out string) error {
	sizes := []int{16, 24, 32, 48, 64, 128, 256}
	pngs := make([][]byte, 0, len(sizes))
	for _, s := range sizes {
		p, err := resizePNG(data, s)
		if err != nil {
			return err
		}
		pngs = append(pngs, p)
	}
	header := make([]byte, 6)
	header[2] = 1 // type: icon
	header[4] = byte(len(pngs))
	entries := make([][]byte, 0, len(pngs))
	offset := 6 + 16*len(pngs)
	for i, s := range sizes {
		e := make([]byte, 16)
		if s >= 256 {
			e[0], e[1] = 0, 0
		} else {
			e[0], e[1] = byte(s), byte(s)
		}
		e[4], e[5] = 1, 0  // planes
		e[6], e[7] = 32, 0 // bpp
		lePut32(e[8:12], uint32(len(pngs[i])))
		lePut32(e[12:16], uint32(offset))
		offset += len(pngs[i])
		entries = append(entries, e)
	}
	var buf bytes.Buffer
	buf.Write(header)
	for _, e := range entries {
		buf.Write(e)
	}
	for _, p := range pngs {
		buf.Write(p)
	}
	return os.WriteFile(out, buf.Bytes(), 0o644)
}

func lePut32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

// iconFor 按平台生成应用图标：返回图标文件路径（无图标配置时返回空串）。
func iconFor(in Inputs, destDir string, platform string) (string, error) {
	if in.Cfg.Desktop.Icon == "" {
		return "", nil
	}
	fmt.Printf("==>    生成图标（源 %s）\n", in.Cfg.Desktop.Icon)
	icon1024, err := loadIcon1024(in)
	if err != nil {
		return "", err
	}
	switch platform {
	case "darwin":
		iconset := filepath.Join(destDir, "dsh.iconset")
		if err := makeIconset(icon1024, iconset); err != nil {
			return "", err
		}
		out := filepath.Join(destDir, "dsh.icns")
		if err := makeIcns(iconset, out); err != nil {
			return "", err
		}
		return out, nil
	case "linux":
		out := filepath.Join(destDir, "icons")
		if err := makeHicolor(icon1024, out, "dsh"); err != nil {
			return "", err
		}
		return out, nil
	case "windows":
		out := filepath.Join(destDir, "dsh.ico")
		if err := makeIco(icon1024, out); err != nil {
			return "", err
		}
		return out, nil
	}
	return "", nil
}
