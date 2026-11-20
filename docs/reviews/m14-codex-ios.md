Verdict : à corriger

## Bloquant

- [ConsoleSession+Events.swift:35](/Users/tfournier/projets/github.com/tfournier/reminislink-m14-ios/ios/ReminisLink/App/ConsoleSession+Events.swift:35) Le flux reste ouvert après avoir quitté la console. Ouvrir A, attendre `hello`, toucher **Consoles**, puis ouvrir B laisse les deux flux ouverts. A reste comptée comme présente par le pak, et le retour au premier plan peut rouvrir son flux alors que B est affichée. Lier le flux à `openConsoleId` et synchroniser son ouverture et sa fermeture à chaque changement de console affichée.
- [ConsoleSession+Events.swift:58](/Users/tfournier/projets/github.com/tfournier/reminislink-m14-ios/ios/ReminisLink/App/ConsoleSession+Events.swift:58) Le retour au premier plan ne rattrape pas les changements de bibliothèque manqués. Ouvrir la bibliothèque, mettre l’app en arrière-plan, faire installer une ROM par un second client, puis revenir à l’app laisse la liste périmée. Le nouveau flux remet `hellosSeen` à zéro, donc son `hello` ne déclenche pas le rechargement. Le contrat précise que le pak ne rejoue pas les événements manqués. Conserver l’information qu’un flux a déjà été ouvert et recharger la bibliothèque et la corbeille à la reprise.
- [StatesView.swift:69](/Users/tfournier/projets/github.com/tfournier/reminislink-m14-ios/ios/ReminisLink/Views/StatesView.swift:69) et [CheatsView.swift:56](/Users/tfournier/projets/github.com/tfournier/reminislink-m14-ios/ios/ReminisLink/Views/CheatsView.swift:56) Deux écrans visés par la spec ignorent `library`. Laisser **States** ouvert pendant qu’un second client supprime un emplacement, ou **Cheats** ouvert sans modification locale pendant qu’il ajoute un code, laisse l’ancien contenu affiché malgré l’événement reçu. Observer la révision de bibliothèque sur ces écrans et préserver le brouillon local si l’éditeur de cheats contient des modifications.
- [FakePakEvents.swift:231](/Users/tfournier/projets/github.com/tfournier/reminislink-m14-ios/ios/ReminisLinkKit/Sources/ReminisLinkDemo/FakePakEvents.swift:231) Le FakePak ne respecte pas les cadences du contrat. Deux écritures réussies à moins d’une seconde d’intervalle produisent deux événements `library` immédiats. De même, deux changements de batterie à moins de cinq secondes d’intervalle peuvent produire deux événements `device`. Fusionner les changements pendant les fenêtres prévues et vérifier la cadence et le contenu fusionné dans les tests.

## À corriger

- [ConsoleSession+Events.swift:98](/Users/tfournier/projets/github.com/tfournier/reminislink-m14-ios/ios/ReminisLink/App/ConsoleSession+Events.swift:98) La réponse de rattrapage de `GET /device` peut écraser un événement plus récent. Si la réponse contenant l’ancienne batterie est retardée et qu’un `device` arrive entre-temps, le tableau de bord revient à l’ancienne valeur. Réconcilier le snapshot avec les événements reçus pendant la requête.
- [SSEParser.swift:121](/Users/tfournier/projets/github.com/tfournier/reminislink-m14-ios/ios/ReminisLinkKit/Sources/ReminisLinkKit/Events/SSEParser.swift:121) La limite s’applique à chaque ligne, mais la taille cumulée d’un événement reste illimitée. Une succession de lignes `data:` sans ligne vide fait grossir le tampon tout en maintenant le flux vivant. Borner la taille cumulée et utiliser une lecture qui régule le débit du producteur. Le pont réseau utilise actuellement un `AsyncThrowingStream` à tampon illimité. [APIClient.swift:1017](/Users/tfournier/projets/github.com/tfournier/reminislink-m14-ios/ios/ReminisLinkKit/Sources/ReminisLinkKit/APIClient.swift:1017)

## Suggestions

- Faire vérifier par les tests que le retour à la liste ramène le nombre de flux du FakePak à zéro et que le retour au premier plan reflète une écriture effectuée pendant l’absence. Le test actuel de reprise vérifie la réouverture du flux. [EventsAppTests.swift:248](/Users/tfournier/projets/github.com/tfournier/reminislink-m14-ios/ios/ReminisLinkTests/EventsAppTests.swift:248)

## Tests lancés

- `gofmt -l cmd internal protocol scripts` — aucun fichier signalé.
- `sh scripts/sync-test-vectors.sh --check` — les copies Swift correspondent aux vecteurs du protocole.
- `make lint test`, `make contract`, `swift test` et `xcodebuild test` — non lancés conformément à la contrainte de lecture seule. Ces commandes écrivent des caches, des artefacts de compilation ou des résultats de tests. Aucun test Go ou iOS n’a donc été exécuté.

La revue porte sur `92f3c698...HEAD`. Aucun prompt M14 n’était présent dans `prompts/`. La description de la PR était inaccessible via `gh`, faute d’authentification. J’ai utilisé le message du commit et les spécifications M14 du dépôt. Aucun fichier n’a été modifié.