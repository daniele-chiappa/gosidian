---
title: Re-run a failed DAG
description: Clear and re-run a failed Airflow task for one day.
tags: [atlas, type:skill]
type: skill
updated: 2026-06-12
---

# Re-run a failed DAG

1. Airflow UI → DAG → Grid → pick the failed day.
2. Clear the failed task with "downstream" checked.
3. Watch the task logs; `sales_ingest` retries three times with 10-minute pauses.
4. If the rollup finishes after 05:00, tell the tidewater team: the dashboard shows yesterday.
