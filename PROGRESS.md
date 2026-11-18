# Avancement ReminisLink

## Jalons
| Jalon | Statut | Branche | Revue Antigravity | Intégré le |
|---|---|---|---|---|
| M0 | en attente des relevés de la console | — | — | — |
| M1 | prêt à intégrer, en attente des décisions de spec | `m1-contrat-crypto` | 3 revues Claude neuves (non croisées, `agy` absent) : plus aucun bloquant — `docs/reviews/m1-claude.md` | — |
| M2 | à faire | — | — | — |
| M3 | à faire | — | — | — |
| M4 | à faire | — | — | — |
| M5 | à faire | — | — | — |
| M6 | à faire | — | — | — |
| M7 | à faire | — | — | — |

## En cours
- M1 : code livré (10 commits). Contrôles §4 OK le 2026-11-18 : `make lint test`, `make contract`, `swift test` (21 tests), vecteurs 5 appairage / 10 signature / 15+23 identifiants, aucun `TODO(M0)`, diff dans le périmètre. Revue (agent Claude neuf) : 1 bloquant confirmé (dépassement du contrôle d'horloge, `X-RL-Time` très lointain accepté), corps lu avant le HMAC (épuisement mémoire), `ENOTDIR` non typé dans `ResolveID`, 429 non déclaré au contrat. Corrections faites (5 commits, `d433abf2`..`77d7d44f`), contrôles §4 de nouveau OK (Swift : 23 tests). Revue ciblée : 1 nouveau bloquant (rejeu pendant un envoi en cours → corps lus en mémoire), horloges murale/monotone mélangées, `Retry-After` absent. Aller-retour 2 fait (`a6a94b98`, `bcd73fe9`) ; contrôles §4 OK, `go test -race` OK. Revue ciblée n° 2 : aucun bloquant ; points mineurs corrigés à l'aller-retour 3 (`73cde9e1`, `57259f3c`, `186f40d3`), diff relu par l'agent principal ; contrôles §4 OK (`go test -race -count=2` OK). En attente des décisions de spec de la personne avant fusion.
- M0 : en attente de `survey-1-before.txt`, `survey-2-after.txt` et de `notes.md` rempli dans `docs/specs/m0/`.

## État du dépôt (2026-11-18)
- Branche `main` sans aucun commit : tous les fichiers sont non suivis (`CLAUDE.md`, `README.md`, `docs/`, `go.mod`, `prompts/`, `scripts/`, `skills/`, `.gitignore`).
- Aucun code Go ni iOS, pas de `Makefile`, pas de `protocol/`, pas de `testdata/`.
- Les worktrees du processus (§3 de l'orchestrateur) exigent un premier commit sur `main`.
- `skills/system/run-workflow/SKILL.md` ne concerne pas le projet (outil `run_workflow` d'un autre environnement) : à confirmer avant de le commiter.
- Projet de référence présent : `../nextui-cheat-manager`.

## Outils (2026-11-18)
| Outil | Résultat |
|---|---|
| `go version` | go1.27.4 darwin/arm64 |
| `docker --version` | 29.7.2, démon actif |
| `xcodebuild -version` | Xcode 27.2 (27B52) |
| `swift --version` | Swift 6.4.2 |
| `make --version` | GNU Make 3.81 |
| `agy --version` | 1.5.11 (`~/.local/bin/agy`), installée le 2026-11-18 ; modèles Gemini 3.8 Flash |
| Simulateurs iOS | runtimes iOS 18.3 et 18.5 installés, **aucun appareil créé** (pas d'« iPhone 16 » comme l'attend `CLAUDE.md`) ; runtimes iOS 26.x indisponibles |

Conséquence : tant que `agy` est absent, la revue croisée se fait avec un sous-agent Claude neuf (revue non croisée, à signaler à chaque jalon).

## Décisions
- 2026-11-18 — `PROGRESS.md` créé à la racine, non commité (le dépôt n'a pas encore de premier commit). Agent principal.
- 2026-11-18 — Premier commit de `main` **sans** `skills/` (skill système `run_workflow` d'un autre environnement, sans rapport avec le projet) ni `.claude/settings.local.json` : les deux sont ajoutés au `.gitignore`. `PROGRESS.md` est commité. Proposé par Claude (session de cadrage) ; considéré comme validé sauf objection de la personne.
- 2026-11-18 — Écart `DELETE /games/{gameId}` tranché : **tout** part dans la corbeille (ROM, save, states, cheats, jaquette) par déplacement ; nouvelles routes `GET /trash`, `DELETE /trash`, `DELETE /trash/{entryId}` (en-tête `X-RL-Confirm: empty-trash`), seules à effacer définitivement. Specs 04, 05, 06, 07, 09 et prompt M4 mis à jour. Proposé par Claude (session de cadrage) ; considéré comme validé sauf objection de la personne.
- 2026-11-18 — Premier commit de `main` fait (`bb3a311`). Agent principal.
- 2026-11-18 — Commits **non signés** pendant le développement (clé GPG sur YubiKey avec PIN, personne absente) : `commit.gpgsign=false` et `tag.gpgsign=false` en config **locale** du dépôt (la config globale n'est pas modifiée). À la fin : retirer ces deux réglages (`git config --local --unset ...`) et re-signer l'historique. Décidé par la personne.
- 2026-11-18 — M1 peut démarrer sans attendre M0 (aucune dépendance aux points « À VÉRIFIER M0 »). Proposé par Claude (session de cadrage) ; considéré comme validé sauf objection de la personne.

- 2026-11-18 — Spec mise à jour avec les choix de M1 (03 §4.2, §4.3, §5, §6 ; 04 ; 07) et `CLAUDE.md` (commandes `make contract`, `make vectors`). Doublons de clés de requête refusés (`400`), à implémenter en M2. Rejeu après redémarrage du pak : risque résiduel accepté (07). Décidé par la personne (« oui pour tout »).
- 2026-11-18 — `agy` installé ; `Bash(make *)` autorisé par la personne.

## Questions ouvertes pour la personne
   - b. 03 §4.3 : HMAC vérifié avant l'empreinte du corps, nonce réservé juste après le HMAC et consommé même si la suite échoue (oui).
   - c. 03 §5 : identifiants non canoniques (`a//b`, `a/`, `./a`) refusés au lieu d'être nettoyés par `filepath.Clean`, pas de normalisation Unicode (oui).
   - d. 03 §4.2 : `+` décodé en espace, paires vides ignorées, tri par octets des paires encodées (oui).
   - e. 04 : corps de réponse ajoutés par `openapi.yaml` là où 04 ne disait rien, `clientName` 1–64 caractères (oui).
1. Relevés M0 sur la console (voir le compte rendu de démarrage).
2. ~~CLI Antigravity~~ : installée (`agy` 1.5.11, modèles Gemini 3.8).
6. ~~Report dans la spec des choix de M1~~ : oui pour tout (voir Décisions).
3. ~~Premier commit de `main`~~ : tranché (voir Décisions).
4. ~~Lancer M1 pendant l'attente de M0~~ : oui (voir Décisions).
5. ~~Écart `DELETE /games/{gameId}`~~ : tranché, tout va dans la corbeille (voir Décisions).

## Plus tard
- Revue M1 : imposer la même horloge au `Verifier` et au cache de nonces (constructeur unique).
- M2 : refuser les clés de requête en double (`400 invalid_request`, 03 §4.2).
- M2 : `ReadHeaderTimeout`, délai d'inactivité de lecture, `BaseContext` annulé à l'arrêt ; brancher `Require(RouteSave)` et `Require(RouteChunk)`.
- Revue M1 (pour M2) : vérifier qu'un `folderId` appartient au `systemId` de la route.
- Revue M1 : NFC/NFD non normalisés dans les identifiants — à vérifier en mode desktop sur macOS (noms souvent en NFD).
- Créer un simulateur « iPhone 16 » (`xcrun simctl create`) avant M1/M3, ou adapter la commande de `CLAUDE.md` au runtime disponible.
