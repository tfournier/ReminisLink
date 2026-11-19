Verdict : à corriger

## Bloquant

- [internal/saves/inventory.go:263](internal/saves/inventory.go:263) **`GET /saves` peut renvoyer deux SHA-256 pour le même `path`.** Deux jeux NextUI partagent une save de 128 Kio. Si ses octets sont réécrits sur place entre le passage du premier jeu et celui du second, le nouveau code hache le fichier deux fois et peut renvoyer un hash différent pour chaque jeu. Le contrat impose le même hash pour les deux entrées. Mémoriser le résultat par chemin pendant une requête et recalculer le hash à la requête suivante. [Contrat OpenAPI](protocol/openapi.yaml:688)

## À corriger

- [FileSaveVault.swift:155](ios/ReminisLinkKit/Sources/ReminisLinkKit/Sync/FileSaveVault.swift:155) **L’échec d’une rotation peut empêcher la reprise des autres jeux.** Si la suppression d’une ancienne version échoue pour le premier jeu parcouru, la boucle s’arrête. `SaveSync` ignore l’erreur et, pour une save inchangée d’un autre jeu, la comparaison ne relance plus sa rotation. Cet historique peut donc rester à onze versions même si ses fichiers sont accessibles. Isoler les erreurs par jeu et poursuivre les rotations restantes. [SaveSync.swift:109](ios/ReminisLinkKit/Sources/ReminisLinkKit/Sync/SaveSync.swift:109)
- [FakePak.swift:998](ios/ReminisLinkKit/Sources/ReminisLinkDemo/FakePak.swift:998) **Le simulateur recrée une save déplacée en corbeille.** Supprimer l’un de deux jeux partageant une save avec `keepSave=false` efface leur entrée commune. À la lecture suivante du jeu restant, `currentData` recrée la save et les states à partir des valeurs initiales de la démo. Le résultat contredit la liste des fichiers déplacés et le comportement du pak Go. Conserver sous la clé du jeu restant un état vide explicite pour les données déplacées et tester la lecture après suppression. [FakePakGameData.swift:136](ios/ReminisLinkKit/Sources/ReminisLinkDemo/FakePakGameData.swift:136) · [internal/library/write.go:157](internal/library/write.go:157)

## Contre-vérification des six points

- **Cache Go.** Une ancienne save de 1 Mio ou moins est rehachée à chaque inventaire. Au-delà, un changement de `ctime` invalide le cache. La réécriture sur place à taille et date constantes sans `ctime` fiable reste la limite acceptée. [inventory.go:101](internal/saves/inventory.go:101)
- **Confinement Go.** L’inventaire et l’ouverture du `.rtc` passent par `os.Root`. Le remplacement du parent après sa vérification ne permet plus d’ouvrir un fichier hors de la racine déjà ouverte. [confine.go:32](internal/saves/confine.go:32)
- **Anciennes versions iOS.** Une métadonnée sans `sequence` reste lisible et la version reste exportable. Les versions héritées sont classées de façon déterministe avant les versions numérotées. [SaveVault.swift:96](ios/ReminisLinkKit/Sources/ReminisLinkKit/Sync/SaveVault.swift:96) · [FileSaveVault.swift:247](ios/ReminisLinkKit/Sources/ReminisLinkKit/Sync/FileSaveVault.swift:247)
- **Reconnexion iOS.** Une demande enregistrée après l’annulation survit à la fin de l’ancienne tâche. Le test ajouté force cet ordre d’événements. [AppModel+SaveSync.swift:100](ios/ReminisLink/App/AppModel+SaveSync.swift:100) · [SaveSyncAppTests.swift:135](ios/ReminisLinkTests/SaveSyncAppTests.swift:135)
- **Rotation iOS.** Le parcours du coffre couvre désormais les jeux absents de l’inventaire. Le test ajouté vérifie le retour de onze à dix versions pour un jeu retiré de la console. [FileSaveVault.swift:153](ios/ReminisLinkKit/Sources/ReminisLinkKit/Sync/FileSaveVault.swift:153) · [SaveSyncTests.swift:149](ios/ReminisLinkKit/Tests/ReminisLinkKitTests/SaveSyncTests.swift:149)
- **Contrôles FakePak.** L’écrasement d’un state et la modification des cheats depuis le second jeu utilisent désormais la même clé que les lectures. [FakePakGameData.swift:204](ios/ReminisLinkKit/Sources/ReminisLinkDemo/FakePakGameData.swift:204)

## Tests lancés

| Commande | Résultat |
|---|---|
| `git diff --check main...HEAD` et `git diff --check a4316447..HEAD` | Réussis. |
| `make lint test`, puis `make test` | `go vet` et `go test` bloqués à la création du répertoire de travail Go. |
| `make contract` | Accès au socket Docker refusé. Redocly et les tests Go des vecteurs n’ont pas démarré. |
| `scripts/sync-test-vectors.sh --check` | Réussi. Les copies Swift sont à jour. |
| `swift test` dans `ios/ReminisLinkKit` | Arrêt avant compilation sur `permissionDenied`. |
| `xcodebuild test -scheme ReminisLink -destination 'platform=iOS Simulator,name=iPhone 16'` dans `ios` | Arrêt avant les tests. Écriture dans `SourcePackages` refusée et service Simulator indisponible. |

Contre-revue limitée aux corrections ciblées. Aucun prompt M9 trouvé dans `prompts/`. La description de PR est restée inaccessible faute d’authentification `gh`. Aucun fichier modifié, aucun commit.