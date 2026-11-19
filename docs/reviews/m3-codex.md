# Revue M3 — Codex (ChatGPT, compte de la personne)

Date : 2026-11-19. Revue croisée en lecture seule (`codex exec -s read-only`, Codex CLI 0.170.0) sur `main` @ `5cb60ebf`, périmètre du jalon. Les tests n'ont pas pu tourner dans le bac à sable de Codex : constats par lecture du code. Chaque point est vérifié par l'agent principal et l'agent du jalon M8 avant correction.

Verdict : à corriger

### Bloquant

- [PairingFlow.swift:99](ios/ReminisLink/App/PairingFlow.swift#L99) **Deux appairages de la même console peuvent laisser des clés différentes sur l’iPhone et le pak.** Le premier `POST /pair` continue après la fermeture de l’écran. Si le pak a déjà enregistré sa clé et que sa réponse tarde, l’utilisateur peut scanner le nouveau QR affiché par le pak. Si ce deuxième échange termine en premier, la réponse valide du premier échange écrase ensuite la clé plus récente dans le Trousseau. Le pak conserve la deuxième clé et rejette les requêtes signées de l’iPhone. Sérialiser les échanges par `deviceId` jusqu’à la fin de l’écriture dans le Trousseau, même après la fermeture de l’écran. [AppModel.swift:169](ios/ReminisLink/App/AppModel.swift#L169), [handlers.go:170](internal/server/handlers.go#L170)
- [APIClient.swift:91](ios/ReminisLinkKit/Sources/ReminisLinkKit/APIClient.swift#L91) **Une redirection contourne la signature par requête et la limite au réseau local.** Si l’endpoint découvert renvoie un `307` vers un autre chemin ou une destination HTTPS externe, `URLSession` suit la redirection par défaut. Cette deuxième requête passe en dehors de la boucle de signature et peut retransmettre le corps d’un `POST`, notamment celui d’un appairage. Refuser les redirections HTTP pour les appels au pak et vérifier qu’une réponse `3xx` ne déclenche aucune deuxième requête. [APIClient.swift:480](ios/ReminisLinkKit/Sources/ReminisLinkKit/APIClient.swift#L480), [APIClient.swift:565](ios/ReminisLinkKit/Sources/ReminisLinkKit/APIClient.swift#L565)

### À corriger

- [AddConsoleView.swift:30](ios/ReminisLink/Views/AddConsoleView.swift#L30) Le bouton **Refaire l’appairage** ouvre un écran qui exclut la console à réparer de la liste des consoles accessibles par PIN. Après un `401 unknown_client`, un utilisateur dont la caméra est indisponible perd donc le mode de secours prévu par la spec. Inclure la console concernée dans cette liste et conserver la confirmation avant remplacement de la clé. [Common.swift:50](ios/ReminisLink/Views/Common.swift#L50)
- [AddConsoleView.swift:33](ios/ReminisLink/Views/AddConsoleView.swift#L33) Dans le parcours PIN, si aucune console n’est découverte, l’indicateur tourne indéfiniment. Appliquer le délai de huit secondes prévu par le protocole et proposer **Réessayer**, tout en conservant l’action **Ouvrir les Réglages** en cas de refus réseau.
- [APIClient.swift:163](ios/ReminisLinkKit/Sources/ReminisLinkKit/APIClient.swift#L163) Les jaquettes sont chargées entièrement en mémoire. Le seuil Go de 8 Mio limite le calcul de l’ETag, puis le pak diffuse les fichiers plus gros en entier. Une jaquette anormalement volumineuse sur la carte SD peut donc faire fermer l’app sous pression mémoire dès l’ouverture de la bibliothèque. Borner la taille reçue, y compris lorsque la réponse ne fournit pas de `Content-Length`. [handlers.go:350](internal/server/handlers.go#L350)

### Tests lancés

| Commande | Résultat |
|---|---|
| `make lint test` | `gofmt` passé, puis `go vet` bloqué par les permissions du cache Go. |
| `make test` | Bloqué à la création du répertoire de travail Go. |
| `make contract` | Bloqué par l’accès au socket Docker. La régénération des vecteurs en mode `-check`, tentée séparément, a aussi été bloquée par les permissions Go. |
| `scripts/sync-test-vectors.sh --check` | Réussi. Les copies Swift correspondent aux vecteurs du dépôt. |
| `swift test` | Bloqué avant compilation par une erreur de permission du compilateur. |
| `xcodebuild test` sur iPhone 16 | Bloqué avant les tests par les permissions d’écriture et l’indisponibilité de CoreSimulator. Le parcours contre le pak headless reste non vérifié. |

Revue statique effectuée sur `5cb60ebf`, dans le périmètre de la fusion `04b39fa5`. `git diff main...HEAD` était vide. La description GitHub était inaccessible sans authentification `gh`. Aucun fichier modifié.