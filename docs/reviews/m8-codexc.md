Verdict : **à corriger**

## Bloquant

- [internal/library/write.go:261](internal/library/write.go:261) — L’aperçu attribue les rôles selon la profondeur du fichier. Pour un jeu dossier sans disque partagé, `Discs/CD1.chd`, pourtant cité dans le `.m3u`, reçoit `other`, tandis qu’un `README.txt` à la racine reçoit `rom` même s’il est absent de la playlist. OpenAPI réserve `other` aux fichiers hors playlist. Identifier les ROMs, disques et pistes à partir de la playlist ou de la CUE, puis réutiliser cette classification lors du parcours récursif. [protocol/openapi.yaml:2115](protocol/openapi.yaml:2115)
- [ios/ReminisLink/Views/DeleteGameView.swift:227](ios/ReminisLink/Views/DeleteGameView.swift:227) — La confirmation peut déplacer un fichier absent de la liste affichée. Avec `keepSave=false`, ouvrir la feuille pour un jeu sans save, puis installer une save depuis un second client avant de confirmer. Le pak recalcule la sélection et met aussi cette nouvelle save à la corbeille. Le verrou du `DELETE` protège l’opération à partir de son lancement. Pour garantir la liste exacte demandée par la spec, faire valider par le `DELETE`, sous ce verrou, une empreinte de la sélection confirmée. Si elle a changé, recharger l’aperçu et demander une nouvelle confirmation. [internal/transfer/service.go:201](internal/transfer/service.go:201) · [docs/specs/06-app-ios.md:83](docs/specs/06-app-ios.md:83)

## À corriger

- [ios/ReminisLink/Views/AddView.swift:388](ios/ReminisLink/Views/AddView.swift:388) — L’app marque le lien partagé comme envoyé même si l’utilisateur a soumis une autre URL. Ouvrir un partage contenant deux liens, remplacer le premier dans le champ URL, envoyer, puis arrêter l’app pendant qu’elle propose le second. Au redémarrage, seul le second est proposé, alors que le premier n’a jamais été envoyé. Associer la consommation à l’URL effectivement soumise et traiter explicitement le remplacement d’un lien partagé. [ios/ReminisLink/Views/AddView.swift:225](ios/ReminisLink/Views/AddView.swift:225)
- [ios/ReminisLink/Views/DeleteGameView.swift:35](ios/ReminisLink/Views/DeleteGameView.swift:35) — L’option « Garder la save et les states » reste masquée si le jeu n’a plus de save ni de state actif, mais possède encore un verrou ou un historique de reprise. Le pak inclut ces copies dans l’aperçu et sait les conserver avec `keepSave=true`. Afficher l’option systématiquement ou la piloter à partir de données fournies par le pak. [internal/library/write.go:174](internal/library/write.go:174)
- [internal/store/trash.go:805](internal/store/trash.go:805) — La reprise peut commencer à remettre les originaux alors que la transition vers `restoring` n’a pas encore été synchronisée. Si le renommage d’`entry.json` réussit et que son `fsync` de dossier échoue, un redémarrage du pak sans extinction lit quand même `restoring` et entre directement dans `rollback`. Synchroniser le dossier du journal avant la première restauration en reprise, et prolonger le test d’injection pour que cette synchronisation échoue aussi au lancement suivant. [internal/store/atomic.go:49](internal/store/atomic.go:49) · [internal/store/replace_test.go:467](internal/store/replace_test.go:467)

## Contre-vérification

- Le rollback vérifie désormais la présence de l’original déjà revenu et retente la synchronisation des deux dossiers avant de retirer le journal. L’écriture de `restoring` propage l’erreur de synchronisation au premier essai. Le chemin de reprise reste à corriger comme indiqué ci-dessus. [internal/store/replace.go:157](internal/store/replace.go:157)
- Le scénario exFAT de la revue précédente est corrigé. L’adaptateur retrouve la casse de chaque dossier, et le test vérifie que `deep run/discs/CD1.chd` laisse bien `Deep Run/Discs/CD1.chd` en place. [internal/osadapter/nextui/nextui.go:1058](internal/osadapter/nextui/nextui.go:1058) · [internal/library/write_test.go:254](internal/library/write_test.go:254)
- La restauration de save conserve ensemble les temporaires `.sav` et `.rtc` quand le remplacement reste en attente. Un `.rtc` dont l’empreinte est invalide passe désormais par le nettoyage. [internal/saves/saves.go:460](internal/saves/saves.go:460)
- La route `deletion` est signée. Go et Swift utilisent les mêmes champs et le même `keepSave`. La sélection commune place les disques partagés dans `kept` et inclut les fichiers imbriqués dans le total de l’aperçu. Le partage mixte survit à un redémarrage après la mise en file de la ROM, et l’envoi du lien d’origine enregistre sa consommation. [internal/server/server.go:208](internal/server/server.go:208) · [ios/ReminisLinkKit/Sources/ReminisLinkKit/APIClient.swift:223](ios/ReminisLinkKit/Sources/ReminisLinkKit/APIClient.swift:223) · [ios/ReminisLinkKit/Sources/ReminisLinkKit/Transfers/ShareInbox.swift:137](ios/ReminisLinkKit/Sources/ReminisLinkKit/Transfers/ShareInbox.swift:137)

## Tests lancés

| Commande | Résultat |
|---|---|
| `make lint test` | Formatage passé. `go vet` bloqué par les permissions du cache Go. |
| `make test` | Création du répertoire temporaire Go refusée. |
| `make contract` | Accès au socket Docker refusé avant Redocly et les vecteurs Go. |
| `swift test --scratch-path /tmp/rl-m8-review3-kit` dans `ios/ReminisLinkKit` | Compilateur bloqué par `permissionDenied` avant les tests. |
| `xcodebuild test -scheme ReminisLink -destination 'platform=iOS Simulator,name=iPhone 16' -derivedDataPath /tmp/rl-m8-review3-xcode` | CoreSimulator inaccessible et écriture de `SourcePackages` refusée. |
| `sh scripts/sync-test-vectors.sh --check` | Réussi. |
| `git diff --check main...HEAD` | Réussi. |

La revue est limitée aux corrections indiquées et à leurs points d’intégration dans `HEAD`. `prompts/m8.md` est absent. La section M8 de la feuille de route a servi de référence, et `gh pr view` a demandé une authentification. Les scénarios du rapport ont été vérifiés par lecture du code. Aucun fichier modifié, aucun commit.