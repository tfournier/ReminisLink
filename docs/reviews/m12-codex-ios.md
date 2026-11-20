Verdict : à corriger

## Bloquant

- [PakManager.swift:208](/Users/tfournier/projets/github.com/tfournier/reminislink-m12-ios/ios/ReminisLink/App/PakManager.swift:208) **La confirmation peut autoriser le remplacement d’un autre pak.** Une URL sert d’abord une archive `Clock.pak`. La console renvoie un `409` et l’app demande « Remplacer Clock ? ». Si la même URL sert ensuite une archive `Files.pak`, le second téléchargement part avec `overwrite` et la console met `Files` à la corbeille sans confirmation pour ce pak. Le même scénario est possible si l’archive à l’emplacement SD sélectionné change avant la relance. Lier la confirmation côté console à l’archive préparée et à la destination du conflit, puis redemander une confirmation si l’une a changé.

## À corriger

- [PakManager.swift:290](/Users/tfournier/projets/github.com/tfournier/reminislink-m12-ios/ios/ReminisLink/App/PakManager.swift:290) **Une annulation confirmée peut laisser la copie sur l’iPhone.** Ajouter une archive depuis l’onglet Transferts, annuler l’envoi, puis effacer les transferts terminés sans ouvrir Apps. La file ne possède pas la copie et le nettoyage du gestionnaire attend un chargement de la liste Apps. L’archive reste sur disque jusqu’à l’ouverture de cet écran ou au redémarrage de l’app. Déclencher le nettoyage dès que la file confirme l’annulation et couvrir ce parcours dans un test.
- [PakManager.swift:134](/Users/tfournier/projets/github.com/tfournier/reminislink-m12-ios/ios/ReminisLink/App/PakManager.swift:134) **Le premier chargement peut manquer un événement `paks`.** Si la console a déjà produit la réponse au premier `GET /paks`, puis qu’un autre client installe un pak et que l’événement arrive avant la réponse HTTP, `paks` et `loadError` valent encore `nil`. L’événement est ignoré et l’ancienne liste s’affiche jusqu’au prochain rafraîchissement. Relancer la lecture lorsqu’un événement arrive pendant le premier chargement et tester cet ordre de livraison.
- [FakePak.swift:1330](/Users/tfournier/projets/github.com/tfournier/reminislink-m12-ios/ios/ReminisLinkKit/Sources/ReminisLinkDemo/FakePak.swift:1330) **FakePak peut installer par lien une archive de plus de 1 Gio.** Avec `downloadSize` réglé à 1 Gio + 1 octet et un nom valide, la simulation termine l’installation alors que le pak Go fait échouer la tâche avec `too_large`. Appliquer la limite à la tâche de téléchargement simulée et vérifier qu’aucun pak n’est ajouté.

## Tests lancés

- `git diff --check 99f9080a...HEAD` a réussi avec le binaire Git direct. Les premiers appels via le lanceur système ont rencontré un refus de création du cache Xcode.
- Le catalogue `Localizable.xcstrings` est valide selon le parseur JSON Python. `plutil -lint` a rejeté ce fichier JSON et son résultat ne constitue pas un échec du catalogue.
- `make lint test`, `make contract`, `swift test` et `xcodebuild test` n’ont pas été lancés, car ils écrivent des caches ou des artefacts de compilation.

La revue porte sur `99f9080a...HEAD`, la base iOS demandée. Le dépôt ne contient pas de prompt M12 dans `prompts/`. La description de la PR n’a pas pu être lue, car `gh pr view` exige une authentification dans cet environnement.