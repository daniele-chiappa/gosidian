# memory_init_agent — modalità augment (generic / AGENTS.md)

Hai ricevuto il payload dal tool MCP `memory_init_agent`. **Ruolo**:
integrare le regole gosidian nel file di istruzioni generico dell'agent
(tipicamente `AGENTS.md`) preservando il contenuto esistente.

- `gosidian_block` — **stub sottile** parametrico: Regola Zero (che
  punta a `memory_bootstrap` per le direttive) + specifiche locali. Le
  direttive operative complete (mappa cartelle, ingest rules, workflow,
  tag) **non** sono qui: le serve `memory_bootstrap` nel campo
  `directives_block`. {{PLACEHOLDER_STATUS}}
- `needs_scaffold` bool, `mode: "augment"`.

## Step 1 — Determina filename

Se `filename_hint` presente → usalo. Altrimenti il tuo ambiente decide:
molti agent moderni convergono su **`AGENTS.md`** come standard comune.
Se sei incerto, chiedi all'utente qual è il file che il tuo runtime
carica automaticamente.

## Step 2 — Pre-check vault

Se `needs_scaffold=true`, chiama
`mcp__gosidian__memory_project_scaffold(project="{{PROJECT}}",
template="karpathy-wiki")`.

## Step 3 — Raccogli placeholder

Solo i placeholder ancora aperti (vedi sopra): da `existing_content` o
chiedi all'utente.

## Step 4 — Merge

- Mantieni tutte le sezioni esistenti.
- Inserisci `gosidian_block` come blocco unitario. {{STUB_PLACEMENT}}
- Risolvi i placeholder prima di scrivere.
- Conflitti → fermati e chiedi all'utente.

## Step 5 — Materializza

Scrivi il file completo sul filename determinato allo Step 1.

## Step 6 — Primo ingest (facoltativo)

Seed `architecture.md` e `hot.md` via `mcp__gosidian__memory_edit` se
dallo scan emerge struttura utile.

## Step 7 — Conferma all'utente

Sommario: filename scritto, scaffold vault, placeholder risolti,
conflitti risolti, prossimo passo.
