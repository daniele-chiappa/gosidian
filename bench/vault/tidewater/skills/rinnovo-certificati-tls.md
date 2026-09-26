---
title: Rinnovo manuale dei certificati TLS
description: Procedura di riserva per rinnovare a mano i certificati e ricaricare HAProxy quando il rinnovo automatico fallisce.
tags: [tidewater, type:skill, topic:ops, topic:tls]
type: skill
updated: 2026-05-27
---

# Rinnovo manuale dei certificati TLS

## Quando
Se l'allarme segnala un certificato in scadenza entro 14 giorni e il timer di certbot non ha funzionato. Il rinnovo automatico è descritto in [[tidewater/plans/2026-05-18-automazione-tls]]; i certificati Let's Encrypt durano 90 giorni e vanno rinnovati ogni 60.

## Passi
1. Su `kelp-1`: `sudo certbot renew --cert-name tidewater.example --force-renewal`.
2. Controllare la data: `openssl x509 -enddate -noout -in /etc/letsencrypt/live/tidewater.example/cert.pem`.
3. Rigenerare il file per HAProxy: `/usr/local/bin/haproxy-cert-bundle tidewater.example`.
4. Ricaricare senza interruzioni: `systemctl reload haproxy`.
5. Verificare dall'esterno con `curl -vI https://tidewater.example`.
