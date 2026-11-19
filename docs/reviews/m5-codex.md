# Revue M5 — Codex (ChatGPT, compte de la personne)

Date : 2026-11-19. Revue croisée en lecture seule (`codex exec -s read-only`, Codex CLI 0.170.0) sur `main` @ `5cb60ebf`, périmètre du jalon. Les tests n'ont pas pu tourner dans le bac à sable de Codex : constats par lecture du code. Chaque point est vérifié par l'agent principal et l'agent du jalon M8 avant correction.

Verdict : **à corriger**

Revue du périmètre `3d0d9c7c^1..5d657ffe` dans son état à `5cb60ebf`, identique au sommet de `main`. `git diff main...HEAD` est vide. Le prompt M5 et les messages de fusion ont servi à cadrer la revue. La description de la PR n’a pas pu être consultée, faute d’accès GitHub authentifié.

## Bloquant

- [internal/saves/saves.go:596](internal/saves/saves.go) **Une restauration peut écraser un `.rtc` sans en garder de copie.** Si le `.sav` a été déplacé hors de la carte et que son `.rtc` est toujours présent, restaurer une copie contenant un `.rtc` saute la sauvegarde `before-restore`, puis remplace l’horloge existante. Sauvegarder ou mettre à la corbeille le `.rtc` indépendamment de la présence du `.sav`.
- [internal/states/states.go:820](internal/states/states.go) **Une coupure peut laisser un state multi-disques restauré sans son fichier de disque.** Le state et le `.txt` actuels partent à la corbeille, puis le state verrouillé est installé avant son `.txt`. Si la console s’éteint entre les deux renommages, la maintenance suivante voit que les octets du state correspondent au verrou et passe au suivant. Même la restauration manuelle s’arrête sur cette égalité. Journaliser la restauration de l’ensemble et la reprendre au démarrage, ou vérifier et réparer les fichiers annexes avant de déclarer le verrou intact.
- [internal/states/states.go:1005](internal/states/states.go) **L’historique peut écraser la seule copie d’un state de reprise.** Si le `.st9` courant a le même horodatage et la même taille qu’une entrée archivée, avec des octets différents, l’archivage s’arrête avant le SHA-256. Restaurer ensuite une entrée écrase le `.st9` courant alors qu’aucune copie de cette version n’existe. Avant une restauration, vérifier le contenu par SHA-256 et archiver la version courante si elle est distincte.
- [internal/osadapter/nextui/nextui.go:632](internal/osadapter/nextui/nextui.go) **Un cœur actif non reconnu peut faire restaurer un verrou dans le dossier de l’ancien cœur.** Si un pak personnalisé sélectionne son cœur via une variable shell, l’analyse de `launch.sh` peut échouer. Avec un seul dossier de states encore présent, celui de l’ancien cœur, l’adaptateur l’utilise comme destination. La maintenance peut alors remplacer un fichier de ce dossier et signaler `restored`, alors que NextUI utilise un autre cœur. Si le pak actif existe mais que son cœur ne peut pas être déterminé, suspendre les écritures et les restaurations pour ce tag.
- [internal/cheats/service.go:299](internal/cheats/service.go) **Un import accepté peut produire un `.cht` que l’API refuse ensuite de lire.** Deux fichiers d’environ 4,5 Mio contenant des cheats valides et peu nombreux passent chacun la limite d’import. Leur fusion peut dépasser les 8 Mio autorisés à la lecture. L’import répond alors avec succès, puis `GET`, `PUT` et les imports suivants échouent en `413`. Vérifier la taille sérialisée avant toute modification du `.cht` ou de son `.bak`.

## À corriger

- [internal/cheats/cht.go:78](internal/cheats/cht.go) **L’analyseur accepte silencieusement un fichier partiel.** Dans un `.cht` de moins de 5 Mio, une ligne de plus de 4 Mio arrête le scanner. Si des cheats valides la précèdent, un import `replace` peut réussir avec ce seul préfixe. Propager l’erreur du scanner et refuser l’import avant l’écriture.
- [internal/osadapter/nextui/nextui.go:532](internal/osadapter/nextui/nextui.go) **La variante NextUI `<Jeu>/<Jeu>.cue` n’est pas reconnue comme jeu au niveau du dossier.** NextUI lance cette `.cue` directement, alors que l’adaptateur réserve ce traitement aux `.m3u`. Reconnaître la variante `.cue` avec le même ordre de résolution que NextUI et tester l’association à ses saves et states. [Analyse M0](docs/specs/m0/source-analysis.md)
- [internal/app/app.go:298](internal/app/app.go) **Le délai de maintenance ne borne pas une opération déjà commencée.** Sur une carte lente, l’inventaire complet ou la lecture d’un gros state peut dépasser le délai avant que le contexte soit consulté. Propager l’annulation dans le parcours et les lectures longues, en conservant un point de reprise pour les écritures déjà engagées.

## Suggestions

- Ajouter des tests de régression pour une restauration avec `.rtc` seul, une interruption entre les renommages du state et de son `.txt`, et deux `.st9` de taille et date identiques au contenu différent.

## Tests lancés

- `make lint test` — arrêt dans `go vet`, accès au cache Go refusé. `make test` lancé séparément a échoué à la création du répertoire de travail Go.
- `make contract` — accès au socket Docker refusé. `scripts/sync-test-vectors.sh --check` a réussi séparément et confirme la synchronisation des copies Swift.
- `swift test --package-path ios/ReminisLinkKit --scratch-path /tmp/reminislink-m5-swift` — arrêté avant les tests, avec `permissionDenied` dans le compilateur Swift.
- `xcodebuild test` avec les répertoires de travail sous `/tmp` — arrêté avant les tests, avec CoreSimulator indisponible et écriture du cache des dépendances refusée. Le parcours contre le pak headless reste à revérifier.

## Vérifications sur console à effectuer

- **Scénario 7** — créer une copie de save, modifier la progression en jeu, restaurer la copie et vérifier en jeu que la progression attendue est revenue.
- **Scénario 8** — verrouiller un state, l’écraser en jeu, rouvrir le pak et vérifier la restauration, le badge « Restauré » dans l’app et la présence de la version écrasée dans la corbeille. [Checklist console](docs/specs/08-tests-qualite.md)