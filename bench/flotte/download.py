#!/usr/bin/env python3
"""Download the fixed open-access corpus of the fitness pilot (IMP-195 phase 1c).

    python3 download.py DIR [--only id,id] [--force]

Reads corpus.json next to this script and saves each document as
DIR/<id>.pdf. A file already there with the expected SHA-256 is not
downloaded again (--force downloads it anyway). Every download is checked
against the SHA-256 in corpus.json: on a mismatch the bytes stay in
DIR/<id>.pdf.part, never under the final name, and the script exits 1.
Network errors are retried a couple of times. One line per document.
Standard library only.
"""
import argparse, hashlib, http.client, json, os, sys, time, urllib.error, urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
USER_AGENT = "gosidian-bench-flotte/1 (fitness pilot corpus downloader; python-urllib)"
ATTEMPTS = 3  # first try plus two retries
CHUNK = 1 << 16


def sha256_of(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for block in iter(lambda: f.read(CHUNK), b""):
            h.update(block)
    return h.hexdigest()


def fetch(url, dst):
    """Stream url into dst; return (sha256, bytes, first bytes). Retries transient errors."""
    for attempt in range(1, ATTEMPTS + 1):
        try:
            req = urllib.request.Request(url, headers={"User-Agent": USER_AGENT})
            with urllib.request.urlopen(req, timeout=120) as r, open(dst, "wb") as out:
                h, n, head = hashlib.sha256(), 0, b""
                for block in iter(lambda: r.read(CHUNK), b""):
                    if not head:
                        head = block[:8]
                    h.update(block)
                    out.write(block)
                    n += len(block)
            return h.hexdigest(), n, head
        except urllib.error.HTTPError as e:
            # Client errors (except rate limiting) will not fix themselves.
            if (e.code < 500 and e.code != 429) or attempt == ATTEMPTS:
                raise
            err = e
        except (urllib.error.URLError, OSError, http.client.HTTPException) as e:  # DNS, reset, timeout, truncated body
            if attempt == ATTEMPTS:
                raise
            err = e
        print("   retry %d/%d after %s" % (attempt, ATTEMPTS - 1, err), flush=True)
        time.sleep(2 * attempt)


def main():
    ap = argparse.ArgumentParser(description="Download the fitness pilot corpus listed in corpus.json.")
    ap.add_argument("dir", help="destination directory (created if missing)")
    ap.add_argument("--only", help="comma-separated document ids to download (default: all)")
    ap.add_argument("--force", action="store_true", help="download again even if the file already matches")
    args = ap.parse_args()

    with open(os.path.join(HERE, "corpus.json")) as f:
        docs = json.load(f)["documents"]
    if args.only:
        wanted = [i.strip() for i in args.only.split(",") if i.strip()]
        unknown = sorted(set(wanted) - {d["id"] for d in docs})
        if unknown:
            sys.exit("unknown id(s): %s" % ", ".join(unknown))
        docs = [d for d in docs if d["id"] in wanted]
    os.makedirs(args.dir, exist_ok=True)

    failed = []
    for d in docs:
        dst = os.path.join(args.dir, d["id"] + ".pdf")
        if not args.force and os.path.exists(dst) and sha256_of(dst) == d["sha256"]:
            print("%-48s skip      %6.2f MB  sha256 ok (already present)" % (d["id"], d["bytes"] / 1e6), flush=True)
            continue
        part = dst + ".part"
        try:
            sha, n, head = fetch(d["url"], part)
        except Exception as e:
            print("%-48s FAILED    %s (%s)" % (d["id"], e, d["url"]), flush=True)
            failed.append(d["id"])
            continue
        if sha != d["sha256"]:
            hint = "" if head.startswith(b"%PDF") else "; not a PDF, starts with %r" % head
            print("%-48s MISMATCH  sha256 %s, expected %s (%d bytes, expected %d%s); kept as %s"
                  % (d["id"], sha, d["sha256"], n, d["bytes"], hint, part), flush=True)
            failed.append(d["id"])
            continue
        os.replace(part, dst)
        print("%-48s download  %6.2f MB  sha256 ok" % (d["id"], n / 1e6), flush=True)

    if failed:
        sys.exit("%d of %d document(s) not verified: %s" % (len(failed), len(docs), ", ".join(failed)))
    print("%d document(s) verified in %s" % (len(docs), args.dir), flush=True)


if __name__ == "__main__":
    main()
