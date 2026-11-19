Verdict : à corriger

## Bloquant

- Une mise à jour depuis M10 écrase le nom choisi sur l’iPhone. Appairer une console sous M10, saisir « Ma Brick » dans l’écran de fin d’appairage, puis installer M11 et ouvrir la console sans l’avoir renommée sur le pak. L’entrée existante n’a pas de `consoleName`. Le premier `/device` est donc traité comme un renommage et remplace « Ma Brick » par le nom du pak, contrairement à la spec. Au premier relevé d’une entrée migrée, mémoriser le nom du pak et conserver le nom affiché. Tester ensuite qu’un véritable changement sur le pak remplace ce nom. [DeviceStore.swift:118](ios/ReminisLinkKit/Sources/ReminisLinkKit/DeviceStore.swift#L118) · [Spec iOS §4.1](docs/specs/06-app-ios.md#L57)
- Un réappairage peut masquer définitivement un renommage sur le pak. Appairer « Salon », renommer la console « Bureau » sur le pak, puis refaire l’appairage avant de rouvrir la console dans l’app. `save` enregistre « Bureau » dans `consoleName` et conserve « Salon » comme nom affiché. À l’ouverture, `/device` renvoie « Bureau », déjà mémorisé, et la liste reste sur « Salon » jusqu’au prochain renommage. Appliquer la règle de mise à jour du nom lors du réappairage avant d’enregistrer le nouveau `consoleName`. [DeviceStore.swift:78](ios/ReminisLinkKit/Sources/ReminisLinkKit/DeviceStore.swift#L78) · [PairingFlow.swift:111](ios/ReminisLink/App/PairingFlow.swift#L111)

## À corriger

- Le test de la jaquette vérifie le calcul de `.disabled`, mais ne couvre pas une sélection déjà engagée. Un téléchargement en attente peut renvoyer `404` pendant que la photothèque est ouverte. Si le rechargement confirme la disparition du jeu, le sélecteur reste ouvert et son résultat peut encore lancer une modification. Fermer les sélecteurs au passage à `.gameRemoved`, ignorer les résultats tardifs et couvrir ce parcours dans un test UI. Retirer aussi l’indication VoiceOver « Modifier ou retirer l’image » dans cet état. [GameArtButton.swift:64](ios/ReminisLink/Views/GameArtButton.swift#L64) · [GameArtButton.swift:95](ios/ReminisLink/Views/GameArtButton.swift#L95)

La lecture du correctif confirme que l’échec réseau de la tentative d’ouverture passe désormais par `meansConsoleClosed`. Les lignes de la bibliothèque à plat affichent le tag quand plusieurs systèmes ayant des jeux partagent un libellé, et le filtre reste fusionné.

## Tests lancés

- `git diff --check` sur `main...HEAD`, `m11-ergonomie...HEAD` et `HEAD~1...HEAD`, `scripts/sync-test-vectors.sh --check`, `gofmt -l cmd internal protocol scripts` et la validation JSON du catalogue avec `jq` ont réussi.
- `make lint test`, `make contract`, `swift test` et `xcodebuild test` ont été omis conformément à la consigne de lecture seule. Les constats reposent sur la lecture du code et des tests.

La contre-revue a porté sur le diff complet de `dda581d0` et les portions pertinentes des diffs cumulés. Aucun prompt M11 ne figure dans `prompts/` et la description GitHub de la PR n’a pas pu être consultée. Aucun fichier modifié, aucun commit créé.