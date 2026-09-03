package bundle

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pngBytes 生成 size x size 的 PNG 字节。
func pngBytes(t *testing.T, size int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestResizePNG：PNG 缩放到目标尺寸。
func TestResizePNG(t *testing.T) {
	out, err := resizePNG(pngBytes(t, 64), 32)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 32 || img.Bounds().Dy() != 32 {
		t.Fatalf("缩放尺寸错误: %v", img.Bounds())
	}
}

// TestResizePNGBadInput：非 PNG 数据报错。
func TestResizePNGBadInput(t *testing.T) {
	if _, err := resizePNG([]byte("not png"), 32); err == nil {
		t.Fatal("非 PNG 应报错")
	}
}

// TestRenderSVG1024：SVG 渲染为 1024 白底 PNG；无 viewBox 报错。
func TestRenderSVG1024(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><rect width="100" height="100" fill="currentColor"/></svg>`
	out, err := renderSVG1024([]byte(svg))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 1024 || img.Bounds().Dy() != 1024 {
		t.Fatalf("渲染尺寸错误: %v", img.Bounds())
	}
	// 无 viewBox：除零防护报错。
	if _, err := renderSVG1024([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect width="10" height="10"/></svg>`)); err == nil {
		t.Fatal("无 viewBox 应报错")
	}
}

// TestMakeIco：ICO 头与条目表正确（多尺寸 PNG 内嵌）。
func TestMakeIco(t *testing.T) {
	out := filepath.Join(t.TempDir(), "dsh.ico")
	if err := makeIco(pngBytes(t, 256), out); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 6+16*7 {
		t.Fatalf("ICO 长度不足: %d", len(raw))
	}
	if raw[0] != 0 || raw[1] != 0 || raw[2] != 1 || raw[3] != 0 || raw[4] != 7 {
		t.Fatalf("ICO 头错误: % x", raw[:6])
	}
	// 最后一个条目（256 尺寸）用 0,0 表示；第一个条目是 16。
	if raw[6] != 16 || raw[7] != 16 {
		t.Fatalf("首个条目应为 16x16: %d,%d", raw[6], raw[7])
	}
	last := 6 + 16*6
	if raw[last] != 0 || raw[last+1] != 0 {
		t.Fatalf("256 条目应编码为 0,0: %d,%d", raw[last], raw[last+1])
	}
}

// TestTarGz：归档内顶层目录名为 topName，内容完整。
func TestTarGz(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "app.tar.gz")
	if err := tarGz(dir, out, "MyApp"); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	names := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag == tar.TypeReg {
			data, _ := io.ReadAll(tr)
			names[hdr.Name] = string(data)
		}
	}
	if names["MyApp/a.txt"] != "hello" || names["MyApp/sub/b.txt"] != "world" {
		t.Fatalf("归档内容错误: %v", names)
	}
}

// TestZipDir：zip 归档顶层目录名为 topName，内容完整。
func TestZipDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "app.zip")
	if err := zipDir(dir, out, "MyApp"); err != nil {
		t.Fatal(err)
	}

	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	found := false
	for _, f := range zr.File {
		if f.Name == "MyApp/a.txt" {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(rc)
			rc.Close()
			if string(data) != "hello" {
				t.Fatalf("zip 内容错误: %q", data)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("zip 缺少 MyApp/a.txt")
	}
}

// TestWriteAppConfig：appconfig.json 与壳读取契约一致（appconfig.Load
// 可解析，字段完整）。
func TestWriteAppConfig(t *testing.T) {
	cfg := testConfig()
	binDir := t.TempDir()
	if err := writeAppConfig(binDir, cfg); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(binDir, "appconfig.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"dshHome": "xdg"`) {
		t.Fatalf("appconfig 应含 dshHome: %s", raw)
	}
	// 壳侧解析：字段完整。
	loaded := loadAppConfig(t, binDir)
	if loaded.Name != "dsh-test" || loaded.Profile != "web" || loaded.DSHHome != "xdg" {
		t.Fatalf("壳侧解析错误: %+v", loaded)
	}
	if loaded.Window.Width != 1280 || loaded.Window.MinHeight != 600 {
		t.Fatalf("窗口字段错误: %+v", loaded.Window)
	}
}
