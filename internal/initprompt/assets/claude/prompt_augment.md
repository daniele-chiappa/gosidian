# memory_init_agent — modalità augment (Claude Code)

Hai appena ricevuto questo payload dal tool MCP `memory_init_agent`.
**Ruolo**: innestare le regole gosidian nel file di istruzioni dell'agent
**preservando tutto il contenuto esistente**.

Il server ti ha passato:

- `gosidian_block` — **stub sottile** parametrico: Regola Zero (che punta
  a `memory_bootstrap` per le direttive) + specifiche locali. **Payload
  principale.** Le direttive operative complete (mappa cartelle, ingest
  rules, workflow end-of-task, tag) **non** sono qui: le serve
  `memory_bootstrap` nel campo `directives_block`. {{PLACEHOLDER_STATUS}} Progetto e data sono già compilati.
- `needs_scaffold` — bool. Se `true`, il progetto vault **non esiste** e
  deve essere creato prima di materializzare il file di istruzioni.
- `mode: "augment"` — stai lavorando su un file di istruzioni che esiste
  già (passato dall'agent come `existing_content`).

Esegui gli step in ordine.

## Step 1 — Determina filename

Per Claude Code il file canonico è **`CLAUDE.md`** nella root della cwd.
Se è già `filename_hint` → usalo. Se l'utente ha già un `CLAUDE.md` nella
cwd (hai `existing_content`), quello è il target. Se ambiguo o esistono
più file candidati, chiedi all'utente con `AskUserQuestion`.

## Step 2 — Pre-check vault

Se `needs_scaffold=true`, **prima** di scrivere qualsiasi cosa:

```
mcp__gosidian__memory_project_scaffold({
  project: "{{PROJECT}}",
  template: "karpathy-wiki"
})
```

Crea `{{PROJECT}}/memory/`, `plans/`, `skills/`, `docs/`, `agents/`,
`hot.md`, `log.md`. Idempotente — ok chiamarlo anche se parte della
struttura esiste.

## Step 3 — Raccogli placeholder

Prima di fare il merge, risolvi i placeholder del `gosidian_block`
ancora aperti (vedi sopra; quelli già compilati da `user_hints` non si
toccano):

- `{{LANGUAGE}}` — lingua delle note del vault (es. "italiano",
  "inglese"). Se `existing_content` è in una lingua evidente,
  presumila; altrimenti chiedi.
- `{{CODE_LANGUAGE}}` — lingua di commit/commenti (spesso "inglese"
  anche se le note sono in italiano). Chiedi se non deducibile.
- `{{PROJECT_TYPE}}` — "applicazione web", "CLI", "libreria",
  "infra self-hosted", "docs-only", ecc. Deduci da `existing_content`
  o dalla cwd.
- `{{STACK}}` — framework/runtime principale (es. "Go 1.22 + HTMX",
  "Next.js + Prisma", "Python 3.12 FastAPI"). Deduci o chiedi.
- `{{HOT_FILES}}` — 2-3 percorsi critici del progetto che cambiano
  spesso. Deduci dallo scan se possibile, altrimenti una lista
  placeholder tipo `_(da popolare al primo giro di lavoro reale)_`.

Batch le domande mancanti in un'unica `AskUserQuestion` con massimo
3-4 choice questions — non bombardare l'utente.

## Step 4 — Merge

Analizza `existing_content` e identifica le sezioni già presenti (es.
Build, Test, Style, Security, Commit, Deploy, Architecture, ecc.).
**Regole di merge**:

1. **Mai sovrascrivere regole esistenti.** Se l'utente ha scritto
   "commit in inglese", nella sezione Lingua del blocco va quel valore,
   anche al posto di uno arrivato da `user_hints`: l'utente ha già
   deciso.
2. **Inserisci `gosidian_block` come blocco unitario**, senza espandere
   le sue sotto-sezioni `###` a top-level. {{STUB_PLACEMENT}}
3. **Sostituisci i placeholder** (`{{LANGUAGE}}` ecc.) con i valori
   raccolti al Step 3 **prima** del merge. Il file finale non deve
   contenere `{{...}}` pendenti — eccetto `{{HOT_FILES}}` se l'utente
   ha chiesto esplicitamente di lasciarlo da popolare dopo.
4. **Conflitti strutturali** (regola esistente che contraddice una
   regola gosidian — es. "non creare file .md di appunti" vs Regola
   Zero, oppure "tutti i task in TODO.md" vs "plan in vault"):
   **fermati**, usa `AskUserQuestion` chiedendo come risolvere:
   - mantieni la regola esistente (l'agent non applicherà la regola
     gosidian equivalente)
   - adotta la regola gosidian (documenta la sostituzione in coda)
   - scrivi entrambe come alternative (rischioso, crea ambiguità)

## Step 5 — Materializza

Usa il tool `Write` per scrivere il file completo (esistente + gosidian)
sul filename determinato allo Step 1. Non append-only: ricostruisci
l'intero contenuto e scrivilo in un colpo.

## Step 6 — Primo ingest (facoltativo ma consigliato)

Se da `existing_content` emerge struttura utile (es. una sezione
"Architecture" o "Deployment"), fanne un primo seed della memoria vault:

- `mcp__gosidian__memory_edit` su `{{PROJECT}}/memory/architecture.md`
  sezione "Overview" con 3-5 righe.
- `mcp__gosidian__memory_edit` su `{{PROJECT}}/hot.md` impostando
  `current focus = "prima sessione su {{PROJECT}}"` e listando gli hot
  file del blocco.

Salta questo step se la raccolta è vuota — meglio memoria vuota che
memoria con dati inventati.

## Step 7 — Conferma all'utente

Rispondi all'utente con un sommario sintetico (5-8 righe):

- filename scritto
- scaffold vault eseguito (sì/no)
- sezioni aggiunte (es. "Memory & workflow (gosidian)")
- conflitti risolti (se ce ne sono stati)
- ingest iniziali fatti (se ce ne sono stati)
- prossimo passo consigliato: "fai `memory_bootstrap({project: \"{{PROJECT}}\"})`
  all'inizio della prossima sessione".
