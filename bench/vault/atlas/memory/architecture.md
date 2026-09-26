---
title: Atlas Architecture
description: How atlas moves tidewater orders into ClickHouse and builds the aggregates.
tags: [atlas, type:memory, topic:architecture]
type: memory
importance: 4
updated: 2026-06-05
---

# Atlas Architecture

## Pipeline
1. **Ingestion** — the job `sales_ingest` copies new orders, admissions and refunds every hour. It reads from tidewater's read replica `pg-replica` (see [[tidewater/memory/environments]]), never from the primary.
2. **Warehouse** — ClickHouse cluster `ch-1`, three shards on the analytics hosts.
3. **Transformations** — dbt models in the `atlas-dbt` repository ([[atlas/memory/decisions#ADR-002]]).
4. **Orchestration** — Apache Airflow. The scheduler and webserver run on `reef-1`; workers on `reef-2` and `reef-3`. The DAG `sales_rollup` runs at 02:00 UTC and normally completes by 04:00 UTC.

## Consumers
- Organizer reports in the admin console.
- The tidewater organizer dashboard (in progress).

## Personal data
Emails are hashed before they reach ClickHouse ([[atlas/memory/decisions#ADR-004]]).
