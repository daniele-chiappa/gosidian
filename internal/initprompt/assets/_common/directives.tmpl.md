<!-- gosidian:directives v={{DIRECTIVES_VERSION}} -->

## Direttive operative (gosidian) — v{{DIRECTIVES_VERSION}}

Per il progetto `{{PROJECT}}`, servite da `memory_bootstrap`. **Leggile e seguile a ogni sessione**; non copiarle nel file di istruzioni: si aggiornano da sole.

### Auto-conversione del file di istruzioni

Se il file di istruzioni del progetto (CLAUDE.md/AGENTS.md/…) **non** ha la riga del marker `gosidian:stub v=N` (un commento HTML), convertilo una volta con `memory_init_agent({project: "{{PROJECT}}", existing_content: "<contenuto del file>"})`: innesta lo stub, preserva le sezioni locali, rimuovi le direttive duplicate. Marker presente: niente da fare. Cercalo nel file su disco: alcuni agent (Claude Code fra questi) tolgono i commenti HTML dal testo che caricano.

### Mappa delle cartelle del vault

| Path vault | Scopo | Quando leggere |
|---|---|---|
| `{{PROJECT}}/memory/` | Conoscenza stabile: architecture, ADR, conventions, glossary, environments | Inizio task |
| `{{PROJECT}}/agents/` | Ruoli specializzati, uno per dominio ricorrente | Task nel suo scope |
| `{{PROJECT}}/plans/` | Piani dei task non banali, `YYYYMMDD-<slug>.md`, con `Outcome` | Prima di un task large |
| `{{PROJECT}}/skills/` | Procedure ripetibili | Prima di un'operazione ricorrente |
| `{{PROJECT}}/docs/` | Q&A, open questions, improvements, bug tracker | Decisioni passate, side finding |
| `{{PROJECT}}/hot.md` | Cache di sessione: lo stato attuale, breve, riscritta a fine task (non una cronaca) | **Sempre** al bootstrap |
| `{{PROJECT}}/log.md` | Log append-only delle attività | Append a fine task |

### Quando scrivere in memoria (ingest rules)

Quando scopri qualcosa che sopravvive al task:

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
| Bug fuori scope del task | `{{PROJECT}}/docs/bugs.md` | `memory_append` `## BUG-NNN`; se è `type: database`, `memory_create` di `{{PROJECT}}/docs/bugs/BUG-NNN.md` come dice la nota |
| Domanda aperta | `{{PROJECT}}/docs/open-questions.md` | `memory_append` sezione "Aperte", `### OQ-NNN` |
| Improvement / tech debt | `{{PROJECT}}/docs/improvements.md` | `memory_append` `## IMP-NNN`; se è `type: database`, `memory_create` di `{{PROJECT}}/docs/improvements/IMP-NNN.md` come dice la nota |
<!-- if html_notes -->
| Report/dashboard HTML self-contained | nota `.html` (es. `{{PROJECT}}/docs/`) | `memory_create` path `.html` |
<!-- end -->
<!-- if table_notes -->
| Dati tabellari lunghi (audit, export CSV) | table note linkata dal report | `memory_ingest` del `.csv` + caption |
<!-- end -->
| File binario (screenshot, PDF, zip) | attachment del vault, o media note dove attive | `memory_ingest` (le sorgenti in «Formati»; **mai** base64 per file grandi) |
<!-- if in -->
| File da far lavorare alle pipeline (corpus, scansioni, PDF o XML da convertire, di ogni dimensione) | la cartella `IN` del progetto, non una nota | `memory_in_upload`: `open` con l'elenco dei file, i pezzi con `curl` e il ticket all'endpoint della risposta, `complete`; tanti file piccoli in uno `.zip` con `unzip: true` |
<!-- end -->
| Più file insieme (una cartella, uno `.zip`, una guida con i suoi link) | una cartella del progetto | `memory_ingest` con `as: "package"` e `dest`, prima con `dry_run: true`: una chiamata per tutto, non una per file; i link relativi diventano wikilink |
| Contenuto di terze parti (una skill, una guida da un repository) | dove serve nel progetto | nel frontmatter della nota d'ingresso `upstream` (URL), `upstream_commit`, `upstream_version` se c'è, `license` se la conosci, solo informativa (se usarlo lo decide l'utente). L'originale è andato avanti? Se l'utente lo chiede, `git ls-remote <upstream> HEAD` contro `upstream_commit` |
| Fine task | `{{PROJECT}}/log.md` + `hot.md` | `memory_append` log, `memory_edit` hot |

**Note database**: una nota `type: database` (es. `{{PROJECT}}/docs/improvements.md`) raccoglie una nota per voce nella cartella omonima; stato e campi stanno nel frontmatter delle voci, e `memory_query` con `from: <source>` ne elenca e filtra le righe (esempi nella nota database). Per sapere cosa è aperto interroga il database, non una lista scritta a mano; in `hot.md` non elencare le voci aperte di un database: rimanda alla nota. Nel frontmatter di una voce scrivi solo i campi di `fields`, senza inventarne altri (commit e dettagli nel corpo); i database del progetto e i loro campi sono in `databases[]` del bootstrap. Una scrittura fuori schema lo dice in `notices`: correggi subito con `memory_edit`. Con un `template`, la voce nuova parte da quella nota coi segnaposto sostituiti (`{{ID}}`, `{{TITLE}}`, `{{TODAY}}`, il progetto). Per un numero, invece di una tabella, usa una vista `as: count` (`group_by` per valore) o nel testo `` `=count(<cartella> where <condizione> and …)` ``.

**Quando una raccolta diventa un database**: note o sezioni dello stesso tipo che crescono, con uno stato o campi da filtrare, ordinare o contare (backlog, plan, inventari come stack, host e certificati, scadenze), diventano una nota database; log, ADR, gotcha, `hot.md` e architettura restano testo. Proponilo all'utente prima di scrivere. Un fatto sta in un posto solo (campo, tag o corpo): col campo `status`, il tag `status:*` lo rispecchia o manca (il lint `field-tag-mismatch` segnala se divergono). Nelle righe niente conteggi (viste `as: count`, campi `rollup`); i dati copiati da un sistema (container, porte, certificati) hanno `checked` con la data; le decisioni aperte emerse lavorando diventano righe del backlog, non solo righe del resoconto. Non chiamare un campo `path`, `name`, `modified`, `links`, `created_by`, `modified_by`, `created_at` o `modified_at`: li calcola gosidian (per una cartella usa `folder`). Nella nota `fields` è una **mappa**, non la lista di `databases[].fields`:

```yaml
type: database
source: {{PROJECT}}/docs/stacks
fields:
  title: {type: text, required: true}
  status: {type: select, required: true, options: [running, retired]}
  checked: {type: date}
```

Procedura completa: la skill `design-project-databases` dei progetti globali, se è in `available_skills[]`.

**Relazioni**: un campo che punta a una nota è un wikilink quotato (`related: ["[[{{PROJECT}}/docs/bugs/BUG-001]]"]`) e conta come link, nei backlink (col campo in `fields`) e nel grafo. Per sapere cosa è collegato a una nota bastano `memory_backlinks` e `memory_outlinks`; in `memory_query`, `{field: links, op: contains, value: "[[nota]]"}` chi la punta, `related contains [[nota]]` chi la punta da quel campo. I `row_views` di un database seguono il corpo di una riga letta con `render_views: true` (lo dice il `hint`).

**Campi calcolati**: un campo `type: rollup` conta (o somma, o riduce a minimo e massimo) le note collegate a ogni riga, alla lettura: chiedilo in `fields` di `memory_query` con `from: <source>`, o ordina per lui, invece di contare a mano i backlink; mai scriverlo nella riga. Per i pari merito `sort` prende più chiavi, ognuna col suo verso (`sort: "plans desc, id asc"`): non riordinare a mano. `created_by` e `modified_by` (chi ha creato e chi ha modificato per ultimo la nota da gosidian, secondo l'audit; l'ora in `created_at` e `modified_at`) si leggono, filtrano e ordinano come campi in `memory_query`, senza `memory_audit_tail`. Senza `created_by` la creazione non è nell'audit: dillo, non indovinare.

**Embed e istantanee**: una riga di solo `![[nota#Titolo]]` (o `![[nota]]`) include la sezione, con le viste calcolate per la nota che la include: una vista da riusare si scrive una volta e si include. Con `render_views: true` l'inclusione arriva fra marcatori `gosidian:embed`, mai da copiare nel file. `memory_snapshot` congela cosa mostrano le viste di una nota in `<nome>.snapshots/AAAA-MM-GG.md`.

**Scadenze e automazioni**: `due` è la data entro cui fare una riga, solo per una scadenza vera (una consegna, una data che dà l'utente), mai stimata. Le `automations:` di una nota database: un avviso quando il `due` di una riga è entro N giorni (`due`, `before`, `where`, `handoff: <agente>`), o un'istantanea o un handoff a orario fisso (`every: monday 09:00`). Scrivine una solo se l'utente la chiede, e prima di dire che funziona provala a secco con `memory_automations` (`as_of: <data>`: cosa scatterebbe quel giorno, senza scrivere).

**Frontmatter**: metti fra virgolette un valore con `: `, un `[[wikilink]]` o un `{{segnaposto}}` (`title: "Plan: uno"`, `related: "[[{{PROJECT}}/nota]]"`), se no il YAML non vale: lo dicono `notices` e il lint `frontmatter-invalid-yaml`.

**Cattura immediata**: bug, OQ e improvement si scrivono **quando emergono**, non a fine task: un "side finding" nell'outcome di un plan è perso. **Ripetizioni**: stessa sequenza ≥2 volte in sessione → skill **prima** della terza; stesse note rilette per riorientarti su un'area → è una richiesta latente di agent.

### Formati di nota e allegati

`capabilities` dice i formati attivi su questa istanza: guardalo prima di scegliere.

- **Markdown è il default**: semplice, piccolo, diff-abile. Nel dubbio, `.md`.
<!-- if html_notes -->
- **Note `.html`**: path `.html` in `memory_create` → nota HTML first-class (frontmatter in un commento di testa, indicizzata come una `.md`, resa in un iframe sandbox). **Solo** per contenuto intrinsecamente HTML; asset esterni bloccati: tutto inline.
<!-- end -->
- **File su disco → `memory_ingest`**, la porta unica per salvare un file: instrada per estensione (`.md` e, dove attive, `.html` → nota vera, letta dal server, se no un `.html` è rifiutato; `.csv` → table note e immagine → media note, dove attive; il resto → attachment). Sorgenti dalla più economica: `bridge_filename` (in `capabilities.attachments.bridge_dir`), `source_path` (nelle allowed roots), `url` (se `ingest_url_enabled`), ticket `transfer:"http"` (poi un POST del file, senza bearer), base64 `data` solo per file piccoli. Limiti ed estensioni in `capabilities.attachments`; `memory_upload_attachment` e `memory_upload_resource` restano per i flussi espliciti stage-then-attach.
<!-- if media_notes -->
- **Media notes**: `memory_ingest` di un'immagine crea la sua nota; scrivi sempre la caption nel body, è il testo che si cerca.
<!-- end -->
<!-- if table_notes -->
- **Table notes**: `memory_ingest` di un `.csv` crea la nota `type: table` + `media:`, una tabella paginata da linkare nel report. Si indicizzano header e numero di righe, non le celle: scrivi una caption. Niente tabelle lunghe nel body markdown.
<!-- end -->

### Plan: vault vs scratchpad

- **Small/medium** (1-2 file, fix isolato): scratchpad dell'agent; annota un `pattern` in `{{PROJECT}}/log.md`.
- **Large/architetturale** (3+ file, migration, ADR, refactor cross-pacchetto): plan in `{{PROJECT}}/plans/` **prima** di implementare; chiudi con `Outcome` (hash commit, sorprese, side findings).

Status dei plan: `draft` → `in-progress` → `done` | `archived`, nel campo `status` e nel tag `status:*` che lo rispecchia, tutti e due con `memory_edit`.

### Workflow end-of-task

0. **Maintenance-check**: con `maintenance.attention: true` (hot.md oltre soglia e/o wikilink rotti) proponi il grooming relativo prima di chiudere: compatta o riscrivi `{{PROJECT}}/hot.md`, ripara i link segnalati (li elenca `memory_lint`). Con `hot_computed_oversize` o `hot_views_cut` pesano le viste di hot.md: restringile (filtri, `limit`), non tagliare la prosa. `stale_count` è contesto, non un obbligo.
1. **Skill-check**: procedura ≥2 volte? Crea la skill.
2. **Riscrivi** le sezioni di `{{PROJECT}}/hot.md` (focus, plan chiusi, recent decisions), non accodare: è la cache dello stato attuale. Il Current focus dice in poche righe cosa è in corso e il prossimo passo; la cronaca va in `log.md`. Oltre 8 KB (default) il bootstrap segnala `maintenance.hot_oversize`. Una sezione fatta da un blocco ` ```view ` si aggiorna da sola: non riscriverla e non copiare nel file il risultato calcolato (fra i marcatori `gosidian:view-result`, che il bootstrap mostra sotto il blocco). Lo stesso per un `` `=count(…)` ``, che il bootstrap mostra come «N (`` `=count(…)` ``)»: nel file resta solo il codice.
3. Append a `{{PROJECT}}/log.md`: entry tipizzata con data ISO (`bootstrap`, `plan-closed`, `adr`, `pattern`, `fix`, `discovery`, `ops`).
4. Compila l'`Outcome` del plan, se c'era.
5. Aggiorna la memory sulle scoperte strutturali.

Saltarlo è la via più rapida perché la memoria diventi inutile.

### Vocabolario tag (chiuso, estendibile per-progetto)

- `type:{memory,agent,plan,skill,doc,index,handoff}`
- `status:{draft,in-progress,done,archived}` — solo plan
- `status:{pending,claimed,done,rejected}` — solo handoff
- `topic:<area>` a valore libero (es. `topic:deploy`); `pinned`; `importance: 1..5`
<!-- if tag_vocabulary -->
- **Estensione per-progetto** (flag di progetto `use_tag_vocabulary`, acceso qui): dichiara le tassonomie di dominio nel frontmatter di `{{PROJECT}}/memory/conventions.md`, campo `tag_vocabulary:` (tag esatti o wildcard `ns:*`, es. `cm:*`, max 64 voci), e documenta nel body il perché di ogni namespace. Il bootstrap mostra il vocabolario attivo in `tag_vocabulary`, e `memory_lint` lo accetta. Non per aggirare un typo: è per tassonomie vive del dominio.
<!-- end -->
<!-- if !tag_vocabulary -->
- **Estensione per-progetto**: spenta in questo progetto; col flag di progetto `use_tag_vocabulary` vi si dichiarano tassonomie di dominio.
<!-- end -->

### Handoff fra agenti

`memory_create_handoff` passa contesto in una nota di `{{PROJECT}}/handoffs/`; ciclo `pending → claimed → done|rejected`. Il bootstrap elenca in `pending_handoffs` quelli in attesa del progetto, avvisi delle automazioni compresi (`from_agent: automation`): se uno è per te, o per nessuno in particolare, occupatene prima del resto o dillo all'utente. Per lavorarne uno: `memory_pending_handoffs` → **`memory_claim_handoff` prima di iniziare** (atomico: fra concorrenti ne vince uno) → `memory_complete_handoff` (`done`/`rejected`). `created_by`, `claimed_by` e `completed_by` li scrive il server (non falsificabili); `from_agent` e `to_agent` sono slug dichiarativi. Non editare a mano il frontmatter del ciclo.

### Economia dei token

- **Bootstrap ripetuti** dello stesso progetto: passa come `known_directives_version` il `directives_version` del payload (uguale → blocco omesso) e `known_etags` (file invariati → `unchanged:true` senza body); coi progetti ad anchors anche `known_anchor_metas` (canonical → meta_version: item invariati senza `content`). Il `mode` di default, **auto**, dà lite un hot.md oltre soglia (il testo scritto, non le viste): frontmatter e outline, `auto_lite:true`; le sezioni con `memory_get_section`, che accetta anche il solo ID (`BUG-014` per `## BUG-014 — …`). Un'intestazione con `views: N` ha viste: leggi la sezione con `render_views: true`, se no ne vedi solo la specifica. Una vista tagliata per il bootstrap finisce con la `memory_query` che dà tutte le righe.
- **Letture**: `memory_get` **tronca** oltre 24 KiB (outline, primo chunk, `truncated:true`): prendi la sezione con `memory_get_section`, `raw:true` solo se serve davvero tutto; in blocco, `memory_batch_get` (`mode: outline|frontmatter`, `max_bytes_per_note`). Le letture danno il file com'è, i blocchi ` ```view ` senza righe (lo dice il `hint`): per vederle, `render_views: true` a `memory_get` o `memory_get_section`; per modificare la nota, leggila senza.
- **Domande sul frontmatter** (stato, tipo, date, `importance`, liste come `implements_imp`): `memory_query` risponde in una chiamata coi soli campi chiesti, invece di `memory_notes_by_tag` + `memory_batch_get` + filtro a mano. Un tag `ns:valore` vale come campo (`status:done` → `status = done`); date ISO e numeri si confrontano come tali. Es., i plan in bozza aggiornati da settembre: `where: [{field: type, value: plan}, {field: status, value: draft}, {field: updated, op: gte, value: 2026-09-01}]`.
- Se `memory_lint` segnala `hot-oversize`, compatta hot.md invece di lasciarlo crescere.

<!-- /gosidian:directives -->
