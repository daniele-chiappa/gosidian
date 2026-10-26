#!/usr/bin/env python3
"""The fitness pilot of gosidian's teams of agents (IMP-195 phase 1c).

    python3 pilota.py setup  --team squadre/claude-sonnet.yaml
    python3 pilota.py fonti  CORPUS_DIR
    python3 pilota.py via
    python3 pilota.py stato
    python3 pilota.py misura --variant V [--reference] [--only knowledge,plans,app,cost]
    python3 pilota.py report results/V.json

setup: the project's fixed notes (the five people in persone/, the
benchmark's pipelines in pipelines/), the team (fitness/squadra.md, from
the block of --team, and an agent note per role from ruoli/).
fonti: uploads the corpus's PDFs (download.py) into the project's IN
folder and converts each into a note of fonti/ with the pipeline
converti-fonte, its frontmatter from corpus.json.
via: gives the objective (obiettivo.md) to the team's coordinator.
stato: the team's tasks and what it spent.
misura: the four measures of a run, into results/<V>.json:
  - knowledge: the 20 questions of domande.jsonl answered from the team's
    notes (conoscenza/) by a fixed agent, scored by two local judges
    (--reference: also from the sources, fonti/, the ceiling);
  - plans: each person's plan checked against its checks (verifiche.json)
    by the same judges;
  - app: the app of main built, tested and used as a person would, on a
    computer and on a phone;
  - cost: tasks, attempts, cost, tokens, time, and the people's answers.
report: the tables of a results file, in markdown.

The connection: --url and --token-file, or GOSIDIAN_MCP_URL and
GOSIDIAN_TOKEN; --project (default fitness). Standard library only.
"""
import argparse, concurrent.futures as cf, datetime, glob, json, os, re, sys, threading, time, urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
JUDGES = "gemma4:12b-12k,qwen3:14b-12k"
ROLES_DIR, PIPES_DIR = os.path.join(HERE, "ruoli"), os.path.join(HERE, "pipelines")


class MCP:
    def __init__(self, url, token):
        self.url, self.token, self.lock = url, token, threading.Lock()
        self.sid, self.n = None, 0
        self.rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "flotte-bench", "version": "1"}})
        self.rpc("notifications/initialized", {}, notify=True)

    def rpc(self, method, params, notify=False):
        with self.lock:
            self.n += 1
            n = self.n
        h = {"Authorization": "Bearer " + self.token, "Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
        if self.sid:
            h["Mcp-Session-Id"] = self.sid
        body = {"jsonrpc": "2.0", "method": method, "params": params}
        if not notify:
            body["id"] = n
        r = urllib.request.urlopen(urllib.request.Request(self.url, data=json.dumps(body).encode(), headers=h), timeout=900)
        self.sid = r.headers.get("Mcp-Session-Id") or self.sid
        raw = r.read().decode()
        msgs = [json.loads(l[5:]) for l in raw.splitlines() if l.startswith("data:") and l[5:].strip()] if raw.lstrip().startswith(("event:", "data:", ":")) else ([json.loads(raw)] if raw.strip() else [])
        for m in msgs:
            if m.get("id") == n:
                return m
        return None

    def call(self, tool, args):
        m = self.rpc("tools/call", {"name": tool, "arguments": args})
        if "error" in m:
            raise RuntimeError(m["error"])
        res = m["result"]
        txt = res["content"][0]["text"] if res.get("content") else ""
        if res.get("isError"):
            raise RuntimeError(txt[:300])
        return res.get("structuredContent") or json.loads(txt or "null")


def connect(a):
    token = open(a.token_file).read().strip() if a.token_file else os.environ["GOSIDIAN_TOKEN"]
    m = re.search(r"gosidian_[A-Za-z0-9_-]+", token)
    return MCP(a.url or os.environ["GOSIDIAN_MCP_URL"], m.group(0) if m else token)


def load(name):
    with open(os.path.join(HERE, name)) as f:
        return json.load(f)


def jsonl(name):
    with open(os.path.join(HERE, name)) as f:
        return [json.loads(l) for l in f if l.strip()]


def num(v):
    """A number from a frontmatter value, which may come back as text; None when it is none."""
    try:
        return float(v) if v not in (None, "") else None
    except (TypeError, ValueError):
        return None


def yes(v):
    return v is True or str(v).lower() == "true"


def put(mcp, path, content):
    """Writes a note whole, creating it when missing."""
    try:
        mcp.call("memory_get_frontmatter", {"path": path})
    except RuntimeError:
        mcp.call("memory_create", {"path": path, "content": content})
        return "created"
    mcp.call("memory_update", {"path": path, "content": content, "allow_shrink": True})
    return "updated"


def run(mcp, a, pipeline, inp, wait=60, poll=10):
    """Starts a pipeline of the project and waits for its end; returns the run with its steps."""
    r = mcp.call("memory_pipeline_run", {"path": "%s/pipelines/%s.md" % (a.project, pipeline), "input": inp, "wait": wait})
    rid = (r.get("run") or r).get("id")
    while True:
        d = mcp.call("memory_pipelines", {"project": a.project, "run": rid})
        if d["run"]["status"] not in ("queued", "running", "pending"):
            return d
        time.sleep(poll)


def person_text(p):
    return "\n".join("- %s: %s" % (k, p[k]) for k in ("nome", "eta", "sesso", "lavoro", "salute", "abitudini", "obiettivo", "tempo", "attrezzatura"))


# --- setup -----------------------------------------------------------------

def setup(a):
    mcp, P = connect(a), a.project
    for p in load("persone.json")["persone"]:
        body = "---\ntitle: %s\ntype: persona\ntags: [%s, persona]\n---\n\n# %s\n\n%s\n" % (p["nome"], P, p["nome"], "\n".join(
            "- **%s**: %s" % (label, p[k]) for k, label in (("eta", "Età"), ("sesso", "Sesso"), ("lavoro", "Lavoro"), ("salute", "Salute"),
                                                            ("abitudini", "Abitudini"), ("obiettivo", "Obiettivo"), ("tempo", "Tempo"), ("attrezzatura", "Attrezzatura"))))
        print("persona", p["id"], put(mcp, "%s/persone/%s.md" % (P, p["id"]), body))
    for f in sorted(glob.glob(os.path.join(PIPES_DIR, "*.md"))):
        print("pipeline", os.path.basename(f), put(mcp, "%s/pipelines/%s" % (P, os.path.basename(f)), open(f).read()))
    block = "".join(l for l in open(a.team) if not l.startswith("#")).strip() + "\n"
    roles = sorted(os.path.splitext(os.path.basename(f))[0] for f in glob.glob(os.path.join(ROLES_DIR, "*.md")))
    try:
        mcp.call("memory_create_team", {"project": P, "name": "squadra", "roles": roles, "coordinator": "coordinatore"})
        print("team created")
    except RuntimeError as e:
        print("team:", str(e)[:120])
    note = mcp.call("memory_get", {"path": "%s/squadra.md" % P})["content"]
    i = note.index("```team")
    j = note.index("```", i + 7)
    mcp.call("memory_update", {"path": "%s/squadra.md" % P, "content": note[:i] + "```team\n" + block + note[j:], "allow_shrink": True})
    for r in roles:
        text = open(os.path.join(ROLES_DIR, r + ".md")).read().strip()
        put(mcp, "%s/agents/%s.md" % (P, r), "---\ntitle: %s\ntype: agent\ntags: [%s, type:agent]\n---\n\n# %s\n\n%s\n" % (r, P, r, text))
    print("team block and agents written:", ", ".join(roles))
    print(json.dumps(mcp.call("memory_lint", {"project": P, "rules": ["team-invalid", "pipeline-invalid"]}).get("summary")))


# --- sources ---------------------------------------------------------------

def upload(mcp, a, folder, files):
    """Puts files ({path under folder: bytes}) into the folder of the project's IN folder, replacing what is there."""
    P = a.project
    up = mcp.call("memory_in_upload", {"action": "open", "project": P, "dir": folder, "policy": "replace",
                                       "files": [{"path": k, "size": len(v)} for k, v in sorted(files.items())]})
    base = re.match(r"https?://[^/]+", a.url or os.environ["GOSIDIAN_MCP_URL"]).group(0)
    uid = up["upload"]["id"]
    for f in up["upload"]["files"]:
        data, got = files[f["path"]], f.get("received") or 0
        while got < f["size"]:
            piece = data[got:got + (32 << 20)]
            req = urllib.request.Request("%s%s/files/%d?offset=%d" % (base, up["endpoint"], f["n"], got), data=piece, method="PUT",
                                         headers={"Authorization": "Bearer " + up["ticket"], "Content-Type": "application/octet-stream"})
            urllib.request.urlopen(req, timeout=600).read()
            got += len(piece)
    mcp.call("memory_in_upload", {"action": "complete", "project": P, "id": uid})
    while True:
        st = mcp.call("memory_in_upload", {"action": "status", "project": P, "id": uid})
        state = (st.get("upload") or st).get("state")
        if state not in ("open", "completing"):
            break
        time.sleep(2)
    print("upload %s: %d files, %s" % (folder, len(files), state))


def fonti(a):
    mcp, P = connect(a), a.project
    docs = load("corpus.json")["documents"]
    files = {}
    for d in docs:
        path = os.path.join(a.dir, d["id"] + ".pdf")
        if os.path.getsize(path) != d["bytes"]:
            sys.exit("%s: not the corpus's file (run download.py)" % path)
        with open(path, "rb") as fh:
            files[d["id"] + ".pdf"] = fh.read()
    upload(mcp, a, "fonti", files)
    for d in docs:
        fm = {k: d[k] for k in ("authors", "year", "journal", "doi", "pmcid", "license", "landing", "sha256") if d.get(k)}
        fm.update({"tags": [P, "fonte"], "corpus_id": d["id"]})
        r = run(mcp, a, "converti-fonte", {"file": "%s/IN/fonti/%s.pdf" % (P, d["id"]), "dest": "fonti/" + d["id"], "title": d["title"], "frontmatter": fm})
        print(d["id"], r["run"]["status"], (r["run"].get("error") or "")[:200])


# --- run -------------------------------------------------------------------

def via(a):
    mcp, P = connect(a), a.project
    text = open(os.path.join(HERE, "obiettivo.md")).read()
    title = re.search(r"^title: (.+)$", text, re.M).group(1).strip()
    body = text.split("---", 2)[2].strip()
    t = mcp.call("memory_task_create", {"project": P, "team": "%s/squadra.md" % P, "assignee": "coordinatore", "title": title, "body": body})["task"]
    print(t["path"])


def stato(a):
    mcp, P = connect(a), a.project
    r = mcp.call("memory_tasks", {"project": P, "status": "all", "team": "%s/squadra.md" % P})
    for t in sorted(r["tasks"], key=lambda t: t["created"]):
        print("%-15s %-12s %4.2f %2d  %s" % (t["status"], t["assignee"], t.get("cost_usd") or 0, t.get("attempts") or 0, t["title"][:70]))
    for tm in r.get("teams") or []:
        print("spent today:", tm.get("spent_today"), "budget:", tm.get("budget"))


# --- measures --------------------------------------------------------------

def notes(mcp, folder):
    """The notes under a folder of the vault ("project/folder")."""
    r = mcp.call("memory_list_notes", {"project": folder.split("/")[0]})
    return sorted(n["path"] for n in r.get("notes") or [] if n["path"].startswith(folder + "/"))


def snapshot(mcp, a, folder):
    """Copies the notes of a folder of the project, as they are now, into
    misure/<variant>/<folder> of the IN folder: the answers read that copy
    and nothing else. Returns the IN folder."""
    prefix = "%s/%s/" % (a.project, folder)
    paths = notes(mcp, prefix.rstrip("/"))
    if not paths or len(paths) > 100:
        sys.exit("%s: %d notes; an agent's step takes 1 to 100 files" % (prefix, len(paths)))
    files = {p[len(prefix):]: mcp.call("memory_get", {"path": p, "raw": True})["content"].encode() for p in paths}
    dest = "misure/%s/%s" % (a.variant, folder)
    upload(mcp, a, dest, files)
    return dest


def knowledge(mcp, a, res):
    qs, judges = jsonl("domande.jsonl"), a.judges.split(",")
    folders = ["conoscenza"] + (["fonti"] if a.reference else [])
    copies = {f: snapshot(mcp, a, f) for f in folders}
    lock, rows = threading.Lock(), []

    def one(job):
        q, folder = job
        nota = "%s/misure/%s/risposte/%s/%s.md" % (a.project, a.variant, folder, q["id"])
        try:
            fm = mcp.call("memory_get_frontmatter", {"path": nota})["parsed"]
        except RuntimeError:
            fm = {}
        if "domanda" not in fm:
            r = run(mcp, a, "rispondi", {"id": q["id"], "domanda": q["domanda"], "attesa": q["attesa"], "evidenza": q["evidenza"], "cartella": copies[folder], "nota": nota})
            if r["run"]["status"] != "done":
                print("ALERT answer", q["id"], folder, r["run"]["status"], str(r["run"].get("error"))[:200], flush=True)
        for j in judges:
            k = "score_" + j.split(":")[0]
            fm = mcp.call("memory_get_frontmatter", {"path": nota})["parsed"]
            if k not in fm:
                r = run(mcp, a, "giudica", {"note": nota, "judge": j})
                if r["run"]["status"] != "done":
                    print("ALERT judge", q["id"], folder, j, r["run"]["status"], flush=True)
        fm = mcp.call("memory_get_frontmatter", {"path": nota})["parsed"]
        with lock:
            rows.append({"id": q["id"], "folder": folder, "cost_usd": num(fm.get("costo_usd")),
                         "scores": {j.split(":")[0]: num(fm.get("score_" + j.split(":")[0])) for j in judges},
                         "invented": {j.split(":")[0]: yes(fm.get("invented_" + j.split(":")[0])) for j in judges}})
            print("answer", q["id"], folder, rows[-1]["scores"], flush=True)

    with cf.ThreadPoolExecutor(a.workers) as ex:
        list(ex.map(one, [(q, f) for f in folders for q in qs]))
    out = {}
    for f in folders:
        rs = [r for r in rows if r["folder"] == f]
        per = {}
        for j in judges:
            k = j.split(":")[0]
            sc = [r["scores"][k] for r in rs if r["scores"][k] is not None]
            per[k] = {"mean": round(sum(sc) / len(sc), 2) if sc else None, "right": sum(1 for s in sc if s > 0), "zero": sum(1 for s in sc if s == 0),
                      "wrong": sum(1 for s in sc if s < 0), "invented": sum(1 for r in rs if r["invented"][k]), "judged": len(sc)}
        out[f] = {"judges": per, "questions": sorted(rs, key=lambda r: r["id"]), "cost_usd": round(sum(r["cost_usd"] or 0 for r in rs), 4)}
    res["knowledge"] = out


def find_plan(paths, p):
    keys = (p["id"], p["nome"].lower())
    hit = [x for x in paths if any(k in x.lower() for k in keys)]
    return hit[0] if hit else None


def plans(mcp, a, res):
    people, checks, judges = load("persone.json")["persone"], load("verifiche.json")["verifiche"], a.judges.split(",")
    schede = notes(mcp, "%s/conoscenza/schede" % a.project)
    out = []
    for p in people:
        path, row = find_plan(schede, p), {"id": p["id"], "plan": None, "judges": {}}
        vs = checks[p["id"]]
        if path:
            row["plan"] = path
            nota = "%s/misure/%s/schede/%s.md" % (a.project, a.variant, p["id"])
            for j in judges:
                k = j.split(":")[0]
                try:
                    fm = mcp.call("memory_get_frontmatter", {"path": nota})["parsed"]
                except RuntimeError:
                    fm = {}
                if "punti_" + k not in fm:
                    r = run(mcp, a, "giudica-scheda", {"scheda": path, "persona": person_text(p), "verifiche": [{"id": v["id"], "testo": v["testo"]} for v in vs], "judge": j, "nota": nota})
                    if r["run"]["status"] != "done":
                        print("ALERT plan", p["id"], j, r["run"]["status"], str(r["run"].get("error"))[:200], flush=True)
                    fm = mcp.call("memory_get_frontmatter", {"path": nota})["parsed"]
                row["judges"][k] = {"points": num(fm.get("punti_" + k)), "of": len(vs), "outcomes": fm.get("esiti_" + k)}
        print("plan", p["id"], path, {k: v["points"] for k, v in row["judges"].items()}, flush=True)
        out.append(row)
    res["plans"] = out


def steps_for(people):
    s = [{"action": "screenshot", "name": "inizio", "full_page": True}]
    for p in people:
        s += [{"action": "goto", "path": ""}, {"action": "click", "selector": "text=" + p["nome"]},
              {"action": "expect_text", "text": p["nome"]}, {"action": "screenshot", "name": p["id"], "full_page": True}]
    s += [{"action": "goto", "path": ""}, {"action": "click", "selector": "text=Esercizi"}, {"action": "screenshot", "name": "esercizi", "full_page": True}]
    return s


def nested(raw, key):
    """A map of the frontmatter's raw YAML ("key:" then indented "k: v" lines), which parsed leaves out."""
    out, inside = {}, False
    for line in raw.splitlines():
        if line == key + ":":
            inside = True
        elif inside and line.startswith((" ", "\t")) and ":" in line:
            k, v = line.strip().split(":", 1)
            v = v.strip()
            out[k] = True if v == "true" else False if v == "false" else (num(v) if num(v) is not None else v)
        elif inside:
            break
    return out or None


def app(mcp, a, res):
    nota = "%s/misure/%s/app.md" % (a.project, a.variant)
    r = run(mcp, a, "prova-app", {"ref": "main", "passi": steps_for(load("persone.json")["persone"]), "nota": nota})
    fm, raw = {}, ""
    try:
        got = mcp.call("memory_get_frontmatter", {"path": nota})
        fm, raw = got["parsed"], got.get("raw") or ""
    except RuntimeError:
        pass
    res["app"] = {"run": r["run"]["id"], "status": r["run"]["status"], "build_exit": num(fm.get("build_exit")),
                  "computer": nested(raw, "computer"), "phone": nested(raw, "telefono")}
    print("app", json.dumps(res["app"]))


def ts(s):
    return datetime.datetime.fromisoformat(s.replace("Z", "+00:00")) if s else None


def cost(mcp, a, res):
    P = a.project
    tasks = mcp.call("memory_tasks", {"project": P, "status": "all", "team": "%s/squadra.md" % P})["tasks"]
    rows = []
    for t in tasks:
        fm = mcp.call("memory_get_frontmatter", {"path": t["path"]})["parsed"]
        rows.append({"path": t["path"], "title": t["title"], "role": t["assignee"], "status": t["status"], "attempts": t.get("attempts") or 0,
                     "cost_usd": t.get("cost_usd") or 0, "tokens": t.get("tokens") or 0, "created": t["created"], "updated": fm.get("updated")})
    # The time: from the objective to the last task that moved (closed, or given up by a person).
    objective = min(rows, key=lambda r: r["created"]) if rows else None
    # The people's answers, in the runs of this run of the pilot: the
    # pipelines keep the runs of the ones before.
    since = ts(objective["created"]) if objective else None
    answers = []
    for pipe in ("squadra-work", "squadra-coordinate"):
        for r in mcp.call("memory_pipelines", {"project": P, "path": "%s/pipelines/%s.md" % (P, pipe), "limit": 100}).get("runs") or []:
            if since and ts(r.get("created") or r.get("started")) < since:
                continue
            d = mcp.call("memory_pipelines", {"project": P, "run": r["id"]})
            for s in d["steps"]:
                if s["name"] == "answer" and s["status"] == "done" and isinstance(s.get("output"), dict):
                    answers.append({"run": r["id"], "by": "approver", "choice": s["output"].get("choice"), "at": s["output"].get("at")})
    wall = None
    if objective:
        last = max(ts(str(r["updated"] or r["created"])) for r in rows)
        wall = round((last - ts(objective["created"])).total_seconds() / 60, 1)
    by_role = {}
    for r in rows:
        b = by_role.setdefault(r["role"], {"tasks": 0, "cost_usd": 0.0, "tokens": 0, "attempts": 0})
        b["tasks"] += 1
        b["cost_usd"] = round(b["cost_usd"] + r["cost_usd"], 4)
        b["tokens"] += r["tokens"]
        b["attempts"] += r["attempts"]
    res["cost"] = {"objective": objective["status"] if objective else None, "tasks": len(rows), "by_status": {s: sum(1 for r in rows if r["status"] == s) for s in sorted({r["status"] for r in rows})},
                   "cost_usd": round(sum(r["cost_usd"] for r in rows), 4), "tokens": sum(r["tokens"] for r in rows), "wall_minutes": wall,
                   "by_role": by_role, "answers": answers, "rows": rows}
    print("cost", json.dumps({k: v for k, v in res["cost"].items() if k != "rows"}, ensure_ascii=False))


def misura(a):
    mcp = connect(a)
    out = os.path.join(HERE, "results", a.variant + ".json")
    res = json.load(open(out)) if os.path.exists(out) else {}
    res.update({"variant": a.variant, "date": datetime.date.today().isoformat(), "judges": a.judges.split(",")})
    only = a.only.split(",") if a.only else ["knowledge", "plans", "app", "cost"]
    for name, fn in (("knowledge", knowledge), ("plans", plans), ("app", app), ("cost", cost)):
        if name in only:
            fn(mcp, a, res)
            os.makedirs(os.path.dirname(out), exist_ok=True)
            with open(out, "w") as f:
                json.dump(res, f, ensure_ascii=False, indent=1)
    print("written", out)


# --- report ----------------------------------------------------------------

def report(a):
    res = json.load(open(a.results))
    print("## %s (%s)\n" % (res["variant"], res["date"]))
    if "knowledge" in res:
        print("| Notes | Judge | Mean (-10..10) | Right | Zero | Wrong | Invented |\n|---|---|---|---|---|---|---|")
        for f, v in res["knowledge"].items():
            for k, j in v["judges"].items():
                print("| %s | %s | %s | %d | %d | %d | %d |" % (f, k, j["mean"], j["right"], j["zero"], j["wrong"], j["invented"]))
        print()
    if "plans" in res:
        ks = [j.split(":")[0] for j in res["judges"]]
        print("| Person | Plan | %s |\n|---|---|%s" % (" | ".join(ks), "---|" * len(ks)))
        for p in res["plans"]:
            print("| %s | %s | %s |" % (p["id"], p["plan"] or "missing", " | ".join(
                "%s/%s" % (p["judges"][k]["points"], p["judges"][k]["of"]) if k in p["judges"] else "-" for k in ks)))
        print()
    if "app" in res:
        ap = res["app"]
        for dev in ("computer", "phone"):
            d = ap.get(dev) or {}
            print("- app on the %s: %s, steps %s/%s, console %s, failed requests %s" % (dev, "ok" if d.get("ok") else "not ok", d.get("passi_ok"), d.get("passi"), d.get("console"), d.get("richieste_fallite")))
        print("- build exit code: %s\n" % ap.get("build_exit"))
    if "cost" in res:
        c = res["cost"]
        print("- tasks: %d %s; cost %.2f USD; tokens %d; time %s minutes; people's answers %d" % (c["tasks"], c["by_status"], c["cost_usd"], c["tokens"], c["wall_minutes"], len(c["answers"])))
        for r, b in sorted(c["by_role"].items()):
            print("  - %s: %d tasks, %d attempts, %.2f USD, %d tokens" % (r, b["tasks"], b["attempts"], b["cost_usd"], b["tokens"]))


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--url")
    ap.add_argument("--token-file")
    ap.add_argument("--project", default="fitness")
    sub = ap.add_subparsers(dest="cmd", required=True)
    s = sub.add_parser("setup")
    s.add_argument("--team", required=True)
    s = sub.add_parser("fonti")
    s.add_argument("dir")
    sub.add_parser("via")
    sub.add_parser("stato")
    s = sub.add_parser("misura")
    s.add_argument("--variant", required=True)
    s.add_argument("--reference", action="store_true")
    s.add_argument("--judges", default=JUDGES)
    s.add_argument("--workers", type=int, default=2)
    s.add_argument("--only")
    s = sub.add_parser("report")
    s.add_argument("results")
    a = ap.parse_args()
    {"setup": setup, "fonti": fonti, "via": via, "stato": stato, "misura": misura, "report": report}[a.cmd](a)


if __name__ == "__main__":
    main()
