// ClipForge 后端 - Go 单文件 HTTP server
// 1. 静态文件服务(前端 SPA,已用 go:embed 内嵌进二进制,单 exe 即可运行)
// 2. DashScope API 代理 - 把 API Key 保存在本地,前端不直接拿 Key
// 3. 轮询视频任务状态
// 4. 上传本地音频/图片到 DashScope OSS
//
// 编译: go build -ldflags="-s -w" -o clipforge.exe .
// 运行: ./clipforge.exe
// 浏览器自动打开: http://127.0.0.1:8731
package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

//go:embed static
var staticFS embed.FS

// ============================================================
// 配置
// ============================================================

const (
	listenAddr = "127.0.0.1:8731"
	appDir     = "ClipForge"
	configFile = "settings.yml"
)

// 内嵌静态文件子系统(static/)
var embeddedStatic, _ = fs.Sub(staticFS, "static")

// ============================================================
// 配置管理(settings.yml 明文,用户自己保管)
// ============================================================

type Config struct {
	APIKey string `json:"apiKey"`
}

func configPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, appDir, configFile)
}

func loadConfig() Config {
	var c Config
	data, err := os.ReadFile(configPath())
	if err != nil {
		return c
	}
	// 简单 yml 解析:只取 apiKey: 后面的值
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "apiKey:") {
			v := strings.TrimSpace(strings.TrimPrefix(line, "apiKey:"))
			v = strings.Trim(v, `"'`)
			c.APIKey = v
		}
	}
	return c
}

func saveConfig(c Config) error {
	dir := filepath.Dir(configPath())
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	content := fmt.Sprintf("# ClipForge 设置(明文,自行保管不要提交到代码仓库)\napiKey: %q\n", c.APIKey)
	return os.WriteFile(configPath(), []byte(content), 0600)
}

// ============================================================
// 全局(读 API Key 一次,后续请求用)
// ============================================================

var (
	apiKeyMu sync.RWMutex
	apiKey   string
)

func getAPIKey() string {
	apiKeyMu.RLock()
	defer apiKeyMu.RUnlock()
	return apiKey
}

func setAPIKey(k string) {
	apiKeyMu.Lock()
	defer apiKeyMu.Unlock()
	apiKey = k
}

// ============================================================
// DashScope 代理
// ============================================================

func dashScopeProxy(w http.ResponseWriter, r *http.Request, path string) {
	key := getAPIKey()
	if key == "" {
		http.Error(w, `{"error":"未设置 API Key,请先在设置页填入"}`, http.StatusUnauthorized)
		return
	}
	body, _ := io.ReadAll(r.Body)
	req, err := http.NewRequest(r.Method,
		"https://dashscope.aliyuncs.com/api/v1"+path, bytes.NewReader(body))
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	for k, v := range r.Header {
		if strings.EqualFold(k, "Host") || strings.EqualFold(k, "Content-Length") {
			continue
		}
		for _, vv := range v {
			req.Header.Add(k, vv)
		}
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("user-agent", "clipforge/2.0.0")
	// body 里引用 oss:// 资源时,让 DashScope 自动解析(与 Mac 版行为一致)
	if bytes.Contains(body, []byte("oss://")) {
		req.Header.Set("X-DashScope-OssResourceResolve", "enable")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, v := range resp.Header {
		for _, vv := range v {
			w.Header().Add(k, vv)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// 上传本地文件到 DashScope OSS(给声音克隆/视频生成引用)
func uploadLocalFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	key := getAPIKey()
	if key == "" {
		http.Error(w, `{"error":"未设置 API Key"}`, http.StatusUnauthorized)
		return
	}

	// 解析 multipart
	if err := r.ParseMultipartForm(100 << 20); err != nil { // 100MB
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	defer file.Close()

	// 1) 申请策略
	policyBody := strings.NewReader(`{"model":"cosyvoice-v3.5-plus","input":{"action":"get_policy"}}`)
	policyReq, _ := http.NewRequest("POST",
		"https://dashscope.aliyuncs.com/api/v1/uploads", policyBody)
	policyReq.Header.Set("Authorization", "Bearer "+key)
	policyReq.Header.Set("Content-Type", "application/json")
	policyResp, err := http.DefaultClient.Do(policyReq)
	if err != nil {
		http.Error(w, `{"error":"申请策略失败: `+err.Error()+`"}`, http.StatusBadGateway)
		return
	}
	defer policyResp.Body.Close()
	if policyResp.StatusCode != 200 {
		b, _ := io.ReadAll(policyResp.Body)
		http.Error(w, fmt.Sprintf(`{"error":"申请策略失败: %s"}`, string(b)), policyResp.StatusCode)
		return
	}
	var policyData struct {
		Output struct {
			Upload struct {
				Host            string `json:"host"`
				Policy          string `json:"policy"`
				AccessKeyID     string `json:"access_key_id"`
				AccessKeySecret string `json:"access_key_secret"`
				SecurityToken   string `json:"security_token"`
				OssKey          string `json:"oss_key"`
				Expiration      string `json:"expiration"`
			} `json:"upload"`
		} `json:"output"`
	}
	if err := json.NewDecoder(policyResp.Body).Decode(&policyData); err != nil {
		http.Error(w, `{"error":"解析策略失败"}`, http.StatusInternalServerError)
		return
	}
	u := policyData.Output.Upload

	// 2) multipart 直传 OSS
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	mw.WriteField("OSSAccessKeyId", u.AccessKeyID)
	mw.WriteField("policy", u.Policy)
	mw.WriteField("SignatureVersion", "200")
	mw.WriteField("SignatureMethod", "OSS-HMAC-SHA256")
	mw.WriteField("dir", "oss")
	mw.WriteField("expire", u.Expiration)
	mw.WriteField("key", u.OssKey)
	mw.WriteField("x-oss-security-token", u.SecurityToken)
	mw.WriteField("success_action_status", "True")
	fw, _ := mw.CreateFormFile("file", header.Filename)
	io.Copy(fw, file)
	mw.Close()

	ossReq, _ := http.NewRequest("POST", u.Host, &b)
	ossReq.Header.Set("Content-Type", mw.FormDataContentType())
	ossResp, err := http.DefaultClient.Do(ossReq)
	if err != nil {
		http.Error(w, `{"error":"OSS 上传失败: `+err.Error()+`"}`, http.StatusBadGateway)
		return
	}
	defer ossResp.Body.Close()
	if ossResp.StatusCode != 200 {
		bb, _ := io.ReadAll(ossResp.Body)
		http.Error(w, fmt.Sprintf(`{"error":"OSS 上传失败: %s"}`, string(bb)), ossResp.StatusCode)
		return
	}

	// 3) 通知 DashScope 资源就绪
	submitBody := fmt.Sprintf(`{"model":"cosyvoice-v3.5-plus","input":{"action":"submit","oss_key":%q}}`, u.OssKey)
	submitReq, _ := http.NewRequest("POST",
		"https://dashscope.aliyuncs.com/api/v1/uploads", strings.NewReader(submitBody))
	submitReq.Header.Set("Authorization", "Bearer "+key)
	submitReq.Header.Set("Content-Type", "application/json")
	submitResp, err := http.DefaultClient.Do(submitReq)
	if err != nil {
		http.Error(w, `{"error":"提交资源失败: `+err.Error()+`"}`, http.StatusBadGateway)
		return
	}
	defer submitResp.Body.Close()
	if submitResp.StatusCode != 200 {
		bb, _ := io.ReadAll(submitResp.Body)
		http.Error(w, fmt.Sprintf(`{"error":"提交资源失败: %s"}`, string(bb)), submitResp.StatusCode)
		return
	}
	var submitData struct {
		Output struct {
			Resource string `json:"resource"`
		} `json:"output"`
	}
	json.NewDecoder(submitResp.Body).Decode(&submitData)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"resource": submitData.Output.Resource})
}

// probeClipforge 探测 addr 上是否已经是本应用在服务(用于端口被占时的挂靠判断)
func probeClipforge(base string) bool {
	c := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := c.Get(base + "/api/config")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// 跨平台打开默认浏览器
func openBrowser(url string) {
	go func() {
		switch runtime.GOOS {
		case "darwin":
			_ = exec.Command("open", url).Start()
		case "windows":
			_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
		default:
			_ = exec.Command("xdg-open", url).Start()
		}
	}()
}

// ============================================================
// 账单(与 Mac 版 BillStore 同构: bill.jsonl 追加式持久化)
// ============================================================

type BillEntry struct {
	ID         string `json:"id"`
	Time       string `json:"time"` // RFC3339
	Action     string `json:"action"`
	Model      string `json:"model"`
	Summary    string `json:"summary"`
	UnitName   string `json:"unitName"`
	UnitCount  int    `json:"unitCount"`
	TokenMin   int    `json:"tokenMin"`
	TokenMax   int    `json:"tokenMax"`
	AmountText string `json:"amountText"`
	Detail     string `json:"detail"`
	TaskID     string `json:"taskId,omitempty"`
	Status     string `json:"status"`
}

var billMu sync.Mutex

func billPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, appDir, "bill.jsonl")
}

func billLoad() []BillEntry {
	data, err := os.ReadFile(billPath())
	if err != nil {
		return nil
	}
	var list []BillEntry
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e BillEntry
		if json.Unmarshal([]byte(line), &e) == nil {
			list = append(list, e)
		}
	}
	// 最新的在前
	for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
		list[i], list[j] = list[j], list[i]
	}
	return list
}

func handleBill(w http.ResponseWriter, r *http.Request) {
	billMu.Lock()
	defer billMu.Unlock()
	switch r.Method {
	case "GET":
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"entries": billLoad()})
	case "POST":
		var e BillEntry
		if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}
		if e.ID == "" {
			e.ID = fmt.Sprintf("%d", time.Now().UnixNano())
		}
		if e.Time == "" {
			e.Time = time.Now().Format(time.RFC3339)
		}
		if e.Status == "" {
			e.Status = "已提交"
		}
		f, err := os.OpenFile(billPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err == nil {
			line, _ := json.Marshal(e)
			f.Write(append(line, '\n'))
			f.Close()
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": e.ID})
	case "PUT": // 回填任务状态(与 Mac 版 BillStore.update 同用途)
		var patch BillEntry
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil || patch.ID == "" {
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}
		list := billLoad() // 已是最新在前
		for i := range list {
			if list[i].ID == patch.ID {
				if patch.TaskID != "" {
					list[i].TaskID = patch.TaskID
				}
				if patch.Status != "" {
					list[i].Status = patch.Status
				}
				break
			}
		}
		var b strings.Builder
		for i := len(list) - 1; i >= 0; i-- { // 反转回旧在前,重写磁盘
			line, _ := json.Marshal(list[i])
			b.Write(line)
			b.WriteByte('\n')
		}
		_ = os.WriteFile(billPath(), []byte(b.String()), 0600)
		w.Write([]byte(`{"ok":true}`))
	case "DELETE":
		os.Remove(billPath())
		w.Write([]byte(`{"ok":true}`))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// CSV 导出(带 BOM,字段转义,与 Mac 版 csvText 一致)
func handleBillExport(w http.ResponseWriter, r *http.Request) {
	billMu.Lock()
	list := billLoad()
	billMu.Unlock()
	csvEscape := func(f string) string {
		if strings.ContainsAny(f, ",\"\n") {
			return `"` + strings.ReplaceAll(f, `"`, `""`) + `"`
		}
		return f
	}
	var b strings.Builder
	b.WriteString("\uFEFF时间,操作,模型,内容摘要,计量单位,数量,token下限,token上限,预估金额,任务ID,状态,计费口径\n")
	for _, e := range list {
		row := []string{e.Time, e.Action, e.Model, e.Summary, e.UnitName,
			fmt.Sprintf("%d", e.UnitCount), fmt.Sprintf("%d", e.TokenMin),
			fmt.Sprintf("%d", e.TokenMax), e.AmountText, e.TaskID, e.Status, e.Detail}
		cells := make([]string, len(row))
		for i, c := range row {
			cells[i] = csvEscape(c)
		}
		b.WriteString(strings.Join(cells, ",") + "\n")
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="clipforge-bill.csv"`)
	io.WriteString(w, b.String())
}

// ============================================================
// CosyVoice TTS WebSocket 代理
// 浏览器 ⇄ 本地(ws://127.0.0.1:8731/api/tts/ws) ⇄ DashScope(wss)
// 服务端注入 Authorization,前端不接触 Key。消息原样双向转发。
// ============================================================

func ttsWSProxy(w http.ResponseWriter, r *http.Request) {
	key := getAPIKey()
	if key == "" {
		http.Error(w, `{"error":"未设置 API Key,请先在设置页填入"}`, http.StatusUnauthorized)
		return
	}
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	client, err := up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer client.Close()

	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+key)
	hdr.Set("user-agent", "clipforge")
	upstream, _, err := websocket.DefaultDialer.Dial(
		"wss://dashscope.aliyuncs.com/api-ws/v1/inference", hdr)
	if err != nil {
		client.WriteMessage(websocket.TextMessage,
			[]byte(`{"header":{"event":"task-failed","error_message":"无法连接 DashScope: `+err.Error()+`"}}`))
		return
	}
	defer upstream.Close()

	go func() { // 浏览器 → DashScope
		for {
			mt, data, err := client.ReadMessage()
			if err != nil {
				return
			}
			if err := upstream.WriteMessage(mt, data); err != nil {
				return
			}
		}
	}()
	for { // DashScope → 浏览器
		mt, data, err := upstream.ReadMessage()
		if err != nil {
			return
		}
		if err := client.WriteMessage(mt, data); err != nil {
			return
		}
	}
}

// ============================================================
// 路由
// ============================================================

func main() {
	for _, a := range os.Args[1:] {
		if a == "-h" || a == "--help" {
			printUsage()
			return
		}
	}

	cfg := loadConfig()
	apiKey = cfg.APIKey

	mux := http.NewServeMux()

	// 静态文件(内嵌在二进制里,无需外部 static/ 目录)
	mux.Handle("/", http.FileServer(http.FS(embeddedStatic)))

	// 配置
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			w.Header().Set("Content-Type", "application/json")
			// 只告诉前端是否已设置,不返回 key
			out := map[string]any{"configured": getAPIKey() != ""}
			json.NewEncoder(w).Encode(out)
		case "POST":
			var body Config
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
				return
			}
			if err := saveConfig(body); err != nil {
				http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
				return
			}
			setAPIKey(body.APIKey)
			w.Write([]byte(`{"ok":true}`))
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// 声音克隆 - 创建 voice
	mux.HandleFunc("/api/voice/create", func(w http.ResponseWriter, r *http.Request) {
		dashScopeProxy(w, r, "/services/audio/tts/customization")
	})

	// 音色查询/列表
	mux.HandleFunc("/api/voice/list", func(w http.ResponseWriter, r *http.Request) {
		dashScopeProxy(w, r, "/services/audio/tts/customization")
	})

	// 视频任务提交
	mux.HandleFunc("/api/video/submit", func(w http.ResponseWriter, r *http.Request) {
		dashScopeProxy(w, r, "/services/aigc/video-generation/video-synthesis")
	})

	// 视频任务查询
	mux.HandleFunc("/api/video/task", func(w http.ResponseWriter, r *http.Request) {
		taskID := r.URL.Query().Get("taskId")
		if taskID == "" {
			http.Error(w, `{"error":"missing taskId"}`, http.StatusBadRequest)
			return
		}
		dashScopeProxy(w, r, "/tasks/"+taskID)
	})

	// 视频下载(转发给 DashScope,前端用 a 标签直接打开 URL 也行)
	mux.HandleFunc("/api/video/download", func(w http.ResponseWriter, r *http.Request) {
		url := r.URL.Query().Get("url")
		if url == "" {
			http.Error(w, `{"error":"missing url"}`, http.StatusBadRequest)
			return
		}
		client := &http.Client{Timeout: 5 * time.Minute}
		req, _ := http.NewRequest("GET", url, nil)
		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(url)+`"`)
		io.Copy(w, resp.Body)
	})

	// 上传本地文件到 DashScope OSS
	mux.HandleFunc("/api/upload", uploadLocalFile)

	// 文生图(同步接口,直接代理)
	mux.HandleFunc("/api/image", func(w http.ResponseWriter, r *http.Request) {
		dashScopeProxy(w, r, "/services/aigc/multimodal-generation/generation")
	})

	// 账单
	mux.HandleFunc("/api/bill", handleBill)
	mux.HandleFunc("/api/bill/export", handleBillExport)

	// CosyVoice TTS WebSocket 代理
	mux.HandleFunc("/api/tts/ws", ttsWSProxy)

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// 监听端口:失败时若探测到已是本应用在跑 → 挂靠模式(attach);
	// 否则真报错退出。
	var attach bool
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		if probeClipforge("http://" + listenAddr) {
			attach = true
		} else {
			fmt.Fprintln(os.Stderr, "❌ 端口启动失败:", err)
			os.Exit(1)
		}
	}

	baseURL := "http://" + listenAddr

	// 先启动 HTTP server(goroutine):CLI 菜单和 Web UI 都要访问它
	errCh := make(chan error, 1)
	if !attach {
		go func() {
			errCh <- srv.Serve(ln)
		}()
	}

	// CLI 模式(Linux 终端默认):服务已就绪(或挂靠已有实例),直接进命令行菜单
	if isCLIMode(os.Args[1:]) {
		runCLI(baseURL, attach)
		if !attach {
			_ = srv.Close() // 让 Serve 的 goroutine 正常收尾后退出
		}
		os.Exit(0)
	}

	// ===== Web/窗口模式:起服务后唤起 UI(窗口或浏览器),Ctrl+C 退出 =====
	if attach {
		// 已有实例在跑:直接唤起它的界面,本进程即退(后端由先到的实例继续服务)
		fmt.Fprintln(os.Stderr, "ℹ️ 检测到 ClipForge 已在运行,打开它的界面")
		launchUI(baseURL)
		os.Exit(0)
	}

	fmt.Printf("🎬 ClipForge 启动 → %s\n", baseURL)
	fmt.Printf("   配置文件: %s\n", configPath())
	fmt.Printf("   按 Ctrl+C 退出\n")

	// Windows: 内嵌 WebView2 窗口(阻塞直到关窗,关窗即退出)
	// 其他平台: 打开系统浏览器后返回
	launchUI(baseURL)

	// Ctrl+C 优雅退出(主要给非 Windows 平台用)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, "❌ server 出错:", err)
			os.Exit(1)
		}
	case <-sigCh:
		fmt.Println("\n收到 Ctrl+C,正在退出…")
		_ = srv.Close()
	}
}

// isCLIMode 决定进 CLI 还是 Web/窗口模式:
//   - 显式 --cli → CLI;显式 --web → Web
//   - 默认:Linux 且 stdin 是终端(真在命令行里跑) → CLI
//     (桌面图标启动没有 TTY → Web 模式,自动开浏览器)
func isCLIMode(args []string) bool {
	forceCLI, forceWeb := false, false
	for _, a := range args {
		switch a {
		case "--cli", "-c":
			forceCLI = true
		case "--web", "-w":
			forceWeb = true
		}
	}
	if forceCLI {
		return true
	}
	if forceWeb || runtime.GOOS != "linux" {
		return false
	}
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
