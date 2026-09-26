---
title: Payouts v1
description: Weekly payouts to promoters with a statement per period.
tags: [tidewater, type:plan, status:done, topic:payouts, topic:worker]
type: plan
status: done
author: Ivo Petrov
importance: 4
created: 2026-04-20
updated: 2026-05-04
---

# Payouts v1

## Context
Until now payouts were prepared by hand from a spreadsheet.

## Solution
- `settle-worker`, a mode of tide-worker, computes each promoter's balance every Monday at 06:00 UTC: face value of paid admissions minus refunds, service charges excluded.
- A statement PDF per promoter and period, stored next to the tickets.
- Bank transfers are still approved by Marta.

## Outcome
First automatic run on 2026-05-04. Onboarding of a new promoter: [[tidewater/skills/onboard-organizer]].
