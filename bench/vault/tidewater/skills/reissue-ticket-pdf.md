---
title: Reissue a ticket PDF
description: Render again the PDF of an order and send it to the buyer.
tags: [tidewater, type:skill, topic:tickets, topic:support]
type: skill
updated: 2026-03-01
---

# Reissue a ticket PDF

## Steps
1. Find the order in the admin console by email or order number.
2. "Reissue PDF" enqueues a render job on `tickets.render`.
3. The mail goes out with the `order-confirm-v2` template.

Reissuing does not change the QR tokens; to invalidate them, cancel and re-create the admissions.
