<!-- gosidian:directives v={{DIRECTIVES_VERSION}} -->
<!--
  directives.tmpl.md — direttive operative gosidian, servite da
  memory_bootstrap nel campo `directives_block` (parametrico solo su
  {{PROJECT}} e {{DIRECTIVES_VERSION}}). NON è un file da scrivere su
  disco: l'agente le legge fresche a ogni bootstrap. La personalizzazione
  per-progetto (stack, hot files, lingua) vive nello stub locale, non qui.
-->

## Direttive operative (gosidian) — v{{DIRECTIVES_VERSION}}

Servite da `memory_bootstrap` (`directives_block`) per il progetto
`{{PROJECT}}`. **Leggile e seguile a ogni sessione**; non tenerne copie nel
file di istruzioni — si aggiornano da sole al bump di versione.

### Auto-conversione del file di istruzioni (one-time)

Se il file di istruzioni del progetto (CLAUDE.md/AGENTS.md/…) **non**
contiene il marker `<!-- gosidian:stub -->`, è pre-stub: convertilo una
tantum con `memory_init_agent({project: "{{PROJECT}}", existing_content:
"<contenuto del file>"})` — innesta lo stub, preserva le sezioni locali,
rimuovi le direttive duplicate (ora le servo io). Idempotente: marker già
presente → non fare nulla.

### Mappa delle cartelle del vault

| Path vault | Scopo | Quando leggere |
|---|---|---|
| `{{PROJECT}}/memory/` | Conoscenza stabile: architecture, ADR, conventions, glossary, environments | Inizio task per contesto |
| `{{PROJECT}}/agents/` | Ruoli specializzati (un agent = un dominio ricorrente) | Task nello scope di un agent |
| `{{PROJECT}}/plans/` | Piani di task non banali, `YYYYMMDD-<slug>.md`, con `Outcome` | Prima di task large |
| `{{PROJECT}}/skills/` | Procedure ripetibili | Prima di operazioni ricorrenti |
| `{{PROJECT}}/docs/` | Q&A, open questions, improvements, bug tracker | Decisioni passate / side findings |
| `{{PROJECT}}/hot.md` | Session cache aggiornata fine-task | **Sempre** al bootstrap |
| `{{PROJECT}}/log.md` | Log append-only di attività | Append a fine task |

### Quando scrivere in memoria (ingest rules)

Quando scopri qualcosa che sopravvive al task corrente:

| Discovery | Dove | Come |
|---|---|---|
| Fatto su codice/sistema non documentato | `{{PROJECT}}/memory/architecture.md` | `memory_edit` sezione |
| Decisione tecnica vincolante | `{{PROJECT}}/memory/decisions.md` | `memory_append` `## ADR-NNN` |
| Termine di dominio nuovo | `{{PROJECT}}/memory/glossary.md` | `memory_append` |
| Nuova convenzione (code/test/ops) | `{{PROJECT}}/memory/conventions.md` | `memory_edit` |
| Cambio infra / deploy / env | `{{PROJECT}}/memory/environments.md` | `memory_edit` |
| Task non banale in partenza | `{{PROJECT}}/plans/<YYYYMMDD>-<slug>.md` | `memory_create` **prima** del codice |
| Procedura ripetuta ≥2 volte in sessione | `{{PROJECT}}/skills/<slug>.md` | `memory_create` `type:skill` + trigger + step + gotcha |
| Dominio riletto in 2+ task (stesse 3-5 note) | `{{PROJECT}}/agents/<slug>.md` | `memory_create` `type:agent` + "Contesto obbligatorio" |
| Bug fuori scope del task | `{{PROJECT}}/docs/bugs.md` | `memory_append` `## BUG-NNN` |
| Domanda aperta | `{{PROJECT}}/docs/open-questions.md` | `memory_append` sezione "Aperte", `### OQ-NNN` |
| Improvement / tech debt | `{{PROJECT}}/docs/improvements.md` | `memory_append` `## IMP-NNN` |
| Report/dashboard HTML self-contained | nota `.html` (es. `{{PROJECT}}/docs/`) | `memory_create` path `.html` (se `capabilities.html_notes`) |
| Dati tabellari lunghi (audit, export CSV) | table note linkata dal report | `memory_ingest` del `.csv` + caption (se `capabilities.table_notes`) |
| File binario (screenshot, PDF, zip) | attachment/media note del vault | `memory_ingest` (bridge dir, `source_path`, `url` o ticket `transfer:"http"`; **mai** base64 per file grandi) |
| Fine task | `{{PROJECT}}/log.md` + `hot.md` | `memory_append` log, `memory_edit` hot |

**Cattura immediata**: bug/OQ/improvement si scrivono **quando emergono**,
non a fine task — lasciarli come "side finding" in un plan outcome equivale
a perderli. **Regola delle ripetizioni**: stessa sequenza eseguita ≥2 volte
in sessione → promuovila a skill **prima** della terza; stesse note rilette
per riorientarti su un'area → è una richiesta latente di agent.

### Formati di nota e allegati

Il bootstrap serve `capabilities` con i formati attivi su questa istanza:
consultalo prima di scegliere.

- **Markdown è il default**: semplice, piccolo, diff-abile. Nel dubbio, `.md`.
- **Note `.html`** (se `capabilities.html_notes`): path `.html` in
  `memory_create` → nota HTML first-class (frontmatter in commento di testa,
  indicizzata come una `.md`, resa in iframe sandbox). **Solo** per contenuto
  intrinsecamente HTML; asset esterni bloccati — inline tutto.
- **File su disco → `memory_ingest`**: la porta unica per "salva questo
  file" — instrada da sola per estensione (`.csv` → table note, immagine →
  media note, `.md`/`.html` → nota vera con body letto server-side, altro →
  attachment). Sorgenti dalla più economica: `bridge_filename` (path in
  `capabilities.attachments.bridge_dir`), `source_path` (dentro le allowed
  roots), `url` (se `ingest_url_enabled`), ticket `transfer:"http"` (mint →
  un POST del file, senza bearer), base64 `data` solo per file piccoli.
  Limiti/estensioni in `capabilities.attachments`. I tool dedicati
  (`memory_upload_attachment`/`memory_upload_resource`) restano per i flussi
  espliciti stage-then-attach.
- **Media notes** (se `capabilities.media_notes`): immagine first-class —
  `memory_ingest` di un'immagine la crea da solo; la caption nel body è il
  testo ricercabile, scrivila sempre.
- **Table notes** (se `capabilities.table_notes`): dati tabellari lunghi —
  `memory_ingest` di un `.csv` crea la nota `type: table` + `media:`, resa
  come tabella paginata e linkabile dal report. Header e numero righe
  indicizzati; i valori delle celle no — scrivi una caption. Non incollare
  tabelle lunghe nel body markdown.

### Plan: vault vs scratchpad

- **Small/medium** (1-2 file, fix isolato): scratchpad dell'agent; annota un
  `pattern` in `{{PROJECT}}/log.md`.
- **Large/architetturale** (3+ file, migration, ADR, refactor
  cross-pacchetto): plan in `{{PROJECT}}/plans/` **prima** di implementare;
  chiudi con `Outcome` (hash commit, sorprese, side findings).

Status dei plan: `draft` → `in-progress` → `done` | `archived` (tag
`status:*` via `memory_edit`).

### Workflow end-of-task

0. **Maintenance-check**: se il bootstrap ha servito `maintenance.attention:
   true` (hot.md oltre soglia e/o wikilink rotti), proponi il grooming
   relativo prima di chiudere — compatta/riscrivi `{{PROJECT}}/hot.md`,
   ripara i link segnalati (`memory_lint` per l'elenco). `stale_count` è
   contesto, non un obbligo.
1. **Skill-check** (procedura ≥2 volte? → crea la skill)
2. Aggiorna `{{PROJECT}}/hot.md` (focus, plan chiusi, recent decisions)
3. Append a `{{PROJECT}}/log.md` (entry tipizzata con data ISO: `bootstrap`,
   `plan-closed`, `adr`, `pattern`, `fix`, `discovery`, `ops`)
4. Compila l'`Outcome` del plan se esisteva
5. Aggiorna la memory su scoperte strutturali

Saltarlo è la via più rapida perché la memoria diventi inutile.

### Vocabolario tag (chiuso, estendibile per-progetto)

- `type:{memory,agent,plan,skill,doc,index,handoff}`
- `status:{draft,in-progress,done,archived}` — solo plan
- `status:{pending,claimed,done,rejected}` — solo handoff
- `topic:<area>` a valore libero (es. `topic:deploy`); `pinned`; `importance: 1..5`
- **Estensione per-progetto** (attiva solo col flag di progetto
  `use_tag_vocabulary`): dichiara le tassonomie di dominio nel frontmatter
  di `{{PROJECT}}/memory/conventions.md`, campo `tag_vocabulary:` — tag
  esatti o wildcard `ns:*` (es. `cm:*`), max 64 voci; documenta nel body il
  perché di ogni namespace. Il bootstrap riflette il vocabolario attivo nel
  blocco `tag_vocabulary` e `memory_lint` lo accetta. Non usarla per
  aggirare un typo: è per tassonomie vive del dominio.

### Handoff fra agenti

`memory_create_handoff` passa contesto come nota in `{{PROJECT}}/handoffs/`;
lifecycle `pending → claimed → done|rejected`. Se ricevi lavoro:
`memory_pending_handoffs` → **`memory_claim_handoff` prima di iniziare**
(claim atomico: fra concorrenti ne vince uno) → `memory_complete_handoff`
(`done`/`rejected`). `created_by`/`claimed_by`/`completed_by` sono stampati
dal server (non falsificabili); `from_agent`/`to_agent` sono slug
dichiarativi. Non editare a mano il frontmatter di lifecycle.

### Economia dei token

- **Bootstrap ripetuti**: passa `known_directives_version` (match → blocco
  omesso) e `known_etags` (file invariati → `unchanged:true` senza body);
  sui progetti con anchors attivi anche `known_anchor_metas`
  (canonical → meta_version: item invariati senza `content`).
  `mode` default è **auto**: hot.md oltre soglia arriva in forma lite
  (frontmatter+outline, `auto_lite:true`) — le sezioni via
  `memory_get_section`.
- **Letture**: `memory_get` **tronca** i body oltre 24 KiB (outline + primo
  chunk + `truncated:true`): prendi la sezione che serve con
  `memory_get_section`, o `raw:true` solo se serve davvero tutto. Letture
  bulk con `memory_batch_get` (`mode: outline|frontmatter`,
  `max_bytes_per_note`).
- Se `memory_lint` segnala `hot-oversize`, compatta hot.md invece di
  lasciarlo crescere.

<!-- /gosidian:directives -->
