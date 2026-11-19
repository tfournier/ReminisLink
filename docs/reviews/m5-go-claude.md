# Revues M5 Go — agents Claude neufs

Pas de revue croisée Antigravity (quota Gemini épuisé ; GPT-OSS jugé trop peu fiable).

## Revue complète — à corriger
- **Bloquant (confirmé)** : l'ancien `.cht.bak` était écrasé sans corbeille → après deux écritures, le `.cht` original n'existait plus nulle part.
- **Bloquant** : course de données dans le test de démo (`go test -race ./cmd/...`).
- À corriger : changement de cœur → state incompatible restauré ; ordre de recherche du pak émulateur inversé par rapport à NextUI (`utils.c:381-384`) ; `intact` annoncé sans comparaison ; `useExtractedFileName` ignoré ; verrous orphelins recréés ; écarts spec/contrat.

## Revue ciblée — à corriger, aucun bloquant
- Le fichier de disque `<rom>.<N>.txt` partait à la corbeille lors d'une restauration de verrou (multi-disques : mauvais disque au rechargement).
- Clés `extra` préfixées renommées en silence ; métadonnée de verrou reconstruite adoptant le cœur actuel ; `core-changed` pour un dossier introuvable ; `.rtc` absent de `GET /games`.

## Aller-retour 2 (`9571ed87`)
Corrections faites et testées (`.txt`/`.bmp` copiés avec le verrou, clés `extra` sans retrait de préfixe côté API, dossier « inconnu » jamais restauré automatiquement, rôle `rtc`, import par URL en `429` si le client abandonne). `make lint test`, `make contract`, `go test -race ./...`, `smoke-saves.sh` OK. Intégration avec l'app contre le pak headless réussie (voir `m5-ios-claude.md`).
