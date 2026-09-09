//go:build !linux

// 非 Linux 平台的 readSecret 兜底:终端不回显需要额外依赖,这里直接读一行
// (本程序 CLI 主要用于 Linux;darwin 上 --cli 仅是开发调试用途)。
package main

import (
	"fmt"
	"strings"
)

func readSecret(prompt string) string {
	fmt.Print(prompt)
	fmt.Println(" (提示:此平台不回显需 x/term,key 会明文显示;建议在 Linux 终端使用)")
	return strings.TrimSpace(readLine())
}
