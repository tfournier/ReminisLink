# Revue M6 — Codex (ChatGPT, compte de la personne)

Date : 2026-11-19. Revue croisée en lecture seule (`codex exec -s read-only`, Codex CLI 0.170.0) sur `main` @ `5cb60ebf`, périmètre du jalon. Les tests n'ont pas pu tourner dans le bac à sable de Codex : constats par lecture du code. Chaque point est vérifié par l'agent principal et l'agent du jalon M8 avant correction.

Verdict : à corriger

Revue M6 sur `main` (`22f19dae`), à partir du diff `5c2fe917^1..cbeccf92`. Le checkout `5cb60ebf` ne diffère de `main` que dans `PROGRESS.md`. Les messages des deux fusions M6 sont disponibles localement. La description GitHub de la PR n’a pas pu être consultée, car `gh` exige une authentification.

## Bloquant

- [internal/transfer/patch.go:482](internal/transfer/patch.go) **Un vidage concurrent de la corbeille peut supprimer la seule copie de la ROM avant la fin du remplacement.** `Trash.Move` publie l’entrée et relâche son verrou avant l’installation du fichier patché. Un second client peut confirmer le vidage de la corbeille dans cet intervalle. Si le renommage échoue ensuite, `Undo` ne retrouve plus l’original. Le vidage appelle directement `Trash.Empty`, sans prendre le verrou d’installation. Protéger l’entrée contre la suppression et la purge jusqu’à la fin du remplacement ou de son annulation. [internal/server/transfers.go:102](internal/server/transfers.go)
- [internal/store/trash.go:340](internal/store/trash.go) **Le déplacement en corbeille ne garantit pas la persistance du chemin de la sauvegarde avant de retirer la ROM de son dossier.** L’opération crée l’entrée et ses sous-répertoires, puis synchronise les répertoires immédiats du renommage. Elle ne synchronise pas tous les parents qui référencent les répertoires fraîchement créés, et `SyncDir` ignore les erreurs. Lors d’une coupure, si le retrait de l’entrée côté `Roms` est persisté mais qu’un de ces liens parents ne l’est pas, la reprise peut installer le patch sans copie accessible de l’original. Synchroniser et vérifier la chaîne de répertoires, ou conserver l’original en place jusqu’à ce qu’une copie de sauvegarde durable soit confirmée. [internal/store/atomic.go:49](internal/store/atomic.go)
- [internal/patch/ips.go:114](internal/patch/ips.go) **Un enregistrement IPS peut écrire au-delà de la limite d’adresse de 16 Mio prescrite par M6.** Un enregistrement de deux octets au dernier offset sur 24 bits écrit son second octet à l’adresse `0x1000000`. Le test actuel exige explicitement ce résultat. Contrôler la fin de chaque enregistrement normal ou RLE et couvrir la borne exacte, tout en permettant la copie inchangée des octets d’une ROM source au-delà de cette limite. [internal/patch/patch_test.go:272](internal/patch/patch_test.go)

## À corriger

- [ios/ReminisLink/App/PatchFlow.swift:325](ios/ReminisLink/App/PatchFlow.swift) Deux interrogations peuvent faire avancer le même téléchargement. Après une erreur réseau, « Réessayer » peut se superposer à l’interrogation périodique. Si la première réponse réussie fait passer le flux à `.starting` et que le `POST /patch` est encore en vol, la seconde réponse peut faire passer le flux à `.failed(.unexpected)` en traitant à nouveau l’ancien job. L’écran peut alors autoriser une relance pendant que le premier remplacement est en cours. Après chaque `await`, vérifier que le job et l’étape correspondent toujours à ceux de l’interrogation.
- [internal/patch/ups.go:92](internal/patch/ups.go) L’annulation n’est vérifiée qu’entre les blocs UPS ou lors d’une écriture tamponnée. Dans un long bloc dont les octets restants sont au-delà de la taille cible, la boucle peut continuer à lire sans écrire ni consulter le contexte. Ajouter un contrôle périodique dans la boucle interne et un test d’annulation pendant cette phase.
- [internal/transfer/patch.go:482](internal/transfer/patch.go) Le traitement de l’erreur suppose que `Trash.Move` ne renvoie une erreur que si rien n’a bougé. Si le déplacement réussit et que l’écriture finale de `entry.json` échoue, `Move` renvoie pourtant une erreur avec la ROM déjà en corbeille. Le jeu disparaît de la bibliothèque jusqu’à la reprise au redémarrage. Conserver le marqueur et tenter la remise en place immédiatement dans ce cas. [internal/store/trash.go:367](internal/store/trash.go)

## Suggestions

- [internal/transfer/patch_test.go:453](internal/transfer/patch_test.go) Ajouter un cas de reprise où le déplacement en corbeille a abouti, mais où le marqueur a encore sa première version, sans `trashEntryId`. Le test actuel renseigne d’emblée cet identifiant dès que la ROM a été déplacée.

## Tests lancés

- `gofmt -l cmd internal protocol scripts` — aucun fichier signalé.
- `make lint test` — bloqué à `go vet` par les permissions du cache Go. `make test`, lancé séparément, a été bloqué à la création du répertoire temporaire Go.
- `make fuzz FUZZTIME=2s` — bloqué à la création du répertoire temporaire Go, avant le premier fuzzer.
- `make contract` — accès à la socket Docker refusé. `sh scripts/sync-test-vectors.sh --check` a réussi séparément et confirme que les copies Swift sont à jour.
- `swift test --scratch-path /tmp/reminislink-m6-review-swift` — le compilateur a renvoyé `permissionDenied` avant l’exécution des tests.
- `xcodebuild test -scheme ReminisLink -destination 'platform=iOS Simulator,name=iPhone 16' -derivedDataPath /tmp/reminislink-m6-review-xcode` — accès à CoreSimulator et écritures de build refusés avant l’exécution des tests.

Le smoke test headless requiert une écriture dans `build/` via `make headless`. Il reste à vérifier, ainsi que le lancement d’une ROM patchée dans NextUI sur console.