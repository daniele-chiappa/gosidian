---
title: Warehouse bootstrap
description: Stand up ClickHouse, the ingestion job and the first rollup.
tags: [atlas, type:plan, status:done]
type: plan
status: done
author: Nadia Osei
created: 2026-02-01
---

# Warehouse bootstrap

## Solution
- ClickHouse `ch-1` with three shards.
- `sales_ingest` hourly from `pg-replica`.
- First model: daily sales per event.

## Outcome
First complete rollup on 2026-02-25.
