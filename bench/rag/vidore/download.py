#!/usr/bin/env python3
"""Download the public ViDoRe V3 corpora (Hugging Face, CC-BY 4.0) at a fixed revision.

    python3 download.py DATA_DIR [--latest] [corpus ...]

Each corpus goes to DATA_DIR/<corpus>/ with its parquet files and a
revision.json (the dataset's commit). The revision is the one the
reference results were measured on (REVISIONS); --latest takes the
dataset's current commit instead. A file already there with the expected
size is not downloaded again. Standard library only.
"""
import json, os, sys, urllib.request

CORPORA = ["hr", "finance_en", "industrial", "pharmaceuticals", "computer_science", "energy", "physics", "finance_fr"]
API = "https://huggingface.co/api/datasets/vidore/vidore_v3_%s"
# The commits of bench/results/rag-2026-10-20-vidore-v3.json.
REVISIONS = {
    "hr": "0cdf0979f2c5a0fd3e335e6373b9da48a9fe3bc3",
    "finance_en": "7f432c176d82e27546501ad8064a713ac3071809",
    "industrial": "e26c864724f5dd71a3d7d739272d95637764cee9",
    "pharmaceuticals": "3abd4aa8a9445fb5538a78a19ba50bd57bd22b5c",
    "computer_science": "d5cc75883d92e294f0c0fc2662551c9708a06ebc",
    "energy": "caec06d3c73434d635f710f93bcd898331c59f20",
    "physics": "a0de276f515acc044b72cae8de53a44bb5a8f1f5",
    "finance_fr": "1d808daa08032ffecdf62da151a7f7a8fe2bd0c9",
}


def get(url):
    with urllib.request.urlopen(urllib.request.Request(url, headers={"User-Agent": "gosidian-bench"}), timeout=60) as r:
        return json.load(r)


def files(name, sha, path=""):
    for f in get(API % name + "/tree/%s/%s" % (sha, path)):
        if f["type"] == "directory":
            yield from files(name, sha, f["path"])
        elif f["path"].endswith(".parquet"):
            yield f["path"], f["size"]


def main():
    args = sys.argv[2:]
    latest = "--latest" in args
    out, names = sys.argv[1], [a for a in args if a != "--latest"] or CORPORA
    for name in names:
        info = get(API % name if latest else API % name + "/revision/" + REVISIONS[name])
        sha = info["sha"]
        d = os.path.join(out, name)
        os.makedirs(d, exist_ok=True)
        for path, size in files(name, sha):
            dst = os.path.join(d, path)
            if os.path.exists(dst) and os.path.getsize(dst) == size:
                continue
            os.makedirs(os.path.dirname(dst), exist_ok=True)
            url = "https://huggingface.co/datasets/vidore/vidore_v3_%s/resolve/%s/%s" % (name, sha, path)
            print("%s %s %.0f MB" % (name, path, size / 1e6), flush=True)
            tmp = dst + ".part"
            urllib.request.urlretrieve(url, tmp)
            if os.path.getsize(tmp) != size:
                sys.exit("%s: %d bytes, expected %d" % (dst, os.path.getsize(tmp), size))
            os.replace(tmp, dst)
        json.dump({"dataset": "vidore/vidore_v3_" + name, "revision": sha, "license": (info.get("cardData") or {}).get("license")},
                  open(os.path.join(d, "revision.json"), "w"), indent=1)
        print("%s done at %s" % (name, sha[:12]), flush=True)


if __name__ == "__main__":
    main()
