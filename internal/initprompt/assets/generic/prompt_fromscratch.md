# memory_init_agent — modalità from-scratch (generic / AGENTS.md)

Hai ricevuto il payload dal tool MCP `memory_init_agent`. **Ruolo**:
creare da zero il file di istruzioni dell'agent nel progetto
**{{PROJECT}}**, innestando il `gosidian_block`. Il `gosidian_block` è
uno **stub sottile** (Regola Zero → bootstrap + specifiche locali): le
direttive operative complete arrivano dal `directives_block` di
`memory_bootstrap`, non da questo file.

## Step 1 — Determina filename

Default: **`AGENTS.md`** nella root della cwd. Se il tuo runtime ha una
convenzione diversa (controlla la sua documentazione o chiedi
all'utente), usa quella. `filename_hint` se presente ha priorità.

Se il file esiste già → errore, dovresti essere in modalità augment.
Chiedi all'utente.

## Step 2 — Scan cwd

Letture read-only: ls root, manifest principali, README.md. Max 10
file, top-level + uno hop.

## Step 3 — Pre-check vault

Se `needs_scaffold=true`, chiama `memory_project_scaffold` con
`project="{{PROJECT}}"`, `template="karpathy-wiki"`.

## Step 4 — Raccogli placeholder

{{PLACEHOLDER_STATUS}} Per quelli aperti: deduci + chiedi.

## Step 5 — Compila file

```md
# {{PROJECT}} — istruzioni per l'agent

> File generato da `memory_init_agent` ({{TODAY}}) per **{{AGENT_NAME}}**.
> Memoria persistente in `{{PROJECT}}/` del vault gosidian.

<3-5 righe di header contestuale dallo scan>

<gosidian_block con placeholder risolti>
```

Non inventare sezioni oltre a quelle che puoi ricavare dal tuo scan.

## Step 6 — Materializza

Scrivi il file sul filename determinato allo Step 1.

## Step 7 — Primo ingest (facoltativo)

Seed `architecture.md` e `hot.md` se lo scan lo giustifica.

## Step 8 — Conferma all'utente

Sommario: filename creato, scaffold eseguito, placeholder risolti,
prossimo passo.
