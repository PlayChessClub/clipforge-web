//go:build linux

// Linux 不再内嵌 webkit 窗口(VM 兼容差/维护成本高):统一走 CLI(有 TTY)或浏览器(无 TTY)。
package main

// launchUI 打开系统默认浏览器访问本地 UI。
func launchUI(url string) {
	openBrowser(url)
}
