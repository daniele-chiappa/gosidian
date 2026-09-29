# memory_init_agent — modalità augment (OpenAI Codex / AGENTS.md)

Hai ricevuto il payload dal tool MCP `memory_init_agent`. **Ruolo**:
integrare le regole gosidian nel file `AGENTS.md` esistente preservando
tutto il contenuto.

Il server ti ha passato:

- `gosidian_block` — **stub sottile** parametrico: Regola Zero (che
  punta a `memory_bootstrap` per le direttive) + specifiche locali. Le
  direttive operative complete (mappa cartelle, ingest rules, workflow,
  tag) **non** sono qui: le serve `memory_bootstrap` nel campo
  `directives_block`. {{PLACEHOLDER_STATUS}}
- `needs_scaffold` — se `true`, crea il progetto vault prima.
- `mode: "augment"` — `existing_content` presente.

## Step 1 — Determina filename

Convenzione Codex: **`AGENTS.md`** nella root della cwd. Se
`filename_hint` diverso, rispettalo. `existing_content` arriva già da
questo file.

## Step 2 — Pre-check vault

Se `needs_scaffold=true`, chiama
`mcp__gosidian__memory_project_scaffold` con `project="{{PROJECT}}"`,
`template="karpathy-wiki"`.

## Step 3 — Raccogli placeholder

Deduci da `existing_content` o chiedi all'utente in chat (Codex non ha
un tool dedicato `AskUserQuestion` come Claude — usa una lista numerata
nel messaggio).

Da risolvere prima del merge: solo i placeholder ancora aperti (vedi
sopra).

## Step 4 — Merge

**Regole**:

1. Non sovrascrivere sezioni esistenti di `AGENTS.md`.
2. Inserisci `gosidian_block` come blocco unitario. {{STUB_PLACEMENT}}
3. Risolvi i placeholder nel blocco prima di scrivere.
4. Conflitti strutturali → fermati e chiedi all'utente.

## Step 5 — Materializza

Scrivi il file completo (existing + gosidian) su `AGENTS.md`.

## Step 6 — Primo ingest (facoltativo)

Seed `{{PROJECT}}/memory/architecture.md` e `{{PROJECT}}/hot.md` via
i tool `mcp__gosidian__memory_edit`.

## Step 7 — Conferma all'utente

Sommario: file scritto, scaffold eseguito, placeholder risolti, prossima
sessione dovrebbe partire con `memory_bootstrap`.
