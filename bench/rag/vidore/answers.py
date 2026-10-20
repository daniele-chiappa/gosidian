#!/usr/bin/env python3
"""The answers of the ViDoRe V3 benchmark: a sample, the bot's answers, the judges.

    python3 answers.py sample  TABLES SAMPLE.jsonl [--per-corpus 25] [--seed 2026]
    python3 answers.py answer  SAMPLE.jsonl --variant V [--set key=value ...] [--workers 4] [--sequential]
    python3 answers.py judge   SAMPLE.jsonl --variant V --judge MODEL [--workers 4]
    python3 answers.py collect SAMPLE.jsonl --variant V OUT.jsonl

sample: per corpus, N questions drawn among those whose translations are
all there (six languages), each in its six languages.
answer: runs the project's pipelines/answer for each question of the
sample without its note in reports/answers/<variant>/; --set passes a
setting (collection, k, candidates, rerank, context, model...).
--sequential asks one at a time and records each run's time (the times
of the report come from such a run).
judge: runs pipelines/judge on each answer without that judge's score,
with the text of the relevant pages (score 2 first) as the reference.
collect: one line per answer with the scores of every judge.

The connection: --url and --token-file, or GOSIDIAN_MCP_URL and
GOSIDIAN_TOKEN; --project (default vidore). Standard library only.
"""
import argparse, collections, concurrent.futures as cf, json, os, random, re, sys, threading, time
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from search import MCP

LANG = {"english": "English", "french": "French", "spanish": "Spanish", "german": "German", "italian": "Italian", "portuguese": "Portuguese"}


def connect(a):
    token = open(a.token_file).read().strip() if a.token_file else os.environ["GOSIDIAN_TOKEN"]
    m = re.search(r"gosidian_[A-Za-z0-9_-]+", token)
    return MCP(a.url or os.environ["GOSIDIAN_MCP_URL"], m.group(0) if m else token)


def sample(a):
    qs = [json.loads(l) for l in open(os.path.join(a.tables, "queries.jsonl"))]
    rel = collections.defaultdict(list)
    for l in open(os.path.join(a.tables, "qrels.jsonl")):
        q = json.loads(l)
        rel[(q["corpus"], q["query_id"])].append((q["corpus_id"], q["score"]))
    pages = {}
    for l in open(os.path.join(a.tables, "pages.jsonl")):
        p = json.loads(l)
        pages[(p["corpus"], p["corpus_id"])] = (p["doc_id"], p["page"])
    groups = collections.defaultdict(list)
    for q in qs:
        groups[(q["corpus"], q["group"])].append(q)
    out = []
    for corpus in sorted({c for c, _ in groups}):
        # A generator of its own per corpus: the sample of a corpus does not move with the others.
        rnd = random.Random("%d-%s" % (a.seed, corpus))
        full = sorted(g for (c, g), rows in groups.items() if c == corpus and sorted(r["language"] for r in rows) == sorted(LANG))
        for g in rnd.sample(full, min(a.per_corpus, len(full))):
            for q in sorted(groups[(corpus, g)], key=lambda r: list(LANG).index(r["language"])):
                refs = sorted(rel[(corpus, q["query_id"])], key=lambda x: -x[1])
                out.append(dict(q, id="%s-%d" % (corpus, q["query_id"]), reference_pages=[{"doc_id": pages[(corpus, c)][0], "page": pages[(corpus, c)][1], "score": s} for c, s in refs]))
    with open(a.out, "w") as f:
        for r in out:
            f.write(json.dumps(r, ensure_ascii=False) + "\n")
    print("%d questions, %d groups" % (len(out), len(out) // 6))


def note_path(a, q):
    return "%s/reports/answers/%s/%s.md" % (a.project, a.variant, q["id"])


def existing(mcp, a):
    try:
        r = mcp.call("memory_list_notes", {"project": a.project})
        prefix = "%s/reports/answers/%s/" % (a.project, a.variant)
        return {n["path"] for n in r.get("notes") or [] if n["path"].startswith(prefix)}
    except Exception:
        return set()


def run(mcp, path, inp, wait=600):
    r = mcp.call("memory_pipeline_run", {"path": a_project_path(path), "input": inp, "wait": wait})
    run = r.get("run") or r
    t0 = time.time()
    while run.get("status") not in ("done", "failed", "canceled"):
        time.sleep(3)
        run = mcp.call("memory_pipelines", {"project": ARGS.project, "run": run["id"]})["run"]
        if time.time() - t0 > 1800:
            break
    return run


def a_project_path(p):
    return "%s/pipelines/%s.md" % (ARGS.project, p)


def answer(a):
    mcp, qs = connect(a), [json.loads(l) for l in open(a.sample)]
    done = existing(mcp, a)
    todo = [q for q in qs if note_path(a, q) not in done]
    if a.limit:
        # The first N of the sample, a group (six languages) after another: a timing run.
        todo = todo[:a.limit]
    sets = dict(s.split("=", 1) for s in a.set or [])
    for k in ("k", "candidates", "max_tokens", "budget", "max_source"):
        if k in sets:
            sets[k] = int(sets[k])
    print("%d questions, %d to answer" % (len(qs), len(todo)), flush=True)
    log = open(a.times, "a") if a.times else None
    lock, n, t0 = threading.Lock(), [0], time.time()

    def one(q):
        inp = dict(sets, id=q["id"], variant=a.variant, question=q["query"], corpus=q["corpus"], language=LANG[q["language"]], expected=q["answer"] or "", group=q["group"])
        t = time.time()
        r = run(mcp, "answer", inp)
        with lock:
            n[0] += 1
            if r.get("status") != "done":
                print("ALERT", q["id"], r.get("status"), str(r.get("error"))[:200], flush=True)
            if log:
                log.write(json.dumps({"id": q["id"], "status": r.get("status"), "seconds": round(time.time() - t, 2), "created": r.get("created"), "finished": r.get("finished")}) + "\n")
                log.flush()
            if n[0] % 50 == 0 or n[0] == len(todo):
                print("%d/%d %.0fs" % (n[0], len(todo), time.time() - t0), flush=True)

    if a.sequential:
        for q in todo:
            one(q)
    else:
        with cf.ThreadPoolExecutor(a.workers) as ex:
            list(ex.map(one, todo))


def reference(mcp, q, cache, limit=8000):
    out, size = [], 0
    for p in q["reference_pages"]:
        doc = cache.get(p["doc_id"])
        if doc is None:
            r = mcp.call("memory_get", {"path": "%s/documents/%s/%s.md" % (ARGS.project, q["corpus"], p["doc_id"]), "raw": True})
            doc = {int(m.group(1)): m.group(2).strip() for m in re.finditer(r"^## Page (\d+)\n(.*?)(?=^## Page \d+\n|\Z)", r["content"], re.S | re.M)}
            cache[p["doc_id"]] = doc
        t = "%s, page %d:\n%s" % (p["doc_id"], p["page"], doc.get(p["page"], ""))
        if size + len(t) > limit:
            t = t[:max(0, limit - size)]
        out.append(t)
        size += len(t)
        if size >= limit:
            break
    return "\n\n".join(out)


def judge(a):
    mcp, qs = connect(a), [json.loads(l) for l in open(a.sample)]
    key = "score_" + a.judge.split(":")[0]
    todo, cache, lock = [], {}, threading.Lock()
    for q in qs:
        try:
            fm = mcp.call("memory_get_frontmatter", {"path": note_path(a, q)})["parsed"]
        except Exception:
            continue
        if key not in fm:
            todo.append(q)
    print("%d answers to judge with %s" % (len(todo), a.judge), flush=True)
    n, t0 = [0], time.time()

    def one(q):
        with lock:
            ref = reference(mcp, q, cache)
        r = run(mcp, "judge", {"note": note_path(a, q), "judge": a.judge, "reference": ref})
        with lock:
            n[0] += 1
            if r.get("status") != "done":
                print("ALERT", q["id"], r.get("status"), str(r.get("error"))[:200], flush=True)
            if n[0] % 50 == 0 or n[0] == len(todo):
                print("%d/%d %.0fs" % (n[0], len(todo), time.time() - t0), flush=True)

    with cf.ThreadPoolExecutor(a.workers) as ex:
        list(ex.map(one, todo))


def collect(a):
    mcp, qs = connect(a), [json.loads(l) for l in open(a.sample)]
    with open(a.out, "w") as f:
        for q in qs:
            try:
                fm = mcp.call("memory_get_frontmatter", {"path": note_path(a, q)})["parsed"]
            except Exception:
                fm = None
            row = {k: q[k] for k in ("id", "corpus", "query_id", "group", "language", "query_types", "query_format", "content_type")}
            row["answered"] = fm is not None
            if fm:
                row["not_found"] = str(fm.get("not_found")).lower() == "true"
                for k, v in fm.items():
                    if k.startswith("score_"):
                        row[k] = int(v)
                    elif k.startswith("invented_"):
                        row[k] = str(v).lower() == "true"
                row["prompt_tokens"] = int(fm.get("prompt_tokens") or 0)
            f.write(json.dumps(row) + "\n")


ARGS = None


def main():
    global ARGS
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    s = sub.add_parser("sample"); s.add_argument("tables"); s.add_argument("out"); s.add_argument("--per-corpus", type=int, default=25); s.add_argument("--seed", type=int, default=2026)
    for name in ("answer", "judge", "collect"):
        p = sub.add_parser(name)
        p.add_argument("sample"); p.add_argument("--variant", required=True)
        p.add_argument("--url"); p.add_argument("--token-file"); p.add_argument("--project", default="vidore")
        p.add_argument("--workers", type=int, default=4)
        if name == "answer":
            p.add_argument("--set", action="append"); p.add_argument("--sequential", action="store_true"); p.add_argument("--times")
            p.add_argument("--limit", type=int)
        if name == "judge":
            p.add_argument("--judge", required=True)
        if name == "collect":
            p.add_argument("out")
    ARGS = ap.parse_args()
    if not hasattr(ARGS, "project"):
        ARGS.project = "vidore"
    {"sample": sample, "answer": answer, "judge": judge, "collect": collect}[ARGS.cmd](ARGS)


if __name__ == "__main__":
    main()
