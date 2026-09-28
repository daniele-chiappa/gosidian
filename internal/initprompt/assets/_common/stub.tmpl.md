<!-- gosidian:stub v={{STUB_VERSION}} -->
<!--
  stub.tmpl.md — stub sottile da innestare nel file di istruzioni
  agent-native del progetto "{{PROJECT}}". Emesso da memory_init_agent.
  Placeholder: {{PROJECT}} {{TODAY}} {{LANGUAGE}} {{CODE_LANGUAGE}}
  {{PROJECT_TYPE}} {{STACK}} {{HOT_FILES}} {{AGENT_NAME}}
  {{AGENT_PROFILE}} {{STUB_VERSION}}.

  Le DIRETTIVE operative complete (mappa cartelle, ingest rules, plan,
  workflow end-of-task, tag) NON stanno qui: le serve memory_bootstrap
  nel campo `directives_block`. La porzione fra `<!-- gosidian:stub v=N -->`
  e `<!-- /gosidian:stub -->` è gestita dal tool: rigenerabile in blocco,
  da NON editare a mano.
-->

## Memory & workflow (gosidian)

Stai lavorando su **{{PROJECT}}** ({{PROJECT_TYPE}}, stack: {{STACK}}).
La memoria persistente cross-sessione vive nel vault gosidian, nel
progetto `{{PROJECT}}/`, accessibile via i tool MCP `mcp__gosidian__memory_*`.
Pattern Karpathy-Wiki-Stack: le note sono la wiki che **tu** (l'agent)
mantieni mentre lavori — non l'output primario, ma la memoria compilata.

### Regola zero — Bootstrap di sessione

All'inizio di **ogni** sessione, **prima** di qualsiasi `Grep`/`Read` sul
codice, fai il bootstrap della memoria con una singola call:

```
mcp__gosidian__memory_bootstrap({project: "{{PROJECT}}"})
```

Ritorna in un solo JSON: `hot_md` e `readme` (`{present, path, content,
etag}`), `active_plans[]`, `available_skills[]`, `available_agents[]`,
`recent_notes[]`, `stats`, `access`, `missing[]` e **`directives_block`** —
le **direttive operative complete** (mappa cartelle vault, ingest rules,
regole plan, workflow end-of-task, vocabolario tag). **Leggi e segui
`directives_block`**: è la fonte autoritativa delle regole di lavoro,
servita sempre aggiornata (non duplicarla in questo file). Controlla
sempre `available_skills[]` prima di iniziare una procedura operativa.

Se `memory_bootstrap` non è disponibile, fallback manuale:

1. `mcp__gosidian__memory_get({path: "{{PROJECT}}/hot.md"})` — orientamento rapido
2. `mcp__gosidian__memory_get({path: "{{PROJECT}}/README.md"})` — mappa del progetto
3. `mcp__gosidian__memory_notes_by_tag({tag: "status:in-progress"})` — plan attivi
4. `mcp__gosidian__memory_notes_by_tag({tag: "type:skill"})` — catalogo skill

### Auto-allineamento dello stub

Questo stub è marcato `<!-- gosidian:stub v=N -->` in testa.
`memory_bootstrap` ritorna `stub_version`: se è **maggiore** della `N` del
marker (o il marker manca), rigenera lo stub con

```
mcp__gosidian__memory_init_agent({
  project: "{{PROJECT}}",
  existing_content: "<contenuto integrale di questo file>"
})
```

e riscrivi **solo** la porzione fra `<!-- gosidian:stub v=... -->` e
`<!-- /gosidian:stub -->`, lasciando intatte le sezioni locali. Lo stub
cambia di rado (solo se cambia il *contratto*); le **direttive operative**
si aggiornano invece da sole via `directives_block`, senza toccare questo
file. Nessun broadcast necessario.

### Hot files

{{HOT_FILES}}

### Lingua

- UI utente, messaggi, note del vault: **{{LANGUAGE}}**
- Codice, commenti inline, commit message, nomi di variabili/funzioni:
  **{{CODE_LANGUAGE}}**

### Specifiche locali

> ⚠️ **Non scrivere le tue note qui.** Questa sezione è **dentro** i marker
> `gosidian:stub`, quindi viene **rigenerata** (e azzerata) a ogni bump di
> `stub_version`. Metti le specifiche di questo repo (comandi build/test,
> deploy, gotcha, vincoli) **sotto** il marker `<!-- /gosidian:stub -->`,
> come sezione locale: lì sopravvivono alla rigenerazione. Le **direttive
> operative** non vanno invece duplicate da nessuna parte — le serve
> `directives_block` dal bootstrap.

### Meta

- Stub generato da `memory_init_agent` ({{TODAY}}) per il profilo agent
  **{{AGENT_NAME}}** (`agent_profile={{AGENT_PROFILE}}`), versione
  **v{{STUB_VERSION}}**. Mantieni il blocco fra i marker `gosidian:stub`
  come unità riconoscibile.

<!-- /gosidian:stub -->
