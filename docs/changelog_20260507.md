# Changelog - 07 Maggio 2026

Questo documento riassume tutte le implementazioni, i refactoring architetturali e i miglioramenti introdotti tra il 6 e il 7 Maggio 2026. Tutte queste funzionalità consolidano il Ibis Assistant per un utilizzo in produzione stabile, multi-linguaggio e altamente affidabile su Windows.

## 1. Gestione Dinamica dei Workspace e Code Ingestion
- **Code Ingestion Dinamica**: Aggiunti `internal/ingest/dynamic/queue.go` e `workspace.go` per orchestrare l'aggiornamento dei repository in tempo reale tramite `ProjectIngestionManager`. La sincronizzazione (fetch/checkout) è separata dalla vettorializzazione asincrona.
- **Scoperta File con `git ls-tree`**: Refactoring massiccio di `internal/ingest/code/ingest.go`. Ora l'albero di Git (HEAD) è la vera Single Source of Truth per la ricerca dei file, abbandonando l'attraversamento ricorsivo obsoleto.
- **Smart Pruning**: La rimozione dei file obsoleti sfrutta direttamente l'`activeFilesMap` estratta da Git, permettendo di rimuovere in automatico dal database tutti i chunk associati a file non più presenti nell'albero di commit corrente.

## 2. Analisi Determinica dei Log (LogAlign) e Smart Chunking
- **LogAlign Methodology**: Modificato `internal/ingest/logs/analyzer.go` per intercettare i log usando template statici estratti dal codice. Questo bypassa le costose e imprevedibili analisi basate su IA (LLM) ogni volta che un log combacia con una firma nota.
- **Batch Manager Esteso**: Aggiornato `internal/ai/batch_manager.go` per ottimizzare le finestre di contesto e gestire l'AI-assisted Smart Chunking senza spezzare stack trace.
- **Schema Database**: Introdotti nuovi nodi ed edge in `internal/schema/schema.go` (`TableLogTemplate` e `EdgeEmitsLog`) per relazionare direttamente e deterministicamente la riga di log alla riga di codice esatta che l'ha generato.

## 3. Integrazione Ast-Grep Sidecar
- **Motore Sidecar**: Creato `internal/ingest/code/ast_chunker.go` per orchestrare l'estrazione sintattica del codice delegandola all'eseguibile Rust `sg.exe` (ast-grep), evitando dipendenze native.
- **Regole Multi-Linguaggio in YAML**: La logica di chunking per C#, Go, JS, TS, Java, Python, C++ è ora codificata nei file `rules/*.yml` insieme alla configurazione `sgconfig.yml`. Questo rende l'estensione del parser immediata senza necessitare ricompilazioni.

## 4. Pulizia del Motore di Ricerca e Agentic
- **Deprecazione Modelli Sperimentali**: Rimossi `internal/optimization/raft_test.go`, `internal/search/reinforce_test.go` e moduli sperimentali fallimentari in favore di una solida implementazione Agentic multi-modale.
- **Agentic Search**: Refactoring in `internal/search/agentic.go` e `search.go` per irrobustire l'agentic search, affiancato da svariati unit test (parsing, regex, robustness, multiline).

## 5. Deployment e Configurazione Unificata (`dist/`)
- **Master Config**: Eletto `config_master.json` come l'unico file di configurazione in controllo di versione. Rimossi i leak accidentali di chiavi nel vecchio `config.json`.
- **Target `make dist`**: Il `Makefile` ora assembla automaticamente l'intero ambiente di produzione in una directory `dist/`.
- **Nuove Path di Sistema**: Uniformate e rinominate le directory di processo rimuovendo gli underscore: ora si usano `dist/db`, `dist/logs`, e `dist/repos`.
- **Pulizia Root**: Eliminati dalla root decine di file temporanei (`patch.diff`, `temp.go`, `test.cs`, vecchi file `.bat` ed esportazioni di Copilot).
