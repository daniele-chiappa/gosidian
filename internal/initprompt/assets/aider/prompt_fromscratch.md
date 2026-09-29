# memory_init_agent — modalità from-scratch (Aider / CONVENTIONS.md)

Hai ricevuto il payload dal tool MCP `memory_init_agent`. **Ruolo**:
creare da zero `CONVENTIONS.md` nel progetto **{{PROJECT}}**, innestando
il `gosidian_block`. Il `gosidian_block` è uno **stub sottile** (Regola
Zero → bootstrap + specifiche locali): le direttive operative complete
arrivano dal `directives_block` di `memory_bootstrap`, non da questo file.

## Step 1 — Determina filename

**`CONVENTIONS.md`** nella root. Se esiste → errore, devi essere in
modalità augment. Chiedi all'utente.

## Step 2 — Scan cwd

Ls + manifest principali (`package.json`, `go.mod`, `pyproject.toml`),
README.md se presente. Max 10 file, top-level + uno hop.

## Step 3 — Pre-check vault

Se `needs_scaffold=true`, chiama `memory_project_scaffold` con
`project="{{PROJECT}}"`, `template="karpathy-wiki"`.

## Step 4 — Raccogli placeholder

{{PLACEHOLDER_STATUS}} Per quelli aperti: deduci + chiedi in chat.

## Step 5 — Compila file

```md
# {{PROJECT}} — CONVENTIONS.md

> File generato da `memory_init_agent` ({{TODAY}}) per **Aider**.
> Caricamento: `aider --read CONVENTIONS.md` oppure via `.aider.conf.yml`.
> Memoria persistente: progetto `{{PROJECT}}/` del vault gosidian.

<3-5 righe di header contestuale dallo scan>

<gosidian_block con placeholder risolti>
```

## Step 6 — Materializza

Scrivi `CONVENTIONS.md`. Reminder configurazione Aider:

```yaml
# .aider.conf.yml
read:
  - CONVENTIONS.md
```

## Step 7 — Primo ingest (facoltativo)

Seed `architecture.md` e `hot.md` se ha senso.

## Step 8 — Conferma all'utente

Sommario + reminder sulla config Aider.
