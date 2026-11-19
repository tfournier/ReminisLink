# Revue M6 iOS — agent Claude neuf

## Revue complète — à corriger
- **Bloquant (reproduit)** : annuler pendant le téléchargement d'un patch pouvait lancer le patch, y compris en « Remplacer le jeu », quand la tâche finissait `succeeded`.
- À corriger : nom de sortie non aligné sur le pak ; ROM `.zip`/`.7z` proposées ; `409` affiché comme un conflit de nom ; courses annulation/interrogation et annulation/fin d'envoi ; erreur d'interrogation jamais affichée.
- Vérifié conforme : confirmation exacte du remplacement, texte `patch_mismatch`, URL http/https, boîte de partage validée, code démo en Debug, chaînes EN/FR.

## Aller-retour 1 (`4ad7d22a`) et test réel (`de71711a`)
Tout corrigé (une annulation n'enchaîne jamais sur le patch ; état « Téléchargé, rien n'a été appliqué » ; `verified`, détails de mismatch, `cancelRequested` gérés). `swift test` 192, `xcodebuild test` 59 tests app + UI démo. Intégration contre le vrai pak M6 (agent principal) : patch refusé pour une autre ROM, BPS appliqué en nouveau fichier, puis remplacement avec la version précédente en corbeille — réussi.
