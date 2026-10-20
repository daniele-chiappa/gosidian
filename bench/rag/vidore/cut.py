#!/usr/bin/env python3
"""Cut the ViDoRe V3 documents into the two collections, one document at a time.

    python3 cut.py DONE.log [corpus ...] [--paragraphs vidore_paragraphs] [--pages vidore_pages]
                   [--model qwen3-embedding-8b-2048] [--page-chars 20000]

For each document note of a corpus (documents/<corpus>/ in the project) it
runs the project's pipelines/cut twice, in paragraphs and in pages, and
waits for each run. A pipeline starting one run per document would gather
every run's output in one step, more than a step may hold for a large
corpus: here each run stands alone. DONE.log gets a line per run that
ended well, and a document already there is not cut again.

The connection: --url and --token-file, or GOSIDIAN_MCP_URL and
GOSIDIAN_TOKEN; --project (default vidore). Standard library only.
"""
import argparse, json, os, re, sys, time
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from search import MCP

CORPORA = ["hr", "finance_en", "industrial", "pharmaceuticals", "computer_science", "energy", "physics", "finance_fr"]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("done"); ap.add_argument("corpora", nargs="*")
    ap.add_argument("--paragraphs", default="vidore_paragraphs"); ap.add_argument("--pages", default="vidore_pages")
    ap.add_argument("--model", default="qwen3-embedding-8b-2048"); ap.add_argument("--page-chars", type=int, default=20000)
    ap.add_argument("--url"); ap.add_argument("--token-file"); ap.add_argument("--project", default="vidore")
    a = ap.parse_args()
    token = open(a.token_file).read().strip() if a.token_file else os.environ["GOSIDIAN_TOKEN"]
    m = re.search(r"gosidian_[A-Za-z0-9_-]+", token)
    mcp = MCP(a.url or os.environ["GOSIDIAN_MCP_URL"], m.group(0) if m else token)
    done = set(l.strip() for l in open(a.done)) if os.path.exists(a.done) else set()
    log = open(a.done, "a")
    say = lambda *x: print(time.strftime("%H:%M:%S"), *x, flush=True)
    for corpus in a.corpora or CORPORA:
        # memory_list_notes lists the whole project: the corpus's documents are those of its folder.
        prefix = "%s/documents/%s/" % (a.project, corpus)
        notes = [n for n in mcp.call("memory_list_notes", {"project": a.project}).get("notes") or []
                 if n["path"].startswith(prefix) and n["path"].endswith(".md") and "/" not in n["path"][len(prefix):]]
        t0, bad = time.time(), 0
        for n in notes:
            for coll, levels, chars in ((a.paragraphs, ["paragraph"], 6000), (a.pages, ["section"], a.page_chars)):
                key = "%s %s" % (coll, n["path"])
                if key in done:
                    continue
                inp = {"note": n["path"], "collection": coll, "levels": levels, "max_chars": chars, "model": a.model}
                r = mcp.call("memory_pipeline_run", {"path": a.project + "/pipelines/cut.md", "input": inp, "wait": 600})
                run = r.get("run") or r
                while run.get("status") not in ("done", "failed", "canceled"):
                    time.sleep(5)
                    run = mcp.call("memory_pipelines", {"project": a.project, "run": run["id"]})["run"]
                if run["status"] == "done":
                    log.write(key + "\n"); log.flush()
                else:
                    bad += 1
                    say("ALERT", corpus, n["path"], coll, run["status"], str(run.get("error"))[:300])
        cs = {c["name"]: c["count"] for c in mcp.call("vec-qdrant_collections", {"project": a.project})["collections"]}
        say("END" if not bad else "ALERT", corpus, "%d documents" % len(notes), "%.0fs" % (time.time() - t0),
            "paragraphs", cs.get(a.paragraphs), "pages", cs.get(a.pages), "failed runs", bad)
    say("-- cut done")


if __name__ == "__main__":
    main()
