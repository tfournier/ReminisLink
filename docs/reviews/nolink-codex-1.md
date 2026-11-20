Verdict : à corriger

## Bloquant

- **L’extension peut encore accepter un lien sous forme de texte.** `ios/ReminisLinkShare/ShareViewController.swift:114` écarte un fournisseur d’URL uniquement s’il ne propose aucun type conforme à `.data`. Si une app partage un vrai fichier et un lien représenté à la fois par `public.url` et `public.plain-text`, l’extension peut copier le texte du lien dans la boîte de réception. Si cette représentation ne peut pas être chargée, l’erreur interrompt aussi la copie du vrai fichier à la ligne 63. Filtrer les représentations de liens dans le traitement de chaque pièce jointe et vérifier ce partage mixte sur iOS.

## À corriger

- **Deux specs décrivent encore un parcours retiré.** `docs/specs/06-app-ios.md:42` affiche « Fichiers / lien / carte SD » pour l’installation d’un pak. `docs/specs/checklists/console.md:14` demande de télécharger un patch depuis une URL. Remplacer ces parcours par les sources disponibles, et retirer « device-side downloads » de `CLAUDE.md:33`.
- **Les textes App Store et les notes d’examen contredisent la bibliothèque.** `ios/AppStore/en-US/description.txt:4`, `ios/AppStore/fr-FR/description.txt:4` et `ios/AppStore/review_notes.txt:3` affirment que l’app ne liste aucun jeu. Sur une console appairée dont la carte SD contient des jeux, l’onglet Bibliothèque les liste. Préciser que l’app liste les jeux présents sur la carte SD et ne fournit aucun catalogue de jeux à télécharger.
- **Les descriptions App Store promettent une conservation trop forte dans la corbeille.** `ios/AppStore/en-US/description.txt:30` et `ios/AppStore/fr-FR/description.txt:30` affirment que l’espace est libéré seulement quand l’utilisateur vide la corbeille. Au lancement du pak, une entrée qui atteint 30 jours d’âge comptabilisé est purgée automatiquement (`internal/app/app.go:342`). Reprendre la formulation du site.
- **La politique peut laisser croire à une suppression automatique des envois après 24 h.** Les textes concordent entre `docs/privacy.md:126`, `docs/privacy.md:285`, `site/privacy/index.html:80` et `site/fr/privacy/index.html:80`. Si un envoi est abandonné et que le pak reste ouvert, son fichier partiel peut rester sur la carte au-delà de 24 h. La suppression intervient à la prochaine opération qui constate l’expiration ou à la maintenance au lancement (`internal/transfer/uploads.go:264`, `internal/transfer/uploads.go:731`). Préciser ces déclencheurs dans les trois publications.

## Suggestions

- Étendre le test de migration de `ios/ReminisLinkKit/Tests/ReminisLinkKitTests/TransferQueueTests.swift:542` à une ancienne requête avec une ROM et une URL. Vérifier que la ROM reste proposée, puis que la requête disparaît après sa prise en charge. Le test actuel couvre le cas composé uniquement de liens.
- Avant la soumission, renseigner le lien vidéo dans `ios/AppStore/review_notes.txt:5` et montrer le parcours depuis Fichiers dans la vidéo.

La revue statique n’a relevé aucun chemin sortant vers Internet dans le pak, ni de régression dans les parcours conservés entre OpenAPI, Go et Swift. Les anciennes requêtes composées uniquement de liens sont ignorées puis purgées. Les catalogues n’ont perdu aucune clé encore utilisée et les traductions conservées sont identiques. Les trois libellés de systèmes de la démo sont neutres.

Revue effectuée en lecture seule sur `main...HEAD`, base `14af068a`. `git diff --check` et la vérification de syntaxe des scripts modifiés passent. Les suites Go, Swift et les essais sur iPhone et console n’ont pas été exécutés.