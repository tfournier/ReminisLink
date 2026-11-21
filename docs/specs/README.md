# Spécifications ReminisLink

Version 0.1 — 18/11/2026. Statut : brouillon validé sur le fond, à affiner pendant le jalon M0.

| # | Document | Contenu |
|---|---|---|
| M0 | [Relevés sur la console](m0/README.md) | [Analyse du code NextUI et du firmware](m0/source-analysis.md) (chemins NextUI établis), procédure réduite sur la Brick (veille, mDNS, horloge), [questions et décisions ouvertes](m0/questions.md) |
| 01 | [Vision et périmètre](01-vision-perimetre.md) | Le produit, les utilisateurs, ce qui est dans la v1 et ce qui n'y est pas |
| 02 | [Architecture](02-architecture.md) | Les deux composants, leurs responsabilités, le dépôt |
| 03 | [Protocole](03-protocole.md) | Découverte Bonjour, appairage QR, signature HMAC, erreurs, transferts |
| 04 | [API v1](04-api-v1.md) | Toutes les routes, leurs entrées et sorties |
| 05 | [Le pak (console)](05-console-pak.md) | Le pak : chemins NextUI, stockage, verrous, écrans |
| 06 | [App iOS](06-app-ios.md) | Écrans, permissions, extension de partage, stockage |
| 07 | [Sécurité](07-securite.md) | Modèle de menace et mesures |
| 08 | [Tests et qualité](08-tests-qualite.md) | Stratégie de test, vecteurs partagés, mode desktop |
| 09 | [Feuille de route](09-feuille-de-route.md) | Jalons M0 à M7 et critères d'acceptation |
| 10 | [App Android](10-app-android.md) | M21 : ce qui est propre à Android (clé dans le Keystore, NSD, épinglage OkHttp, F-Droid) |

## Vocabulaire

| Terme | Sens |
|---|---|
| **Console** | La console portable (TrimUI Brick au départ) |
| **Pak** | Le programme ReminisLink installé sur la console (affiché « ReminisLink » dans le menu Outils), qui sert l'API |
| **App** | L'app iOS ReminisLink |
| **Client** | Une installation de l'app appairée avec une console |
| **Système** | Un dossier de `Roms/` avec un tag, ex. `Game Boy Advance (GBA)` → tag `GBA` |
| **Jeu** | Une ROM (fichier, ou dossier avec `.m3u`) et tous ses fichiers associés |
| **Save** | La sauvegarde interne du jeu (`.sav`) |
| **State** | Une sauvegarde d'état de l'émulateur, dans un emplacement (*slot*) numéroté |
| **State de reprise** | Le state écrit automatiquement par NextUI à l'extinction en jeu |
| **Verrou** | Une copie protégée d'un state, restaurée si l'original est écrasé |
| **Tâche** (*job*) | Une opération longue côté console (téléchargement, patch), suivie par l'app |

## Conventions de rédaction

- **DOIT / NE DOIT PAS** : obligatoire. **DEVRAIT** : recommandé, écart à justifier. **PEUT** : optionnel.
- Les points marqués **[À VÉRIFIER M0]** dépendent d'une observation sur une vraie console. Ils sont à confirmer au jalon M0 (procédure : [m0/README.md](m0/README.md)) avant d'écrire le code qui en dépend.
