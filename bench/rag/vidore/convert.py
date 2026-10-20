#!/usr/bin/env python3
"""Turn the ViDoRe V3 parquet files into gosidian notes and benchmark tables.

    python3 convert.py notes  DATA_DIR VAULT_FOLDER [--paragraph-chars N] [corpus ...]
    python3 convert.py tables DATA_DIR OUT_DIR      [corpus ...]

notes: one note per document, VAULT_FOLDER/<corpus>/<doc_id>.md, with a
section "## Page N" per page holding the page's markdown as the dataset
gives it. Headings inside a page become bold lines, so that a section is
always a page. The dataset's page text has no blank lines, so a page would
be one paragraph: its lines are joined into paragraphs of at least N
characters (default 600; 0 keeps the page one block), always at the end
of a line, so that a cut in paragraphs has pieces smaller than a page. Write them into a folder of the vault (as the user the
server runs as): gosidian indexes them as they appear.

tables: OUT_DIR/pages.jsonl (corpus, corpus_id, doc_id, page),
queries.jsonl (corpus, query_id, group, language, query, query_types,
query_format, content_type, answer) and qrels.jsonl (corpus, query_id,
corpus_id, score). The translations of a question share its relevant
pages: "group" joins them (the qrels set), so a question can be asked in
its six languages.

Needs pyarrow (pip install pyarrow==25.0.1).
"""
import glob, json, os, re, sys
import pyarrow.parquet as pq

CORPORA = ["hr", "finance_en", "industrial", "pharmaceuticals", "computer_science", "energy", "physics", "finance_fr"]
HEADING = re.compile(r"^#{1,6}\s+(.*)$")


def rows(data, corpus, config, columns=None):
    for f in sorted(glob.glob(os.path.join(data, corpus, config, "*.parquet"))):
        for b in pq.ParquetFile(f).iter_batches(batch_size=256, columns=columns):
            yield from b.to_pylist()


def page_text(md, chars):
    lines = []
    for line in (md or "").replace("\r\n", "\n").split("\n"):
        if not line.strip():
            continue
        m = HEADING.match(line)
        lines.append("**%s**" % m.group(1).strip() if m and m.group(1).strip() else line.rstrip())
    if chars <= 0:
        return "\n".join(lines)
    paras, cur = [], []
    for line in lines:
        cur.append(line)
        if sum(len(x) + 1 for x in cur) >= chars:
            paras.append("\n".join(cur))
            cur = []
    if cur:
        paras.append("\n".join(cur))
    return "\n\n".join(paras)


def yaml_str(s):
    return json.dumps(str(s), ensure_ascii=False)


def notes(data, folder, corpus, chars):
    rev = json.load(open(os.path.join(data, corpus, "revision.json")))
    meta = {m["doc_id"]: m for m in rows(data, corpus, "documents_metadata")}
    pages = {}
    for r in rows(data, corpus, "corpus", ["doc_id", "page_number_in_doc", "markdown"]):
        pages.setdefault(r["doc_id"], []).append((r["page_number_in_doc"], r["markdown"]))
    os.makedirs(os.path.join(folder, corpus), exist_ok=True)
    for doc, ps in sorted(pages.items()):
        ps.sort()
        m = meta.get(doc, {})
        fm = ["---", "title: " + yaml_str(doc), "type: vidore-document", "corpus: " + corpus, "doc_id: " + yaml_str(doc),
              "language: " + yaml_str(m.get("doc_language", "")), "pages: %d" % len(ps), "license: " + yaml_str(m.get("license") or rev.get("license") or ""),
              "source_url: " + yaml_str(m.get("url", "")), "dataset: " + yaml_str("%s@%s" % (rev["dataset"], rev["revision"])), "---", ""]
        body = []
        for n, md in ps:
            body.append("## Page %d\n\n%s\n" % (n, page_text(md, chars)))
        with open(os.path.join(folder, corpus, doc + ".md"), "w", encoding="utf-8") as f:
            f.write("\n".join(fm) + "\n".join(body))
    return len(pages), sum(len(p) for p in pages.values())


def tables(data, out, corpus):
    with open(os.path.join(out, "pages.jsonl"), "a", encoding="utf-8") as f:
        for r in rows(data, corpus, "corpus", ["corpus_id", "doc_id", "page_number_in_doc"]):
            f.write(json.dumps({"corpus": corpus, "corpus_id": r["corpus_id"], "doc_id": r["doc_id"], "page": r["page_number_in_doc"]}) + "\n")
    rel = {}
    with open(os.path.join(out, "qrels.jsonl"), "a", encoding="utf-8") as f:
        for r in rows(data, corpus, "qrels", ["query_id", "corpus_id", "score"]):
            rel.setdefault(r["query_id"], set()).add((r["corpus_id"], r["score"]))
            f.write(json.dumps({"corpus": corpus, "query_id": r["query_id"], "corpus_id": r["corpus_id"], "score": r["score"]}) + "\n")
    groups = {}
    n = 0
    with open(os.path.join(out, "queries.jsonl"), "a", encoding="utf-8") as f:
        for r in rows(data, corpus, "queries"):
            key = tuple(sorted(rel.get(r["query_id"], ())))
            g = groups.setdefault(key, "%s-%d" % (corpus, len(groups)))
            q = {k: r.get(k) for k in ["query_id", "language", "query", "query_types", "query_format", "content_type", "answer", "query_generator"]}
            for k in ("query_types", "content_type"):
                if isinstance(q[k], str):
                    q[k] = [x.strip(" '\"") for x in q[k].strip("[]").split(",") if x.strip()]
            f.write(json.dumps(dict(q, corpus=corpus, group=g), ensure_ascii=False) + "\n")
            n += 1
    return n, len(groups)


def main():
    mode, data, out, args = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4:]
    chars = 600
    if "--paragraph-chars" in args:
        i = args.index("--paragraph-chars")
        chars = int(args[i + 1])
        del args[i:i + 2]
    names = args or CORPORA
    if mode == "tables":
        os.makedirs(out, exist_ok=True)
        for f in ("pages.jsonl", "queries.jsonl", "qrels.jsonl"):
            open(os.path.join(out, f), "w").close()
    for c in names:
        if mode == "notes":
            print(c, "documents %d, pages %d" % notes(data, out, c, chars), flush=True)
        else:
            print(c, "queries %d, groups %d" % tables(data, out, c), flush=True)


if __name__ == "__main__":
    main()
