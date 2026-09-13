#!/usr/bin/env python3
"""ClipForge web 自测用 DashScope mock。
把 ClipForge 发出的每个请求原样记录到 /tmp/cf-mock-records.jsonl，并返回与真实
DashScope 结构一致的响应，用于在「不碰真实 API、不需要真 Key」的前提下验证：
  - 上传是否走现行协议(GET /uploads?action=getPolicy + OSS multipart 字段集)
  - 视频提交是否带 X-DashScope-Async: enable
  - 各端点 URL / 方法 / 请求体是否符合官方文档
"""
import json, os, re, sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

REC = os.environ.get("CF_MOCK_REC") or os.path.join(
    os.path.dirname(os.path.abspath(__file__)), ".records.jsonl")

def rec(kind, **kw):
    with open(REC, "a") as f:
        f.write(json.dumps({"kind": kind, **kw}, ensure_ascii=False) + "\n")

def emb(dim=8):
    return [0.1 * (i + 1) for i in range(dim)]

class H(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def _send(self, code, obj, raw=None, ctype="application/json"):
        body = raw if raw is not None else json.dumps(obj, ensure_ascii=False).encode()
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _read(self):
        n = int(self.headers.get("Content-Length") or 0)
        return self.rfile.read(n) if n else b""

    def do_GET(self):
        if self.path.startswith("/api/v1/uploads"):
            q = self.path.split("?", 1)[1] if "?" in self.path else ""
            rec("getPolicy", method="GET", path=self.path,
                query=q, auth=self.headers.get("Authorization", ""),
                accept=self.headers.get("Accept", ""))
            self._send(200, {
                "request_id": "mock-req-pol",
                "output": {
                    "upload_host": "http://127.0.0.1:8799/oss",
                    "upload_dir": "dashscope-instant/mock/dir",
                    "policy": "MOCK_POLICY_B64",
                    "signature": "MOCK_SIG",
                    "oss_access_key_id": "MOCK_AK_ID",
                    "x_oss_object_acl": "private",
                    "x_oss_forbid_overwrite": "true",
                    "expiration": "2030-01-01T00:00:00Z",
                },
            })
            return
        m = re.match(r"^/api/v1/tasks/(.+)$", self.path)
        if m:
            rec("taskQuery", method="GET", path=self.path, task=m.group(1),
                auth=self.headers.get("Authorization", ""))
            self._send(200, {"request_id": "mock-req-task", "output": {
                "task_id": m.group(1), "task_status": "SUCCEEDED",
                "video_url": "http://127.0.0.1:8799/v.mp4",
                "submit_time": "2026-01-01 00:00:00", "scheduled_time": "2026-01-01 00:00:00",
                "end_time": "2026-01-01 00:01:00"}})
            return
        if self.path == "/v.mp4":
            self._send(200, None, raw=b"\x00\x00\x00\x18ftypmp42MOCK", ctype="video/mp4")
            return
        rec("unknownGET", method="GET", path=self.path)
        self._send(404, {"code": "NotFound", "message": "mock: no route " + self.path})

    def do_POST(self):
        body = self._read()
        if self.path == "/oss":
            rec("ossUpload", method="POST", path=self.path,
                ctype=self.headers.get("Content-Type", ""),
                accept=self.headers.get("Accept", ""),
                raw=body.decode("utf-8", "replace"))
            self._send(200, None, raw=b"", ctype="text/plain")
            return
        try:
            js = json.loads(body or b"{}")
        except Exception:
            js = {"__raw__": body.decode("utf-8", "replace")}
        rec("post", method="POST", path=self.path,
            auth=self.headers.get("Authorization", ""),
            hdr={"X-DashScope-Async": self.headers.get("X-DashScope-Async"),
                 "X-DashScope-OssResourceResolve": self.headers.get("X-DashScope-OssResourceResolve"),
                 "Content-Type": self.headers.get("Content-Type")},
            body=js)

        if self.path == "/api/v1/uploads":
            # 旧协议(POST get_policy)：真实环境已下线 → 这里也回 405，用于证明兜底路径
            self._send(405, {"code": "MethodNotAllowed"}, raw=json.dumps({"code": "MethodNotAllowed"}).encode())
            return
        if "/video-generation/video-synthesis" in self.path:
            self._send(200, {"request_id": "mock-req-vid", "output": {"task_id": "mock-task-1", "task_status": "PENDING"}})
            return
        if "/audio/tts/customization" in self.path:
            act = (js.get("input") or {}).get("action")
            if act == "list_voice":
                self._send(200, {"output": {"voice_list": [{"voice_id": "mock-voice-a", "status": "OK"}]}})
            elif act == "query_voice":
                self._send(200, {"output": {"status": "OK", "voice_id": (js.get("input") or {}).get("voice_id")}})
            elif act == "delete_voice":
                self._send(200, {"output": {}})
            else:
                self._send(200, {"output": {"voice_id": "mock-voice-new"}})
            return
        if "/multimodal-generation/generation" in self.path:
            self._send(200, {"output": {"choices": [{"message": {"content": [
                {"image": "http://127.0.0.1:8799/i.png"}]}}]}})
            return
        if "/text-embedding/" in self.path:
            texts = ((js.get("input") or {}).get("texts") or [])
            self._send(200, {"output": {"embeddings": [
                {"text_index": i, "embedding": emb()} for i in range(len(texts))]},
                "usage": {"total_tokens": 333}})
            return
        if "/text-generation/generation" in self.path:
            self._send(200, {"output": {"text": "MOCK 扩写结果：" + "字" * 40},
                             "usage": {"input_tokens": 100, "output_tokens": 200, "total_tokens": 300}})
            return
        rec("unknownPOST", path=self.path)
        self._send(404, {"code": "NotFound", "message": "mock: no route " + self.path})


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 8799
    open(REC, "w").close()
    ThreadingHTTPServer(("127.0.0.1", port), H).serve_forever()
