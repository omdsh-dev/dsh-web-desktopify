# 网关按 authority 过滤历史端口的 dsh-auth cookie

dsh 的浏览器认证（@deepseek-ai/dsh-client-connection 的 BrowserAuth）把
cookie 的 domain 设为 127.0.0.1（不含端口），每次启动后端端口随机
（--port 0）都会 Set-Cookie 一个新的 dsh-auth-<sha256(authority)>（默认
30 天过期）。RFC 6265 的 domain 匹配不含端口，WKWebView 把所有历史端口的
cookie 一并带上，累积超过 Node 默认 maxHeaderSize（16KB）时后端返回 431，
script/API 全部加载失败，页面空白。认证本身可插拔（cordis 插件），但
BrowserAuth 与 /api 网关耦合在同一插件、前端协议绑定，替换成本高。

选择在壳网关（gateway）转发时按 cookie payload 的 authority（签发时的
后端 host）过滤：只保留与当前后端一致的 dsh-auth-* cookie，其余原样
透传；payload 解析失败或非 dsh-auth 的 cookie 保守保留（不阻断请求）。
与 dsh 的 cookieName(authority) 设计一致——cookie 名含端口 hash，后端只
认当前 authority 对应的那个。后果：请求头不再随启动次数线性膨胀，历史
cookie 自然过期后由 WKWebView 清理；过滤逻辑只依赖 cookie 自带的
authority 字段，不感知 dsh 内部协议变更。
