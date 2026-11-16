---
title: Scheda di Elena
---

Una persona nuova, Elena (fitness/persone/elena.md), ha bisogno della sua scheda e di comparire nell'app. La conoscenza c'è già: non si rileggono le fonti.

Il risultato, in tre task piccoli, uno dopo l'altro (depends_on):

1. **La scheda** (ruolo conoscenza): una nota sola e breve (al più 120 righe), fitness/conoscenza/schede/elena.md. Da leggere, e nient'altro: fitness/persone/elena.md, fitness/conoscenza/forza-principianti.md, fitness/conoscenza/comportamento-sedentario.md e fitness/conoscenza/esercizi/forza-indice.md (per i nomi esatti degli esercizi). La forma, uguale alle altre schede: frontmatter (title, type: conoscenza, persona: elena, tags); `## Profilo e vincoli`; `## Fase 1` con `### Settimana 1` … `### Settimana 4`; `## Fase 2` con `### Settimana 5` … `### Settimana 8`; in ogni settimana i giorni in grassetto (`**Giorno A — forza (30 min)**`) e sotto una riga per esercizio: `- [[fitness/conoscenza/esercizi/<nome>]] — 2 × 10 ripetizioni, recupero 60 s`; l'attività aerobica in minuti; poi `## Progressione`, `## Quando fermarsi`, `## Fonti` (i link alle note di conoscenza lette). Una settimana uguale alla precedente si scrive per intero lo stesso, con i suoi esercizi.
2. **L'app** (ruolo sviluppo), nel repository agents-fitness: copiare la scheda dal vault in test/fixtures/vault/conoscenza/schede/elena.md e la nota della persona in test/fixtures/vault/persone/elena.md, accanto alle altre cinque, rigenerare i dati con `node data/build-data.mjs`, portare da 5 a 6 le persone attese nei test. Accettazione: `npm test && npm run build` verdi.
3. **La revisione** (ruolo revisore): la scheda dice quello che dicono le note citate, è sicura e adatta a Elena, usa solo esercizi del catalogo.

Si accetta quando la scheda è nel vault, l'app è su main con Elena e i test verdi, e la revisione non trova problemi aperti.
