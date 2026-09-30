# memory_init_agent — modalità from-scratch (Claude Code)

Hai appena ricevuto questo payload dal tool MCP `memory_init_agent`.
**Ruolo**: creare da zero il file di istruzioni per l'agent nella cwd
del progetto **{{PROJECT}}**, innestando il `gosidian_block` all'interno
di un contenitore minimale.

Il server ti ha passato:

- `gosidian_block` — **stub sottile** parametrico (Regola Zero → bootstrap
  + specifiche locali): le direttive operative complete arrivano dal
  `directives_block` di `memory_bootstrap`, non da questo file. {{PLACEHOLDER_STATUS}}
- `needs_scaffold` — se `true`, il progetto vault non esiste ancora.
- `mode: "from-scratch"` — non c'è `existing_content`; stai creando
  il file di istruzioni da zero.

Esegui gli step in ordine.

## Step 1 — Determina filename

Per Claude Code usa **`CLAUDE.md`** nella root della cwd. Se
`filename_hint` è presente e diverso, rispettalo. Se la cwd contiene
già un `CLAUDE.md`, **fermati**: dovevi essere chiamato in modalità
augment. Chiedi all'utente se vuole sovrascrivere o passare ad augment.

## Step 2 — Scan cwd (letture read-only)

Esegui letture mirate per ricavare linguaggio/stack/hot files. **Limite
duro**: massimo 10 file letti, top-level + uno hop, no recursion
profonda. Verifica:

- `ls` della root (tool `Bash`: `ls -la`)
- `README.md` se presente (`Read`)
- Presenza di manifest: `go.mod`, `package.json`, `pyproject.toml`,
  `Cargo.toml`, `Gemfile`, `requirements.txt`, `composer.json` →
  identifica linguaggio + framework principale
- Presenza di `.git`, `docker-compose.yml`, `Dockerfile`,
  `.github/workflows/` → identifica tipo di progetto (app/libreria/infra)

Sintetizza in 3-5 righe ciò che hai visto. **Non** scrivere ancora il
file.

## Step 3 — Pre-check vault

Se `needs_scaffold=true`:

```
mcp__gosidian__memory_project_scaffold({
  project: "{{PROJECT}}",
  template: "karpathy-wiki"
})
```

## Step 4 — Raccogli placeholder

Dallo scan allo Step 2 deduci quanto puoi dei placeholder ancora
aperti. Quelli già compilati da `user_hints` non si toccano e non si
chiedono. Aperti:

{{OPEN_PLACEHOLDERS}}

Batch le domande mancanti in **una** `AskUserQuestion` con 2-4 scelte
ciascuna.

## Step 5 — Compila template

Costruisci il file completo come:

```md
# {{PROJECT}} — istruzioni per l'agent

> File generato da `memory_init_agent` ({{TODAY}}) per **Claude Code**.
> Memoria persistente nel progetto `{{PROJECT}}/` del vault gosidian.

<breve header contestuale — 3-5 righe dallo scan: linguaggio, stack,
 tipo di progetto, cosa fa il repo>

<inserisci qui `gosidian_block` con placeholder risolti>
```

Non aggiungere sezioni Build/Test/Style/ecc. da zero — non sai abbastanza
della cwd per essere utile. Lascia che le arricchisca l'utente o un
successivo `/init` nativo.

## Step 6 — Materializza

`Write` sul filename determinato allo Step 1 (tipicamente `CLAUDE.md`).

## Step 7 — Primo ingest (facoltativo)

Stesso dello Step 6 in modalità augment — seed `architecture.md` /
`hot.md` solo se dallo scan emerge struttura utile. Salta se vuoto.

## Step 8 — Conferma all'utente

Sommario sintetico: filename creato, scaffold vault eseguito, placeholder
risolti, prossimo passo ("rilancia una sessione Claude Code e vedrai il
bootstrap automatico").
