Verdict : à corriger

## Bloquant

- [internal/osadapter/nextui/nextui.go:663] Un dossier de système illisible fait échouer toute la bibliothèque à plat. En mode desktop, si `Roms/` reste lisible mais que le compte du pak perd l’accès à `Roms/PlayStation (PS)`, `GET /systems` écarte ce dossier et renvoie les autres systèmes. `GET /games` le retient via `SystemFolders`, reçoit `EACCES` au parcours et répond `500`, même si les autres systèmes sont lisibles. Avant `42b98d14`, les deux routes écartaient ce dossier. Appliquer le même filtrage à partir de l’unique lecture de chaque racine et ajouter un test qui injecte `fs.ErrPermission` pour un système parmi plusieurs.

## À corriger

- [internal/server/files_test.go:475] Le test instrumenté vérifie le renouvellement des échéances, mais il peut échouer si le processus est suspendu 25 ms entre `SetWriteDeadline` et `Write` ou `FlushError`. Vérifier la séquence `deadline → write` pour chaque morceau, puis `deadline → flush → clear`. Le test peut ainsi détecter une échéance manquante sans dépendre de l’ordonnancement.

## Suggestions

- [internal/osadapter/nextui/walk_test.go:197] Le test de lecture unique couvre la découverte et le parcours via `AllGames`. Ajouter un cas au niveau HTTP qui vérifie que `GET /games` conserve les jeux des systèmes lisibles lorsqu’une autre racine est inaccessible.

Les corrections du vidage final et de l’annulation entre deux lectures successives de playlists ou de cheats sont confirmées par la lecture du code. Sur les racines lisibles, `AllGames` évite bien la lecture préalable de visibilité.

**Tests lancés** — `git diff --check` a réussi sur `main...HEAD`, `m11-ergonomie...HEAD` et `42b98d14~1...HEAD`. `gofmt -l cmd internal protocol scripts` n’a signalé aucun fichier. `scripts/sync-test-vectors.sh --check` a confirmé que les copies Swift sont à jour. `make lint test` et `make contract` ont été omis car ils écrivent. Le diff ne touche aucun fichier iOS, donc les suites Swift et Xcode ont été omises.

Aucun prompt M11 n’était présent dans `prompts/`. La description de la PR est restée inaccessible faute d’authentification GitHub CLI. Le lanceur Git système a tenté de créer un cache temporaire que le bac à sable a refusé. Les vérifications ont ensuite utilisé le binaire Git directement. Aucun fichier du dépôt n’a été modifié.