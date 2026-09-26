---
title: Tidewater Bugs
description: Bug log of tidewater with status and severity.
tags: [tidewater, type:doc, topic:bugs]
type: doc
updated: 2026-06-26
---

# Bugs

One section per bug, newest last. Status: `open`, `fixed in <version>`, `wontfix`.

## BUG-001 — Event page returns 500 when the venue has no address

- **Status**: fixed in 1.1.1 · **Severity**: low
- Null venue address in the event template. Reported during the first organizer onboarding.

## BUG-002 — Hold not created for general admission with quantity 1

- **Status**: fixed in 1.2.1 · **Severity**: medium
- Off-by-one in the quantity validation of `POST /holds`.

## BUG-003 — PDF artwork missing for events created before 2026-02-01

- **Status**: fixed in 1.3.1 · **Severity**: low
- Old events had no cover in Nimbus; fallback to the organizer logo.

## BUG-004 — Organizer dashboard totals include cancelled orders

- **Status**: fixed in 1.4.0 · **Severity**: medium
- Query summed all orders; now only paid and not refunded.

## BUG-005 — Confirmation mail sent twice after a worker restart

- **Status**: fixed in 1.4.2 · **Severity**: medium
- Stream message acknowledged after the send instead of before the retry window.

## BUG-006 — Search on the home page ignores accented characters

- **Status**: fixed in 1.5.0 · **Severity**: low
- Postgres unaccent extension added to the search index.

## BUG-007 — Gatekeeper crashes on Android 9 devices

- **Status**: wontfix · **Severity**: low
- Venues were given newer devices; Android 9 is unsupported.

## BUG-008 — Event start times shifted by one hour for organizers in another time zone

- **Status**: fixed in 1.6.1 · **Severity**: high
- Reported by the organizer Harbor Hall ([[tidewater/docs/partners/harbor-hall]]): times were stored in the browser's local time without zone. Now stored in UTC with the venue's zone.

## BUG-009 — Refund of a partially used order refunds every admission

- **Status**: fixed in 1.6.2 · **Severity**: high
- Refund logic ignored `consumed_at`.

## BUG-010 — Staging database copy keeps real email addresses

- **Status**: fixed in 1.6.2 · **Severity**: high
- Scrubbing step skipped the `buyers_archive` table.

## BUG-011 — Retried Paylane webhooks create a second charge

- **Status**: fixed in 1.7.0 · **Severity**: critical
- Root cause of [[tidewater/docs/incidents/2026-02-27-inc-2-duplicate-charges|INC-2]]. Fixed by [[tidewater/plans/2026-03-02-webhook-idempotency]].

## BUG-012 — Seat map editor loses changes on tab switch

- **Status**: fixed in 1.8.0 · **Severity**: medium
- Unsaved state kept only in the component; now autosaved.

## BUG-013 — Service charge rounded per admission instead of per order

- **Status**: fixed in 1.9.1 · **Severity**: medium
- Cents drifted on orders of 8 admissions.

## BUG-014 — Hold release worker frees places of paid orders on slow webhooks

- **Status**: fixed in 1.9.1 · **Severity**: high
- Late confirmation arrived after the lapse; now refunds automatically, see [[tidewater/plans/2026-04-01-hold-release-worker]].

## BUG-015 — Payout statement PDF shows the wrong period end

- **Status**: fixed in 1.9.3 · **Severity**: low
- Period end computed in local time.

## BUG-016 — Gatekeeper offline list not refreshed after midnight

- **Status**: fixed in 1.9.3 · **Severity**: medium
- Cache key used the event date instead of the entry window.

## BUG-017 — Same admission accepted at two gates within two seconds

- **Status**: fixed in 1.9.2 · **Severity**: high
- Race between two gates validating the same QR token. Fixed by [[tidewater/plans/2026-04-16-gatekeeper-single-use]] ([[tidewater/memory/decisions#ADR-011]]).

## BUG-018 — Waiting room pass accepted after expiry

- **Status**: fixed in 1.10.0 · **Severity**: medium
- Signature checked, expiry not.

## BUG-019 — Dashboard sales chart empty for events with comps only

- **Status**: open · **Severity**: low
- atlas aggregates skip orders with total 0.

## BUG-020 — 3-D Secure 2 challenge loops on some iOS browsers

- **Status**: open · **Severity**: high
- Seen only with payments v2 at 10%; tracked in [[tidewater/plans/2026-05-12-payments-v2]].
