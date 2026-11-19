Verdict : à corriger

## Bloquant

- [internal/server/games.go:100] Le délai d’écriture est retiré avant que `net/http` ait vidé son tampon de réponse. Si les derniers octets d’une ROM tiennent dans ce tampon et que le client cesse de lire alors que le tampon TCP est plein, `io.CopyN` peut réussir, puis le vidage final se bloquer au-delà des 60 secondes prévues. Conserver l’échéance jusqu’au vidage final et ajouter un test où le client se bloque sur un dernier morceau court.
- [internal/osadapter/nextui/nextui.go:661] Une déconnexion pendant l’indexation des playlists laisse le pak lire toutes les `.m3u` du dossier. Sur un système qui contient des centaines de playlists, `m3uSiblings` les ouvre toutes avant le premier contrôle du contexte dans la boucle. Le test d’annulation intervient après cette étape. Propager le contexte à ce prétraitement, puis vérifier aussi l’annulation pendant l’enrichissement des jeux, qui lit leurs fichiers de cheats.

## À corriger

- [internal/library/library.go:428] `GET /games` lit encore deux fois la racine de chaque système. `Systems()` en lit les premières entrées pour vérifier la présence d’un élément visible, puis `WalkSummaries` relit le dossier en entier. Le test actuel appelle `WalkSummaries` directement et ne compte donc pas la première lecture. Réutiliser le relevé de la racine entre la découverte et le parcours, puis vérifier le nombre de lectures depuis la route complète.
- [internal/server/files_test.go:458] Le test du lecteur lent mesure le temps de lecture côté client. Si les tampons TCP acceptent toute la réponse d’emblée, le client met quand même plus de trois secondes à la lire et le test réussit sans vérifier que le serveur renouvelle son échéance. Imposer une contre-pression côté serveur ou contrôler les échéances avec un transport instrumenté.

La règle de profondeur commune couvre désormais les jeux-dossiers à la limite. Le cache mutualise les lectures des sous-dossiers référencés par plusieurs playlists. Le tas conserve au plus le nombre de places restantes plus un candidat, dans l’ordre contractuel. Le validateur applique de nouveau les contraintes `additionalProperties` explicites dans `allOf`.

**Tests lancés** — `git diff --check` a réussi sur les trois plages (`main...HEAD`, `m11-ergonomie...HEAD` et `7df6a8bc…HEAD`). `gofmt -l cmd internal protocol scripts` n’a signalé aucun fichier. `scripts/sync-test-vectors.sh --check` a confirmé que les copies Swift sont à jour. `make lint test` et `make contract` ont été omis conformément à la consigne de lecture seule. Le diff ne touche aucun fichier iOS, donc les suites Swift et Xcode ont été omises.

Aucun prompt M11 n’était présent dans `prompts/`. GitHub CLI exigeait une authentification pour accéder à la description de la PR. La revue s’appuie sur le prompt fourni, la feuille de route, les specs et les messages de commit. Aucun fichier du dépôt n’a été modifié.