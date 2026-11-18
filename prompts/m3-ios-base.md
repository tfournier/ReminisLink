# M3 — App iOS : découverte, appairage, navigation (Swift)

## À lire

`docs/specs/06-app-ios.md` (en entier), `docs/specs/03-protocole.md` (§2, 3, 4, 6, 9), `docs/specs/04-api-v1.md` (Console et Bibliothèque), `CLAUDE.md` (section Swift). Code de `ios/ReminisLinkKit` (jalon M1) et `protocol/openapi.yaml`.

## Livrables

1. **Projet Xcode** `ios/ReminisLink.xcodeproj` :
   - cibles `ReminisLink` (app, iOS 17) et `ReminisLinkShare` (extension de partage : squelette seulement, la logique vient en M4) ;
   - le package local `ReminisLinkKit` en dépendance ;
   - Info.plist et capacités de `06 §3` (réseau local, Bonjour, appareil photo, ATS local, URL scheme `reminislink`, App Group, partage de Trousseau) ; identifiants avec un préfixe facile à changer (un seul fichier `Config.xcconfig`) ;
   - String Catalog anglais + français ; aucune chaîne visible en dur.
2. **ReminisLinkKit** :
   - `Discovery` : `NWBrowser` Bonjour avec TXT ; publie la liste des consoles vues (`id`, nom, modèle, endpoint) ; distingue « refusé par la politique réseau local » de « rien trouvé » ;
   - `APIClient` : `async`, signe chaque requête (`Signer` de M1), corrige l'écart d'horloge à partir de `/hello` (un nouvel essai après `clock_skew`), décode les erreurs `03 §6` en un `enum` Swift ; modèles `Codable` pour les routes de ce jalon, tolérants aux champs inconnus ;
   - `KeychainStore` et `DeviceStore` (consoles appairées) ;
   - tests avec un `URLProtocol` factice.
3. **Écrans** (`06 §4.1` à `§4.5`, en lecture seule pour la fiche de jeu) :
   - Consoles (avec écran d'accueil sans console), Appairage (scanner VisionKit, lien `reminislink://pair` reçu par l'app, saisie du PIN avec avertissement), Tableau de bord, Bibliothèque (systèmes, dossiers, jeux, recherche, tri, jaquettes en cache), Fiche de jeu (en-tête, fichiers, compteurs) ;
   - toutes les erreurs du tableau `06 §5` avec leurs actions.
4. **Développement sans console** : une configuration de schéma Xcode (ou une note dans `ios/README.md`) pour tester contre `make headless` lancé sur le Mac.

## Tests

- `swift test` (Kit) : client, décodage des erreurs, découverte (logique de filtrage par `id`), stockage.
- XCUITest : ouverture d'un lien `reminislink://pair` de test → console ajoutée → navigation jusqu'à une fiche de jeu, contre le pak en mode headless (test désactivable par variable d'environnement si le pak n'est pas lancé).
- Si le pak en mode headless n'existe pas encore (M2 en cours), utilise des réponses factices tirées des exemples de `openapi.yaml` et signale-le dans le rapport.

## Contraintes

- SwiftUI + Observation, pas de dépendance tierce.
- Accessibilité : libellés VoiceOver sur les pastilles et boutons iconiques ; Dynamic Type respecté.
- L'app ne propose aucune source de ROMs, ni dans les textes, ni dans les exemples.

## Fait quand

- `swift test` et `xcodebuild test` (simulateur) passent.
- Sur simulateur, contre `make headless` : découverte, appairage par lien, tableau de bord et navigation jusqu'à une fiche de jeu fonctionnent.
