Verdict : **à corriger**

## Bloquant

- `internal/store/replace.go:74` — **Une erreur de synchronisation après le renommage laisse le remplacement à moitié appliqué.** Lors d’une restauration de state, si le renommage du `.txt` réussit mais que le `fsync` de son dossier échoue, `installOne` renvoie une erreur. Le rollback ne prend en compte que `records[:i]`. Il retire donc le nouveau state mais laisse le nouveau `.txt` à sa destination, où il empêche la remise en place de l’ancien. `restoreCopy` efface ensuite ses temporaires, y compris en cas d’`ErrReplaceFailed` (`internal/states/states.go:778`, `:805`). Enregistrer le renommage dès qu’il réussit, conserver les temporaires tant que le journal les référence et tester une erreur de `fsync` sur le dernier fichier.
- `internal/store/trash.go:552` — **Le rollback peut effacer le journal avant que l’original remis en place soit durable.** Après un échec de renommage du patch, `restoreItem` remet la ROM en place et rapporte un succès même si la synchronisation du dossier échoue. `rollback` supprime alors le dossier de corbeille (`internal/store/replace.go:152`). Si le retrait côté corbeille est persisté et que la console s’éteint avant la persistance de la destination, la seule copie peut disparaître. Vérifier les synchronisations des deux côtés et conserver le journal jusqu’à confirmation.
- `internal/library/write.go:208` — **Les routes d’écriture de jaquette acceptent une destination hors de la racine ROM.** En mode desktop ou sur un système de fichiers qui accepte les liens symboliques, un `.media` lié à un dossier extérieur est suivi. `GameFiles` vérifie le type du fichier final et `Trash.Replace` vérifie seulement le chemin lexical. Un client appairé peut ainsi déplacer, remplacer ou supprimer la jaquette dans le dossier extérieur. Appliquer aux destinations de `PUT` et `DELETE` la résolution avec contrôle de confinement déjà utilisée pour les ROMs.
- `internal/osadapter/nextui/nextui.go:938` — **Supprimer un jeu dossier peut toujours rendre une autre playlist injouable.** Si une playlist du système référence `Jeu/Disques/CD1.chd`, `SharedFiles` ignore le disque parce que `GameFiles` ne relève que les fichiers immédiatement dans `Jeu`. `DeleteGame` déplace alors le dossier entier et le vidage peut effacer le disque partagé. Détecter les références à tout descendant avant de déplacer le dossier, puis préserver les fichiers référencés. Le test actuel couvre les disques au même niveau.
- `internal/ui/memfile_other.go:23` — **Le bloquant M7 sur le QR subsiste sur macOS.** Un `SIGKILL` entre la création du PNG temporaire et `Release` laisse sur disque le QR qui contient le secret. Le `memfd` couvre Linux. Sur macOS, faire charger le PNG en mémoire par SDL, ou afficher uniquement le PIN si le rendu en mémoire est indisponible.
- `internal/auth/session.go:180` — **Une session fermée peut répondre `429` au lieu de `401 pairing_failed`.** Après cinq preuves invalides, quitter l’écran puis envoyer une requête bien formée, sous la limite par IP, déclenche le blocage de session avant le contrôle de fermeture. OpenAPI exige `401` pendant la fermeture. Vérifier la fermeture avant le blocage de session et conserver le délai pour la prochaine ouverture.

## À corriger

- `ios/ReminisLink/Views/DeleteGameView.swift:92` — **La confirmation de suppression diverge encore du pak.** Elle compte les disques partagés dans le total à déplacer et affirme qu’un dossier `folder-m3u` part entier même si le pak conserve des fichiers partagés. Pour `folder-cue`, elle omet l’avertissement alors que le dossier entier peut partir, y compris ses fichiers absents du détail. Faire produire l’aperçu par la même sélection que `DeleteGame`, annexes comprises.
- `ios/ReminisLinkKit/Sources/ReminisLinkKit/ArtCache.swift:29` — **Deux lectures concurrentes peuvent associer un PNG à l’ETag d’une autre version.** Après un remplacement, deux vues peuvent terminer les écritures des deux fichiers dans un ordre différent. Si l’ancien PNG reste associé au nouvel ETag, les prochains `304` le conservent. Stocker la paire atomiquement et écarter les réponses lancées avant l’invalidation.
- `docs/specs/05-console-pak.md:118` — La spec décrit encore le crédit hors session depuis la dernière fermeture. `creditWallMax` corrige bien le scénario M4 de l’horloge qui oscille entre les deux mêmes dates. Documenter la borne haute persistante. Ajouter aussi `folder-cue` à la liste des types dans `docs/specs/04-api-v1.md:148`, en accord avec OpenAPI et Go. Le type Swift accepte déjà cette valeur.

## Suggestions

La lecture du code confirme les autres corrections ciblées des revues précédentes sur la sérialisation des appairages iOS, le refus des redirections, le PIN de secours et son délai, la limite des jaquettes côté Swift, la reprise sans validateur, l’annulation après acquisition du verrou, le `.rtc` isolé, l’archivage des states de reprise par contenu, les cœurs inconnus, les limites `.cht` et IPS, l’annulation UPS, le polling des patchs, la réannonce Bonjour, `exec`, `LICENSE` et l’identifiant App Store Connect. Les tests de reprise présents couvrent les entrées `installing` et leur exclusion du vidage et de la purge.

- `internal/store/replace_test.go:48` — Étendre l’injection de pannes aux synchronisations après renommage, puis relancer `Recover` deux fois. Vérifier les octets du state et du `.txt`, la présence de l’original et l’état du journal à chaque reprise.

## Tests lancés

| Commande | Résultat |
|---|---|
| `make lint test` | Contrôle de formatage passé. `go vet` bloqué par les permissions du cache Go. |
| `make test` | Bloqué à la création du répertoire temporaire Go. |
| `make contract` | Accès au socket Docker refusé avant Redocly et les vecteurs Go. |
| `sh scripts/sync-test-vectors.sh --check` | Réussi. Les copies Swift des vecteurs sont synchronisées. |
| `swift test --scratch-path /tmp/rl-m8-review-swift` | Le compilateur renvoie `permissionDenied` avant les tests. |
| `xcodebuild test -scheme ReminisLink -destination 'platform=iOS Simulator,name=iPhone 16' -derivedDataPath /tmp/rl-m8-review-xcode` | CoreSimulator inaccessible et écriture du cache des dépendances refusée. |
| `git diff --check main...HEAD` et `sh -n pak/launch.sh` | Réussis. |

Les scénarios bloquants sont établis par lecture du code. Les parcours headless et sur Brick, notamment la coupure réelle et l’affichage de la batterie, restent à valider. `gh pr view` exigeait une authentification. Le périmètre a été établi à partir de `main...HEAD`, des commits locaux et de la section M8 de la feuille de route, puisque `prompts/m8.md` est absent. Aucun fichier modifié.