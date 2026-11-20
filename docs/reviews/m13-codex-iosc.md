Verdict : à corriger

## Bloquant

Aucun blocage confirmé dans le correctif `dedac6e1`. Les deux scénarios du tour précédent sont couverts par des tests de régression. Une clé arrivée avant sa fiche survit à un autre appairage, et la confirmation d’oubli RL1 retire uniquement l’entrée locale même si l’appairage M13 est arrivé entre-temps.

## À corriger

- [TLSPinTests.swift:178](/Users/tfournier/projets/github.com/tfournier/reminislink-m13-ios/ios/ReminisLinkKit/Tests/ReminisLinkKitTests/TLSPinTests.swift:178) Le témoin HTTP dépend encore du journal échantillonné. Si le budget de journalisation est épuisé au moment du contrôle, le pak renvoie bien `401` à l’identité générée pour le test, mais le marqueur peut être omis. Le test échoue alors après cinq secondes malgré un épinglage correct. À l’inverse, voir le marqueur du contrôle ne garantit pas que les requêtes précédentes auraient été journalisées. De plus, `openEvents()` et les appels à `/hello` ne portent aucun marqueur recherché. Utiliser un témoin de test qui observe chaque requête HTTP avant l’authentification et l’échantillonnage, avec un identifiant distinct pour chaque essai, y compris SSE et `/hello`. [Journalisation HTTP du pak](/Users/tfournier/projets/github.com/tfournier/reminislink-m13-ios/internal/server/middleware.go:98)

## Tests lancés

- `scripts/sync-test-vectors.sh --check` — réussi. Les copies Swift correspondent aux vecteurs partagés.
- `git diff --check` sur `main...HEAD`, `ea9836ca...HEAD` et `HEAD~1...HEAD` — réussi.
- `make lint test`, `make contract`, `swift test`, `xcodebuild test` et le test TLS contre un pak — non lancés, car ils écrivent sur disque.

La contre-revue a ciblé `dedac6e1` et les parcours iOS concernés. En l’absence de prompt M13 dédié, je me suis appuyé sur la feuille de route, les spécifications et le contrat local. La description de la PR reste non vérifiée. Le premier appel à `git` a déclenché une tentative de création d’un cache Xcode, refusée par le bac à sable. Les vérifications suivantes ont utilisé directement le binaire Git des outils en ligne de commande. Aucun fichier modifié et aucun commit créé.