---
title: Tidewater Conventions
description: Branching, commits, database migrations, feature flags, reviews and release rules of the tidewater team.
tags: [tidewater, type:memory, topic:conventions]
type: memory
importance: 4
updated: 2026-05-30
---

# Conventions

## Branches and commits

- Trunk-based: short branches off `main`, merged within two days.
- Conventional commits (`feat:`, `fix:`, `chore:`), imperative mood, English.
- Every pull request needs one approval; payments code needs Ivo or Marta.

## Database migrations

- Files live in `db/migrations/` and are named `YYYYMMDDHHMM_<description>.up.sql`, each with its paired `YYYYMMDDHHMM_<description>.down.sql`.
- A migration must be reversible unless the plan says otherwise.
- How to apply them: [[tidewater/skills/run-db-migration]].

## Feature flags

- Named `ff_<area>_<name>`, for example `ff_payments_v2`.
- Removed at most two releases after reaching 100%.

## Releases

- Semantic versions, tagged from `main`, notes in [[tidewater/log]].
- Deploy freeze: no production releases after 15:00 on the last working day of the week, nor on public holidays. Exceptions need Marta.
- Staging first, always: [[tidewater/skills/deploy-to-staging]].

## Code review

- Reviewers check tests, migrations, flags and the audit trail of money movements.
- Checklist: [[global/skills/code-review-checklist]].
