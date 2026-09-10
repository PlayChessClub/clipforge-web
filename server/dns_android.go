//go:build android

package main

import (
	"context"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Android 上没有 /etc/resolv.conf，且 Go 的 net 包在 android 上默认优先
// cgo 解析器（源码注释：DNS requests don't work on Android, so prefer the
// cgo resolver）。我们以 CGO_ENABLED=0 编译，cgo 回退不可用，纯 Go 解析器又
// 读不到 resolv.conf，最终退化成 127.0.0.1:53 —— 所有域名解析失败，对外请求
// 全部报错（表现就是接口返回 502）。
//
// 这里显式接管解析器：
//   1) 优先用壳（Java 侧 ConnectivityManager）注入的 CLIPFORGE_DNS
//   2) 其次读 Android 系统属性 net.dns1~4
//   3) 最后兜底公共 DNS（国内优先）
func init() { initAndroidResolver() }

func initAndroidResolver() {
	servers := androidDNSServers()
	if len(servers) == 0 {
		return
	}
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := &net.Dialer{Timeout: 4 * time.Second}
			var lastErr error
			for _, s := range servers {
				c, err := d.DialContext(ctx, "udp", s)
				if err == nil {
					return c, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
	}
}

func androidDNSServers() []string {
	var out []string
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if !strings.Contains(v, ":") {
			v = net.JoinHostPort(v, "53")
		}
		for _, e := range out {
			if e == v {
				return
			}
		}
		out = append(out, v)
	}

	for _, v := range strings.Split(os.Getenv("CLIPFORGE_DNS"), ",") {
		add(v)
	}
	for _, prop := range []string{"net.dns1", "net.dns2", "net.dns3", "net.dns4"} {
		add(getprop(prop))
	}
	// 公共 DNS 兜底
	add("223.5.5.5")   // 阿里
	add("119.29.29.29") // 腾讯 DNSPod
	add("8.8.8.8")      // Google
	return out
}

func getprop(name string) string {
	p, err := exec.LookPath("getprop")
	if err != nil {
		p = "/system/bin/getprop"
	}
	b, err := exec.Command(p, name).Output()
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	s = strings.Trim(s, "[]")
	if s == "" || s == name {
		return ""
	}
	return s
}
