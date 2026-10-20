#!/usr/bin/env python3
"""Measure the search of a gosidian vector archive on ViDoRe V3, page by page.

    python3 search.py TABLES_DIR OUT.jsonl --collection C [options]

Each question goes to the archive's search tool through gosidian's MCP
endpoint, filtered by its corpus. The chunks found become pages, in the
order each page first appears (a chunk's page is its section, "Page N",
in its document), and the pages are scored against the qrels: NDCG@10
with the graded relevance of the dataset (gain = score), recall@10 and
MRR@10. One call gives two orders: the cosine one (score) and, with
--rerank, the reranker's (rerank_score). OUT.jsonl gets one line per
question with both page lists; a line already there is not asked again,
so a run that stops goes on where it was.

Options:
  --url URL          gosidian's MCP endpoint (default $GOSIDIAN_MCP_URL)
  --token-file F     a file with the bearer token (default $GOSIDIAN_TOKEN)
  --project P        the project the archive is exposed in (default vidore)
  --tool T           the search tool (default vec-qdrant_search)
  --k N              chunks asked (default 40)
  --candidates N     chunks the reranker reads (default 40)
  --rerank NAME      the reranker of the archive's preset (none: cosine only)
  --corpus C,...     only these corpora
  --limit N          only the first N questions (a trial)
  --workers N        questions asked at once (default 4)

Standard library only.
"""
import argparse, concurrent.futures as cf, json, math, os, re, sys, threading, time, urllib.request

PAGE = re.compile(r"^Page (\d+)")


class MCP:
    def __init__(self, url, token):
        self.url, self.token, self.lock = url, token, threading.Lock()
        self.sid, self.n = None, 0
        self.rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "vidore-bench", "version": "1"}})
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
        r = urllib.request.urlopen(urllib.request.Request(self.url, data=json.dumps(body).encode(), headers=h), timeout=300)
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
        return res.get("structuredContent") or json.loads(txt)


def load(tables):
    pages = {}
    for l in open(os.path.join(tables, "pages.jsonl")):
        p = json.loads(l)
        pages[(p["corpus"], p["doc_id"], p["page"])] = p["corpus_id"]
    rel = {}
    for l in open(os.path.join(tables, "qrels.jsonl")):
        q = json.loads(l)
        rel.setdefault((q["corpus"], q["query_id"]), {})[q["corpus_id"]] = q["score"]
    qs = [json.loads(l) for l in open(os.path.join(tables, "queries.jsonl"))]
    return pages, rel, qs


def ranking(hits, key, pages, corpus):
    out, seen = [], set()
    for h in sorted(hits, key=lambda h: -(h.get(key) or 0.0)) if key else hits:
        m = h.get("meta") or {}
        doc = m.get("doc_id") or os.path.basename(h.get("doc") or "")[:-3]
        sec = m.get("section") or ""
        mm = PAGE.match(sec)
        if not mm:
            continue
        cid = pages.get((corpus, doc, int(mm.group(1))))
        if cid is not None and cid not in seen:
            seen.add(cid)
            out.append(cid)
    return out


def scores(ranked, rel, k=10):
    dcg = sum(rel.get(c, 0) / math.log2(i + 2) for i, c in enumerate(ranked[:k]))
    ideal = sorted(rel.values(), reverse=True)[:k]
    idcg = sum(g / math.log2(i + 2) for i, g in enumerate(ideal))
    hit = [i for i, c in enumerate(ranked[:k]) if rel.get(c, 0) > 0]
    return {"ndcg10": dcg / idcg if idcg else 0.0, "recall10": len(hit) / len([g for g in rel.values() if g > 0]) if rel else 0.0,
            "mrr10": 1.0 / (hit[0] + 1) if hit else 0.0}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("tables"); ap.add_argument("out")
    ap.add_argument("--collection", required=True); ap.add_argument("--url", default=os.environ.get("GOSIDIAN_MCP_URL"))
    ap.add_argument("--token-file"); ap.add_argument("--project", default="vidore"); ap.add_argument("--tool", default="vec-qdrant_search")
    ap.add_argument("--k", type=int, default=40); ap.add_argument("--candidates", type=int, default=40); ap.add_argument("--rerank", default="none")
    ap.add_argument("--corpus"); ap.add_argument("--limit", type=int); ap.add_argument("--workers", type=int, default=4)
    a = ap.parse_args()
    token = open(a.token_file).read().strip() if a.token_file else os.environ["GOSIDIAN_TOKEN"]
    m = re.search(r"gosidian_[A-Za-z0-9_-]+", token)
    token = m.group(0) if m else token
    pages, rel, qs = load(a.tables)
    if a.corpus:
        keep = set(a.corpus.split(","))
        qs = [q for q in qs if q["corpus"] in keep]
    if a.limit:
        qs = qs[:a.limit]
    done = set()
    if os.path.exists(a.out):
        for l in open(a.out):
            r = json.loads(l)
            done.add((r["corpus"], r["query_id"]))
    todo = [q for q in qs if (q["corpus"], q["query_id"]) not in done]
    print("%d questions, %d to ask" % (len(qs), len(todo)), flush=True)
    mcp, lock, t0 = MCP(a.url, token), threading.Lock(), time.time()
    out = open(a.out, "a")

    def ask(q):
        args = {"project": a.project, "collection": a.collection, "query": q["query"], "k": a.k, "filter": {"meta": {"corpus": q["corpus"]}}}
        if a.rerank != "none":
            args["rerank"], args["candidates"] = a.rerank, a.candidates
        t = time.time()
        for attempt in range(3):
            try:
                res = mcp.call(a.tool, args)
                break
            except Exception as e:
                if attempt == 2:
                    return {"corpus": q["corpus"], "query_id": q["query_id"], "error": str(e)[:300]}
                time.sleep(5 * (attempt + 1))
        hits = res.get("hits") or []
        r = rel.get((q["corpus"], q["query_id"]), {})
        row = {"corpus": q["corpus"], "query_id": q["query_id"], "group": q["group"], "language": q["language"], "ms": int((time.time() - t) * 1000),
               "cosine": ranking(hits, "score", pages, q["corpus"])}
        row["cosine_scores"] = scores(row["cosine"], r)
        if a.rerank != "none":
            row["rerank"] = ranking(hits, None, pages, q["corpus"])
            row["rerank_scores"] = scores(row["rerank"], r)
        return row

    n = 0
    with cf.ThreadPoolExecutor(a.workers) as ex:
        for row in ex.map(ask, todo):
            with lock:
                out.write(json.dumps(row) + "\n")
                out.flush()
                n += 1
                if n % 200 == 0 or n == len(todo):
                    print("%d/%d %.0fs" % (n, len(todo), time.time() - t0), flush=True)
    out.close()


if __name__ == "__main__":
    main()
