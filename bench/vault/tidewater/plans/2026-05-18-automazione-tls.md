---
title: Automazione del rinnovo TLS
description: Rinnovo automatico dei certificati con certbot e ricarica di HAProxy senza interruzioni.
tags: [tidewater, type:plan, status:done, topic:ops, topic:tls]
type: plan
status: done
author: Luca Ferri
created: 2026-05-18
updated: 2026-05-27
---

# Automazione del rinnovo TLS

## Contesto
Il certificato di `tidewater.example` è scaduto per 20 minuti il 2026-05-16: il rinnovo era manuale.

## Soluzione
- certbot con challenge DNS sul provider del dominio, timer systemd ogni 12 ore.
- Hook di deploy che concatena chiave e catena nel formato di HAProxy e fa un reload senza perdere connessioni.
- Allarme se un certificato scade entro 14 giorni.

## Esito
In produzione dal 2026-05-27. Procedura manuale di riserva: [[tidewater/skills/rinnovo-certificati-tls]].
