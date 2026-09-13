#!/bin/bash
# ClipForge web 逐功能自测(不需要真实 API Key、不产生任何计费调用)
#
# 原理：把服务端的 DashScope 根地址指向本地 mock(CLIPFORGE_DASHSCOPE_BASE)，
#       截获并断言每个功能真正发出去的方法/URL/请求头/请求体，
#       再用与 DashScope 同构的响应驱动完整流程(上传→提交→轮询→下载)。
#
# 用法：bash selftest/run.sh          # 在 clipforge-web 仓库根目录执行
set -u
cd "$(dirname "$0")/.." || exit 1
ROOT=$(pwd)
PY=${PY:-python3}
GO=${GO:-$(command -v go || echo "$HOME/go-sdk/go/bin/go")}
PORT_MOCK=${PORT_MOCK:-8799}
PORT_APP=${PORT_APP:-8731}
WORK=$(mktemp -d)
export CF_MOCK_REC="$WORK/records.jsonl"
pass=0; fail=0

cleanup() {
  for p in "${MOCK_PID:-}" "${APP_PID:-}"; do
    [[ -n "$p" ]] && { kill "$p" 2>/dev/null; wait "$p" 2>/dev/null; }
  done
  rm -rf "$WORK"
}
trap cleanup EXIT

ck() { if [[ "$3" == *"$2"* ]]; then printf '  ✅ %-26s %s\n' "$1" "$(echo "$3" | head -c 110)"; pass=$((pass+1));
       else printf '  ❌ %-26s 期望[%s] 实际=%s\n' "$1" "$2" "$(echo "$3" | head -c 200)"; fail=$((fail+1)); fi; }
req() { curl -s -m 25 "$@"; }
B="http://127.0.0.1:$PORT_APP"

echo "▶ 编译服务端…"
(cd server && "$GO" build -o "$WORK/clipforge" .) || { echo "编译失败"; exit 1; }

echo "▶ 启动 mock 与测试实例…"
"$PY" selftest/mock_dashscope.py "$PORT_MOCK" >"$WORK/mock.log" 2>&1 & MOCK_PID=$!
mkdir -p "$WORK/home"
sleep 1
HOME="$WORK/home" CLIPFORGE_DASHSCOPE_BASE="http://127.0.0.1:$PORT_MOCK/api/v1" "$WORK/clipforge" --web >"$WORK/app.log" 2>&1 & APP_PID=$!
sleep 2
curl -s -m 5 "$B/api/config" >/dev/null || { echo "服务未起来，见 $WORK/app.log"; cat "$WORK/app.log"; exit 1; }

echo "=== 1. 设置页：保存 / 读取 ==="
ck "保存 Key" '{"ok":true}' "$(req -X POST $B/api/config -H 'Content-Type: application/json' -d '{"apiKey":"sk-selftest"}')"
ck "读取状态" '"configured":true' "$(req $B/api/config)"

echo "=== 2. 上传（声音克隆/参考图共用）：现行协议 + 字段集 ==="
printf 'MOCKPNGBYTES' > "$WORK/mock.png"
UP=$(req -F "file=@$WORK/mock.png;filename=mock.png" -F "model=cosyvoice-v3.5-plus" "$B/api/upload")
ck "返回 oss:// 资源" 'oss://dashscope-instant/mock/dir/mock.png' "$UP"
GP=$("$PY" -c "
import json
r=[json.loads(l) for l in open('$CF_MOCK_REC') if l.strip()]
g=[x for x in r if x['kind']=='getPolicy']
print(g[-1]['method']+' '+g[-1]['query'] if g else 'NONE')")
ck "取凭证 GET+getPolicy" 'GET action=getPolicy&model=cosyvoice-v3.5-plus' "$GP"
OSSF=$("$PY" -c "
import json,re
r=[json.loads(l) for l in open('$CF_MOCK_REC') if l.strip()]
o=[x for x in r if x['kind']=='ossUpload'][-1]['raw']
print(','.join(re.findall(r'name=\"([^\"]+)\"',o)))")
ck "OSS multipart 字段集" 'OSSAccessKeyId,Signature,policy,key,x-oss-object-acl,x-oss-forbid-overwrite,success_action_status,x-oss-content-type,file' "$OSSF"

echo "=== 3. 视频工坊：提交（关键：异步头）/ 查询 / 下载 ==="
VBODY='{"model":"wan2.6-i2v","input":{"prompt":"一只猫","img_url":"oss://dashscope-instant/mock/dir/mock.png"},"parameters":{"resolution":"720P","prompt_extend":true,"duration":5,"shot_type":"multi"}}'
ck "提交返回 task_id" 'mock-task-1' "$(req -X POST $B/api/video/submit -H 'Content-Type: application/json' -d "$VBODY")"
VH=$("$PY" -c "
import json
r=[json.loads(l) for l in open('$CF_MOCK_REC') if l.strip()]
v=[x for x in r if x['kind']=='post' and 'video-synthesis' in x['path']][-1]
print('async='+str(v['hdr']['X-DashScope-Async'])+' ossResolve='+str(v['hdr']['X-DashScope-OssResourceResolve']))")
ck "带 X-DashScope-Async" 'async=enable ossResolve=enable' "$VH"
ck "任务查询 SUCCEEDED" '"task_status": "SUCCEEDED"' "$(req "$B/api/video/task?taskId=mock-task-1")"
ck "视频下载转发" '200 video/mp4' "$(req -o "$WORK/v.mp4" -w '%{http_code} %{content_type}' "$B/api/video/download?url=http://127.0.0.1:$PORT_MOCK/v.mp4")"

echo "=== 4. 声音工坊：创建 / 查询 / 列表 / 删除 ==="
ck "创建音色" 'mock-voice-new' "$(req -X POST $B/api/voice/create -H 'Content-Type: application/json' -d '{"model":"voice-enrollment","input":{"action":"create_voice","target_model":"cosyvoice-v3.5-plus","prefix":"myvoice","url":"oss://x/y.wav"}}')"
ck "查询音色状态" '"status": "OK"' "$(req -X POST $B/api/voice/list -H 'Content-Type: application/json' -d '{"model":"voice-enrollment","input":{"action":"query_voice","voice_id":"mock-voice-new"}}')"
ck "音色列表" 'mock-voice-a' "$(req -X POST $B/api/voice/list -H 'Content-Type: application/json' -d '{"model":"voice-enrollment","input":{"action":"list_voice","prefix":"","page_index":0,"page_size":50}}')"
ck "删除音色" '"output"' "$(req -X POST $B/api/voice/delete -H 'Content-Type: application/json' -d '{"model":"voice-enrollment","input":{"action":"delete_voice","voice_id":"mock-voice-a"}}')"

echo "=== 5. 图片工坊：文生图 ==="
ck "返回图片 URL" "http://127.0.0.1:$PORT_MOCK/i.png" "$(req -X POST $B/api/image -H 'Content-Type: application/json' -d '{"model":"qwen-image-2.0","input":{"messages":[{"role":"user","content":[{"text":"一只猫"}]}]},"parameters":{"size":"1024*1024","n":1,"prompt_extend":true,"watermark":false}}')"

echo "=== 6. 试试手气：免费词库 / Pro 预估 / Pro 两阶段 ==="
ck "免费词库" '"image"' "$(req $B/api/lucky/themes)"
ck "预估(本地)" '"candidates"' "$(req -X POST $B/api/lucky/plan -H 'Content-Type: application/json' -d '{"kind":"image","purpose":"赛博都市"}')"
ck "Pro 走通两阶段" '"usedPro":true' "$(req -X POST $B/api/lucky/pro -H 'Content-Type: application/json' -d '{"kind":"image","purpose":"赛博都市"}')"

echo "=== 7. 账本：写 / 读 / 改 / 导出 / 清空 ==="
ck "写入" '"id"' "$(req -X POST $B/api/bill -H 'Content-Type: application/json' -d '{"id":"t1","action":"文生图","model":"qwen-image-2.0","summary":"测试","unitName":"张","unitCount":1,"amountText":"¥0.20","status":"已提交"}')"
ck "读取含记录" '"t1"' "$(req $B/api/bill)"
ck "回填状态" '"ok":true' "$(req -X PUT $B/api/bill -H 'Content-Type: application/json' -d '{"id":"t1","status":"成功"}')"
ck "导出 CSV(BOM)" '时间,操作,模型' "$(req $B/api/bill/export)"
ck "清空" '"ok":true' "$(req -X DELETE $B/api/bill)"

echo "=== 8. 边界：未注册接口 / 方法错误 / 未配置 Key ==="
ck "未注册接口 → JSON 404" '{"error":"unknown api:' "$(req -X POST $B/api/nope -d '{}')"
ck "上传 GET → JSON 405" '{"error":"method not allowed"}' "$(req $B/api/upload)"
ck "未配置 Key → 401" '{"error":"未设置 API Key' "$(req -X POST $B/api/config -H 'Content-Type: application/json' -d '{"apiKey":""}'; req -X POST $B/api/image -H 'Content-Type: application/json' -d '{}')"

echo
echo "==== 通过 $pass / 失败 $fail ===="
[[ $fail -eq 0 ]] || exit 1
