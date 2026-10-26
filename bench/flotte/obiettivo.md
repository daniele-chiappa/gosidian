---
title: Schede di allenamento per principianti
---

Costruisci, dalle fonti di questo progetto, la conoscenza e un'app per cinque principianti.

Le fonti sono in fitness/fonti/: le linee guida dell'OMS sull'attività fisica e il comportamento sedentario (2020) e alcuni articoli scientifici open access, una nota per documento. Le persone sono in fitness/persone/: una nota per persona, con età, salute, abitudini, obiettivo, tempo e attrezzatura. Fonti e persone non si cambiano.

Il risultato:

1. **La conoscenza**, in fitness/conoscenza/:
   - note brevi sugli argomenti che servono alle schede: quanta attività fisica raccomandano le linee guida (adulti, anziani, gravidanza e post-parto, persone con malattie croniche), il comportamento sedentario, l'allenamento della forza per principianti (serie, ripetizioni, carichi, frequenza, recupero, progressione), il riscaldamento e la sicurezza, l'equilibrio e la prevenzione delle cadute, l'esercizio con l'artrosi del ginocchio, con la pressione alta e dopo il parto;
   - un catalogo di esercizi in fitness/conoscenza/esercizi/, una nota per esercizio: come si esegue, livello, attrezzatura, muscoli, errori comuni, quando evitarlo o adattarlo;
   - ogni affermazione con la sua fonte: il link alla nota della fonte e la pagina o la sezione.
2. **Le schede**, in fitness/conoscenza/schede/, una per persona: 8 settimane in due fasi; i giorni della settimana con gli esercizi del catalogo (serie, ripetizioni o minuti, recupero) e l'attività aerobica; come si progredisce; i segnali per fermarsi o sentire il medico; le fonti delle scelte.
3. **L'app**, nel repository agents-fitness: un sito statico che legge le schede e il catalogo da data/*.json, scritti dalle note del vault. La pagina iniziale elenca le cinque persone per nome; un clic sul nome mostra la sua scheda, settimana per settimana; un link «Esercizi» mostra il catalogo, e ogni esercizio delle schede vi si trova. Funziona anche su un telefono (390 px di larghezza), senza errori nella console. I test controllano che i dati siano completi e coerenti.
4. **La revisione**: un controllo indipendente che la conoscenza e le schede dicano quello che dicono le fonti citate e siano sicure e adatte a ciascuna persona.

Si accetta quando le cinque schede e il catalogo sono nel vault con le loro fonti, l'app è su main con i test e la build verdi e mostra tutte le schede, e la revisione non trova problemi aperti.
