# Contexte commun (à coller avant chaque prompt de jalon)

Tu travailles sur **ReminisLink**, un dépôt Git situé dans le dossier courant.

ReminisLink est une app iPhone compagnon pour consoles portables rétro sous Linux (première cible : TrimUI Brick sous NextUI). Sur la console tourne le **pak ReminisLink** (affiché « ReminisLink » dans le menu Outils), un programme Go au premier plan qui sert une API HTTP sur le Wi-Fi local et s'annonce en Bonjour (`_reminislink._tcp`). L'app iOS (SwiftUI) le découvre, s'appaire par QR code et gère les ROMs, saves, states, cheats et patchs.

## Avant d'écrire du code

1. Lis `CLAUDE.md` en entier. Ses règles sont obligatoires.
2. Lis `docs/specs/README.md`, puis les documents de spécification cités dans le prompt de ton jalon. Lis-les vraiment, ne les survole pas : les détails (codes d'erreur, noms de champs, ordre des opérations) comptent.
3. Regarde l'état actuel du dépôt (`git log`, arborescence) pour savoir ce qui existe déjà.

## Projet de référence

Le dépôt voisin `../nextui-cheat-manager` (même auteur) est la référence pour : le style Go, gabagool, le Makefile, la chaîne de compilation ARM64, `pak.json`, `launch.sh`, la CI, et le format `.cht` (`internal/cheats`). Reprends ses conventions plutôt que d'en inventer. Tu peux copier son code en citant l'origine dans un commentaire d'en-tête.

## Règles de travail

- **Respecte la spec.** Si elle est ambiguë, contradictoire, ou si un point marqué **[À VÉRIFIER M0]** n'a pas encore été vérifié et bloque ton travail : n'invente pas. Choisis l'hypothèse la plus prudente, isole-la derrière l'adaptateur ou une constante nommée, marque-la `// TODO(M0): ...`, et signale-la dans ton rapport final.
- **Le contrat d'abord** : toute modification de l'API passe d'abord par `protocol/openapi.yaml`.
- **Tests obligatoires** pour chaque fonction ajoutée ; la suite complète doit passer (`make lint test`, et `swift test` / `xcodebuild test` côté iOS).
- **Petits commits** clairs, en anglais, au présent (« Add pairing key derivation »). Ne pousse pas et ne fusionne pas : la personne relit d'abord.
- **N'ajoute pas de dépendance** non listée dans `CLAUDE.md` sans le demander.
- **Aucune ROM commerciale**, aucune URL de source de ROMs, nulle part (code, tests, docs, textes de l'app).
- **Sécurité** : jamais de clé, secret, PIN ou signature dans les journaux ; jamais de chemin venant du réseau utilisé sans vérification.
- Code, commentaires, commits et README en **anglais** ; textes affichés à l'utilisateur traduits (anglais et français).

## Rapport final attendu

Termine par un rapport court, en français :

1. Ce qui a été fait (liste des fichiers ou paquets principaux).
2. Comment le vérifier (commandes exactes).
3. Écarts avec la spec, hypothèses `TODO(M0)` et questions ouvertes.
4. Ce qui reste pour terminer le jalon, s'il en reste.
