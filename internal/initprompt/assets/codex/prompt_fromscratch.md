# memory_init_agent — modalità from-scratch (OpenAI Codex / AGENTS.md)

Hai ricevuto il payload dal tool MCP `memory_init_agent`. **Ruolo**:
creare `AGENTS.md` da zero nel progetto **{{PROJECT}}**, innestando il
`gosidian_block`. Il `gosidian_block` è uno **stub sottile** (Regola Zero
→ bootstrap + specifiche locali): le direttive operative complete arrivano
dal `directives_block` di `memory_bootstrap`, non da questo file.

## Step 1 — Determina filename

**`AGENTS.md`** nella root della cwd. Se esiste già → modalità errata,
fermati e chiedi all'utente se passare ad augment o sovrascrivere.

## Step 2 — Scan cwd

Letture read-only top-level + uno hop. Ls + manifest principali
(`package.json`, `go.mod`, `pyproject.toml`, ecc.), README.md se
presente. Max 10 file.

## Step 3 — Pre-check vault

Se `needs_scaffold=true`, chiama `memory_project_scaffold` con
`project="{{PROJECT}}"`, `template="karpathy-wiki"`.

## Step 4 — Raccogli placeholder

{{PLACEHOLDER_STATUS}} Per quelli aperti: deduci + chiedi.

## Step 5 — Compila file

```md
# {{PROJECT}} — AGENTS.md

> File generato da `memory_init_agent` ({{TODAY}}) per **OpenAI Codex**.
> Memoria persistente in `{{PROJECT}}/` del vault gosidian.

<3-5 righe di header contestuale dallo scan>

<gosidian_block con placeholder risolti>
```

Non inventare sezioni (Build / Test / ecc.) oltre a quello che puoi
ricavare direttamente dallo scan.

## Step 6 — Materializza

Scrivi `AGENTS.md`.

## Step 7 — Primo ingest (facoltativo)

Seed `architecture.md` e `hot.md` se sensato.

## Step 8 — Conferma all'utente

Sommario: file creato, placeholder risolti, prossimo passo.
