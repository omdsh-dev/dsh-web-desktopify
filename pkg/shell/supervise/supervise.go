// Package supervise 守护 SEA 后端（dsh-server）进程：启动、把窗口指向其
// URL、异常退出退避重启，直到上下文取消（应用退出）。
//
// 窗口指向的是壳内网关（gateway）而非后端直连：网关在 index.html 中强制
// 注入 wails runtime.js 与共享 localStorage bridge，且网关端口稳定（后端
// 随机端口重启不影响页面 origin）。网关由入口（cmd/main）创建并接上 wails
// （Transport/ServeAssets 注入 runtime.js 伺服与 IPC），本包只负责更新其
// target 与窗口地址——全壳只有一个网关实例，窗口加载的必然是被 wails
// 接线的那个；若 supervise 自建网关，页面会经未接线的网关加载，导致
// /wails/runtime.js 503、window.wails 缺失、bridge 回退原生 localStorage。
//
// 异常退出兜底：壳被强杀（SIGKILL/panic）时后端会残留为孤儿，下次启动时
// 依据 $DSH_HOME/shell.pid（上次会话记录的壳 PID 与后端进程组 PID）清扫
// 残留后端进程组（Unix；Windows 由 Job Object KILL_ON_JOB_CLOSE 兜底）。
package supervise

import (
	"context"
	"log"
	"net/url"
	"strconv"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/omdsh-dev/dsh-web-desktopify/pkg/shell/gateway"
	"github.com/omdsh-dev/dsh-web-desktopify/pkg/shell/server"
)

// 重启退避的初值与上限。
const (
	restartBackoff = time.Second
	maxRestartWait = 30 * time.Second
)

// Run 守护后端与壳内配套服务：先清扫上次异常退出的残留后端，再启动 SEA
// 后端，就绪后把网关（入口创建并已接上 wails）指向其 URL 并把窗口切到
// 网关地址。后端异常退出则退避重启（网关 target 跟随更新），直到 ctx
// 取消（应用退出）。gw 为 nil 时退化直连后端（无注入，功能降级）。
func Run(ctx context.Context, exeDir, profile, port, dshHome string, win *application.WebviewWindow, gw *gateway.Gateway) {
	backoff := restartBackoff

	// 清扫上次会话异常退出（壳被强杀）残留的后端进程组，避免孤儿 node。
	sweep(dshHome)
	// 正常退出时移除本会话记录；异常退出（强杀）时文件残留，交给下次启动
	// 的 sweep 处理。
	defer removePidFile(dshHome)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		p, url, err := server.Start(ctx, exeDir, profile, port, dshHome)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("dsh server 启动失败：%v（%s 后重试）", err, backoff)
		} else {
			backoff = restartBackoff
			// 记录本会话（壳 PID + 后端进程组 PID），供下次启动清扫孤儿。
			writePidFile(dshHome, p.Pid())
			if gw != nil {
				gw.SetTarget(url)
				view := viewURL(gw, url)
				log.Printf("dsh server ready at %s（经网关 %s）", url, view)
				win.SetURL(view)
			} else {
				log.Printf("dsh server ready at %s", url)
				win.SetURL(url)
			}

			select {
			case <-ctx.Done():
				p.Stop()
				return
			case exit := <-p.Exit():
				if exit.Err != nil {
					log.Printf("dsh server 异常退出：%v", exit.Err)
				} else {
					log.Printf("dsh server 退出（重启）")
				}
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < maxRestartWait {
			backoff *= 2
			if backoff > maxRestartWait {
				backoff = maxRestartWait
			}
		}
	}
}

// viewURL 返回窗口加载地址：网关根路径 + 后端 ready URL 的完整 query
// 透传（认证 token 等全部参数）。WebView 首次加载带 query，经网关反代
// （Host/Origin 改写为后端）触发 dsh 认证，Set-Cookie 落库后后续请求走
// cookie。
func viewURL(gw *gateway.Gateway, backendURL string) string {
	view := "http://127.0.0.1:" + strconv.Itoa(gw.Port()) + "/"
	if u, err := url.Parse(backendURL); err == nil && u.RawQuery != "" {
		view += "?" + u.RawQuery
	}
	return view
}
