#!/usr/bin/env python3
"""Smoke test of the gosidian presets, base + rag (or base + agents), end
to end.

It copies deploy/presets to a temporary folder (a real .env is never
touched), fills .env with init.sh and a fake OpenAI-compatible endpoint
(test/fake-openai.py), starts the stack under the compose project
gosidian-smoke, and checks what an install goes through:

  - the plugins with a pinned manifest are accepted by the server, and
    plugin accept --pending accepts the database;
  - a project from the rag template, with its pipelines on;
  - an HTML document in its IN folder becomes a note, git sync commits it,
    the cut stores its paragraphs, and a question is answered from them,
    citing the source.

With the argument agents it starts base + agents instead, the fake
endpoint as pi's model, and checks:

  - plugin accept --pending accepts pi, exec, preview and notify, whose
    manifests name the install's files;
  - a team from memory_create_team, whose pipelines load (they name the
    nodes of exec, preview and notify);
  - a task of its role runs on pi, through its proxy to the endpoint,
    and closes with the agent's report.

Then it removes the stack, its volumes and the folder (--keep leaves them).
Needs Docker with Compose, where a bind mount names a local path (CT 202,
a GitHub runner; not a runner that talks to another host's daemon).

  SMOKE_GOSIDIAN_IMAGE  the gosidian image (default: the preset's)
  SMOKE_DATABASE_IMAGE  the database plugin's image (default: the preset's)
  SMOKE_PI_IMAGE        pi's image, with agents (default: the preset's)
  SMOKE_PORT            gosidian's port on the loopback (default 18180)
"""
import json
import os
import secrets
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
PRESETS = os.path.dirname(HERE)
PROJECT = "gosidian-smoke"
PORT = os.environ.get("SMOKE_PORT", "18180")
BASE = "http://127.0.0.1:" + PORT
DOC = """<!doctype html>
<html><head><title>The office coffee machine</title></head><body>
<h1>The office coffee machine</h1>
<p>This procedure covers the espresso machine in the kitchen.</p>
<h2>Faults</h2>
<p>When the red light blinks three times, the brewing unit is jammed: switch the machine off and push the unit back into place.</p>
<h2>Cleaning</h2>
<p>Empty the grounds drawer every evening and rinse it under warm water.</p>
</body></html>
"""


def log(msg):
    print("smoke: " + msg, flush=True)


def run(cmd, cwd, check=True, quiet=False):
    r = subprocess.run(cmd, cwd=cwd, text=True, capture_output=True)
    if check and r.returncode != 0:
        raise SystemExit("smoke: %s failed:\n%s%s" % (" ".join(cmd), r.stdout[-2000:], r.stderr[-2000:]))
    if not quiet and r.stdout.strip():
        print(r.stdout.strip())
    return r


def set_env(path, values):
    lines = open(path).read().splitlines()
    out, seen = [], set()
    for line in lines:
        key = line.split("=", 1)[0]
        if key in values:
            out.append("%s=%s" % (key, values[key]))
            seen.add(key)
        else:
            out.append(line)
    out += ["%s=%s" % (k, v) for k, v in values.items() if k not in seen]
    open(path, "w").write("\n".join(out) + "\n")


def http(method, path, body=None, token=None):
    headers = {"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
    if token:
        headers["Authorization"] = "Bearer " + token
    req = urllib.request.Request(BASE + path, data=json.dumps(body).encode() if body is not None else None, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=120) as r:
            return r.status, r.read().decode(), r.headers
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode(), e.headers
    except OSError:
        # Not up yet: the port refuses or resets the connection.
        return 0, "", {}


def mcp_call(token, tool, args):
    def rpc(body, sid=None):
        headers = {"Content-Type": "application/json", "Accept": "application/json, text/event-stream", "Authorization": "Bearer " + token}
        if sid:
            headers["Mcp-Session-Id"] = sid
        req = urllib.request.Request(BASE + "/mcp", data=json.dumps(body).encode(), headers=headers, method="POST")
        with urllib.request.urlopen(req, timeout=120) as r:
            raw, sid = r.read().decode(), r.headers.get("Mcp-Session-Id") or sid
        msgs = [json.loads(l[5:]) for l in raw.splitlines() if l.startswith("data:") and l[5:].strip()] if raw.startswith(("event:", "data:")) else ([json.loads(raw)] if raw.strip() else [])
        done = [m for m in msgs if "result" in m or "error" in m]
        return (done[-1] if done else None), sid
    _, sid = rpc({"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "smoke", "version": "1"}}})
    rpc({"jsonrpc": "2.0", "method": "notifications/initialized"}, sid)
    res, _ = rpc({"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": {"name": tool, "arguments": args}}, sid)
    r = (res or {}).get("result") or {}
    text = "".join(c.get("text", "") for c in r.get("content", []) if c.get("type") == "text")
    if r.get("isError") or not res or "error" in res:
        raise SystemExit("smoke: %s: %s" % (tool, text or res))
    return json.loads(text) if text.strip().startswith("{") else text


def wait(what, check, timeout=300, every=3):
    end = time.time() + timeout
    while time.time() < end:
        v = check()
        if v:
            return v
        time.sleep(every)
    raise SystemExit("smoke: timed out waiting for " + what)


def admin(compose, work):
    """An MCP token, and a session of the admin with its password changed."""
    tok_out = run(compose + ["exec", "-T", "gosidian", "/gosidian", "token", "create", "--name", "smoke"], work, quiet=True).stdout
    token = next(w for w in tok_out.split() if len(w) >= 40)
    password = run(compose + ["exec", "-T", "gosidian", "cat", "/data/initial-admin-password"], work, quiet=True).stdout.strip()
    st, body, _ = http("POST", "/api/v1/login", {"username": "admin", "password": password})
    session = json.loads(body)["token"]
    new = "smoke-" + secrets.token_urlsafe(12)
    st, body, _ = http("POST", "/api/v1/me/password", {"current_password": password, "new_password": new}, session)
    if st != 200:
        raise SystemExit("smoke: password change: %d %s" % (st, body))
    return token, json.loads(http("POST", "/api/v1/login", {"username": "admin", "password": new})[1])["token"]


def plugin_rows(compose, work):
    out = run(compose + ["exec", "-T", "gosidian", "/gosidian", "plugin", "list"], work, check=False, quiet=True).stdout
    return {l.split()[0]: l.split()[1] for l in out.splitlines()[1:] if len(l.split()) > 1}


def cleanup(compose, work, keep, project):
    if keep:
        log("left up: %s, compose project %s" % (work, project))
        return
    run(compose + ["down", "-v", "--remove-orphans"], work, check=False, quiet=True)
    # The container's files back to whoever runs the test, then away.
    run(["docker", "run", "--rm", "-v", work + ":/w", "alpine:3.20", "chown", "-R", "%d:%d" % (os.getuid(), os.getgid()), "/w"], work, check=False, quiet=True)
    shutil.rmtree(work, ignore_errors=True)


# The agents preset's files for the fake endpoint: one free model, no
# repositories, nobody to ask (notify wants a channel: one it never calls).
PI_YAML = """name: pi
description: The agents of the smoke test
endpoint:
  base_url: http://fake:8090/v1
models:
  - id: fake
    name: agent
    context: 32768
    max_output: 4000
    cost: {input: 0, output: 0}
default_model: agent
tools:
  allowed: [read, grep, find, ls]
limits:
  default: {max_turns: 5, max_seconds: 2m, max_tokens: 100000}
  max: {max_turns: 10, max_seconds: 5m, max_tokens: 200000}
mcp: [read, write]
"""
EXEC_YAML = """name: exec
description: The checks of the smoke test
"""
NOTIFY_YAML = """name: notify
description: The questions of the smoke test
gosidian_api: http://gosidian:8080
store_dir: /var/lib/gosidian-notify
channels:
  ntfy: {server: "http://fake:8090"}
"""


def agents(keep):
    project = PROJECT + "-agents"
    work = tempfile.mkdtemp(prefix="gosidian-smoke-agents-")
    shutil.copytree(PRESETS, work, dirs_exist_ok=True,
                    ignore=shutil.ignore_patterns(".env", "vault", "state", "secrets", "keys", "notify-secrets", "*.yaml", "docker-compose.override.yml"))
    files = ["-f", "docker-compose.yml", "-f", "docker-compose.agents.yml", "-f", "test/docker-compose.smoke-agents.yml"]
    images = {s: os.environ[e] for s, e in (("gosidian", "SMOKE_GOSIDIAN_IMAGE"), ("pi", "SMOKE_PI_IMAGE")) if os.environ.get(e)}
    if images:
        with open(os.path.join(work, "test", "docker-compose.images.yml"), "w") as f:
            f.write("services:\n" + "".join("  %s:\n    image: %s\n" % kv for kv in images.items()))
        files += ["-f", "test/docker-compose.images.yml"]
    compose = ["docker", "compose", "-p", project] + files
    try:
        log("folder " + work)
        run(["sh", "./init.sh"], work, quiet=True)
        set_env(os.path.join(work, ".env"), {
            "GOSIDIAN_PORT": PORT, "NOTIFY_PORT": str(int(PORT) + 16), "PREVIEW_PORT": str(int(PORT) + 17), "TZ": "UTC",
            "GOSIDIAN_PUBLIC_URL": BASE, "AGENTS_AT_ONCE": "2", "PIPELINES_WORKERS": "4",
        })
        for name, text in (("pi.yaml", PI_YAML), ("exec.yaml", EXEC_YAML), ("notify.yaml", NOTIFY_YAML)):
            with open(os.path.join(work, name), "w") as f:
                f.write(text)
        run(["docker", "run", "--rm", "-v", work + ":/w", "alpine:3.20", "sh", "-c",
             "chown -R 65532:65532 /w/vault /w/state /w/secrets && chown -R 0:0 /w/keys && chown -R 1000:1000 /w/notify-secrets"], work, quiet=True)
        run(compose + ["up", "-d", "--quiet-pull"], work, quiet=True)
        log("stack up")
        wait("gosidian's health", lambda: http("GET", "/healthz")[0] == 200 and '"ok"' in http("GET", "/healthz")[1], timeout=180)

        names = ("pi", "exec", "preview", "notify")
        rows = wait("the agents' plugins up", lambda: (lambda r: r if all(r.get(p) in ("unaccepted", "ok") for p in names) else None)(plugin_rows(compose, work)), timeout=240, every=5)
        log("plugins %s" % rows)
        out = run(compose + ["exec", "-T", "gosidian", "/gosidian", "plugin", "accept", "--pending"], work, quiet=True).stdout
        rows = wait("the agents' plugins accepted", lambda: (lambda r: r if all(r.get(p) == "ok" for p in names) else None)(plugin_rows(compose, work)), timeout=120, every=5)
        log("accepted %s" % rows)

        token, session = admin(compose, work)
        mcp_call(token, "memory_project_scaffold", {"project": "crew"})
        st, body, _ = http("PUT", "/api/v1/projects/crew", {"use_pipelines": True}, session)
        if st != 200:
            raise SystemExit("smoke: use_pipelines: %d %s" % (st, body))
        mcp_call(token, "memory_create_team", {"project": "crew", "name": "team", "roles": ["writer"], "run": "pi.run"})
        log("team crew/team, its role on pi.run")

        def pipelines_load():
            st, body, _ = http("GET", "/api/v1/pipelines?project=crew", None, session)
            ps = json.loads(body).get("pipelines", []) if st == 200 else []
            work_p = [p for p in ps if p.get("path", "").endswith("team-work.md")]
            if work_p and work_p[0].get("problem"):
                raise SystemExit("smoke: the team's work pipeline does not load: %s" % work_p[0]["problem"])
            return work_p or None
        wait("the team's pipelines", pipelines_load, timeout=60)

        mcp_call(token, "memory_create", {"path": "crew/tasks/20261118-000000-hello.md", "content":
                 "---\ntitle: Hello\ntype: task\nstatus: submitted\nteam: \"[[crew/team]]\"\nassignee: writer\ntags: [crew, type:task]\n---\n\n# Hello\n\nSay hello.\n"})
        log("task submitted")

        def closed():
            note = mcp_call(token, "memory_get", {"path": "crew/tasks/20261118-000000-hello.md"})
            content = note.get("content", "") if isinstance(note, dict) else ""
            for st in ("failed", "canceled", "input-required"):
                if "\nstatus: %s\n" % st in content:
                    raise SystemExit("smoke: the task went %s:\n%s" % (st, content[-3000:]))
            return content if "\nstatus: completed\n" in content else None
        content = wait("the task closed", closed, timeout=300, every=5)
        if "The documents do not say." not in content or "## Report" not in content:
            raise SystemExit("smoke: the task closed without the agent's report:\n" + content[-3000:])
        log("task completed with the agent's report")
        log("ok")
    finally:
        cleanup(compose, work, keep, project)


def main():
    keep = "--keep" in sys.argv
    if "agents" in sys.argv[1:]:
        return agents(keep)
    work = tempfile.mkdtemp(prefix="gosidian-smoke-")
    # An install's own files stay where they are.
    shutil.copytree(PRESETS, work, dirs_exist_ok=True,
                    ignore=shutil.ignore_patterns(".env", "vault", "state", "secrets", "storage.yaml", "docker-compose.override.yml"))
    compose = ["docker", "compose", "-p", PROJECT, "-f", "docker-compose.yml", "-f", "docker-compose.rag.yml", "-f", "test/docker-compose.smoke.yml"]
    try:
        log("folder " + work)
        run(["sh", "./init.sh"], work, quiet=True)
        set_env(os.path.join(work, ".env"), {
            "GOSIDIAN_PORT": PORT, "CHAT_PORT": str(int(PORT) + 15), "TZ": "UTC",
            "LLM_URL": "http://fake:8090/v1", "LLM_MODEL": "fake",
            "EMBED_URL": "http://fake:8090/v1", "EMBED_MODEL": "fake", "EMBED_DIMS": "256",
        })
        # The image runs as uid 65532: the folders must be its.
        run(["docker", "run", "--rm", "-v", work + ":/w", "alpine:3.20", "chown", "-R", "65532:65532", "/w/vault", "/w/state", "/w/secrets"], work, quiet=True)
        run(compose + ["up", "-d", "--quiet-pull"], work, quiet=True)
        log("stack up")
        wait("gosidian's health", lambda: http("GET", "/healthz")[0] == 200 and '"ok"' in http("GET", "/healthz")[1], timeout=180)

        def plugins_ok():
            rows = plugin_rows(compose, work)
            return rows if all(rows.get(p) == "ok" for p in ("docling", "images", "chat")) and rows.get("database") in ("unaccepted", "ok") else None
        rows = wait("the pinned plugins accepted", plugins_ok, timeout=240, every=5)
        log("plugins %s" % rows)
        out = run(compose + ["exec", "-T", "gosidian", "/gosidian", "plugin", "accept", "--pending"], work, quiet=True).stdout
        if rows.get("database") != "ok" and "Accepted database" not in out:
            raise SystemExit("smoke: accept --pending did not accept the database:\n" + out)

        token, session = admin(compose, work)

        scaffold = mcp_call(token, "memory_project_scaffold", {"project": "docs", "template": "rag"})
        log("scaffolded %d files" % len(scaffold.get("created", [])))
        st, body, _ = http("PUT", "/api/v1/projects/docs", {"use_pipelines": True}, session)
        if st != 200:
            raise SystemExit("smoke: use_pipelines: %d %s" % (st, body))
        lint = mcp_call(token, "memory_lint", {"project": "docs", "min_severity": "warning"})
        if lint.get("issues"):
            raise SystemExit("smoke: the rag template does not lint clean: %s" % lint["issues"])

        run(["docker", "run", "--rm", "-u", "65532:65532", "-v", os.path.join(work, "vault") + ":/vault", "alpine:3.20", "sh", "-c",
             "mkdir -p /vault/docs/IN && cat > /vault/docs/IN/coffee-machine.html <<'EOF'\n" + DOC + "EOF"], work, quiet=True)
        log("document in IN")

        def runs():
            st, body, _ = http("GET", "/api/v1/runs?project=docs", None, session)
            return json.loads(body).get("runs", []) if st == 200 else []

        def cut_done():
            rs = runs()
            failed = [r for r in rs if r["status"] == "failed"]
            if failed:
                raise SystemExit("smoke: a run failed: %s" % failed)
            return any(r["pipeline"].endswith("cut-note.md") and r["status"] == "done" for r in rs)
        wait("arrive, the commit and the cut", cut_done, timeout=300, every=5)
        log("cut done")

        started = mcp_call(token, "memory_pipeline_run", {"path": "docs/pipelines/answer.md", "input": {"question": "What does it mean when the red light blinks three times?"}})
        rid = started["run"]["id"]

        def answered():
            st, body, _ = http("GET", "/api/v1/runs/" + rid, None, session)
            d = json.loads(body) if st == 200 else {}
            return d if d.get("run", {}).get("status") in ("done", "failed") else None
        d = wait("the answer", answered, timeout=120)
        reply = next((s.get("output") for s in d.get("steps", []) if s["name"] == "reply"), None) or {}
        if d["run"]["status"] != "done" or "jammed" not in reply.get("text", "") or "[1]" not in reply.get("text", "") \
                or not any(s.get("doc") == "docs/documents/coffee-machine.md" for s in reply.get("sources", [])):
            raise SystemExit("smoke: the answer is not from the document: %s" % json.dumps(d)[:2000])
        log("answer: " + reply["text"])
        log("ok")
    finally:
        cleanup(compose, work, keep, PROJECT)


if __name__ == "__main__":
    main()
