//go:build !windows && !linux

// 非 Windows 平台 UI 唤起：打开系统默认浏览器。
// (macOS 用 open, Linux 用 xdg-open)
package main

// launchUI 在非 Windows 平台打开系统默认浏览器访问本地 UI。
// 函数返回后调用方继续运行 server,程序等到 Ctrl+C 才退出。
func launchUI(url string) {
	openBrowser(url)
}
