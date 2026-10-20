#!/usr/bin/env python3
"""The numbers of a ViDoRe V3 run, as a gosidian-rag-bench/1 JSON.

    python3 report.py TABLES RUNS_DIR OUT.json [--meta meta.json]

RUNS_DIR holds what the other scripts wrote: paragraphs.jsonl and
pages.jsonl (search.py), answers.jsonl (answers.py collect) and, when
measured, times.jsonl (answers.py answer --sequential --times). --meta
adds the run's description (date, versions, hardware, configuration),
merged over the computed fields. Standard library only.
"""
import argparse, collections, json, os, statistics as st

CORPORA = ["hr", "finance_en", "industrial", "pharmaceuticals", "computer_science", "energy", "physics", "finance_fr"]
LANGS = ["english", "french", "spanish", "german", "italian", "portuguese"]
# The paper's NDCG@10 (arXiv 2601.08620, table 1 and table 2), per public corpus.
PAPER_QWEN3_8B = {"computer_science": 71.7, "finance_en": 49.4, "pharmaceuticals": 59.2, "hr": 47.6, "industrial": 40.4, "physics": 45.6, "energy": 58.9, "finance_fr": 35.8}
PAPER_RERANK = {"computer_science": 82.1, "finance_en": 69.2, "pharmaceuticals": 66.2, "hr": 66.5, "industrial": 53.2, "physics": 48.2, "energy": 71.5, "finance_fr": 53.7}


def mean(xs):
    xs = [x for x in xs if x is not None]
    return round(st.mean(xs), 4) if xs else None


def load(path):
    return [json.loads(l) for l in open(path)] if os.path.exists(path) else []


def search_tables(rows, queries):
    by = collections.defaultdict(list)
    for r in rows:
        if "error" in r:
            continue
        q = queries.get((r["corpus"], r["query_id"]), {})
        for key in ("cosine", "rerank"):
            s = r.get(key + "_scores")
            if s:
                by[("all", key)].append(s)
                by[("corpus", r["corpus"], key)].append(s)
                by[("language", r["language"], key)].append(s)
                for t in q.get("query_types") or []:
                    by[("type", t, key)].append(s)
                for t in q.get("content_type") or []:
                    by[("content", t, key)].append(s)
    def m(k):
        v = by.get(k) or []
        return {x: mean([s[x] for s in v]) for x in ("ndcg10", "recall10", "mrr10")} | {"questions": len(v)}
    out = {"all": {k: m(("all", k)) for k in ("cosine", "rerank")}}
    for dim in ("corpus", "language", "type", "content"):
        keys = sorted({k[1] for k in by if k[0] == dim})
        out[dim] = {x: {k: m((dim, x, k)) for k in ("cosine", "rerank")} for x in keys}
    out["errors"] = sum(1 for r in rows if "error" in r)
    out["ms_median"] = st.median([r["ms"] for r in rows if "ms" in r]) if rows else None
    return out


def answer_tables(rows):
    judges = sorted({k[6:] for r in rows for k in r if k.startswith("score_")})
    def agg(rs):
        rs = [r for r in rs if r.get("answered") and all(("score_" + j) in r for j in judges)]
        if not rs:
            return None
        sc = [st.mean(r["score_" + j] for j in judges) for r in rs]
        return {"answers": len(rs), "score": round(st.mean(sc), 2), "good": round(sum(s >= 7 for s in sc) / len(sc), 3),
                "wrong": round(sum(s < 0 for s in sc) / len(sc), 3), "not_found": round(sum(bool(r.get("not_found")) for r in rs) / len(rs), 3),
                "invented": round(sum(any(r.get("invented_" + j) for j in judges) for r in rs) / len(rs), 3),
                **{"score_" + j: round(st.mean(r["score_" + j] for r in rs), 2) for j in judges},
                "judges_gap": round(st.mean(abs(r["score_" + judges[0]] - r["score_" + judges[-1]]) for r in rs), 2) if len(judges) > 1 else None}
    out = {"judges": judges, "all": agg(rows)}
    out["corpus"] = {c: agg([r for r in rows if r["corpus"] == c]) for c in CORPORA if any(r["corpus"] == c for r in rows)}
    out["language"] = {l: agg([r for r in rows if r["language"] == l]) for l in LANGS if any(r["language"] == l for r in rows)}
    types = sorted({t for r in rows for t in r.get("query_types") or []})
    out["type"] = {t: agg([r for r in rows if t in (r.get("query_types") or [])]) for t in types}
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("tables"); ap.add_argument("runs"); ap.add_argument("out"); ap.add_argument("--meta")
    a = ap.parse_args()
    queries = {(q["corpus"], q["query_id"]): q for q in load(os.path.join(a.tables, "queries.jsonl"))}
    res = {"search": {}, "paper": {"qwen3_embedding_8b": PAPER_QWEN3_8B, "jina_v4_zerank_2": PAPER_RERANK,
                                   "qwen3_embedding_8b_mean8": round(st.mean(PAPER_QWEN3_8B.values()), 1), "jina_v4_zerank_2_mean8": round(st.mean(PAPER_RERANK.values()), 1)}}
    for index in ("paragraphs", "pages"):
        rows = load(os.path.join(a.runs, index + ".jsonl"))
        if rows:
            res["search"][index] = search_tables(rows, queries)
    ans = load(os.path.join(a.runs, "answers.jsonl"))
    if ans:
        res["answers"] = answer_tables(ans)
    times = [t["seconds"] for t in load(os.path.join(a.runs, "times.jsonl")) if t.get("status") == "done"]
    if times:
        times.sort()
        res["times"] = {"answers": len(times), "median_s": round(st.median(times), 2), "p95_s": round(times[int(0.95 * (len(times) - 1))], 2), "max_s": round(times[-1], 2)}
    out = {"format": "gosidian-rag-bench/1", "kind": "reference", "corpus": {"label": "ViDoRe V3", "public": True}, "results": res}
    if a.meta:
        meta = json.load(open(a.meta))
        out = {**out, **meta, "results": {**res, **(meta.get("results") or {})}}
    json.dump(out, open(a.out, "w"), ensure_ascii=False, indent=2)


if __name__ == "__main__":
    main()
