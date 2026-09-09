//go:build linux

// Linux 终端读取密码:临时关闭回显,输完恢复(防止 API Key 泄露在屏幕上/历史里)。
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

func readSecret(prompt string) string {
	fmt.Print(prompt)
	fd := os.Stdin.Fd()
	var old syscall.Termios
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd, syscall.TCGETS,
		uintptr(unsafe.Pointer(&old)), 0, 0, 0); errno == 0 {
		nw := old
		nw.Lflag &^= syscall.ECHO
		_, _, _ = syscall.Syscall6(syscall.SYS_IOCTL, fd, syscall.TCSETS,
			uintptr(unsafe.Pointer(&nw)), 0, 0, 0)
		defer syscall.Syscall6(syscall.SYS_IOCTL, fd, syscall.TCSETS,
			uintptr(unsafe.Pointer(&old)), 0, 0, 0)
	}
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	fmt.Println()
	return strings.TrimSpace(line)
}
