Verdict : à corriger

## Bloquant

- [PatchFlow.swift:448](/Users/tfournier/projets/github.com/tfournier/reminislink-m10-ios/ios/ReminisLink/App/PatchFlow.swift:448) La feuille Patch peut encore supprimer la copie locale avant de recevoir le refus d’annulation. Pendant un envoi, un autre iPhone prend la main. Avant que le premier iPhone apprenne le changement de bail, l’utilisateur touche « Annuler ». Le flux passe immédiatement à `.cancelled`, ce qui permet de fermer la feuille pendant que le `DELETE` est en vol. La fermeture supprime la copie locale. Quand le `423` arrive, le flux tente de reprendre l’envoi avec un fichier qui a disparu. Garder la feuille et la copie protégées jusqu’à la réponse, puis remettre l’envoi en pause si le pak refuse. [PatchView.swift:63](/Users/tfournier/projets/github.com/tfournier/reminislink-m10-ios/ios/ReminisLink/Views/PatchView.swift:63) · [PatchFlow.swift:532](/Users/tfournier/projets/github.com/tfournier/reminislink-m10-ios/ios/ReminisLink/App/PatchFlow.swift:532)
- [TransferQueue.swift:271](/Users/tfournier/projets/github.com/tfournier/reminislink-m10-ios/ios/ReminisLinkKit/Sources/ReminisLinkKit/Transfers/TransferQueue.swift:271) Un envoi peut disparaître de la file pendant une annulation que le pak finit par refuser. Si l’autre iPhone vient de prendre la main, l’utilisateur peut demander l’annulation puis toucher le bouton qui efface les transferts terminés avant le retour du `423`. L’état provisoire `.cancelled` rend l’envoi effaçable. Le refus le remet ensuite en pause, mais l’envoi a déjà été retiré de la file que parcourt la reprise automatique. Garder l’envoi dans un état non terminal pendant le `DELETE` et ne le rendre effaçable qu’après confirmation. [TransfersView.swift:53](/Users/tfournier/projets/github.com/tfournier/reminislink-m10-ios/ios/ReminisLink/Views/TransfersView.swift:53) · [TransferQueue.swift:350](/Users/tfournier/projets/github.com/tfournier/reminislink-m10-ios/ios/ReminisLinkKit/Sources/ReminisLinkKit/Transfers/TransferQueue.swift:350)

## À corriger

- [FakeHTTP.swift:90](/Users/tfournier/projets/github.com/tfournier/reminislink-m10-ios/ios/ReminisLinkKit/Sources/ReminisLinkDemo/FakeHTTP.swift:90) La garantie OpenAPI « `423` avant lecture du corps » reste sans couverture dans ce correctif. Le faux transport lit toujours le corps avant d’appeler le faux pak. La vérifier côté Go avec un corps instrumenté et une assertion qu’aucune lecture n’a eu lieu avant le refus.

## Suggestions

- Remplacer les temporisations fixes des nouveaux tests de bail par un signal du faux transport qui confirme que le `PUT` attend encore d’être traité. Déclencher l’arrière-plan à ce moment rendrait le scénario reproductible. Pour le patch, ajouter un test qui tente de fermer la feuille pendant le `DELETE`. Pour la file, tester l’effacement des transferts terminés dans le même intervalle.

À la lecture, la sérialisation corrige l’inversion `PUT`/`DELETE` signalée pour le renouvellement et la prise de main forcée lorsque le pak répond au `PUT`. Les boutons d’annulation des tâches de patch et « Réessayer » sont désactivés en lecture seule. Les deux branches d’annulation de `TransferQueue`, y compris après l’envoi de `complete`, appellent désormais le mécanisme commun de mise en pause. La reprise peut donc être déclenchée si le bail est déjà revenu, tant que l’envoi figure encore dans la file.

## Tests lancés

- `git diff --check` sur `HEAD~1...HEAD`, `m10-writer...HEAD` et `main...HEAD` — réussi.
- `gofmt -l cmd internal protocol scripts` — aucun fichier signalé.
- `scripts/sync-test-vectors.sh --check` — réussi.
- `make lint test`, `make contract`, `swift test` et `xcodebuild test` — non lancés, car ils écrivent des fichiers ou des caches. Les scénarios bloquants sont issus de la lecture du code.

Contre-revue centrée sur `95eb074b`. Aucun prompt M10 trouvé dans `prompts/`. La description de la PR n’a pas pu être consultée, car GitHub CLI n’est pas authentifié. Au premier appel, le lanceur Git système a tenté de créer un cache temporaire. Le sandbox a refusé l’écriture et les appels suivants ont utilisé directement le binaire Git de Xcode. Aucun fichier modifié, aucun commit créé.