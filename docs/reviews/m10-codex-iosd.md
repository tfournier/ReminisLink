Verdict : à corriger

## Bloquant

- [ResumableUpload.swift:303](/Users/tfournier/projets/github.com/tfournier/reminislink-m10-ios/ios/ReminisLinkKit/Sources/ReminisLinkKit/Transfers/ResumableUpload.swift:303) Une coupure réseau est traitée comme une annulation confirmée. Pendant un envoi, l’utilisateur touche « Annuler » alors que le Wi-Fi vient de tomber. Le `DELETE` n’atteint pas le pak. Avant `complete`, l’erreur réseau est convertie en `.cancelled`. La file efface alors sa copie de travail et la feuille Patch autorise la fermeture, qui efface la sienne. Au retour du réseau, l’envoi partiel peut toujours exister sur le pak, mais l’app a perdu sa possibilité de reprise. Distinguer une annulation confirmée d’un résultat indéterminé, conserver l’identifiant et la copie locale dans ce dernier cas, puis permettre de réessayer l’annulation ou de vérifier l’état distant. [TransferQueue.swift:290](/Users/tfournier/projets/github.com/tfournier/reminislink-m10-ios/ios/ReminisLinkKit/Sources/ReminisLinkKit/Transfers/TransferQueue.swift:290) · [PatchFlow.swift:515](/Users/tfournier/projets/github.com/tfournier/reminislink-m10-ios/ios/ReminisLink/App/PatchFlow.swift:515)
- [FakeHTTP.swift:66](/Users/tfournier/projets/github.com/tfournier/reminislink-m10-ios/ios/ReminisLinkKit/Sources/ReminisLinkDemo/FakeHTTP.swift:66) La compilation des tests Swift en mode Debug rencontre deux conversions de closures `() -> Void` vers le paramètre `@Sendable` de `DispatchQueue.async`. `swift test` compile la cible `ReminisLinkDemo` sous Swift 6.2 avec les avertissements traités comme des erreurs. Déclarer les actions stockées, les actions extraites et le paramètre de `hold` en `@Sendable`. Constat statique, compilation non exécutée. [FakeHTTP.swift:75](/Users/tfournier/projets/github.com/tfournier/reminislink-m10-ios/ios/ReminisLinkKit/Sources/ReminisLinkDemo/FakeHTTP.swift:75) · [Package.swift](/Users/tfournier/projets/github.com/tfournier/reminislink-m10-ios/ios/ReminisLinkKit/Package.swift)

## À corriger

- [TransferQueue.swift:248](/Users/tfournier/projets/github.com/tfournier/reminislink-m10-ios/ios/ReminisLinkKit/Sources/ReminisLinkKit/Transfers/TransferQueue.swift:248) Après un `complete` dont la réponse a été perdue, l’annulation conserve l’état `.paused` ou `.failed` pendant le `DELETE`. L’utilisateur peut donc encore toucher « Reprendre » pendant cette attente. Utiliser aussi `.cancelling` dans ce chemin pour bloquer la reprise manuelle et automatique jusqu’au résultat du `DELETE`.

Les deux scénarios du tour précédent sont corrigés à la lecture. Pendant le `DELETE`, `.cancellingUpload` bloque la fermeture de Patch et protège sa copie locale. `.cancelling` empêche l’effacement de l’envoi par « Effacer les transferts terminés ». Les nouveaux tests maintiennent la réponse `423` en attente pour vérifier ces protections, puis vérifient la reprise. Les tests de sérialisation du bail attendent désormais un signal du faux transport avant de déclencher le passage en arrière-plan.

**Tests lancés**

- `git diff --check` sur `HEAD~1...HEAD`, `m10-writer...HEAD` et `main...HEAD` — réussi.
- `gofmt -l cmd internal protocol scripts` — aucun fichier signalé.
- `scripts/sync-test-vectors.sh --check` — réussi.
- `make lint test`, `make contract`, `swift test` et `xcodebuild test` — non lancés, car ces commandes peuvent écrire des fichiers ou des caches.

Contre-revue ciblée sur `a0f929e4`. Aucun prompt M10 trouvé dans `prompts/`. La description de la PR reste indisponible, car GitHub CLI n’est pas authentifié. Aucun fichier modifié, aucun commit créé.