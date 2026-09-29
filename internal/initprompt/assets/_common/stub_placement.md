**Dove va il blocco**: copia `gosidian_block` intero, dalla riga
`<!-- gosidian:stub v=N -->` alla riga `<!-- /gosidian:stub -->`
comprese. Se il file contiene già uno stub (una riga che inizia con
`<!-- gosidian:stub v=`), **sostituiscilo**: togli tutto dalla riga del
marker di apertura alla riga che contiene **soltanto**
`<!-- /gosidian:stub -->`, comprese, e metti il blocco nuovo al loro
posto. Non fermarti alla prima occorrenza di `<!-- /gosidian:stub -->`:
gli stub fino alla v2 la citano anche nel testo. Quello che sta fuori
dai marker resta com'è. Se non c'è nessuno stub, aggiungi il blocco in
coda al file, preceduto da una riga separatrice.
