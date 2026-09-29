# memory_init_agent — modalità from-scratch (Cursor)

Hai ricevuto il payload dal tool MCP `memory_init_agent`. **Ruolo**:
creare da zero le Cursor Rules del progetto **{{PROJECT}}** includendo
il `gosidian_block`. Il `gosidian_block` è uno **stub sottile** (Regola
Zero → bootstrap + specifiche locali): le direttive operative complete
arrivano dal `directives_block` di `memory_bootstrap`, non da questo file.

## Step 1 — Determina filename

**Default consigliato**: nuovo formato `.cursor/rules/gosidian-memory.mdc`.
Se l'utente preferisce il legacy `.cursorrules` chiedilo esplicitamente
prima di scrivere.

Se esiste già uno dei due → **fermati**: dovevi essere chiamato in
modalità augment. Chiedi all'utente.

## Step 2 — Scan cwd (letture read-only)

Ls root + lettura mirata di manifest (`package.json`, `tsconfig.json`,
`next.config.*`, `go.mod`, `pyproject.toml`, ecc.). Massimo 10 file,
top-level + uno hop.

## Step 3 — Pre-check vault

Se `needs_scaffold=true`, chiama `memory_project_scaffold` con
`project="{{PROJECT}}"`, `template="karpathy-wiki"`.

## Step 4 — Raccogli placeholder

{{PLACEHOLDER_STATUS}} Per quelli aperti: deduci dallo scan + chiedi
quel che manca.

## Step 5 — Compila file

Nuovo formato `.cursor/rules/gosidian-memory.mdc`:

```mdc
---
description: Gosidian memory layer — session bootstrap, ingest rules, workflow end-of-task
globs:
  - "**/*"
alwaysApply: true
---

# {{PROJECT}} — Cursor Rules (gosidian memory layer)

> File generato da `memory_init_agent` ({{TODAY}}) per **Cursor**.

<gosidian_block con placeholder risolti>
```

Formato legacy: come sopra ma senza frontmatter, nel file
`.cursorrules`.

## Step 6 — Materializza

Crea la dir `.cursor/rules/` se serve, poi scrive il file.

## Step 7 — Primo ingest (facoltativo)

Seed `architecture.md` e `hot.md` se lo scan lo giustifica.

## Step 8 — Conferma all'utente

Path file creato + formato + prossimo passo ("riavvia Cursor perché
legga la nuova rule").
