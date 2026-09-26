---
title: Atlas Decisions
description: Architecture decision records of atlas.
tags: [atlas, type:memory, topic:decisions]
type: memory
updated: 2026-03-06
---

# Atlas Decisions

## ADR-001 — ClickHouse over BigQuery
- **Date**: 2026-02-09 · **Status**: accepted
- Self-hosted next to tidewater, predictable cost, fast aggregations on order data.

## ADR-002 — dbt for transformations
- **Date**: 2026-02-11 · **Status**: accepted
- SQL models under version control, tests on every model.

## ADR-003 — Airflow instead of cron
- **Date**: 2026-02-16 · **Status**: accepted
- Retries, dependencies between jobs and a UI to re-run a failed day.

## ADR-004 — No raw personal data in the warehouse
- **Date**: 2026-03-06 · **Status**: accepted
- Buyer emails are hashed with a secret salt at ingestion; names and addresses are never copied.
