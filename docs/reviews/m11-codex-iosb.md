Verdict : à corriger

## Bloquant

- Le jeu disparu reste modifiable depuis sa jaquette. Sur une console où l’iPhone a la main, ouvrir un jeu avec une jaquette, supprimer le jeu depuis la console, puis toucher **Télécharger**. Les deux requêtes renvoient `404` et la fiche affiche « Ce jeu n’est plus sur la console », mais la jaquette reste interactive. Elle propose encore **Photothèque**, **Fichiers** et **Retirer l’image** à partir des données de navigation. La spec prévoit le retrait des actions. Afficher la jaquette sans action dans l’état `.gameRemoved` et vérifier ce parcours dans un test UI. [GameDetailView.swift:37](ios/ReminisLink/Views/GameDetailView.swift#L37) · [GameArtButton.swift:61](ios/ReminisLink/Views/GameArtButton.swift#L61)
- Une fermeture juste après la résolution Bonjour laisse encore l’utilisateur sur un écran d’erreur. Avec le navigateur Bonjour actif depuis au moins trois secondes, toucher la console, puis fermer le pak après la résolution de son adresse. Si `/hello` ou `/device` échoue au niveau réseau après le retrait de l’annonce, la tentative passe en `.failed`. La spec prévoit le retour à la liste avec le message de fermeture dans ce cas. Appliquer `meansConsoleClosed` aux erreurs des requêtes de la tentative courante et tester la disparition de l’annonce pendant l’ouverture. [ConsoleSession.swift:190](ios/ReminisLink/App/ConsoleSession.swift#L190) · [Spec iOS §5](docs/specs/06-app-ios.md#L120)

## Bilan des six points repris

Le repli M10 décode les fichiers sans `fileId` et masque leurs boutons de téléchargement. Le scénario **Ajouter** laisse les nouveaux envois en pause et conserve la liste des consoles. Un `503 shutting_down` sur `/hello` ou `/device` ramène bien à cette liste. Les fichiers de démo et le `.cht` sont servis à la taille annoncée. Le message de rechargement attend désormais le succès de la requête, avec le défaut résiduel sur la jaquette décrit ci-dessus. Le test d’annulation passe par le délégué de progression, borne l’attente de la requête et vérifie que le dossier d’export est vide.

## Tests lancés

- `git diff --check` sur `main...HEAD`, `m11-ergonomie...HEAD` et `HEAD~1...HEAD`, `scripts/sync-test-vectors.sh --check`, `gofmt -l cmd internal protocol scripts` et validation du catalogue avec `jq` ont réussi.
- `make lint test`, `make contract`, `swift test` et `xcodebuild test` n’ont pas été lancés, car ils créent des caches ou des artefacts. Lors des premières lectures Git, le lanceur système a tenté de créer un cache `xcrun`, que le bac à sable a refusé. Les vérifications Git ont ensuite utilisé le binaire directement. Aucun fichier modifié, aucun commit créé.

Le dossier `prompts/` ne contient pas de prompt M11. La description GitHub de la PR était inaccessible. La contre-revue s’appuie sur les specs iOS, le contrat OpenAPI et les messages des commits `eb2f348b` et `6cfd9595`.