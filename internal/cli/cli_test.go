package cli

import (
	"runtime"
	"testing"
)

// TestCheckPlatform：本机平台通过；macos 是 darwin 别名；交叉编译与
// 非法格式报错。
func TestCheckPlatform(t *testing.T) {
	// 空（缺省本机）通过。
	if err := checkPlatform(""); err != nil {
		t.Fatalf("空平台应通过: %v", err)
	}
	// 本机 os/arch 通过。
	if err := checkPlatform(runtime.GOOS + "/" + runtime.GOARCH); err != nil {
		t.Fatalf("本机平台应通过: %v", err)
	}
	// macos 别名：darwin 上接受 macos/arm64（usage 示例写法）。
	if runtime.GOOS == "darwin" {
		if err := checkPlatform("macos/" + runtime.GOARCH); err != nil {
			t.Fatalf("macos 别名应通过: %v", err)
		}
	}
	// 交叉编译报错。
	if err := checkPlatform("linux/amd64"); err == nil {
		t.Fatal("交叉编译应报错")
	}
	// 非法格式报错。
	for _, bad := range []string{"macos", "macos/", "/arm64", "macos/arm64/extra"} {
		if err := checkPlatform(bad); err == nil {
			t.Errorf("非法平台 %q 应报错", bad)
		}
	}
}
