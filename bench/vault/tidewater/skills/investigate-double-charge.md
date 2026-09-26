---
title: Investigate a double charge
description: What to check when the duplicate-charge alert fires or support reports two Paylane charges for one order.
tags: [tidewater, type:skill, topic:payments, topic:support]
type: skill
updated: 2026-03-10
---

# Investigate a double charge

## Trigger
Alert `paylane_duplicate_charge`, or support reports two Paylane charges for one order.

## Steps
1. Look up the order: `tidectl orders show <id>` lists charges and their idempotency keys.
2. Same key twice → Paylane processed one charge; the second line is an authorization that expires by itself in 7 days.
3. Different keys → a real second charge. Check `payment_events` for the Paylane event ids and the webhook log.
4. Refund the extra charge from the Paylane dashboard and note it on the order.
5. Open a bug if the cause is on our side ([[tidewater/docs/bugs]]).

## Background
Paylane retries webhook deliveries every 5 minutes for up to 24 hours until it gets a 2xx. The handler is idempotent since [[tidewater/plans/2026-03-02-webhook-idempotency]].
