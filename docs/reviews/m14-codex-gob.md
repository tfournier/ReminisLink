Verdict : à corriger

## Bloquant

- [internal/events/hub.go:296](/Users/tfournier/projets/github.com/tfournier/reminislink-m14-go/internal/events/hub.go:296) **Un désappairage peut laisser le flux écrire pendant l’arrêt.** `DELETE /clients/me` est accepté juste avant l’arrêt, puis le pak lance `Shutdown`, qui retire le flux de `h.subs`. Le désappairage reprend et renvoie `204`, mais `CloseClient` ne retrouve plus le flux. Celui-ci peut ensuite écrire les événements en file et `shutdown`. Garder les flux en cours de fermeture accessibles à la révocation jusqu’à la sortie du handler et faire primer la révocation sur l’envoi final.
- [internal/server/events.go:184](/Users/tfournier/projets/github.com/tfournier/reminislink-m14-go/internal/server/events.go:184) **Un relevé `device` déjà en file peut réafficher un appareil révoqué.** Le serveur met en file pour A un relevé qui contient B, pendant que le flux de A écrit un événement précédent. La console déconnecte B, puis un `GET /device` sur une autre connexion de A confirme son retrait. Quand le flux reprend, il envoie le relevé périmé. Le verrou protège la mise en file, mais le relevé reste livrable après la révocation. Invalider les relevés de présence en attente et vérifier leur génération avant l’écriture.
- [internal/server/events.go:349](/Users/tfournier/projets/github.com/tfournier/reminislink-m14-go/internal/server/events.go:349) **Deux échantillonneurs peuvent publier à moins de cinq secondes d’intervalle.** Une lecture de stockage tarde pendant que le dernier flux se ferme. Un nouveau flux ouvre et lance un autre échantillonneur. Si celui-ci publie un changement juste avant que l’ancienne lecture se termine, l’ancien échantillonneur peut publier à son tour sur le nouveau flux. Vérifier que l’échantillonneur est encore actif au moment de publier et appliquer la limite de cadence au niveau du hub.

## À corriger

- [internal/server/events_test.go:781](/Users/tfournier/projets/github.com/tfournier/reminislink-m14-go/internal/server/events_test.go:781) Le nouveau test de révocation peut expirer avant d’exercer la révocation. Si le premier relevé lit l’espace libre après `f.storage.Add`, il prend la valeur modifiée comme référence et le premier événement attendu peut ne jamais arriver. Synchroniser la fin du relevé initial avant de modifier le stockage.

## Tests lancés

- `gofmt -l cmd internal protocol scripts` — aucun fichier signalé.
- `git diff --check main...HEAD` et `git diff --check b3aede4b...HEAD` — réussis.
- `scripts/sync-test-vectors.sh --check` — les copies Swift correspondent aux vecteurs partagés.
- `make lint test` et `make contract` — non lancés en raison des écritures qu’entraînent Go et Docker. Les suites iOS étaient hors périmètre du diff.

Contre-revue statique de `b3aede4b...HEAD`, avec `m14-events...HEAD` et `main...HEAD` pour le contexte. La feuille de route a servi de référence pour M14 en l’absence de prompt dédié. La description de la PR est restée inaccessible.