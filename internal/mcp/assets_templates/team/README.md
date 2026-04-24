---
title: {{PROJECT}} Project Index
description: Landing page per la memoria persistente del progetto {{PROJECT}}.
tags: [{{PROJECT}}, type:index, topic:meta]
type: index
updated: {{TODAY}}
---

# {{PROJECT}} — Project Memory

Memoria persistente del progetto **{{PROJECT}}**. Pattern Karpathy-Wiki-Stack (stabile, ma aggiornabile incrementalmente).

## Mappa

| Cartella | Scopo |
|---|---|
| `memory/` | Conoscenza stabile (architecture, decisions, conventions, glossary, environments) |
| `agents/` | Ruoli specializzati attivi |
| `plans/` | Piani di task non banali, `YYYYMMDD-<slug>.md`, con `Outcome` post-esecuzione |
| `skills/` | Procedure ripetibili |
| `docs/` | Q&A, open questions, improvements, bug tracker |
| `hot.md` | Session cache |
| `log.md` | Log append-only |

## Bootstrap di sessione

1. `memory_bootstrap({project: "{{PROJECT}}"})` — aggregato completo in una call
2. Se serve, `memory_plans(project, status:"in-progress")` + `memory_skills(project)`
3. Per task mirati, letture puntuali via `memory_get`
