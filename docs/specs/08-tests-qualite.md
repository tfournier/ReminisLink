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
| Intégration | `APIClient` contre le pak en mode `--headless` lancé par le script de test | XCTest + script `scripts/run-headless.sh` |
| UI | Parcours principaux : appairage par lien, navigation dans la bibliothèque, ajout d'un fichier, suppression | XCUITest sur simulateur |

## 4. Mode desktop et headless

- `make desktop` : le pak avec son interface, sur Mac, contre `testdata/sdcard` copiée dans un dossier temporaire.
- `make headless` : serveur et annonce Bonjour seulement. Le simulateur iOS le trouve sur le Mac par Bonjour, ce qui permet de développer toute l'app sans console.

## 5. Tests sur console (manuels, à chaque jalon)

Checklist dans `docs/specs/checklists/` (à créer au jalon M2) :

1. Installation du pak à la main et lancement.
2. Sans Wi-Fi : message d'erreur et sortie propre.
3. Appairage par QR code, puis par PIN.
4. Navigation dans une vraie bibliothèque (temps de réponse).
5. Envoi d'une ROM de 512 Mo : durée, reprise après coupure du Wi-Fi.
6. Téléchargement d'un patch depuis une URL, application BPS, lancement du jeu patché dans NextUI.
7. Save : copie, modification en jeu, restauration, vérification en jeu.
8. State verrouillé, écrasé en jeu, restauré à l'ouverture suivante du pak.
9. Suppression d'un jeu, contenu de la corbeille.
10. Fermeture pendant un transfert.

## 6. CI (GitHub Actions)

- Job Go : `make lint test` puis compilation ARM64 dans `debian:bullseye` (comme Cheat Manager), publication de `ReminisLink.pak.zip` sur tag.
- Job iOS (runner macOS) : `swift test` pour `ReminisLinkKit`, `xcodebuild test` pour l'app.
- Job contrat : vérifie que `openapi.yaml` est valide et que les vecteurs sont lisibles.

## 7. Qualité du code

- Go : `gofmt`, `go vet`, pas de variable globale mutable hors `main`, erreurs enveloppées (`fmt.Errorf("...: %w", err)`).
- Swift : avertissements traités comme erreurs dans `ReminisLinkKit`, `swift-format`.
- Revue : chaque PR est relue (voir le prompt de revue dans `prompts/`).
