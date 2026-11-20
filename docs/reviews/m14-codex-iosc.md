Verdict : à corriger

## Bloquant

- **Cheats peut manquer le premier rattrapage.** Ouvrir l’écran pendant que `/events` tarde à s’ouvrir. Le pak lit la liste pour le premier `GET /cheats`, mais sa réponse tarde. Un second appareil modifie ensuite les cheats avant le premier `hello`. Le rattrapage invalide la bibliothèque, mais `consoleMayHaveChanged()` quitte parce que `editor` est encore `nil`. L’ancienne réponse initialise ensuite l’écran avec une liste périmée, qui reste affichée jusqu’à un autre rafraîchissement. Donner une nouvelle génération à la lecture déclenchée par l’invalidation, y compris pendant le chargement initial. [CheatsView.swift:201](ios/ReminisLink/Views/CheatsView.swift:201) · [CheatsView.swift:220](ios/ReminisLink/Views/CheatsView.swift:220)
- **Un rafraîchissement manuel peut encore rendre un brouillon impossible à valider.** Dans Cheats, tirer pour rafraîchir, puis ouvrir la fiche d’un code et modifier sa description pendant que la réponse tarde. La liste reste interactive et `load()` remplace l’éditeur à la réception, même à `etag` identique. La fiche conserve l’ancien identifiant et **Terminé** ne valide plus le brouillon. Faire passer le rafraîchissement manuel par les mêmes protections que les lectures SSE et réserver le remplacement forcé à la confirmation explicite d’abandon. [CheatsView.swift:59](ios/ReminisLink/Views/CheatsView.swift:59) · [CheatsView.swift:67](ios/ReminisLink/Views/CheatsView.swift:67) · [CheatsView.swift:201](ios/ReminisLink/Views/CheatsView.swift:201)
- **La corbeille peut réafficher une entrée supprimée définitivement.** Retarder la réponse de `GET /trash` lancée au premier `hello`, après la lecture de la corbeille par le pak. Un second appareil supprime une entrée. Le rafraîchissement déclenché par `library` affiche la liste à jour, puis la réponse du `hello` rétablit l’entrée supprimée et son volume. Le contrôle de génération intervient après que `loadTrash()` a publié. Vérifier la génération avant la publication, avec une génération commune à tous les chargements de la corbeille. [ConsoleSession+Events.swift:124](ios/ReminisLink/App/ConsoleSession+Events.swift:124) · [ConsoleSession+Events.swift:148](ios/ReminisLink/App/ConsoleSession+Events.swift:148) · [ConsoleSession.swift:263](ios/ReminisLink/App/ConsoleSession.swift:263)

## À corriger

- **Une fin de flux suivie d’une reconnexion laisse le rattrapage précédent actif jusqu’au prochain `hello`.** L’arrêt explicite et le nouveau `hello` annulent le travail précédent. Lors d’une fin de flux spontanée, `eventStreamEnded` désactive seulement l’état live avant la sonde et la reconnexion. Une réponse peut donc encore être publiée pendant cet intervalle, contrairement à la spec. Invalider le rattrapage dès l’entrée dans `eventStreamEnded` et propager ce contrôle aux opérations qui publient après une attente, dont `refreshJobs`. [ConsoleSession+Events.swift:51](ios/ReminisLink/App/ConsoleSession+Events.swift:51) · [ConsoleSession+Events.swift:164](ios/ReminisLink/App/ConsoleSession+Events.swift:164) · [TransferQueue.swift:583](ios/ReminisLinkKit/Sources/ReminisLinkKit/Transfers/TransferQueue.swift:583)

## Vérification des correctifs précédents

- Le premier `hello` invalide désormais les écrans, y compris après une interruption avant le premier `hello`. Le chargement initial de Cheats reste le cas décrit plus haut.
- Dans States, une ancienne réponse `GET /states` ne peut plus réafficher un emplacement supprimé après qu’un chargement plus récent a actualisé l’écran.
- Sur le chemin SSE de Cheats, un `etag` identique préserve les identifiants. Une liste modifiée attend la fermeture de la fiche et **Abandonner** affiche la liste du conflit mémorisé.
- Sur un FakePak neuf, l’ouverture du premier flux établit désormais la présence de son client comme référence. La fermeture du dernier flux efface cette référence.
- Une ancienne réponse `GET /device` est écartée après l’arrêt explicite du flux ou après un `hello` plus récent. Un événement `device` reçu pendant la lecture conserve la priorité.

## Tests lancés

- `git diff --check main...HEAD` et `git diff --check HEAD~1...HEAD` — aucun problème signalé.
- `gofmt -l cmd internal protocol scripts` — aucun fichier signalé.
- `sh scripts/sync-test-vectors.sh --check` — copies Swift conformes aux vecteurs du protocole.
- `make lint test`, `make contract`, `swift test` et `xcodebuild test` — non lancés, car ils écrivent des caches ou des artefacts. Les scénarios ci-dessus résultent de la lecture du code.

Contre-revue ciblée sur `7d816ca9` et son contexte iOS M14. Aucun prompt M14 n’était présent dans `prompts/`. `gh pr view` n’a pas donné accès à la description faute d’authentification. Aucun fichier modifié, aucun commit créé.