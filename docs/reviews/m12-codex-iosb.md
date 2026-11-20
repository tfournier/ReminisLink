Verdict : à corriger

### À corriger

- [PakManager.swift:280](/Users/tfournier/projets/github.com/tfournier/reminislink-m12-ios/ios/ReminisLink/App/PakManager.swift:280) **L’avertissement peut manquer après une installation réussie.** Le succès lance un `GET /paks`, puis un événement `paks` lance une seconde lecture. Si la première réponse arrive après le départ de la seconde, `LoadGeneration` l’écarte. Si `GET /trash` se termine avant la seconde lecture, `checkReplacement` consulte encore l’ancienne liste et ne trouve pas le `pakId` installé. La seconde réponse met ensuite la liste à jour sans relancer la vérification. Faire dépendre l’avertissement d’une liste fraîche effectivement reçue et couvrir cet ordre de livraison.
- [PakManager.swift:317](/Users/tfournier/projets/github.com/tfournier/reminislink-m12-ios/ios/ReminisLink/App/PakManager.swift:317) **Une ancienne entrée de corbeille peut faire annoncer un remplacement qui n’a pas eu lieu.** Remplacer `Files` une première fois, supprimer ensuite sa version installée, puis installer de nouveau `Files` lors d’une relance confirmée pour `Clock`. L’ancienne entrée `replace-pak` de `Files` suffit à afficher « Files a été remplacé », alors que la dernière tentative a installé `Files` dans un emplacement libre. N’affirmer le remplacement que si le déplacement en corbeille est attribuable à cette tentative. Sinon, indiquer quel pak a été installé.

### Suggestions

- [PaksAppTests.swift:251](/Users/tfournier/projets/github.com/tfournier/reminislink-m12-ios/ios/ReminisLinkTests/PaksAppTests.swift:251) Le test d’annulation bloque le `POST /uploads` avant la création de l’envoi côté console. Ajouter un cas qui attend l’identifiant d’envoi et retient la réponse au `DELETE`, puis vérifier que la copie existe avant la confirmation et disparaît dès sa réception, sans ouvrir Apps.

La lecture du code confirme les trois corrections ciblées. Le gestionnaire nettoie la copie au passage à `.cancelled`, un événement pendant le premier chargement peut déclencher une lecture plus récente, et `FakePak` renvoie `too_large` au-delà de 1 Gio avant d’installer le pak. Aucun point bloquant relevé.

### Tests lancés

- `git diff --check` sur `main...HEAD`, `99f9080a...HEAD` et `HEAD~1...HEAD` a réussi. Le catalogue `Localizable.xcstrings` a été lu avec succès par le parseur JSON Python.
- `make lint test`, `make contract`, `swift test` et `xcodebuild test` n’ont pas été exécutés, conformément à la consigne de lecture seule. Le lanceur système de Git a tenté de créer un cache Xcode et le sandbox a refusé l’écriture. Les vérifications ont été reprises avec le binaire Git direct.

La contre-revue porte sur le commit `ea905db1` et les parcours iOS concernés depuis `99f9080a`. Aucun prompt M12 n’est présent dans `prompts/`. La description de la PR reste inaccessible, car `gh pr view` exige une authentification.