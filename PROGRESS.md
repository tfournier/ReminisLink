# Avancement ReminisLink

## Jalons
| Jalon | Statut | Branche | Revue Antigravity | Intégré le |
|---|---|---|---|---|
| M0 | en attente des relevés de la console | — | — | — |
| M1 | à faire | — | — | — |
| M2 | à faire | — | — | — |
| M3 | à faire | — | — | — |
| M4 | à faire | — | — | — |
| M5 | à faire | — | — | — |
| M6 | à faire | — | — | — |
| M7 | à faire | — | — | — |

## En cours
Rien. M0 attend les relevés de la console. `docs/specs/m0/` contient la procédure (`README.md`), le modèle `notes.md` (vide) et `questions.md` (vide), mais pas encore `survey-1-before.txt` ni `survey-2-after.txt`.

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
| `agy --version` | **absent** : `Antigravity.app` est installée mais la CLI `agy` n'est pas dans le `PATH` |
| Simulateurs iOS | runtimes iOS 18.3 et 18.5 installés, **aucun appareil créé** (pas d'« iPhone 16 » comme l'attend `CLAUDE.md`) ; runtimes iOS 26.x indisponibles |

Conséquence : tant que `agy` est absent, la revue croisée se fait avec un sous-agent Claude neuf (revue non croisée, à signaler à chaque jalon).

## Décisions
- 2026-11-18 — `PROGRESS.md` créé à la racine, non commité (le dépôt n'a pas encore de premier commit). Agent principal.
- 2026-11-18 — Premier commit de `main` **sans** `skills/` (skill système `run_workflow` d'un autre environnement, sans rapport avec le projet) ni `.claude/settings.local.json` : les deux sont ajoutés au `.gitignore`. `PROGRESS.md` est commité. Proposé par Claude (session de cadrage) ; considéré comme validé sauf objection de la personne.
- 2026-11-18 — Écart `DELETE /games/{gameId}` tranché : **tout** part dans la corbeille (ROM, save, states, cheats, jaquette) par déplacement ; nouvelles routes `GET /trash`, `DELETE /trash`, `DELETE /trash/{entryId}` (en-tête `X-RL-Confirm: empty-trash`), seules à effacer définitivement. Specs 04, 05, 06, 07, 09 et prompt M4 mis à jour. Proposé par Claude (session de cadrage) ; considéré comme validé sauf objection de la personne.
- 2026-11-18 — M1 peut démarrer sans attendre M0 (aucune dépendance aux points « À VÉRIFIER M0 »). Proposé par Claude (session de cadrage) ; considéré comme validé sauf objection de la personne.

## Questions ouvertes pour la personne
1. Relevés M0 sur la console (voir le compte rendu de démarrage).
2. Installer et connecter la CLI Antigravity (`agy`) pour la revue croisée.
3. ~~Premier commit de `main`~~ : tranché (voir Décisions).
4. ~~Lancer M1 pendant l'attente de M0~~ : oui (voir Décisions).
5. ~~Écart `DELETE /games/{gameId}`~~ : tranché, tout va dans la corbeille (voir Décisions).

## Plus tard
- Créer un simulateur « iPhone 16 » (`xcrun simctl create`) avant M1/M3, ou adapter la commande de `CLAUDE.md` au runtime disponible.
