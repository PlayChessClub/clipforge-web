//go:build windows

// Windows 版 UI 唤起：内嵌 WebView2(Chromium 内核)窗口直接显示前端,
// 不再依赖用户手动打开浏览器。
// 库 jchv/go-webview2 已把 WebView2Loader.dll 内嵌进二进制,
// 运行时用 Win11/Win10 自带的 WebView2 Runtime(Edge 的 Chromium 内核)。
package main

import (
	"os"

	webview2 "github.com/jchv/go-webview2"
)

// launchUI 在 Windows 上创建一个原生窗口,内嵌 WebView2 并导航到本地 UI。
// 窗口被用户关闭后,整个程序退出(与"关掉窗口=关掉 App"的直觉一致)。
// 若 WebView2 Runtime 不可用(极老的精简系统),退回打开系统默认浏览器。
func launchUI(url string) {
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  "ClipForge AI",
			Width:  1280,
			Height: 880,
			Center: true, // 屏幕居中
		},
	})
	if w == nil {
		// 创建窗口失败(如缺少 WebView2 Runtime),退回浏览器方案
		openBrowser(url)
		return
	}
	w.Navigate(url)
	w.Run() // 阻塞:Win32 消息循环,直到窗口关闭
	w.Destroy()
	os.Exit(0) // 窗口关 = 应用退出
}
