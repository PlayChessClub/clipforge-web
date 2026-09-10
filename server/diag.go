package main

import (
	"encoding/json"
	"net"
	"net/http"
	"runtime"
	"time"
)

// handleDiag 网络自检：用于排查「接口返回 502」这类出网故障
// （安卓壳里 Go 后端取不到 DNS 时，域名解析会全失败）。
// 浏览器打开 /api/diag 即可看到 DNS 列表、解析结果与 443 连通性。
func handleDiag(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{
		"goos":       runtime.GOOS,
		"goarch":     runtime.GOARCH,
		"dnsServers": androidDNSServers(),
	}

	if ips, err := net.LookupHost("dashscope.aliyuncs.com"); err != nil {
		out["resolve"] = "FAIL"
		out["resolveErr"] = err.Error()
	} else {
		out["resolve"] = "OK"
		out["ips"] = ips
	}

	c, err := net.DialTimeout("tcp", "dashscope.aliyuncs.com:443", 6*time.Second)
	if err != nil {
		out["tcp443"] = "FAIL"
		out["tcp443Err"] = err.Error()
	} else {
		out["tcp443"] = "OK"
		c.Close()
	}

	var apiKeySet bool
	if k := getAPIKey(); k != "" {
		apiKeySet = true
	}
	out["apiKeySet"] = apiKeySet

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
