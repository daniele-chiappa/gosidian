#!/usr/bin/env python3
"""A fake OpenAI-compatible endpoint for the smoke test of the presets.

/v1/embeddings gives each text a vector of its words, hashed into DIMS
numbers and made length 1, so that a question and the paragraph that
shares its words are close. /v1/chat/completions answers with the first
line of source [1] of the prompt and cites it, which is what the test
checks: the answer comes from the documents, through the pipelines.
Standard library only; no model, no network.
"""
import hashlib
import json
import math
import os
import re
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

DIMS = int(os.environ.get("FAKE_DIMS", "256"))


def embed(text):
    v = [0.0] * DIMS
    for w in re.findall(r"[a-z0-9]+", text.lower()):
        if len(w) < 3:
            continue
        h = int.from_bytes(hashlib.sha256(w.encode()).digest()[:4], "big")
        v[h % DIMS] += 1.0
    n = math.sqrt(sum(x * x for x in v)) or 1.0
    return [x / n for x in v]


def answer(messages):
    prompt = "\n".join(m.get("content") or "" for m in messages if isinstance(m.get("content"), str))
    m = re.search(r"\[1\][^\n]*\n(.*?)(?:\n\n\[2\]|\Z)", prompt, re.S)
    lines = [l.strip() for l in (m.group(1) if m else "").splitlines() if l.strip() and not l.strip().startswith("#")]
    if not lines:
        return "The documents do not say."
    return "From the documents: %s [1]" % lines[0]


class Handler(BaseHTTPRequestHandler):
    def _send(self, code, body):
        b = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def do_GET(self):
        if self.path.rstrip("/") in ("/v1/models", "/health"):
            self._send(200, {"object": "list", "data": [{"id": "fake", "object": "model"}]})
        else:
            self._send(404, {"error": "not found"})

    def do_POST(self):
        n = int(self.headers.get("Content-Length") or 0)
        req = json.loads(self.rfile.read(n) or b"{}")
        if self.path == "/v1/embeddings":
            texts = req.get("input")
            texts = [texts] if isinstance(texts, str) else (texts or [])
            data = [{"object": "embedding", "index": i, "embedding": embed(t)} for i, t in enumerate(texts)]
            tokens = sum(len(t.split()) for t in texts)
            self._send(200, {"object": "list", "data": data, "model": req.get("model") or "fake",
                             "usage": {"prompt_tokens": tokens, "total_tokens": tokens}})
        elif self.path == "/v1/chat/completions":
            text = answer(req.get("messages") or [])
            self._send(200, {"id": "fake", "object": "chat.completion", "model": req.get("model") or "fake",
                             "choices": [{"index": 0, "finish_reason": "stop", "message": {"role": "assistant", "content": text}}],
                             "usage": {"prompt_tokens": 10, "completion_tokens": len(text.split()), "total_tokens": 10 + len(text.split())}})
        else:
            self._send(404, {"error": "not found"})

    def log_message(self, *args):
        pass


if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", int(os.environ.get("PORT", "8090"))), Handler).serve_forever()
