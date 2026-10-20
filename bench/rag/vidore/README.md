# ViDoRe V3, end to end

The public reference of [docs/rag-benchmarks.md](../../../docs/rag-benchmarks.md):
gosidian's RAG pipeline measured on [ViDoRe V3](https://arxiv.org/abs/2601.08620),
a benchmark of professional documents (reports, manuals, financial
statements, lecture notes, regulations) with questions checked by hand
in six languages. Its eight public corpora are on Hugging Face under
CC-BY 4.0 (`vidore/vidore_v3_<corpus>`): 189 documents, 19,252 pages,
14,514 questions (2,419 in six languages each). Anyone with a gosidian
server, the database plugin and a GPU for the models can run it again;
the numbers of the maintainers' lab are the reference.

What it measures:

- **search**, on every question: the chunks the archive finds become
  pages, scored against the dataset's relevant pages (NDCG@10 with
  graded relevance, recall@10, MRR@10), for the cosine order and the
  reranker's, on two indexes: paragraphs and whole pages;
- **answers**, on a sample (25 questions per corpus, each in its six
  languages): the reference configuration answers in the question's
  language, and two local models judge each answer against the
  reference answer, as in the rest of the page;
- **time**: the answers of a run one at a time.

## What you need

- a gosidian server with pipelines on (`GOSIDIAN_PIPELINES_ENABLED`),
  an LLM endpoint for `llm.chat` (the answer and judge models) and an
  MCP token that can write the project;
- the database plugin ([gosidian-plugins](https://github.com/daniele-chiappa/gosidian-plugins),
  0.5.0 or later) with an embedding model and a reranker in its preset,
  exposed in the project; set `limits.max_items` to 5000 or more: the
  largest document loads its paragraphs in one call;
- Python 3 with `pyarrow` 25 for `convert.py` (the other scripts use the
  standard library);
- about 9 GB for the parquet files and 70 MB of notes.

The lab's models: Qwen3-Embedding-8B (Q4_K_M on Ollama, 2048 numbers),
bge-reranker-v2-m3 (llama-server), `gemma4:12b` for the answers and
`gemma4:12b` and `qwen3:14b` as judges, with a context of 12,288 tokens.

## Steps

```bash
export GOSIDIAN_MCP_URL=https://your-server/mcp
T=token.txt                                  # a file with the MCP token

# 1. The corpora at the revision of the reference results (8.4 GB;
#    --latest takes the current one) and the tables.
python3 download.py data
python3 convert.py tables data tables

# 2. One note per document in the project's folder documents/: write them
#    into the vault (as the server's user); gosidian indexes them.
python3 convert.py notes data /path/to/vault/vidore/documents

# 3. Create the project vidore with pipelines on (Projects window, chip
#    "pipelines"), expose the database plugin in it
#    (gosidian plugin expose <plugin> vidore), and copy pipelines/*.md into
#    vidore/pipelines/ (the node names say vec-qdrant: change them for your
#    plugin's name).

# 4. The two indexes: every document in paragraphs and in pages.
python3 cut.py cut-done.log --token-file $T

# 5. Search, on every question, with the reranker (both orders in one run).
python3 search.py tables runs/paragraphs.jsonl --collection vidore_paragraphs \
    --rerank bge-reranker-v2-m3 --k 40 --candidates 40 --token-file $T
python3 search.py tables runs/pages.jsonl --collection vidore_pages \
    --rerank bge-reranker-v2-m3 --k 20 --candidates 20 --token-file $T

# 6. Answers and judges on the sample. The answers search vidore_paragraphs:
#    with other collection names, add --set collection=<name> to answer.
python3 answers.py sample tables sample.jsonl
python3 answers.py answer  sample.jsonl --variant reference --token-file $T
python3 answers.py judge   sample.jsonl --variant reference --judge gemma4:12b-12k --token-file $T
python3 answers.py judge   sample.jsonl --variant reference --judge qwen3:14b-12k --token-file $T
python3 answers.py collect sample.jsonl --variant reference runs/answers.jsonl --token-file $T

# 7. The numbers, as the JSON of bench/results.
python3 report.py tables runs report.json
```

A step that stops goes on where it was when started again: `cut.py`
skips the documents in its log, `search.py` the questions already in its
output, `answers.py` the answers and scores already in the notes.

## Choices to know

- **The text is the dataset's**: the markdown of each page as the
  dataset gives it (the OCR of the benchmark's authors), the same text
  the paper's textual retrievers read; no conversion, no image
  descriptions. `convert.py` turns headings inside a page into bold lines,
  so that a section is always a page.
- **Paragraphs**: the page text has no blank lines, so a page would be
  one paragraph. `convert.py` joins its lines into paragraphs of at least
  600 characters, always at the end of a line (`--paragraph-chars`; 0
  keeps a page one block). The paper found that chunking within pages did
  not help; gosidian's reference cut is in paragraphs, with the page as
  the section a model reads.
- **Translations**: the six translations of a question have no common id
  in the dataset; they share their relevant pages, which `convert.py`
  uses to group them (`group`). A few groups hold two questions with the
  same pages; the sample leaves them out.
- **Not the paper's numbers**: two of the ten corpora are private, so
  the averages are over eight; the reranker, the embedding size and the
  quantization differ; the paper's end-to-end correctness is another
  judge's yes or no, ours a score from -10 to 10.
