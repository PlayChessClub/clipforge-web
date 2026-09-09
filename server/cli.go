// 命令行前端(在终端里完成 视频/图片/语音/克隆/账本 全流程;全平台可用,Linux 终端默认)。
// 复用本机 127.0.0.1:8731 的 HTTP 端点 + /api/tts/ws 做 CosyVoice 合成;
// 与 web 版同一套 DashScope 后端,费用估算/账本字段同口径。
package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// ==================== 常量(与前端 VIDEO_RATES / est* 同口径) ====================
var videoModels = []struct{ id, label string }{
	{"wan2.6-i2v", "wan2.6-i2v        图生视频"},
	{"wan2.7-i2v", "wan2.7-i2v        图生视频"},
	{"wan2.6-i2v-flash", "wan2.6-i2v-flash 图生视频·Flash(半价)"},
	{"wan2.6-t2v", "wan2.6-t2v        文生视频"},
	{"wan2.7-t2v", "wan2.7-t2v        文生视频"},
}

var imageModels = []struct{ id, label string }{
	{"qwen-image-2.0", "qwen-image-2.0     (¥0.20/张)"},
	{"qwen-image-2.0-pro", "qwen-image-2.0-pro (¥0.50/张)"},
	{"wan2.7-image", "wan2.7-image       (¥0.20/张)"},
	{"wan2.7-image-pro", "wan2.7-image-pro   (¥0.50/张)"},
}

var videoRates = map[string][4]float64{ // [720有声,1080有声,720无声,1080无声] 元/秒
	"wan2.6-i2v": {0.6, 1.0, 0.6, 1.0}, "wan2.7-i2v": {0.6, 1.0, 0.6, 1.0},
	"wan2.6-t2v": {0.6, 1.0, 0.6, 1.0}, "wan2.7-t2v": {0.6, 1.0, 0.6, 1.0},
	"wan2.6-i2v-flash": {0.3, 0.5, 0.15, 0.25},
}

// ==================== 通用小工具 ====================

type clipClient struct {
	base string
	hc   *http.Client
}

func newClipClient(base string) *clipClient {
	return &clipClient{base: base, hc: &http.Client{Timeout: 30 * time.Second}}
}

var numRe = regexp.MustCompile(`[0-9]+(?:\.[0-9]+)?`)

func fmtMoney(v float64) string {
	if v < 0.005 && v > 0 {
		return "<¥0.01"
	}
	return fmt.Sprintf("¥%.2f", v)
}

func readLine() string {
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimSpace(line)
}

func ask(msg, def string) string {
	if def != "" {
		fmt.Printf("%s [%s]: ", msg, def)
	} else {
		fmt.Printf("%s: ", msg)
	}
	s := readLine()
	if s == "" {
		return def
	}
	return s
}

func askYesNo(msg string, def bool) bool {
	s := ask(msg, map[bool]string{true: "y", false: "n"}[def])
	return strings.HasPrefix(strings.ToLower(s), "y")
}

func pick(msg string, items []struct{ id, label string }) string {
	if len(items) == 0 {
		return ""
	}
	fmt.Println(msg)
	for i, it := range items {
		fmt.Printf("  [%d] %s\n", i+1, it.label)
	}
	for {
		s := ask("选择(1-" + strconv.Itoa(len(items)) + ")", strconv.Itoa(1))
		n, err := strconv.Atoi(s)
		if err == nil && n >= 1 && n <= len(items) {
			return items[n-1].id
		}
		fmt.Println("输入无效,重试")
	}
}

func pickStr(msg string, items []string, def string) string {
	fmt.Println(msg)
	for i, it := range items {
		fmt.Printf("  [%d] %s\n", i+1, it)
	}
	return ask("选择(数字)", def)
}

// jsonReq 请求本机端点,统一 JSON 编解码;out 可为 nil
func (c *clipClient) jsonReq(method, path string, payload, out any) error {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// uploadFile 走 /api/upload 上传本地文件到 DashScope OSS,返回 oss:// resource
func (c *clipClient) uploadFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(fw, f); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequest("POST", c.base+"/api/upload", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("上传失败 HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out struct {
		Resource string `json:"resource"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Resource == "" {
		return "", fmt.Errorf("上传响应异常: %s", strings.TrimSpace(string(raw)))
	}
	return out.Resource, nil
}

// ensureKey 检查是否已配置 API Key;没有则引导输入(不回显)。
func (c *clipClient) ensureKey() bool {
	var cfg struct {
		Configured bool `json:"configured"`
	}
	if err := c.jsonReq("GET", "/api/config", nil, &cfg); err != nil {
		fmt.Println("⚠️ 无法连接本地服务:", err)
		return false
	}
	if cfg.Configured {
		return true
	}
	fmt.Println("首次使用:需要先配置 DashScope(阿里云百炼)API Key")
	fmt.Println("  (获取: https://bailian.console.aliyun.com/ → API-KEY 管理)")
	key := readSecret("API Key(输入不回显): ")
	if strings.TrimSpace(key) == "" {
		fmt.Println("未输入,已取消")
		return false
	}
	var out map[string]any
	if err := c.jsonReq("POST", "/api/config", map[string]any{"apiKey": key}, &out); err != nil {
		fmt.Println("❌ 保存失败:", err)
		return false
	}
	masked := "****"
	if len(key) > 8 {
		masked = key[:4] + "****" + key[len(key)-4:]
	}
	fmt.Println("✅ API Key 已保存(" + masked + "),配置存于本机,不会上报")
	return true
}

// downloadURL 把远程文件存到本地
func downloadURL(u, outPath string) error {
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Get(u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("下载失败 HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

func ts() string { return time.Now().Format("20060102-150405") }

func billEntry(id, action, model, summary, unitName string, unitCount int, amountText, detail string, tokenMin, tokenMax int, taskID string) map[string]any {
	e := map[string]any{
		"id": id, "action": action, "model": model, "summary": summary,
		"unitName": unitName, "unitCount": unitCount,
		"tokenMin": tokenMin, "tokenMax": tokenMax,
		"amountText": amountText, "detail": detail, "status": "已提交",
	}
	if taskID != "" {
		e["taskId"] = taskID
	}
	return e
}

func (c *clipClient) addBill(e map[string]any) {
	_ = c.jsonReq("POST", "/api/bill", e, nil)
}

func (c *clipClient) setBill(id, status, taskID string) {
	p := map[string]any{"id": id, "status": status}
	if taskID != "" {
		p["taskId"] = taskID
	}
	_ = c.jsonReq("PUT", "/api/bill", p, nil)
}

// ==================== 费用估算 ====================

type est struct {
	amount  string
	detail  string
	tokens  [2]int
	unit    string
	unitCnt int
	action  string
	model   string
}

func estVideo(model string, res string, dur int, audio bool) est {
	r := videoRates[model]
	idx := 0
	if res == "1080P" {
		idx = 1
	}
	if !audio {
		idx += 2
	}
	rate := r[idx]
	token := int(float64(dur) * 120000 * rate / 0.6)
	return est{
		amount: fmtMoney(rate * float64(dur)),
		detail: fmt.Sprintf("%s · %ds · %s · ¥%.2f/秒", res, dur, map[bool]string{true: "有声", false: "无声"}[audio], rate),
		tokens: [2]int{int(float64(token) * 0.7), int(float64(token) * 1.3)},
		unit:   "秒", unitCnt: dur, model: model,
	}
}

func estImage(model string, n int) est {
	rate := 0.2
	if strings.HasSuffix(model, "-pro") {
		rate = 0.5
	}
	token := 120000 * n
	return est{
		amount: fmtMoney(rate * float64(n)),
		detail: fmt.Sprintf("%s · %d 张 · ¥%.2f/张", model, n, rate),
		tokens: [2]int{int(float64(token) * 0.7), int(float64(token) * 1.3)},
		unit:   "张", unitCnt: n, model: model,
	}
}

func estTTS(chars int) est {
	amount := float64(chars) / 10000 * 1.5
	token := float64(chars) / 10000 * 15000
	return est{
		amount: fmtMoney(amount),
		detail: fmt.Sprintf("输入 %d 字符 · ¥1.5/万字符", chars),
		tokens: [2]int{int(token * 0.7), int(token * 1.3)},
		unit:   "字符", unitCnt: chars, model: "cosyvoice-v3.5-plus",
	}
}

func (e est) show() {
	fmt.Printf("  预估费用: %s\n", e.amount)
	fmt.Printf("  说明: %s\n", e.detail)
	fmt.Printf("  预估 token: %s\n", fmtTokenRange(e.tokens))
}

func fmtTokenRange(t [2]int) string {
	return fmt.Sprintf("%d ~ %d", t[0], t[1])
}

// ==================== 各功能 ====================

func (c *clipClient) doVideo() {
	model := pick("选择视频模型:", videoModels)
	isT2V := strings.HasSuffix(model, "-t2v")
	isFlash := strings.HasSuffix(model, "-flash")

	var imgRes, audRes string
	if !isT2V {
		p := ask("首帧参考图路径(i2v 必需,支持 png/jpg/webp)", "")
		if p == "" || !fileExists(p) {
			fmt.Println("❌ 未选择有效图片,取消")
			return
		}
		r, err := c.uploadFile(p)
		if err != nil {
			fmt.Println("❌ 上传失败:", err)
			return
		}
		imgRes = r
		fmt.Println("✅ 首帧图已上传")
	}
	if p := ask("参考音频路径(可选,仅部分模型支持)", ""); p != "" {
		if !fileExists(p) {
			fmt.Println("⚠️ 文件不存在,忽略参考音频")
		} else if r, err := c.uploadFile(p); err == nil {
			audRes = r
		} else {
			fmt.Println("⚠️ 上传失败,忽略:", err)
		}
	}

	audio := false
	if isFlash {
		audio = askYesNo("生成音频轨(有声)?", true)
	}
	res := pickStr("分辨率:", []string{"720P", "1080P"}, "1080P")
	if !strings.Contains(res, "P") {
		res = "1080P"
	}
	dur := 10
	durS := pickStr("时长(秒):", []string{"5", "10", "15"}, "10")
	if n, err := strconv.Atoi(durS); err == nil {
		dur = n
	}
	shot := "multi"
	if pickStr("镜头:", []string{"multi(多镜头)", "single(单镜头)"}, "1") == "single(单镜头)" {
		shot = "single"
	}
	extend := askYesNo("AI 提示词增强?", true)
	prompt := ask("画面描述 prompt", "")
	if prompt == "" {
		prompt = luckyPrompt("video")
		fmt.Println("(用了随机示例 prompt:", prompt, ")")
	}

	e := estVideo(model, res, dur, audio)
	if isT2V {
		e.action = "文生视频"
	} else {
		e.action = "图生视频"
	}
	e.show()
	if !askYesNo("继续生成?", false) {
		return
	}

	input := map[string]any{"prompt": prompt}
	if imgRes != "" {
		input["img_url"] = imgRes
	}
	if audRes != "" {
		input["audio_url"] = audRes
	}
	params := map[string]any{
		"resolution": res, "prompt_extend": extend,
		"duration": dur, "shot_type": shot,
	}
	if isFlash {
		params["audio"] = audio
	}
	var submit map[string]any
	if err := c.jsonReq("POST", "/api/video/submit",
		map[string]any{"model": model, "input": input, "parameters": params}, &submit); err != nil {
		fmt.Println("❌ 提交失败:", err)
		return
	}
	taskID, _ := submit["output"].(map[string]any)["task_id"].(string)
	if taskID == "" {
		fmt.Println("❌ 响应中没有 task_id")
		return
	}
	billID := "cli-vid-" + ts()
	c.addBill(billEntry(billID, e.action, model, truncate(prompt, 60), e.unit, e.unitCnt, e.amount, e.detail, e.tokens[0], e.tokens[1], taskID))
	fmt.Printf("✅ 任务已提交 task_id=%s,开始轮询(每 8s,最长 30 分钟,可 Ctrl+C 中断)\n", taskID)

	deadline := time.Now().Add(30 * time.Minute)
	lastStatus := ""
	for time.Now().Before(deadline) {
		time.Sleep(8 * time.Second)
		var t map[string]any
		if err := c.jsonReq("GET", "/api/video/task?taskId="+url.QueryEscape(taskID), nil, &t); err != nil {
			fmt.Println("⚠️ 查询失败:", err)
			continue
		}
		out, _ := t["output"].(map[string]any)
		st, _ := out["task_status"].(string)
		if st == "" {
			st = "RUNNING"
		}
		if st != lastStatus {
			fmt.Printf("  状态: %s (%s)\n", st, time.Now().Format("15:04:05"))
			lastStatus = st
		}
		switch st {
		case "SUCCEEDED":
			videoURL, _ := out["video_url"].(string)
			if videoURL == "" {
				fmt.Println("❌ 完成但没有视频 URL")
				c.setBill(billID, "失败", taskID)
				return
			}
			outPath := saveName("clipforge-video", ".mp4")
			fmt.Println("✅ 生成完成,正在下载 →", outPath)
			if err := downloadURL(videoURL, outPath); err != nil {
				fmt.Println("⚠️ 下载失败(可手动打开 URL):", videoURL, err)
			} else {
				fmt.Println("💾 已保存:", outPath)
			}
			c.setBill(billID, "成功", taskID)
			return
		case "FAILED", "CANCELED":
			msg, _ := out["message"].(string)
			if msg == "" {
				msg = "未知失败原因"
			}
			fmt.Println("❌", msg)
			c.setBill(billID, "失败", taskID)
			return
		}
	}
	fmt.Println("⏱️ 轮询超时(30 分钟),任务仍在后台运行,稍后可到 Web 界面查看")
}

func (c *clipClient) doImage() {
	model := pick("选择图片模型:", imageModels)
	size := pickStr("尺寸:", []string{"1024*1024", "720*1280(竖)", "1280*720(横)"}, "1024*1024")
	if !strings.Contains(size, "*") {
		size = "1024*1024"
	}
	n := 1
	if s := pickStr("张数:", []string{"1", "2", "3", "4"}, "1"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v >= 1 && v <= 4 {
			n = v
		}
	}
	extend := askYesNo("AI 提示词增强?", true)
	prompt := ask("图片描述 prompt", "")
	if prompt == "" {
		prompt = luckyPrompt("image")
		fmt.Println("(用了随机示例 prompt:", prompt, ")")
	}
	e := estImage(model, n)
	e.action = "文生图"
	e.show()
	if !askYesNo("继续生成?", false) {
		return
	}

	var resp map[string]any
	if err := c.jsonReq("POST", "/api/image", map[string]any{
		"model":      model,
		"input":      map[string]any{"messages": []map[string]any{{"role": "user", "content": []map[string]any{{"text": prompt}}}}},
		"parameters": map[string]any{"size": size, "n": n, "prompt_extend": extend, "watermark": false},
	}, &resp); err != nil {
		fmt.Println("❌ 生成失败:", err)
		return
	}
	urls := extractImageURLs(resp)
	if len(urls) == 0 {
		fmt.Println("❌ 响应中没有图片 URL")
		return
	}
	billID := "cli-img-" + ts()
	c.addBill(billEntry(billID, "文生图", model, truncate(prompt, 60), "张", n, e.amount, e.detail, e.tokens[0], e.tokens[1], ""))
	fmt.Printf("✅ 已生成 %d 张,开始下载…\n", len(urls))
	ext := "png"
	lower := strings.ToLower(urls[0])
	for _, e2 := range []string{".jpg", ".jpeg", ".webp"} {
		if strings.Contains(lower, e2) {
			ext = strings.TrimPrefix(e2, ".")
			break
		}
	}
	for i, u := range urls {
		out := saveName(fmt.Sprintf("clipforge-img-%d", i+1), "."+ext)
		if err := downloadURL(u, out); err != nil {
			fmt.Println("⚠️ 第", i+1, "张下载失败:", err)
			continue
		}
		fmt.Println("💾", out)
	}
	c.setBill(billID, "成功", "")
}

func extractImageURLs(resp map[string]any) []string {
	var urls []string
	out, _ := resp["output"].(map[string]any)
	if choices, ok := out["choices"].([]any); ok && len(choices) > 0 {
		if first, ok := choices[0].(map[string]any); ok {
			if msg, ok := first["message"].(map[string]any); ok {
				if content, ok := msg["content"].([]any); ok {
					for _, ci := range content {
						if m, ok := ci.(map[string]any); ok {
							if img, ok := m["image"].(string); ok && img != "" {
								urls = append(urls, img)
							}
						}
					}
				}
			}
		}
	}
	if len(urls) == 0 {
		if results, ok := out["results"].([]any); ok {
			for _, ri := range results {
				if m, ok := ri.(map[string]any); ok {
					if u, ok := m["url"].(string); ok && u != "" {
						urls = append(urls, u)
					}
				}
			}
		}
	}
	return urls
}

func (c *clipClient) doTTS() {
	// 获取可用音色(克隆的)
	var voices map[string]any
	if err := c.jsonReq("POST", "/api/voice/list", map[string]any{
		"model": "voice-enrollment",
		"input": map[string]any{"action": "list_voice", "prefix": "", "page_index": 0, "page_size": 50},
	}, &voices); err != nil {
		fmt.Println("⚠️ 获取音色失败:", err)
	}
	voiceID := ""
	var list []string
	if vl, ok := voices["output"].(map[string]any)["voice_list"].([]any); ok {
		for _, vi := range vl {
			if m, ok := vi.(map[string]any); ok {
				if id, ok := m["voice_id"].(string); ok {
					list = append(list, id)
				}
			}
		}
	}
	if len(list) == 0 {
		fmt.Println("⚠️ 暂无克隆音色;可手动输入音色 ID(留空取消)")
		voiceID = ask("voice_id", "")
		if voiceID == "" {
			return
		}
	} else {
		voiceID = pickStr("选择音色:", list, list[0])
	}

	text := ask("要合成的文本", "")
	if text == "" {
		fmt.Println("空文本,取消")
		return
	}
	rate := 1.0
	if s := ask("语速 0.5~2.0(默认 1.0)", "1.0"); s != "" {
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			rate = v
		}
	}
	volume := 50
	if s := ask("音量 0~100(默认 50)", "50"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			volume = v
		}
	}
	pitch := 1.0
	if s := ask("音调 0.5~2.0(默认 1.0)", "1.0"); s != "" {
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			pitch = v
		}
	}
	e := estTTS(len([]rune(text)))
	e.show()
	if !askYesNo("继续合成?", false) {
		return
	}

	billID := "cli-tts-" + ts()
	c.addBill(billEntry(billID, "语音合成", "cosyvoice-v3.5-plus", truncate(text, 60), "字符", len([]rune(text)), e.amount, e.detail, e.tokens[0], e.tokens[1], ""))
	fmt.Println("🔊 WebSocket 合成中…")
	mp3, err := synthTTSviaProxy(c.base, voiceID, text, rate, volume, pitch)
	if err != nil {
		fmt.Println("❌", err)
		c.setBill(billID, "失败", "")
		return
	}
	out := saveName("clipforge-tts", ".mp3")
	if err := os.WriteFile(out, mp3, 0o644); err != nil {
		fmt.Println("❌ 保存失败:", err)
		return
	}
	fmt.Printf("✅ 合成完成 %d KB → %s\n", len(mp3)/1024, out)
	c.setBill(billID, "成功", "")
}

func (c *clipClient) doClone() {
	p := ask("参考音频路径(3~10s 人声,mp3/wav/m4a)", "")
	if p == "" || !fileExists(p) {
		fmt.Println("❌ 文件无效,取消")
		return
	}
	prefix := ask("音色前缀(英文,克隆后的 voice_id 前缀)", "myvoice")
	text := "此操作将产生费用,随出账扣除(约 ¥0.3–¥2);费用随样本训练与首次合成出账,金额波动较大"
	fmt.Println("  ", text)
	if !askYesNo("继续克隆?", false) {
		return
	}
	fmt.Println("正在上传参考音频…")
	res, err := c.uploadFile(p)
	if err != nil {
		fmt.Println("❌ 上传失败:", err)
		return
	}
	fmt.Println("✅ 已上传,提交克隆任务…")
	var create map[string]any
	if err := c.jsonReq("POST", "/api/voice/create", map[string]any{
		"model": "voice-enrollment",
		"input": map[string]any{"action": "create_voice", "target_model": "cosyvoice-v3.5-plus", "prefix": prefix, "url": res},
	}, &create); err != nil {
		fmt.Println("❌ 提交失败:", err)
		return
	}
	voiceID, _ := create["output"].(map[string]any)["voice_id"].(string)
	if voiceID == "" {
		fmt.Println("❌ 响应中没有 voice_id")
		return
	}
	billID := "cli-clone-" + ts()
	c.addBill(billEntry(billID, "声音克隆", "voice-enrollment", "音色前缀 "+prefix, "次", 1, "随出账波动(约 ¥0.3–¥2)", "费用随样本训练与首次合成出账", 0, 0, voiceID))
	fmt.Printf("✅ 已提交 voice_id=%s,开始轮询(每 10s,最长 5 分钟)\n", voiceID)
	for i := 0; i < 30; i++ {
		time.Sleep(10 * time.Second)
		var q map[string]any
		if err := c.jsonReq("POST", "/api/voice/list", map[string]any{
			"model": "voice-enrollment",
			"input": map[string]any{"action": "query_voice", "voice_id": voiceID},
		}, &q); err != nil {
			continue
		}
		st, _ := q["output"].(map[string]any)["status"].(string)
		fmt.Printf("  轮询 #%d: %s\n", i+1, st)
		if st == "OK" {
			fmt.Println("✅ 克隆成功:", voiceID, "(可在 语音合成 里选择)")
			c.setBill(billID, "成功", voiceID)
			return
		}
		if st == "UNDEPLOYED" {
			fmt.Println("❌ 克隆失败:音频质量不达标(清晰人声,3~10s,无背景音)")
			c.setBill(billID, "失败", voiceID)
			return
		}
	}
	fmt.Println("⏱️ 轮询超时(5 分钟),稍后可用 5.我的音色 查看")
}

func (c *clipClient) doVoiceList() {
	var r map[string]any
	if err := c.jsonReq("POST", "/api/voice/list", map[string]any{
		"model": "voice-enrollment",
		"input": map[string]any{"action": "list_voice", "prefix": "", "page_index": 0, "page_size": 50},
	}, &r); err != nil {
		fmt.Println("❌", err)
		return
	}
	vl, _ := r["output"].(map[string]any)["voice_list"].([]any)
	if len(vl) == 0 {
		fmt.Println("暂无音色,先用 4.声音克隆 克隆一个")
		return
	}
	fmt.Printf("共 %d 个音色:\n", len(vl))
	for _, vi := range vl {
		m, _ := vi.(map[string]any)
		id, _ := m["voice_id"].(string)
		fmt.Println("  •", id)
	}
}

func (c *clipClient) doBill() {
	var r struct {
		Entries []struct {
			ID         string `json:"id"`
			Time       string `json:"time"`
			Action     string `json:"action"`
			Model      string `json:"model"`
			Summary    string `json:"summary"`
			UnitCount  int    `json:"unitCount"`
			UnitName   string `json:"unitName"`
			AmountText string `json:"amountText"`
			TaskID     string `json:"taskId"`
			Status     string `json:"status"`
		} `json:"entries"`
	}
	if err := c.jsonReq("GET", "/api/bill", nil, &r); err != nil {
		fmt.Println("❌", err)
		return
	}
	if len(r.Entries) == 0 {
		fmt.Println("账本为空")
		return
	}
	parse := func(s string) float64 {
		m := numRe.FindString(s)
		v, _ := strconv.ParseFloat(m, 64)
		return v
	}
	now := time.Now()
	var today, month, all float64
	for _, e := range r.Entries {
		t, err := time.Parse(time.RFC3339, e.Time)
		if err == nil {
			if t.Year() == now.Year() && t.Month() == now.Month() {
				month += parse(e.AmountText)
				if t.YearDay() == now.YearDay() {
					today += parse(e.AmountText)
				}
			}
		}
		all += parse(e.AmountText)
	}
	fmt.Printf("今日 %s | 本月 %s | 累计 %s\n\n", fmtMoney(today), fmtMoney(month), fmtMoney(all))
	fmt.Println("最近 15 条:")
	for i := len(r.Entries) - 1; i >= 0 && i >= len(r.Entries)-15; i-- {
		e := r.Entries[i]
		fmt.Printf("  %s  %-4s %-16s %-8s %d%s  %s\n",
			e.Time[:16], e.Status, e.Action, truncate(e.Model, 16), e.UnitCount, e.UnitName, e.AmountText)
	}
	if askYesNo("导出 CSV?", false) {
		resp, err := c.hc.Get(c.base + "/api/bill/export")
		if err != nil {
			fmt.Println("❌", err)
			return
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		out := saveName("clipforge-bill", ".csv")
		if err := os.WriteFile(out, raw, 0o644); err == nil {
			fmt.Println("💾 已导出:", out)
		}
	}
}

// ==================== 语音合成 WS(与前端 synthTTS 协议一致) ====================

func synthTTSviaProxy(base, voiceID, text string, rate float64, volume int, pitch float64) ([]byte, error) {
	wsURL := strings.Replace(base, "http://", "ws://", 1) + "/api/tts/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	taskID := randomID()
	var chunks [][]byte
	done := false
	finish := func(err error, data []byte) ([]byte, error) {
		if done {
			return nil, fmt.Errorf("连接提前结束")
		}
		done = true
		return data, err
	}

	send := func(v any) error {
		b, _ := json.Marshal(v)
		return conn.WriteMessage(websocket.TextMessage, b)
	}
	if err := send(map[string]any{
		"header": map[string]any{"action": "run-task", "task_id": taskID, "streaming": "duplex"},
		"payload": map[string]any{
			"model": "cosyvoice-v3.5-plus", "task_group": "audio", "task": "tts",
			"function": "SpeechSynthesizer", "input": map[string]any{},
			"parameters": map[string]any{
				"voice": voiceID, "volume": volume, "text_type": "PlainText",
				"sample_rate": 22050, "rate": rate, "format": "mp3", "pitch": pitch,
				"seed": 0, "type": 0, "enable_ssml": true,
			},
		},
	}); err != nil {
		return nil, err
	}

	timer := time.AfterFunc(120*time.Second, func() {
		// 超时兜底:只能靠 onmessage 侧判断;这里先置标志由读循环处理
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "timeout"))
	})

	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			timer.Stop()
			if len(chunks) > 0 {
				break // 服务端正常关流且已有音频
			}
			return finish(fmt.Errorf("连接被关闭: %v", err), nil)
		}
		if mt == websocket.BinaryMessage {
			chunks = append(chunks, data)
			continue
		}
		var j struct {
			Header struct {
				Event        string `json:"event"`
				ErrorCode    string `json:"error_code"`
				ErrorMessage string `json:"error_message"`
			} `json:"header"`
		}
		if json.Unmarshal(data, &j) != nil {
			continue
		}
		switch j.Header.Event {
		case "task-started":
			_ = send(map[string]any{
				"header": map[string]any{"action": "continue-task", "task_id": taskID, "streaming": "duplex"},
				"payload": map[string]any{
					"model": "cosyvoice-v3.5-plus", "task_group": "audio", "task": "tts",
					"function": "SpeechSynthesizer", "input": map[string]any{"text": text},
				},
			})
			_ = send(map[string]any{
				"header":  map[string]any{"action": "finish-task", "task_id": taskID, "streaming": "duplex"},
				"payload": map[string]any{"input": map[string]any{}},
			})
		case "task-finished":
			timer.Stop()
			if len(chunks) == 0 {
				return finish(fmt.Errorf("合成返回空音频"), nil)
			}
			return finish(nil, mergeBytes(chunks))
		case "task-failed":
			timer.Stop()
			msg := j.Header.ErrorCode + " " + j.Header.ErrorMessage
			return finish(fmt.Errorf("%s", strings.TrimSpace(msg)), nil)
		}
	}
	timer.Stop()
	return mergeBytes(chunks), nil
}

func mergeBytes(chunks [][]byte) []byte {
	total := 0
	for _, c := range chunks {
		total += len(c)
	}
	out := make([]byte, 0, total)
	for _, c := range chunks {
		out = append(out, c...)
	}
	return out
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ==================== 主菜单 ====================

func runCLI(base string, attach bool) {
	c := newClipClient(base)
	if attach {
		fmt.Println("ℹ️ ClipForge 服务已在运行,直接连接。退出本程序不影响后台服务。")
	} else {
		fmt.Printf("🎬 ClipForge CLI v3.2.2(本地服务 %s)\n", base)
		fmt.Printf("   配置文件: %s\n", configPath())
	}
	fmt.Println("   --web 可切换回浏览器界面;--cli 强制命令行(本会话默认)")
	fmt.Printf("   📁 生成的视频/图片/语音将保存到: %s\n", saveDir())

	for {
		fmt.Println()
		fmt.Println("┌─────────────────────────────────────────────┐")
		fmt.Println("│  ClipForge · 阿里云百炼 AI 生成             │")
		fmt.Println("├─────────────────────────────────────────────┤")
		fmt.Println("│  1. 视频生成    2. 文生图                   │")
		fmt.Println("│  3. 语音合成    4. 声音克隆                 │")
		fmt.Println("│  5. 我的音色    6. 账本                     │")
		fmt.Println("│  7. API Key     8. 打开 Web 界面            │")
		fmt.Println("│  0. 退出                                    │")
		fmt.Println("└─────────────────────────────────────────────┘")
		opt := ask("选择", "")
		switch opt {
		case "1":
			if !c.ensureKey() {
				continue
			}
			c.doVideo()
		case "2":
			if !c.ensureKey() {
				continue
			}
			c.doImage()
		case "3":
			if !c.ensureKey() {
				continue
			}
			c.doTTS()
		case "4":
			if !c.ensureKey() {
				continue
			}
			c.doClone()
		case "5":
			c.doVoiceList()
		case "6":
			c.doBill()
		case "7":
			c.doKey()
		case "8":
			fmt.Println("在浏览器打开:", c.base)
			openBrowser(c.base)
		case "0", "q", "exit":
			fmt.Println("再见 👋")
			return
		default:
			fmt.Println("无效选项")
		}
	}
}

func (c *clipClient) doKey() {
	var cfg struct {
		Configured bool `json:"configured"`
	}
	_ = c.jsonReq("GET", "/api/config", nil, &cfg)
	fmt.Println("当前状态:", map[bool]string{true: "✅ 已配置", false: "未配置"}[cfg.Configured])
	if askYesNo("重新设置 API Key?", false) {
		key := readSecret("新 API Key(输入不回显): ")
		if key != "" {
			var out map[string]any
			if err := c.jsonReq("POST", "/api/config", map[string]any{"apiKey": key}, &out); err == nil {
				fmt.Println("✅ 已更新")
			} else {
				fmt.Println("❌", err)
			}
		}
	}
}

// ==================== 小工具 ====================

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// saveDir 返回系统下载目录(CLI 产物默认保存处)。
// Linux 优先 xdg-user-dir(尊重用户自定义下载位置/本地化目录名,如"下载");
// 回退 $HOME/Downloads(不存在则自动创建);全失败才退回当前目录。
func saveDir() string {
	if runtime.GOOS == "linux" {
		if out, err := exec.Command("xdg-user-dir", "DOWNLOAD").Output(); err == nil {
			if d := strings.TrimSpace(string(out)); d != "" {
				if fi, err := os.Stat(d); err == nil && fi.IsDir() {
					return d
				}
			}
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		d := filepath.Join(home, "Downloads")
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			return d
		}
		if err := os.MkdirAll(d, 0o755); err == nil {
			return d
		}
	}
	return "."
}

func saveName(prefix, ext string) string {
	dir := saveDir()
	name := fmt.Sprintf("%s-%s%s", prefix, ts(), ext)
	if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
		name = fmt.Sprintf("%s-%s-%s%s", prefix, ts(), randomID()[:4], ext)
	}
	return filepath.Join(dir, name)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

var luckyBanks = map[string][]string{
	"video": {
		"一只毛茸茸的小猫戴着宇航员头盔,漂浮在失重的空间站里,慢镜头,柔和灯光。",
		"雨夜霓虹都市,一名撑红伞的女子走过湿漉漉的街道,倒影斑斓,赛博朋克风格。",
		"一只机械蝴蝶停在一朵盛开的金属花上,特写微距,齿轮转动,蒸汽朋克。",
		"雪山之巅,登山者插下旗帜,风吹雪雾,逆光剪影,史诗感构图。",
		"热闹的夜市摊档,铁板烧师傅翻炒食材,火焰腾起,升格慢镜头。",
	},
	"image": {
		"一只戴墨镜的柯基犬坐在海滩上,身边放着椰子,阳光明媚,插画风格。",
		"未来主义摩天楼群,悬浮列车穿梭,紫色与青色霓虹,赛博朋克城市。",
		"水彩画,宁静的江南水乡,白墙黛瓦,小桥流水,清晨薄雾。",
		"一只发光的水母在深海中漂浮,蓝色荧光,神秘幽深。",
		"冬日雪景,红色小木屋烟囱冒烟,松树挂雪,温馨童话感。",
	},
}

func luckyPrompt(kind string) string {
	bank := luckyBanks[kind]
	if len(bank) == 0 {
		return ""
	}
	return bank[time.Now().UnixNano()%int64(len(bank))]
}

func printUsage() {
	fmt.Println("ClipForge AI 本地客户端(阿里云百炼)")
	fmt.Println()
	fmt.Println("用法:")
	fmt.Println("  clipforge            启动(自动判断:Linux 终端→命令行;其他→窗口/浏览器)")
	fmt.Println("  clipforge --cli      强制命令行界面")
	fmt.Println("  clipforge --web      强制浏览器界面(Windows 为内嵌窗口)")
	fmt.Println()
	fmt.Println("命令行菜单: 1视频 2文生图 3语音合成 4声音克隆 5我的音色 6账本 7APIKey 8Web 0退出")
}
