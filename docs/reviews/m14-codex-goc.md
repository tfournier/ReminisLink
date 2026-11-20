Verdict : à corriger

## Bloquant

- [internal/server/events.go:381](/Users/tfournier/projets/github.com/tfournier/reminislink-m14-go/internal/server/events.go:381) **Un appareil peut rester affiché comme connecté après sa déconnexion.** Un relevé détecte une baisse de l’espace libre alors que seul A est connecté. B se connecte avant que le flux de A construise son événement `device`. L’événement inclut donc B. La console déconnecte B avant le relevé suivant. Les deux relevés ne voient que A et, si la batterie et l’espace libre restent stables, aucun événement ne corrige la présence affichée par A. Marquer les flux survivants lors d’une révocation, en conservant la limite de cadence par flux. Ajouter un test où B apparaît dans un événement sans avoir figuré dans un relevé.
- [internal/server/events.go:189](/Users/tfournier/projets/github.com/tfournier/reminislink-m14-go/internal/server/events.go:189) **Deux événements `device` peuvent être écrits à moins de cinq secondes d’intervalle sur le même flux.** Le flux enregistre `lastDevice` à T+0, puis attend `access.mu` pendant qu’un oubli d’appareil synchronise le registre des clients sur la carte SD. Si le verrou est libéré à T+4 s, le premier événement est écrit à ce moment. Un nouveau changement détecté à T+5 s peut déclencher une deuxième écriture aussitôt. Mesurer l’intervalle à partir de l’écriture effective, par exemple après le `Flush` réussi. Ajouter un test qui retarde la construction du premier événement après le démarrage du compteur.

## Tests lancés

- `gofmt -l cmd internal protocol scripts` — aucun fichier signalé.
- `git diff --check` sur `HEAD~1...HEAD`, `m14-events...HEAD` et `main...HEAD` — réussi.
- `scripts/sync-test-vectors.sh --check` — les copies Swift correspondent aux vecteurs partagés.
- `make lint test` et `make contract` — non lancés conformément à la contrainte de lecture seule. Les suites iOS étaient hors périmètre du diff.

Contre-revue statique de `d039f8b1`. La feuille de route a servi de référence pour M14 en l’absence de prompt dédié. La description de la PR est restée inaccessible, car GitHub CLI demandait une authentification.