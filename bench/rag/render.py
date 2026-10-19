#!/usr/bin/env python3
"""Write the tables of docs/rag-benchmarks.md from bench/results/rag-*.json.

The JSON files are the source of truth; the page keeps its prose and every
table sits between two markers:

    <!-- rag-table src="rag-2026-10-16-answers-a" rows="variants" cols="label=Variant;score:.2f=Score;good:%=Good" -->
    (generated)
    <!-- /rag-table -->

src    the JSON file in bench/results/, without ".json"
rows   the list to render: a dotted path inside the JSON ("results.levels")
cols   the columns, separated by ";", each "key[:format]=Header".
       Formats: ".Nf" (N decimals), "%" (a share as a whole percentage),
       "int" (thousands separator); without one the value prints as it is
where  optional filter: "key=value" pairs separated by ";", all must match;
       "key=a|b" matches either value
bold   optional: the key of the column to bold in a row with "chosen": true
       (default: the first column)

A missing value prints as an em dash. Standard library only.

    python3 bench/rag/render.py            # rewrite the tables in place
    python3 bench/rag/render.py --check    # exit 1 if a table is out of date
"""

import argparse
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
RESULTS = ROOT / "bench" / "results"
DEFAULT_PAGE = ROOT / "docs" / "rag-benchmarks.md"

BLOCK = re.compile(r"(<!-- rag-table (.*?) -->\n)(.*?)(<!-- /rag-table -->)", re.S)
ATTR = re.compile(r'(\w+)="([^"]*)"')


def load(src, cache):
    if src not in cache:
        with open(RESULTS / f"{src}.json", encoding="utf-8") as f:
            cache[src] = json.load(f)
    return cache[src]


def walk(data, path):
    for part in path.split("."):
        data = data[part]
    return data


def cell(value, fmt):
    if value is None or value == "":
        return "—"
    if isinstance(value, bool):
        return "yes" if value else "no"
    if isinstance(value, list):
        return ", ".join(str(v) for v in value)
    if isinstance(value, (int, float)):
        if fmt == "%":
            return f"{round(value * 100)}%"
        if fmt == "int":
            return f"{value:,}"
        if fmt and fmt.startswith(".") and fmt.endswith("f"):
            return format(value, fmt)
    return str(value).replace("|", "\\|")


def table(attrs, cache):
    data = load(attrs["src"], cache)
    rows = walk(data, attrs["rows"])
    conds = [c.split("=", 1) for c in attrs.get("where", "").split(";") if c]
    rows = [r for r in rows if all(str(r.get(k)) in v.split("|") for k, v in conds)]
    cols = []
    for spec in attrs["cols"].split(";"):
        key, header = spec.split("=", 1)
        key, _, fmt = key.partition(":")
        cols.append((key, fmt, header))
    lines = ["| " + " | ".join(h for _, _, h in cols) + " |",
             "|" + "---|" * len(cols)]
    keys = [k for k, _, _ in cols]
    bold = keys.index(attrs["bold"]) if "bold" in attrs else 0
    for r in rows:
        cells = [cell(r.get(k), f) for k, f, _ in cols]
        if r.get("chosen") and cells[bold] != "—":
            cells[bold] = f"**{cells[bold]}**"
        lines.append("| " + " | ".join(cells) + " |")
    return "\n".join(lines) + "\n"


def render(text, cache):
    def repl(m):
        attrs = dict(ATTR.findall(m.group(2)))
        return m.group(1) + table(attrs, cache) + m.group(4)
    return BLOCK.sub(repl, text)


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--check", action="store_true", help="only report pages whose tables are out of date")
    ap.add_argument("pages", nargs="*", type=Path, default=[DEFAULT_PAGE])
    args = ap.parse_args()
    cache, stale = {}, []
    for page in args.pages:
        text = page.read_text(encoding="utf-8")
        new = render(text, cache)
        if new != text:
            stale.append(page)
            if not args.check:
                page.write_text(new, encoding="utf-8")
    if args.check and stale:
        print("out of date: " + ", ".join(str(p) for p in stale), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
