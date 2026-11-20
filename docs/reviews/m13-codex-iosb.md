Verdict : à corriger

## Bloquant

- [DeviceStore.swift:144](/Users/tfournier/projets/github.com/tfournier/reminislink-m13-ios/ios/ReminisLinkKit/Sources/ReminisLinkKit/DeviceStore.swift:144) Le nettoyage des clés peut casser un appairage pendant la synchronisation iCloud. L’iPhone appaire X avec la clé A et l’iPad appaire Y avec la clé B. Le pointeur courant de l’iPad reste sur B. L’iPad reçoit la clé A avant la fiche de X, puis appaire une troisième console. `save` déclenche le nettoyage, qui supprime A puisqu’aucune fiche locale ne la référence encore. Cette suppression peut se propager à l’iPhone. Quand la fiche de X arrive, `credentials(for:)` renvoie `nil` et X doit être réappairée. Conserver les clés tant que l’absence de références sur les autres appareils ne peut pas être établie. Ajouter un test qui livre la clé et la fiche séparément, avec un appairage entre les deux livraisons.
- [ConsolesView.swift:71](/Users/tfournier/projets/github.com/tfournier/reminislink-m13-ios/ios/ReminisLink/Views/ConsolesView.swift:71) La confirmation d’oubli d’une fiche RL1 peut supprimer un appairage M13 créé entre-temps. Sur l’iPhone, ouvrir la confirmation pour la fiche RL1 de X. Sur l’iPad du même compte, réappairer X et laisser sa fiche se synchroniser. Confirmer ensuite sur l’iPhone. Le bouton appelle `store.remove` avec l’identifiant de X et efface l’élément synchronisé qui vient d’arriver. L’iPad peut alors perdre l’accès à X. Donner à cette action une méthode dédiée qui retire uniquement la fiche RL1 de `UserDefaults`.

## À corriger

- [TLSPinTests.swift:145](/Users/tfournier/projets/github.com/tfournier/reminislink-m13-ios/ios/ReminisLinkKit/Tests/ReminisLinkKitTests/TLSPinTests.swift:145) Les cas SSE, téléchargement avec progression, envoi de fichier et JSON vérifient désormais `identityChanged`. La vérification côté pak peut toutefois réussir avec un chemin de journal erroné, puisque chaque lecture renvoie alors zéro. Elle exclut aussi `/hello`, et les refus d’authentification sont échantillonnés côté pak. Quand `RL_TEST_PAK_LOG` est fourni, faire échouer le test si le journal est illisible et utiliser un témoin HTTP observable sans échantillonnage.

## Vérification des autres points

- **Compilation Swift**. Je retire le constat précédent. C’était un faux positif. `swift test` compile et passe d’après le résultat fourni.
- **Identité par appairage**. Après convergence complète, chaque console signe avec la clé utilisée lors de son appairage, quel que soit le pointeur courant. Le test ajouté couvre ce cas. Le premier blocage concerne l’arrivée séparée des éléments du Trousseau.
- **Repli sans Bonjour**. L’appairage conserve l’adresse atteinte et l’ouverture la réutilise avec l’empreinte SPKI enregistrée. Une connexion authentifiée actualise ensuite cette adresse. Deux tests d’app couvrent le repli.
- **Oubli RL1**. Le balayage complet est désactivé, une confirmation précède l’oubli et les erreurs de suppression sont affichées. Le test UI ajouté couvre le parcours sans synchronisation concurrente.

## Tests lancés

- `scripts/sync-test-vectors.sh --check` — réussi. Les copies Swift correspondent aux vecteurs partagés.
- `git diff --check` sur `main...HEAD`, `ea9836ca...HEAD` et `HEAD~1...HEAD` — réussi.
- `make lint test`, `make contract`, `swift test` et `xcodebuild test` — non relancés pendant cette contre-revue. Ces commandes écrivent des caches, des produits de compilation ou un état Docker.

La revue a porté sur le correctif `3a678348` et les parcours iOS concernés dans le diff du jalon. En l’absence de prompt M13 dédié, je me suis appuyé sur la feuille de route, les spécifications et le contrat local. La description de la PR reste non vérifiée, car GitHub CLI exigeait une session authentifiée. Aucun fichier modifié et aucun commit créé.