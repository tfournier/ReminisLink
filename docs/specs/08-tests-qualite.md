# 08 — Tests et qualité

## 1. Principes

- Chaque tâche livre ses tests. Une fonction sans test n'est pas terminée.
- Les tests ne dépendent ni d'une console, ni d'un réseau extérieur, ni de l'heure réelle (horloge injectable).
- Le contrat (`protocol/openapi.yaml`) et les vecteurs de test (`protocol/test-vectors/`) sont partagés : une divergence entre Go et Swift doit faire échouer la CI.

## 2. Go (le pak)

| Niveau | Contenu | Outil |
|---|---|---|
| Unitaire | Analyse et écriture `.cht`, patchs IPS/BPS/UPS, nettoyage des noms, identifiants, signature, appairage, cache de nonces, adaptateur NextUI | `go test` (`CGO_ENABLED=0`) |
| Vecteurs | `protocol/test-vectors/*.json` | `go test ./internal/auth/...` |
| Intégration HTTP | Chaque route avec `httptest.Server` et une carte SD factice copiée dans `t.TempDir()` | `go test ./internal/server/...` |
| Contrat | Les réponses respectent `openapi.yaml` (validation de schéma) | Test dédié, bibliothèque de validation en dépendance de test uniquement |
| Robustesse | Fuzzing des analyseurs (`.cht`, IPS, BPS, UPS, identifiants) | `go test -fuzz` (court en CI) |

### Carte SD factice

`testdata/sdcard/` reproduit l'organisation NextUI relevée au jalon M0 : plusieurs systèmes (dont deux dossiers avec des tags différents pour la même console), sous-dossiers, jeu multi-disques `.m3u`, noms avec accents, espaces, parenthèses et emojis, saves, states (dont un emplacement de reprise), cheats, jaquettes. Les ROMs sont de petits fichiers sans contenu réel (aucune ROM commerciale dans le dépôt).

### Patchs

Les tests de patchs utilisent des fichiers générés par les tests eux-mêmes (source aléatoire, cible modifiée, patch produit par un encodeur de test ou par des fichiers de référence créés avec Flips et commités). Vérifier : application correcte, rejet d'une mauvaise source (BPS/UPS), patch tronqué, fichiers de plus de 16 Mio (limite IPS).

## 3. Swift (iOS)

| Niveau | Contenu | Outil |
|---|---|---|
| Unitaire | `Signer`, `Pairing`, modèles Codable, logique de reprise des envois | `swift test` dans `ReminisLinkKit` |
| Vecteurs | `protocol/test-vectors/*.json` (copiés comme ressources de test) | `swift test` |
| Intégration | `APIClient` contre le pak en mode `--headless` : poignée de main TLS épinglée (`PinnedHandshakeTests` : `RL_TEST_PAK_LINK='<lien>' swift test --filter PinnedHandshake`) et parcours de l'app par les tests UI réels | `swift test` contre `make headless` ; script `scripts/run-headless-uitests.sh` (ci-dessous) |
| UI | Parcours principaux : appairage par lien, navigation dans la bibliothèque, ajout d'un fichier, suppression | XCUITest sur simulateur |

**Tests UI réels contre le pak headless** : `scripts/run-headless-uitests.sh [NOM...]` lance les tests `*AgainstHeadlessPak` un par un. Un lien d'appairage ne sert qu'une fois : pour chaque test, le script démarre un pak headless avec `--pairing-open` sur une copie neuve de `testdata/sdcard`, passe le lien affiché dans la variable lue par le test (`TEST_RUNNER_RL_UITEST_PAIR_URL[_<NOM>]`, par l'environnement seulement, jamais en argument ni à l'écran), puis arrête le pak par son PID. Il joue aussi le rôle de la personne : `STATES` puis `RESTORED` sur la même carte (state modifié à la main entre les deux), `SIGTERM` au pak pour `CLOSING` et `EVENTS_QUIT`, second client qui supprime un jeu pour `EVENTS_LIBRARY` (`scripts/smoke-events.sh` en mode `UI_DELETE=1`). Variables : `DESTINATION` (simulateur, par défaut `platform=iOS Simulator,name=iPhone 16`), `DERIVED_DATA`, `SKIP_BUILD=1`, `KEEP_LOGS=1` ; `--list` liste les tests. Un test n'est vert que si `xcodebuild` sort avec le code 0 **et** que XCTest indique que cette méthode précise a réussi (le code 0 seul ne suffit pas : une exécution vide sort aussi avec 0) ; un test sauté (lien non reçu) compte comme un échec. Un code non nul est un échec ; il n'entraîne une seule relance, avec un pak neuf, que si l'app n'a pas appairé (le pak n'a jamais affiché « Paired with ») et que ce n'est pas le code 70 (destination ou configuration invalide, « Unable to find a destination »). Le traçage shell (`bash -x`) est coupé pour ne jamais afficher le lien, et les journaux gardés (`KEEP_LOGS=1` ou après un échec, y compris sur interruption) sont expurgés du lien et du QR code. Compatible avec le bash 3.2 de macOS.

## 4. Mode desktop et headless

- `make desktop` : le pak avec son interface, sur Mac, contre `testdata/sdcard` copiée dans un dossier temporaire.
- `make headless` : serveur et annonce Bonjour seulement. Le simulateur iOS le trouve sur le Mac par Bonjour, ce qui permet de développer toute l'app sans console.

### Plateformes sans console (M20)

Sans les consoles tg5050, h700, my355 ni Knulli :

- Tests Go des adaptateurs (`internal/osadapter/nextui/platform_test.go`, `internal/osadapter/knulli`) sur les cartes factices `testdata/sdcard` et `testdata/knulli`, reconstituées à partir du code source des firmwares (sources citées en 05 §3.2 et §3.3).
- `TestSmokeLayouts` (`cmd/reminislink/smoke_layout_test.go`) : le vrai binaire en headless sur la carte de chaque plateforme et de Knulli ; appairage v2 à partir du lien imprimé, description de la console, bibliothèque, envoi d'un jeu vérifié sur la carte, copie de secours d'une save vérifiée dans la zone de copies. Sur macOS : système de fichiers insensible à la casse.
- `make emulation` (`scripts/emulation/run.sh`) : le même test, compilé pour linux/arm64, dans un conteneur par plateforme (natif sur Mac Apple Silicon, qemu-user par binfmt sur une machine x86_64) : rootfs d'origine de la Brick pour tg5040 quand l'image est présente, sinon des images Debian ou Ubuntu épinglées avec la glibc du firmware ou une plus ancienne (tg5050 : glibc 2.31, h700 : 2.35, my355 : 2.28, Knulli : 2.36). Système de fichiers sensible à la casse. Si `build/reminislink` existe (`make docker-build`), la glibc la plus récente qu'il demande est comparée à celle de chaque système.
- Limites assumées : ni écran, ni boutons, ni Wi-Fi, ni SDL réels ; les volontaires (voir `docs/appel-testeurs.md`) valident chaque console avant de l'annoncer comme prise en charge.

## 5. Tests sur console (manuels, à chaque jalon)

Checklist dans `docs/specs/checklists/` (à créer au jalon M2) :

1. Installation du pak à la main et lancement.
2. Sans Wi-Fi : message d'erreur et sortie propre.
3. Appairage par QR code, puis par PIN.
4. Navigation dans une vraie bibliothèque (temps de réponse).
5. Envoi d'une ROM de 512 Mo : durée, reprise après coupure du Wi-Fi.
6. Envoi d'un patch depuis l'iPhone, application BPS, lancement du jeu patché dans NextUI.
7. Save : copie, modification en jeu, restauration, vérification en jeu.
8. State verrouillé, écrasé en jeu, restauré à l'ouverture suivante du pak.
9. Suppression d'un jeu, contenu de la corbeille.
10. Fermeture pendant un transfert.

## 6. CI (GitHub Actions)

- Job Go : `make lint test` puis compilation ARM64 dans `debian:bullseye` (comme Cheat Manager), publication de `ReminisLink.pak.zip` sur tag.
- Job iOS (runner macOS) : `swift test` pour `ReminisLinkKit`, `xcodebuild test` pour l'app.
- Job contrat : vérifie que `openapi.yaml` est valide et que les vecteurs sont lisibles.
- Job émulation (M20, runner `ubuntu-24.04-arm`) : `make emulation` avec le binaire du zip construit par le job de compilation (sans la Brick, dont le rootfs n'est pas dans la CI). Le job de compilation construit aussi `ReminisLink.knulli.zip`, gardé comme artefact (pas publié).

## 7. Qualité du code

- Go : `gofmt`, `go vet`, pas de variable globale mutable hors `main`, erreurs enveloppées (`fmt.Errorf("...: %w", err)`).
- Swift : avertissements traités comme erreurs dans `ReminisLinkKit`, `swift-format`.
- Revue : chaque PR est relue (voir le prompt de revue dans `prompts/`).
