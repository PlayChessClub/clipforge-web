//go:build !android

package main

// 非 Android 平台不需要接管解析器（有正常的 /etc/resolv.conf）。
func init() {}

// androidDNSServers 在非 Android 平台返回 nil，供 /api/diag 使用。
func androidDNSServers() []string { return nil }
