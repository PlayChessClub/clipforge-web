// ClipForge 前端逻辑 - 原生 JS,无框架
// 功能对齐 Mac v1.6.x:声音克隆/语音合成(WS)/文生图/视频生成(多模型)/账本/价目/确认弹窗/随机prompt
// 调后端 /api/* 端点,API Key 只在本地 server,前端不接触 Key

const $ = (s) => document.querySelector(s);
const $$ = (s) => document.querySelectorAll(s);

// ==================== 标签页切换 ====================
$$('.tab').forEach(btn => {
  btn.addEventListener('click', () => {
    $$('.tab').forEach(b => b.classList.remove('active'));
    $$('.panel').forEach(p => p.classList.remove('active'));
    btn.classList.add('active');
    document.getElementById(btn.dataset.tab).classList.add('active');
  });
});

// ==================== 工具函数 ====================
function setStatus(el, text, kind) {
  el.textContent = text || '';
  el.className = 'status' + (kind ? ' ' + kind : '');
}

async function api(method, path, body, isForm) {
  const opts = { method, headers: {} };
  if (body && !isForm) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  } else if (body && isForm) {
    opts.body = body;  // FormData,浏览器自动设 Content-Type
  }
  const r = await fetch(path, opts);
  if (!r.ok) {
    let msg = 'HTTP ' + r.status;
    try { const j = await r.json(); if (j.error) msg = j.error; } catch {}
    throw new Error(msg);
  }
  const ct = r.headers.get('content-type') || '';
  if (ct.includes('application/json')) return await r.json();
  return await r.text();
}

async function uploadFile(file) {
  const fd = new FormData();
  fd.append('file', file);
  return await api('POST', '/api/upload', fd, true);
}

// ==================== 费用确认弹窗 ====================
// 用法: const ok = await confirmCost('文生图', {amount:'预估金额 ≈ ¥0.20', detail:'qwen-image-2.0 · 1024*1024 · 1 张'});
function confirmCost(title, { amount, detail }) {
  return new Promise((resolve) => {
    $('#confirm-title').textContent = `确认生成 · ${title}`;
    $('#confirm-amount').textContent = amount;
    $('#confirm-detail').textContent = detail;
    const ov = $('#confirm-overlay');
    ov.hidden = false;
    const done = (v) => { ov.hidden = true; cleanup(); resolve(v); };
    const onOk = () => done(true), onCancel = () => done(false);
    function cleanup() {
      $('#confirm-ok').removeEventListener('click', onOk);
      $('#confirm-cancel').removeEventListener('click', onCancel);
    }
    $('#confirm-ok').addEventListener('click', onOk);
    $('#confirm-cancel').addEventListener('click', onCancel);
  });
}

// ==================== 费用估算(与 Mac TokenEstimator 同口径) ====================
const VIDEO_RATES = { // 元/秒 [720P有声, 1080P有声, 720P无声, 1080P无声]
  'wan2.6-i2v': [0.6, 1.0, 0.6, 1.0], 'wan2.7-i2v': [0.6, 1.0, 0.6, 1.0],
  'wan2.6-t2v': [0.6, 1.0, 0.6, 1.0], 'wan2.7-t2v': [0.6, 1.0, 0.6, 1.0],
  'wan2.6-i2v-flash': [0.3, 0.5, 0.15, 0.25],
};
const fmtMoney = (v) => '¥' + v.toFixed(2);
const fmtToken = (n) => {
  if (n >= 10000) return (n / 10000).toFixed(1) + '万';
  return String(n);
};

function estVideo(model, res, dur, audio) {
  const t = VIDEO_RATES[model] || [0.6, 1.0, 0.6, 1.0];
  const rate = t[(res === '1080P' ? 1 : 0) + (audio ? 0 : 2)];
  const token = Math.round(120000 * rate / 0.6) * dur;
  return {
    amount: fmtMoney(rate * dur),
    tokens: [Math.round(token * 0.7), Math.round(token * 1.3)],
    detail: `${res} · ${dur}s · ${audio ? '有声' : '无声'} · ¥${rate}/秒`,
  };
}
function estImage(model, n) {
  const rate = model.endsWith('-pro') ? 0.5 : 0.2;
  const token = 120000 * n;
  return { amount: fmtMoney(rate * n), tokens: [Math.round(token * 0.7), Math.round(token * 1.3)],
           detail: `${model} · ${n} 张 · ¥${rate}/张` };
}
function estTTS(text) {
  const chars = [...text].length;
  const amount = chars / 10000 * 1.5;
  const token = Math.round(chars / 10000 * 15000);
  return { amount: amount >= 0.005 ? fmtMoney(amount) : '<¥0.01',
           tokens: [Math.round(token * 0.7), Math.round(token * 1.3)],
           detail: `输入 ${chars} 字符 · ¥1.5/万字符` };
}

// ==================== 账本 ====================
async function addBill(entry) {
  try { await api('POST', '/api/bill', entry); } catch (e) { console.warn('记账失败', e); }
}
async function updateBill(id, patch) {
  try { await api('PUT', '/api/bill', { ...patch, id }); } catch (e) { console.warn(e); }
}

async function loadBill() {
  try {
    const { entries } = await api('GET', '/api/bill');
    const now = new Date();
    const sameDay = (t) => { const d = new Date(t); return d.toDateString() === now.toDateString(); };
    const sameMonth = (t) => { const d = new Date(t); return d.getFullYear() === now.getFullYear() && d.getMonth() === now.getMonth(); };
    const sum = (list) => list.reduce((acc, e) => {
      const m = parseFloat((e.amountText || '').replace(/[^0-9.]/g, ''));
      return acc + (isNaN(m) ? 0 : m);
    }, 0);
    $('#sum-today').textContent = fmtMoney(sum(entries.filter(e => sameDay(e.time))));
    $('#sum-month').textContent = fmtMoney(sum(entries.filter(e => sameMonth(e.time))));
    $('#sum-all').textContent = fmtMoney(sum(entries));

    const tb = $('#bill-table tbody');
    tb.innerHTML = entries.slice(0, 200).map(e => `
      <tr>
        <td>${new Date(e.time).toLocaleString('zh-CN', { hour12: false })}</td>
        <td>${e.action}</td><td>${e.model}</td>
        <td class="clip" title="${e.summary}">${e.summary}</td>
        <td>${e.unitCount}${e.unitName}</td>
        <td>${e.amountText}</td>
        <td class="clip">${e.taskId || ''}</td>
        <td>${e.status}</td>
      </tr>`).join('') || '<tr><td colspan="8" style="text-align:center">暂无记录</td></tr>';
  } catch (e) { console.warn(e); }
}

$('#bill-refresh').addEventListener('click', loadBill);
$('#bill-clear').addEventListener('click', async () => {
  if (!confirm('确定清空全部账本记录?')) return;
  await api('DELETE', '/api/bill');
  loadBill();
});

// ==================== 随机提示词库 ====================
const PROMPT_BANK = {
  video: [
    '一个由喷漆画成的少年从混凝土墙上活过来，边 rap 边摆出充满活力的说唱姿势，夜晚铁路桥下，街灯孤照，电影感氛围。',
    '一只毛茸茸的小猫戴着宇航员头盔，漂浮在失重的空间站里，慢镜头，柔和灯光。',
    '雨夜霓虹都市，一名撑红伞的女子走过湿漉漉的街道，倒影斑斓，赛博朋克风格。',
    '俯拍视角，沙漠中一辆复古吉普扬尘飞驰，夕阳把沙丘染成金色，长焦压缩感。',
    '一间深夜食堂，热气腾腾的拉面，镜头缓缓推近，蒸汽在暖黄灯光下袅袅升起。',
    '海浪拍打礁石，海鸥掠过，慢动作水花四溅，清晨薄雾，电影感自然光。',
    '一只机械蝴蝶停在一朵盛开的金属花上，特写微距，齿轮转动，蒸汽朋克。',
    '雪山之巅，登山者插下旗帜，风吹雪雾，逆光剪影，史诗感构图。',
    '热闹的夜市摊档，铁板烧师傅翻炒食材，火焰腾起，升格慢镜头。',
    '清晨森林，一束阳光穿透树冠，光柱中尘埃飞舞，镜头缓缓上摇。',
  ],
  image: [
    '一只戴墨镜的柯基犬坐在海滩上，身边放着椰子，阳光明媚，插画风格。',
    '未来主义摩天楼群，悬浮列车穿梭，紫色与青色霓虹，赛博朋克城市。',
    '水彩画，宁静的江南水乡，白墙黛瓦，小桥流水，清晨薄雾。',
    '一只巨大的鲸鱼在云海中游弋，天空之城，梦幻超现实主义。',
    '特写：一杯拉花拿铁，木质桌面，暖色侧光，商业摄影质感。',
    '宫崎骏风格的田园小屋，绿草如茵，蓝天白云，远处风车转动。',
    '一只发光的水母在深海中漂浮，蓝色荧光，神秘幽深。',
    '像素风 8-bit 游戏场景，勇士站在城堡前，勇者斗恶龙。',
    '极简主义海报，一只红色气球飘向天空，大量留白，高级灰背景。',
    '冬日雪景，红色小木屋烟囱冒烟，松树挂雪，温馨童话感。',
  ],
  random: (arr) => arr[Math.floor(Math.random() * arr.length)],
};
$('#video-lucky').addEventListener('click', () => { $('#video-prompt').value = PROMPT_BANK.random(PROMPT_BANK.video); });
$('#image-lucky').addEventListener('click', () => { $('#image-prompt').value = PROMPT_BANK.random(PROMPT_BANK.image); });

// ==================== 设置页 ====================
async function loadSettings() {
  try {
    const c = await api('GET', '/api/config');
    const el = $('#settings-status');
    if (c.configured) {
      el.textContent = '✓ 已配置 API Key(明文存储在本地)';
      el.className = 'hint';
    }
  } catch (e) { console.warn(e); }
}

$('#settings-save').addEventListener('click', async () => {
  const key = $('#settings-key').value.trim();
  if (!key) { setStatus($('#settings-status'), '请先输入 API Key', 'warn'); return; }
  try {
    await api('POST', '/api/config', { apiKey: key });
    setStatus($('#settings-status'), '✓ 已保存到本地', 'ok');
    $('#settings-key').value = '';
  } catch (e) {
    setStatus($('#settings-status'), '保存失败: ' + e.message, 'err');
  }
});

$('#settings-clear').addEventListener('click', async () => {
  try {
    await api('POST', '/api/config', { apiKey: '' });
    setStatus($('#settings-status'), '已清除', 'ok');
  } catch (e) {
    setStatus($('#settings-status'), '清除失败: ' + e.message, 'err');
  }
});

// ==================== 价目表 ====================
const PRICE = {
  video: [
    ['wan2.6-t2v', '文生视频', '720P ¥0.6 · 1080P ¥1.0 元/秒'],
    ['wan2.7-t2v', '文生视频', '720P ¥0.6 · 1080P ¥1.0 元/秒'],
    ['wan2.6-i2v', '图生视频', '720P ¥0.6 · 1080P ¥1.0 元/秒'],
    ['wan2.7-i2v', '图生视频', '720P ¥0.6 · 1080P ¥1.0 元/秒'],
    ['wan2.6-i2v-flash', '图生视频·Flash', '有声 0.3/0.5 · 无声 0.15/0.25 元/秒'],
  ],
  image: [
    ['qwen-image-2.0', '文生图', '¥0.20 元/张'],
    ['qwen-image-2.0-pro', '文生图', '¥0.50 元/张'],
    ['wan2.7-image', '文生图', '¥0.20 元/张'],
    ['wan2.7-image-pro', '文生图', '¥0.50 元/张'],
  ],
  audio: [
    ['cosyvoice-v3.5-plus', '语音合成', '¥1.50 元/万字符'],
    ['voice-enrollment', '声音克隆', '随训练/合成出账'],
  ],
};
function renderPrice() {
  const sec = (title, rows) => `
    <h3 class="price-title">${title}</h3>
    ${rows.map(r => `<div class="price-row"><span class="price-model">${r[0]}</span><span>${r[1]}</span><span class="price-num">${r[2]}</span></div>`).join('')}`;
  $('#price-list').innerHTML =
    sec('视频生成', PRICE.video) + sec('图片生成', PRICE.image) + sec('语音', PRICE.audio);
}

// ==================== 声音工坊(克隆) ====================
let _cloneFile = null;

$('#voice-pick').addEventListener('click', () => $('#voice-file').click());
$('#voice-file').addEventListener('change', (e) => {
  _cloneFile = e.target.files[0];
  $('#voice-filename').textContent = _cloneFile ? _cloneFile.name : '未选择';
});

$('#voice-clone').addEventListener('click', async () => {
  if (!_cloneFile) { setStatus($('#voice-status'), '请先选择参考音频', 'warn'); return; }
  const prefix = $('#voice-prefix').value.trim() || 'myvoice';
  const ok = await confirmCost('声音克隆', {
    amount: '此操作将产生费用,随出账扣除(约 ¥0.3–¥2)',
    detail: 'voice-enrollment · 费用随样本训练与首次合成出账,金额波动较大',
  });
  if (!ok) return;
  setStatus($('#voice-status'), '正在上传音频到 OSS…', 'warn');
  try {
    const up = await uploadFile(_cloneFile);
    setStatus($('#voice-status'), '已上传,提交克隆任务…', 'warn');
    const create = await api('POST', '/api/voice/create', {
      model: 'voice-enrollment',
      input: { action: 'create_voice', target_model: 'cosyvoice-v3.5-plus', prefix, url: up.resource }
    });
    const voiceId = create.output.voice_id;
    const billId = 'clone-' + Date.now();
    addBill({ id: billId, action: '声音克隆', model: 'voice-enrollment', summary: '音色前缀 ' + prefix,
              unitName: '次', unitCount: 1, tokenMin: 0, tokenMax: 0,
              amountText: '随出账波动(约 ¥0.3–¥2)', detail: '费用随样本训练与首次合成出账', taskId: voiceId });
    setStatus($('#voice-status'), `已提交,voice_id = ${voiceId},开始轮询…`, 'warn');

    for (let i = 0; i < 30; i++) {
      await new Promise(r => setTimeout(r, 10000));
      const q = await api('POST', '/api/voice/list', {
        model: 'voice-enrollment',
        input: { action: 'query_voice', voice_id: voiceId }
      });
      const status = q.output?.status;
      setStatus($('#voice-status'), `轮询 #${i + 1}: status = ${status}`, 'warn');
      if (status === 'OK') {
        setStatus($('#voice-status'), `✅ 克隆成功:${voiceId}`, 'ok');
        updateBill(billId, { status: '成功' });
        loadVoices(); loadTTSVoices();
        return;
      }
      if (status === 'UNDEPLOYED') {
        setStatus($('#voice-status'), '❌ 克隆失败(音频质量不达标)', 'err');
        updateBill(billId, { status: '失败' });
        return;
      }
    }
    setStatus($('#voice-status'), '⏱️ 轮询超时(5 分钟),请稍后在音色列表查看', 'err');
  } catch (e) {
    setStatus($('#voice-status'), '❌ ' + e.message, 'err');
  }
});

async function loadVoices() {
  const sel = $('#voice-list');
  sel.innerHTML = '<option>— 加载中 —</option>';
  try {
    const prefix = $('#voice-prefix').value.trim() || 'myvoice';
    const r = await api('POST', '/api/voice/list', {
      model: 'voice-enrollment',
      input: { action: 'list_voice', prefix, page_index: 0, page_size: 50 }
    });
    const voices = r.output?.voice_list || [];
    if (!voices.length) { sel.innerHTML = '<option>— 暂无 —</option>'; return; }
    sel.innerHTML = voices.map(v => `<option value="${v.voice_id}">${v.voice_id}</option>`).join('');
  } catch (e) {
    sel.innerHTML = '<option>— 加载失败 —</option>';
    setStatus($('#voice-status'), '加载音色失败: ' + e.message, 'err');
  }
}
$('#voice-refresh').addEventListener('click', loadVoices);

// ==================== 语音合成(WebSocket 全双工,协议与 Mac CosyVoiceTTS 一致) ====================
function synthTTS({ voiceId, text, rate, volume, pitch }) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(`ws://${location.host}/api/tts/ws`);
    const taskId = (crypto.randomUUID ? crypto.randomUUID() : String(Date.now())).replace(/-/g, '');
    const chunks = [];
    let done = false;
    const timer = setTimeout(() => finish(new Error('合成超时(120s)且未收到完整音频')), 120000);

    function finish(err, blob) {
      if (done) return;
      done = true;
      clearTimeout(timer);
      try { ws.close(); } catch {}
      err ? reject(err) : resolve(blob);
    }

    ws.onopen = () => {
      ws.send(JSON.stringify({
        header: { action: 'run-task', task_id: taskId, streaming: 'duplex' },
        payload: {
          model: 'cosyvoice-v3.5-plus', task_group: 'audio', task: 'tts',
          function: 'SpeechSynthesizer', input: {},
          parameters: { voice: voiceId, volume, text_type: 'PlainText',
                        sample_rate: 22050, rate, format: 'mp3', pitch, seed: 0, type: 0, enable_ssml: true },
        },
      }));
    };
    ws.onmessage = (ev) => {
      if (ev.data instanceof Blob) { chunks.push(ev.data); return; }
      let j;
      try { j = JSON.parse(ev.data); } catch { return; }
      const event = j.header?.event;
      if (event === 'task-started') {
        ws.send(JSON.stringify({
          header: { action: 'continue-task', task_id: taskId, streaming: 'duplex' },
          payload: { model: 'cosyvoice-v3.5-plus', task_group: 'audio', task: 'tts',
                     function: 'SpeechSynthesizer', input: { text } },
        }));
        ws.send(JSON.stringify({
          header: { action: 'finish-task', task_id: taskId, streaming: 'duplex' },
          payload: { input: {} },
        }));
      } else if (event === 'task-finished') {
        if (!chunks.length) return finish(new Error('合成返回空音频'));
        finish(null, new Blob(chunks, { type: 'audio/mpeg' }));
      } else if (event === 'task-failed') {
        finish(new Error(`${j.header?.error_code || ''} ${j.header?.error_message || '合成失败'}`));
      }
    };
    ws.onerror = () => finish(new Error('WebSocket 连接失败(请检查 API Key)'));
    ws.onclose = () => { if (!done && !chunks.length) finish(new Error('连接被关闭')); };
  });
}

function loadTTSVoices() {
  const sel = $('#tts-voice');
  api('POST', '/api/voice/list', {
    model: 'voice-enrollment',
    input: { action: 'list_voice', prefix: '', page_index: 0, page_size: 50 },
  }).then(r => {
    const voices = r.output?.voice_list || [];
    sel.innerHTML = voices.length
      ? voices.map(v => `<option value="${v.voice_id}">${v.voice_id}</option>`).join('')
      : '<option value="">— 暂无克隆音色,请先到声音工坊克隆 —</option>';
  }).catch(() => { sel.innerHTML = '<option value="">— 加载失败 —</option>'; });
}
$('#tts-refresh').addEventListener('click', loadTTSVoices);

['tts-rate', 'tts-volume', 'tts-pitch'].forEach(id => {
  $('#' + id).addEventListener('input', (e) => {
    $(`#${id}-v`).textContent = e.target.value;
  });
});

$('#tts-go').addEventListener('click', async () => {
  const text = $('#tts-text').value.trim();
  const voiceId = $('#tts-voice').value;
  if (!text) { setStatus($('#tts-status'), '请输入合成文本', 'warn'); return; }
  if (!voiceId) { setStatus($('#tts-status'), '没有可用音色,请先克隆', 'warn'); return; }
  const est = estTTS(text);
  const ok = await confirmCost('语音合成', { amount: est.amount, detail: est.detail });
  if (!ok) return;
  const billId = 'tts-' + Date.now();
  addBill({ id: billId, action: '语音合成', model: 'cosyvoice-v3.5-plus',
            summary: text.slice(0, 60), unitName: '字符', unitCount: [...text].length,
            tokenMin: est.tokens[0], tokenMax: est.tokens[1],
            amountText: est.amount, detail: est.detail });
  setStatus($('#tts-status'), 'WebSocket 合成中…', 'warn');
  try {
    const blob = await synthTTS({
      voiceId, text,
      rate: parseFloat($('#tts-rate').value),
      volume: parseInt($('#tts-volume').value, 10),
      pitch: parseFloat($('#tts-pitch').value),
    });
    const url = URL.createObjectURL(blob);
    $('#tts-result').innerHTML = `
      <audio controls src="${url}"></audio>
      <a class="btn" href="${url}" download="clipforge-tts-${Date.now()}.mp3">下载 mp3 (${(blob.size / 1024).toFixed(0)} KB)</a>`;
    setStatus($('#tts-status'), '✅ 合成完成', 'ok');
    updateBill(billId, { status: '成功' });
  } catch (e) {
    setStatus($('#tts-status'), '❌ ' + e.message, 'err');
    updateBill(billId, { status: '失败' });
  }
});

// ==================== 图片工坊 ====================
$('#image-go').addEventListener('click', async () => {
  const prompt = $('#image-prompt').value.trim();
  if (!prompt) { setStatus($('#image-status'), '请输入图片描述', 'warn'); return; }
  const model = $('#image-model').value;
  const size = $('#image-size').value;
  const n = parseInt($('#image-count').value, 10);
  const est = estImage(model, n);
  const ok = await confirmCost('文生图', { amount: est.amount, detail: est.detail });
  if (!ok) return;
  const billId = 'img-' + Date.now();
  addBill({ id: billId, action: '文生图', model, summary: prompt.slice(0, 60),
            unitName: '张', unitCount: n, tokenMin: est.tokens[0], tokenMax: est.tokens[1],
            amountText: est.amount, detail: est.detail });
  setStatus($('#image-status'), '提交生成请求…', 'warn');
  try {
    const resp = await api('POST', '/api/image', {
      model,
      input: { messages: [{ role: 'user', content: [{ text: prompt }] }] },
      parameters: { size, n, prompt_extend: $('#image-extend').checked, watermark: false },
    });
    // 兼容两种响应结构
    let urls = [];
    const content = resp.output?.choices?.[0]?.message?.content;
    if (Array.isArray(content)) urls = content.filter(c => c.image).map(c => c.image);
    if (!urls.length && Array.isArray(resp.output?.results)) urls = resp.output.results.map(c => c.url).filter(Boolean);
    if (!urls.length) throw new Error('响应中没有图片 url');
    setStatus($('#image-status'), `已生成 ${urls.length} 张`, 'ok');
    $('#image-result').innerHTML = urls.map(u => `
      <div class="img-item">
        <img src="${u}" alt="">
        <a class="btn" href="${u}" target="_blank" download>下载原图</a>
      </div>`).join('');
    updateBill(billId, { status: '成功' });
  } catch (e) {
    setStatus($('#image-status'), '❌ ' + e.message, 'err');
    updateBill(billId, { status: '失败' });
  }
});

// ==================== 视频工坊 ====================
let _videoImage = null, _videoAudio = null;

const isT2V = () => $('#video-model').value.endsWith('-t2v');
const isFlash = () => $('#video-model').value.endsWith('-flash');

$('#video-model').addEventListener('change', () => {
  $('#video-image-block').hidden = isT2V();   // 文生视频不需要首帧图
  $('#video-audio-block').hidden = !isFlash(); // 仅 Flash 有有声/无声价差
});

$('#video-pick-image').addEventListener('click', () => $('#video-image').click());
$('#video-image').addEventListener('change', (e) => {
  _videoImage = e.target.files[0];
  $('#video-image-name').textContent = _videoImage ? _videoImage.name : '未选择';
});
$('#video-pick-audio').addEventListener('click', () => $('#video-audio').click());
$('#video-audio').addEventListener('change', (e) => {
  _videoAudio = e.target.files[0];
  $('#video-audio-name').textContent = _videoAudio ? _videoAudio.name : '未选择';
});

$('#video-submit').addEventListener('click', async () => {
  const prompt = $('#video-prompt').value.trim();
  if (!prompt) { setStatus($('#video-status'), '请输入提示词', 'warn'); return; }
  const model = $('#video-model').value;
  if (!isT2V() && !_videoImage) { setStatus($('#video-status'), '图生视频需要选择首帧参考图(或改用 t2v 模型)', 'warn'); return; }
  const res = $('#video-resolution').value;
  const dur = parseInt($('#video-duration').value, 10);
  const audio = isFlash() ? $('#video-with-audio').checked : !!_videoAudio || false;
  const est = estVideo(model, res, dur, audio);
  const ok = await confirmCost('视频生成', { amount: est.amount, detail: est.detail });
  if (!ok) return;

  setStatus($('#video-status'), '正在上传参考资源(如有)…', 'warn');
  try {
    let imgUrl = '', audioUrl = '';
    if (_videoImage) {
      const up = await uploadFile(_videoImage);
      imgUrl = up.resource;
      setStatus($('#video-status'), '参考图已上传,上传参考音频…', 'warn');
    }
    if (_videoAudio) {
      const up = await uploadFile(_videoAudio);
      audioUrl = up.resource;
    }

    const req = {
      model,
      input: {
        prompt,
        ...(imgUrl && { img_url: imgUrl }),
        ...(audioUrl && { audio_url: audioUrl })
      },
      parameters: {
        resolution: res,
        prompt_extend: $('#video-extend').checked,
        duration: dur,
        shot_type: $('#video-shottype').value,
        ...(isFlash() && { audio: $('#video-with-audio').checked }),
      }
    };
    setStatus($('#video-status'), '提交任务…', 'warn');
    const submit = await api('POST', '/api/video/submit', req);
    const taskId = submit.output.task_id;
    const billId = 'vid-' + Date.now();
    addBill({ id: billId, action: model.endsWith('-t2v') ? '文生视频' : '图生视频', model,
              summary: prompt.slice(0, 60), unitName: '秒', unitCount: dur,
              tokenMin: est.tokens[0], tokenMax: est.tokens[1],
              amountText: est.amount, detail: est.detail, taskId });
    setStatus($('#video-status'), `任务 ${taskId.slice(0, 8)}… 提交成功,开始轮询`, 'warn');

    const deadline = Date.now() + 30 * 60 * 1000;
    while (Date.now() < deadline) {
      await new Promise(r => setTimeout(r, 8000));
      const t = await api('GET', `/api/video/task?taskId=${encodeURIComponent(taskId)}`);
      const status = t.output?.task_status || 'RUNNING';
      setStatus($('#video-status'), `任务 ${taskId.slice(0, 8)}… 状态:${status}`, 'warn');
      if (status === 'SUCCEEDED') {
        const url = t.output.video_url;
        setStatus($('#video-status'), '✅ 生成完成', 'ok');
        updateBill(billId, { status: '成功' });
        $('#video-result').innerHTML = `
          <video controls src="${url}"></video>
          <a href="${url}" target="_blank" download>在新标签页打开</a>`;
        return;
      }
      if (status === 'FAILED' || status === 'CANCELED') {
        const msg = t.output?.message || '未知失败原因';
        setStatus($('#video-status'), '❌ ' + msg, 'err');
        updateBill(billId, { status: '失败' });
        return;
      }
    }
    setStatus($('#video-status'), '⏱️ 轮询超时(30 分钟)', 'err');
  } catch (e) {
    setStatus($('#video-status'), '❌ ' + e.message, 'err');
  }
});

// ==================== 启动 ====================
loadSettings();
loadVoices();
loadTTSVoices();
loadBill();
renderPrice();
$('#video-model').dispatchEvent(new Event('change')); // 初始化显隐
