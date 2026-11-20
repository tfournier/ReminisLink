Verdict : à corriger

## Bloquant

- [DeviceStore.swift:96](/Users/tfournier/projets/github.com/tfournier/reminislink-m13-ios/ios/ReminisLinkKit/Sources/ReminisLinkKit/DeviceStore.swift:96) Le package Swift ne compile pas. Dans `(try? identityRecord())?.flatMap(\.name)`, `try?` conserve un seul niveau d’optionnel et `?.` cherche ensuite `flatMap` sur `IdentityRecord`, qui ne définit pas cette méthode. Toute compilation de `ReminisLinkKit` atteint cette ligne. Utiliser `(try? identityRecord())?.name`.
- [DeviceStore.swift:86](/Users/tfournier/projets/github.com/tfournier/reminislink-m13-ios/ios/ReminisLinkKit/Sources/ReminisLinkKit/DeviceStore.swift:86) Deux appareils du même compte Apple peuvent perdre l’accès à une console après la synchronisation du trousseau. Avant que leurs trousseaux convergent, A appaire la console X avec une identité et B appaire Y avec une autre. Les deux identités occupent le même compte `identity`, tandis que les fiches X et Y occupent des comptes distincts. Si l’identité de B l’emporte, l’app conserve X dans la liste et signe ses requêtes avec l’identité de B. X répond `401 unknown_client`, en écart à la promesse M13 d’un appairage partagé. Enregistrer l’identité effectivement vérifiée avec chaque appairage, conserver les clés encore référencées et prévoir une résolution explicite du conflit. Ajouter un test où deux trousseaux divergent, puis convergent.

## À corriger

- [AppModel.swift:541](/Users/tfournier/projets/github.com/tfournier/reminislink-m13-ios/ios/ReminisLink/App/AppModel.swift:541) Le repli sur l’adresse du QR permet de terminer l’appairage, mais l’ouverture immédiate de la console repart sur une résolution Bonjour seule. Si Bonjour reste indisponible, l’utilisateur termine l’appairage avec succès puis obtient « Console introuvable » alors que le pak répond toujours. Réutiliser l’adresse validée pour la première ouverture, avec la même empreinte SPKI.
- [ConsolesView.swift:43](/Users/tfournier/projets/github.com/tfournier/reminislink-m13-ios/ios/ReminisLink/Views/ConsolesView.swift:43) Un balayage complet peut effacer immédiatement la fiche d’une console RL1. Cette fiche est le repère conservé pour le réappairage après suppression de l’ancienne clé. Désactiver le balayage complet, demander confirmation et afficher une éventuelle erreur de suppression.

## Suggestions

- [TLSPinTests.swift:110](/Users/tfournier/projets/github.com/tfournier/reminislink-m13-ios/ios/ReminisLinkKit/Tests/ReminisLinkKitTests/TLSPinTests.swift:110) Le test de négociation TLS réelle est optionnel et couvre uniquement `/hello`. Étendre le test d’intégration au flux SSE, au téléchargement avec progression et à l’envoi de fichier. Pour chaque parcours, vérifier qu’une empreinte incorrecte empêche le pak de recevoir la requête HTTP.

## Tests lancés

- `scripts/sync-test-vectors.sh --check` — réussi. Les copies Swift correspondent aux vecteurs partagés.
- `git diff --check ea9836ca...HEAD` — réussi.
- `make lint test`, `make contract`, `swift test` et `xcodebuild test` — non lancés. Ces commandes créent des caches, des produits de build ou un conteneur, ce qui contrevient à la consigne de lecture seule. Le défaut de compilation ci-dessus est issu de la revue statique.

Aucun fichier modifié. Le dépôt ne contient pas de prompt M13 dédié. La revue s’est appuyée sur la section M13 de la feuille de route et le contrat OpenAPI. La description de la PR reste non vérifiée, faute de session GitHub authentifiée.