package main

// 提示词库 + 「试试手气 Pro」两阶段流程
// 移植自 macOS / iOS 版 PromptBank.swift，保持同一套词库与计费口径。
//
//   「试试手气」(免费)：从 promptCorpus 随机取一条整句，不联网、不计费
//   「试试手气 Pro」(计费)：两阶段
//     阶段 1 选句：qwen3.7-text-embedding-flash 把「目的种子」与候选句一起向量化，
//                  按余弦相似度取 top3 作参照句
//     阶段 2 扩写：qwen-plus 按媒介定制的指令，把参照句 + 目的扩写成 ~500 字全新提示词
//   任一阶段失败 → 降级为普通随机，且不计费

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"time"
)

// ============================================================
// 模型与计价常量（与 Mac PriceList / TokenEstimator 同口径）
// ============================================================

const (
	modelEmbedding  = "qwen3.7-text-embedding-flash" // 阶段一：语义选句（按输入 token 计费）
	modelTextGen    = "qwen-plus"                    // 阶段二：扩写生成
	embedPricePerK  = 0.000125                       // 元/千token
	llmInputPerK    = 0.00096                        // 元/千token
	llmOutputPerK   = 0.0024                         // 元/千token
	genInputEstimate = 600                           // 指令 + 参照句的输入量级估算
)

// 各媒介阶段二的 max_tokens 预算护栏（图片 ¥0.03 / 视频 ¥0.05 / 语音 ¥0.10 以内）
var maxGenTokens = map[string]int{"image": 800, "video": 1100, "audio": 1400}

var kindLabel = map[string]string{"video": "视频", "image": "图片", "audio": "语音"}

// normalizeKind 收敛前端传入的 kind，非法值一律按 image 处理
func normalizeKind(k string) string {
	switch k {
	case "video", "image", "audio":
		return k
	}
	return "image"
}

// ============================================================
// 结构化词库（维度 → 词语/短语），驱动「组合句」
// ============================================================

type lexiconDim struct {
	Dim   string
	Words []string
}

var promptLexicon = map[string][]lexiconDim{
	"video": {
		{"场景", []string{"雨夜街道", "废弃工厂", "雪山垭口", "深夜食堂", "热闹夜市",
			"无垠沙漠", "深海遗迹", "云端城市", "旧车站台", "竹林小径"}},
		{"主体", []string{"独行旅人", "机械蝴蝶", "老式吉普", "流浪的猫", "涂鸦少年",
			"拉面师傅", "登山者", "发光水母", "送信的鸟", "修钟老人"}},
		{"运镜", []string{"缓慢推近", "手持跟拍", "环绕运镜", "俯拍拉升", "长焦压缩",
			"低角度仰拍", "一镜到底", "航拍俯冲", "轨道横移", "定格特写"}},
		{"氛围感", []string{"孤独寂寥", "热血澎湃", "温柔治愈", "紧张悬疑", "史诗壮阔",
			"慵懒日常", "怀旧胶片", "梦幻迷离", "静谧禅意", "荒诞幽默"}},
		{"声音氛围", []string{"雨声与闷雷", "机械嗡鸣", "柴火噼啪", "风声与呼吸", "远处汽笛",
			"心跳低频", "蝉鸣鸟叫", "电子低频", "木门吱呀", "海浪拍岸"}},
		{"光影", []string{"霓虹湿地面反射", "逆光剪影", "烛火暖光", "晨雾漫射", "硬顶光",
			"月夜冷蓝", "金色黄昏", "频闪灯影", "百叶窗条纹光", "雪地反光"}},
	},
	"image": {
		{"主体", []string{"柯基犬", "水乡小镇", "机械城堡", "玻璃花房", "老木桌",
			"宇航员", "云中鲸鱼", "纸飞机", "灯塔", "旧皮箱"}},
		{"风格", []string{"水彩", "赛博朋克", "极简主义", "宫崎骏", "8-bit 像素",
			"商业摄影", "超现实", "国风工笔", "蒸汽朋克", "胶片写实"}},
		{"构图", []string{"三分法", "中心对称", "大量留白", "低角度", "框架构图",
			"对角线", "俯视平铺", "特写微距", "黄金螺旋", "重复阵列"}},
		{"光线", []string{"暖色侧光", "清晨薄雾", "逆光光晕", "柔和窗光", "夜景灯海",
			"硬光投影", "体积光柱", "冷调月光", "烛光摇曳", "霓虹漫射"}},
		{"色彩", []string{"莫兰迪灰", "高饱和撞色", "紫绿幻彩", "黑白", "暖橙蓝对比",
			"粉彩", "金属冷灰", "大地色", "青橙调", "单色强调"}},
		{"细节", []string{"露珠", "颗粒质感", "发丝光泽", "布料纹理", "金属划痕",
			"纸张纤维", "光斑", "薄烟雾", "水波倒影", "剥落墙皮"}},
	},
	"audio": {
		{"语气", []string{"温柔低语", "沉稳播报", "轻快活泼", "深情独白", "冷静克制",
			"俏皮调侃", "磁性低音", "亲切邻家", "略带沙哑", "坚定有力"}},
		{"场景", []string{"深夜电台", "清晨问候", "旅途解说", "睡前故事", "产品介绍",
			"现场主持", "电话留言", "开幕致辞", "课堂讲解", "车站广播"}},
		{"节奏", []string{"缓慢悠长", "明快跳跃", "先缓后急", "停顿留白", "稳定均匀",
			"渐强推进", "循环往复", "短句利落", "一气呵成", "轻重交替"}},
		{"内容内核", []string{"回忆童年", "城市夜色", "生活小确幸", "探索未知", "人间烟火",
			"季节更替", "孤独与陪伴", "告别与重逢", "慢下来的午后", "雨天的窗"}},
	},
}

// ============================================================
// 完整整句（免费「试试手气」随机取用；也作为 Pro 选句候选）
// ============================================================

var promptCorpus = map[string][]string{
	"video": {
		"一个由喷漆画成的少年从混凝土墙上活过来，边 rap 边摆出充满活力的说唱姿势，夜晚铁路桥下，街灯孤照，电影感氛围。",
		"一只毛茸茸的小猫戴着宇航员头盔，漂浮在失重的空间站里，慢镜头，柔和灯光。",
		"雨夜霓虹都市，一名撑红伞的女子走过湿漉漉的街道，倒影斑斓，赛博朋克风格。",
		"俯拍视角，沙漠中一辆复古吉普扬尘飞驰，夕阳把沙丘染成金色，长焦压缩感。",
		"一间深夜食堂，热气腾腾的拉面，镜头缓缓推近，蒸汽在暖黄灯光下袅袅升起。",
		"海浪拍打礁石，海鸥掠过，慢动作水花四溅，清晨薄雾，电影感自然光。",
		"一只机械蝴蝶停在一朵盛开的金属花上，特写微距，齿轮转动，蒸汽朋克。",
		"雪山之巅，登山者插下旗帜，风吹雪雾，逆光剪影，史诗感构图。",
		"热闹的夜市摊档，铁板烧师傅翻炒食材，火焰腾起，升格慢镜头。",
		"清晨森林，一束阳光穿透树冠，光柱中尘埃飞舞，镜头缓缓上摇。",
	},
	"image": {
		"一只戴墨镜的柯基犬坐在海滩上，身边放着椰子，阳光明媚，插画风格。",
		"未来主义摩天楼群，悬浮列车穿梭，紫色与青色霓虹，赛博朋克城市。",
		"水彩画，宁静的江南水乡，白墙黛瓦，小桥流水，清晨薄雾。",
		"一只巨大的鲸鱼在云海中游弋，天空之城，梦幻超现实主义。",
		"特写：一杯拉花拿铁，木质桌面，暖色侧光，商业摄影质感。",
		"宫崎骏风格的田园小屋，绿草如茵，蓝天白云，远处风车转动。",
		"一只发光的水母在深海中漂浮，蓝色荧光，神秘幽深。",
		"像素风 8-bit 游戏场景，勇士站在城堡前，勇者斗恶龙。",
		"极简主义海报，一只红色气球飘向天空，大量留白，高级灰背景。",
		"冬日雪景，红色小木屋烟囱冒烟，松树挂雪，温馨童话感。",
	},
	"audio": {
		"夜色渐深，城市慢慢安静下来。远处的灯火一盏盏熄灭，只剩下风穿过树梢的声音。",
		"欢迎收听今天的节目。我们来聊一个有趣的话题：为什么有些人，天生就更乐观一点？",
		"先把米淘洗干净，加一小撮盐，再滴两滴油，这样煮出来的米饭粒粒分明，还带着光泽。",
		"风从海面上吹来，带着一点咸味。她站在礁石上，看着远处的灯塔，一闪，一闪。",
		"各位旅客您好，本次列车即将到达终点站，请您带好随身物品，准备下车。",
		"小时候，夏天的傍晚总是很长。蝉鸣、蒲扇、冰镇西瓜，还有外婆讲不完的故事。",
		"在这个快节奏的时代，能安静地读完一本书，已经变成了一种小小的奢侈。",
		"雨停了。空气里有泥土的味道，孩子们跑出家门，踩着水洼，笑声传得很远很远。",
		"亲爱的朋友，愿你今天遇到的每一件小事，都刚好合你的心意。晚安，好梦。",
		"他推开门，屋里的灯还亮着。桌上留着一张纸条：饭在锅里，记得热一热再吃。",
	},
}

// Pro 的主题种子（下拉菜单可选；留空时随机取一个作为目的）
var promptSeeds = map[string][]string{
	"video": {"雨夜城市", "童年记忆", "孤独旅人", "机械与自然", "美食烟火", "深海雪原"},
	"image": {"静谧江南", "赛博都市", "暖色日常", "超现实梦境", "极简秩序", "荒野星光"},
	"audio": {"温柔晚安", "晨间问候", "旅途旁白", "深夜电台", "日常开场白", "季节随笔"},
}

// ============================================================
// 词库取用
// ============================================================

// randomPrompt 免费「试试手气」：随机取一条整句
func randomPrompt(kind string) string {
	c := promptCorpus[kind]
	if len(c) == 0 {
		return ""
	}
	return c[rand.Intn(len(c))]
}

// comboPromptSentence 用结构化词库随机组合一条短句（体现「由特定词语构成」而非复读整句）
func comboPromptSentence(kind string) string {
	parts := make([]string, 0, 8)
	for _, d := range promptLexicon[kind] {
		if len(d.Words) == 0 {
			continue
		}
		parts = append(parts, d.Words[rand.Intn(len(d.Words))])
	}
	return strings.Join(parts, " · ")
}

// effectiveSeed 实际使用的目的种子：用户填了就用它，否则随机取该媒介主题
func effectiveSeed(kind, purpose string) string {
	if t := strings.TrimSpace(purpose); t != "" {
		return t
	}
	seeds := promptSeeds[kind]
	if len(seeds) == 0 {
		return kindLabel[kind]
	}
	return seeds[rand.Intn(len(seeds))]
}

// plannedTexts 本次向量化的文本：首条为种子，其余为候选句（接口单次 ≤ 20 条）
func plannedTexts(kind, purpose string) []string {
	seed := effectiveSeed(kind, purpose)

	pool := append([]string{}, promptCorpus[kind]...)
	rand.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	for i := 0; i < 8; i++ {
		pool = append(pool, comboPromptSentence(kind))
	}
	rand.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	if len(pool) > 18 {
		pool = pool[:18] // 1 + 18 = 19 ≤ 20
	}
	return append([]string{seed}, pool...)
}

// ============================================================
// 阶段二：扩写指令
// ============================================================

func systemInstruction(kind string) string {
	base := "你是资深创意文案与分镜脚本撰写人。只输出正文，不要标题、不要解释、不要复述或引用输入的原句。"
	switch kind {
	case "image":
		return base + "请输出一段约 500 字的中文文生图提示词，需覆盖：主体、构图、质感、风格、光线、景别、色彩、细节。语言具体、画面感强，可直接贴给文生图模型。"
	case "video":
		return base + "请输出一段约 500 字的中文视频提示词，需覆盖：场景、主体、运镜、分镜与转场、声音氛围、光影、节奏走向。语言具体、可执行，可直接贴给文生视频模型。"
	default:
		return base + "请输出一段约 500 字、可直接朗读的中文解说/旁白文案（不是画面描述）。要求：口语化、句子短、有停顿节奏、情绪连贯，适合 TTS 朗读。"
	}
}

func buildUserPrompt(kind, seed string, refs []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "创作目的/关键词：%s\n\n可参考的素材（仅供风格与方向参考，禁止照抄原句）：\n", seed)
	for i, r := range refs {
		fmt.Fprintf(&b, "%d. %s\n", i+1, r)
	}
	fmt.Fprintf(&b, "\n请围绕「%s」重新创作一段全新的内容：", seed)
	switch kind {
	case "audio":
		b.WriteString("一段可直接朗读的旁白/解说文案。")
	case "image":
		b.WriteString("一段可直接用于文生图的画面提示词。")
	default:
		b.WriteString("一段可直接用于文生视频的分镜化提示词。")
	}
	return b.String()
}

// ============================================================
// 计费估算（与 Mac TokenEstimator.estimateProTotal 同口径）
// ============================================================

type proEstimate struct {
	Amount   string `json:"amount"`   // 已含「预估金额 ≈ 」前缀，直接给确认弹窗用
	TokenMin int    `json:"tokenMin"`
	TokenMax int    `json:"tokenMax"`
	Detail   string `json:"detail"`
	Total    int    `json:"totalTokens"`
}

func estimateProTotal(kind string, embedTokens, genTokens int) proEstimate {
	embedAmount := float64(embedTokens) / 1000.0 * embedPricePerK
	genAmount := float64(genInputEstimate)/1000.0*llmInputPerK + float64(genTokens)/1000.0*llmOutputPerK
	total := embedTokens + genTokens

	return proEstimate{
		Amount:   "预估金额 ≈ " + fmtMoney(embedAmount+genAmount),
		TokenMin: int(float64(total) * 0.7),
		TokenMax: int(float64(total) * 1.3),
		Detail: fmt.Sprintf("向量选句 ≈%d token（¥0.000125/千）+ 扩写%s ≤%d token（输入 ¥0.00096/千 · 输出 ¥0.0024/千）",
			embedTokens, kindLabel[kind], genTokens),
		Total: total,
	}
}

// estimateEmbeddingTokens 粗略估算 embedding 输入 token（CJK 按字计、其他按 4 字符 ≈ 1 token）
func estimateEmbeddingTokens(texts []string) int {
	n := 0
	for _, t := range texts {
		cjk, total := 0, 0
		for _, r := range t {
			total++
			if r > 0x2E80 {
				cjk++
			}
		}
		n += cjk + (total-cjk)/4
	}
	if n < 1 {
		n = 1
	}
	return n
}

// ============================================================
// DashScope 调用
// ============================================================

func dashScopePost(key, path string, payload, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", "https://dashscope.aliyuncs.com/api/v1"+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("user-agent", "clipforge/3.2.2")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// embedTexts 阶段一：文本向量化
func embedTexts(key string, texts []string) (vectors [][]float64, tokens int, err error) {
	payload := map[string]any{
		"model":      modelEmbedding,
		"input":      map[string]any{"texts": texts},
		"parameters": map[string]any{"dimension": 1024},
	}
	var resp struct {
		Output struct {
			Embeddings []struct {
				TextIndex int       `json:"text_index"`
				Embedding []float64 `json:"embedding"`
			} `json:"embeddings"`
		} `json:"output"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := dashScopePost(key, "/services/embeddings/text-embedding/text-embedding", payload, &resp); err != nil {
		return nil, 0, err
	}

	list := resp.Output.Embeddings
	// 部分模型不保证返回顺序，按 text_index 归位
	sort.Slice(list, func(i, j int) bool { return list[i].TextIndex < list[j].TextIndex })

	vectors = make([][]float64, 0, len(list))
	for _, e := range list {
		vectors = append(vectors, e.Embedding)
	}
	tokens = resp.Usage.TotalTokens
	if tokens == 0 {
		tokens = estimateEmbeddingTokens(texts)
	}
	return vectors, tokens, nil
}

// generateText 阶段二：文本扩写
func generateText(key, system, user string, maxTokens int, temperature float64) (text string, tokens int, err error) {
	payload := map[string]any{
		"model": modelTextGen,
		"input": map[string]any{
			"messages": []map[string]string{
				{"role": "system", "content": system},
				{"role": "user", "content": user},
			},
		},
		"parameters": map[string]any{
			"result_format": "message",
			"max_tokens":    maxTokens,
			"temperature":   temperature,
		},
	}
	var resp struct {
		Output struct {
			Text    string `json:"text"`
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		} `json:"output"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := dashScopePost(key, "/services/aigc/text-generation/generation", payload, &resp); err != nil {
		return "", 0, err
	}

	// 兼容 output.text 与 output.choices[].message.content 两种返回
	text = strings.TrimSpace(resp.Output.Text)
	if text == "" {
		var sb strings.Builder
		for _, ch := range resp.Output.Choices {
			sb.WriteString(ch.Message.Content)
		}
		text = strings.TrimSpace(sb.String())
	}

	tokens = resp.Usage.TotalTokens
	if tokens == 0 {
		tokens = resp.Usage.InputTokens + resp.Usage.OutputTokens
	}
	if tokens == 0 {
		tokens = estimateEmbeddingTokens([]string{system, user}) + maxTokens
	}
	return text, tokens, nil
}

// ============================================================
// Pro 主流程
// ============================================================

type proResult struct {
	Prompt      string      `json:"prompt"`
	EmbedTokens int         `json:"embedTokens"`
	GenTokens   int         `json:"genTokens"`
	TotalTokens int         `json:"totalTokens"`
	UsedPro     bool        `json:"usedPro"` // false = 降级为普通随机（未计费）
	Refs        []string    `json:"refs"`
	Seed        string      `json:"seed"`
	Estimate    proEstimate `json:"estimate"`
}

func degradedPro(kind string, embedTokens int, refs []string, seed string) proResult {
	return proResult{
		Prompt:      randomPrompt(kind),
		EmbedTokens: embedTokens,
		GenTokens:   0,
		TotalTokens: embedTokens,
		UsedPro:     false,
		Refs:        refs,
		Seed:        seed,
		Estimate:    estimateProTotal(kind, embedTokens, 0),
	}
}

// runLuckyPro 两阶段：embedding 选 top3 参照句 → qwen-plus 扩写 ~500 字
// 任一阶段失败则降级为普通随机（不计费）
func runLuckyPro(key, kind, purpose string) proResult {
	texts := plannedTexts(kind, purpose)
	seed := effectiveSeed(kind, purpose)
	candidates := texts[1:]

	// ---- 阶段 1：语义选句 ----
	vectors, embedTokens, err := embedTexts(key, texts)
	if err != nil {
		return degradedPro(kind, 0, nil, seed)
	}

	var refs []string
	if len(vectors) == len(texts) && len(vectors[0]) > 0 {
		type scored struct {
			text  string
			score float64
		}
		sc := make([]scored, 0, len(candidates))
		for i, c := range candidates {
			sc = append(sc, scored{c, cosine(vectors[0], vectors[i+1])})
		}
		sort.Slice(sc, func(i, j int) bool { return sc[i].score > sc[j].score })
		for i := 0; i < 3 && i < len(sc); i++ {
			refs = append(refs, sc[i].text)
		}
	}
	if len(refs) == 0 {
		for i := 0; i < 3 && i < len(candidates); i++ {
			refs = append(refs, candidates[i])
		}
	}

	// ---- 阶段 2：扩写 ----
	text, genTokens, err := generateText(key,
		systemInstruction(kind), buildUserPrompt(kind, seed, refs), maxGenTokens[kind], 0.9)
	if err != nil || strings.TrimSpace(text) == "" {
		return degradedPro(kind, embedTokens, refs, seed)
	}

	return proResult{
		Prompt:      text,
		EmbedTokens: embedTokens,
		GenTokens:   genTokens,
		TotalTokens: embedTokens + genTokens,
		UsedPro:     true,
		Refs:        refs,
		Seed:        seed,
		Estimate:    estimateProTotal(kind, embedTokens, genTokens),
	}
}

// cosine 余弦相似度
func cosine(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	den := math.Sqrt(na) * math.Sqrt(nb)
	if den == 0 {
		return 0
	}
	return dot / den
}
