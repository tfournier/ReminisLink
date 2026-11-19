# Revue M4 — Codex (ChatGPT, compte de la personne)

Date : 2026-11-19. Revue croisée en lecture seule (`codex exec -s read-only`, Codex CLI 0.170.0) sur `main` @ `5cb60ebf`, périmètre du jalon. Les tests n'ont pas pu tourner dans le bac à sable de Codex : constats par lecture du code. Chaque point est vérifié par l'agent principal et l'agent du jalon M8 avant correction.

Verdict : à corriger

Revue du périmètre M4 sur le code actuel à `5cb60ebf`. `git diff main...HEAD` est vide. J’ai utilisé la plage historique `8f48b6e5^1..fcbe2223` pour identifier les fichiers du jalon et les messages des deux fusions pour situer les changements. La description de PR n’était pas disponible dans les éléments consultés.

## Bloquant

- `internal/store/credit.go:87` — La borne de sept jours s’applique à chaque démarrage. Si la console enregistre à chaque fermeture une heure en retard, puis redémarre avec une heure corrigée de plus de sept jours, cinq cycles rapprochés créditent 35 jours en quelques minutes. `internal/store/trash.go:752` efface alors définitivement une entrée créée au début de ces cycles, malgré les 30 jours annoncés. La formule suit la spec, mais la garantie de rétention ne tient pas dans ce scénario. Suspendre le crédit hors session quand la continuité de l’horloge est douteuse et tester plusieurs corrections successives.
- `internal/transfer/service.go:486` et `internal/states/states.go:885` — Une coupure après le déplacement de l’ancienne ROM ou de l’ancien state, avant le renommage du fichier reçu, laisse l’emplacement vide. Au redémarrage, `internal/store/trash.go:653` ignore l’entrée déjà marquée `done`. L’ancien fichier reste dans la corbeille, où la purge pourra l’effacer même si le remplacement n’a jamais abouti. Journaliser le remplacement avant le premier déplacement et, au démarrage, terminer l’installation ou remettre l’original en place.
- `internal/library/write.go:93` — Supprimer un jeu `.m3u` déplace tous les disques résolus par sa playlist sans vérifier s’ils sont encore utilisés. Deux playlists du même système peuvent référencer le même disque. Supprimer la première laisse la seconde visible mais injouable, puis la purge efface le disque partagé. Conserver les disques encore référencés et ajouter un test avec deux playlists partageant un disque.
- `internal/transfer/download.go:329` — La reprise assemble les octets même sans validateur de la ressource. Si une URL sans `ETag` ni `Last-Modified` change de contenu après une coupure, une réponse `206` avec le bon offset et la même taille permet d’installer un fichier composé de deux versions. Avec le SHA-256 optionnel absent, la tâche réussit. Repartir de zéro sans validateur fiable ni SHA-256 attendu, et tester un changement de contenu entre deux requêtes.

## À corriger

- `ios/ReminisLink/Views/TransfersView.swift:127` — Après une réponse de `complete` perdue, « Renvoyer » promet de garder les deux copies. Depuis M5, une save de plus de 64 Mio peut passer par cette file (`ios/ReminisLink/App/ConsoleSession+GameData.swift:20`). Pour la cible `save`, le pak ignore `rename` et remplace la save active, en sauvegardant la précédente. Si le joueur a repris sa partie entre-temps, le nouvel essai remplace sa progression active. Adapter le message et demander une confirmation de remplacement pour cette cible.
- `ios/ReminisLink/Views/DeleteGameView.swift:32` — Avec « Garder la save et les states », la feuille affiche encore le fichier RTC parmi les éléments à déplacer et annonce la suppression des verrous et de l’historique. Le serveur conserve ces éléments avec `keepSave=true`. Dans l’autre sens, la liste omet les fichiers annexes des states que le serveur déplace. Alimenter la confirmation avec la même sélection que la suppression côté pak.
- `internal/transfer/download.go:271` — Une annulation reçue pendant que le téléchargement attend le verrou d’installation peut tout de même aboutir à un remplacement. Vérifier le contexte juste après l’acquisition du verrou, comme le fait déjà le chemin d’installation des patchs.

## Tests lancés

- `make -k lint test` — Le contrôle `gofmt` a atteint l’étape suivante. `go vet` et `go test` ont été bloqués par les permissions du cache et du répertoire temporaire Go.
- `make contract` — Accès au socket Docker refusé avant la validation OpenAPI et la vérification des vecteurs Go.
- `scripts/sync-test-vectors.sh --check` — Réussi. Les copies Swift des vecteurs sont à jour.
- `swift test --scratch-path /tmp/reminislink-m4-swift` — Bloqué par `permissionDenied` lors de l’interrogation du compilateur.
- `xcodebuild test` sur iPhone 16, avec `DerivedData` sous `/tmp` — Bloqué à la résolution des dépendances par les permissions d’écriture. CoreSimulator était également inaccessible.

Les scénarios du rapport ont été vérifiés par lecture du code. Le parcours headless avec coupure et reprise n’a pas été exécuté. Aucun fichier du dépôt n’a été modifié.