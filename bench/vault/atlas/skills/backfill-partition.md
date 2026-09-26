---
title: Backfill a partition
description: Rebuild one month of data in ClickHouse from the replica.
tags: [atlas, type:skill]
type: skill
updated: 2026-06-12
---

# Backfill a partition

1. `airflow dags backfill sales_ingest -s 2026-03-01 -e 2026-03-31`.
2. Before inserting, drop the target partition: `ALTER TABLE sales DROP PARTITION '2026-03'` — skipping this step duplicated rows (BUG-005).
3. Re-run `sales_rollup` for the same range.
