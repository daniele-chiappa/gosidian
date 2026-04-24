---
title: {{PROJECT}} Activity Log
description: Log append-only di attività sul progetto {{PROJECT}}.
tags: [{{PROJECT}}, type:index, topic:meta]
type: index
updated: {{TODAY}}
---

# {{PROJECT}} — Log

Log **append-only** delle attività cross-sessione. Nuove entry **in fondo**, mai modificare le esistenti.

## Convenzioni di entry

```markdown
## YYYY-MM-DD — <tipo> — <titolo breve>

Corpo dell'entry: 1-5 righe. Per task grandi linka il plan.
```

Tipi: `bootstrap`, `plan-closed`, `adr`, `pattern`, `fix`, `discovery`, `ops`.

---

## {{TODAY}} — bootstrap — Scaffold creato

Scaffold iniziale del progetto {{PROJECT}} generato via `memory_project_scaffold`.
