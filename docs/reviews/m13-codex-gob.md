Verdict : à corriger

## Bloquant

- [internal/server/transfers.go:245](/Users/tfournier/projets/github.com/tfournier/reminislink-m13-go/internal/server/transfers.go:245) **Une tâche peut commencer son installation après la révocation de son client.** A tient le verrou d’écriture. B lance un téléchargement court vers la bibliothèque. La file démarre la tâche avant que le gestionnaire HTTP enregistre son propriétaire. Si le gestionnaire est suspendu à cet endroit, la tâche peut finir le téléchargement et attendre le verrou. La console déconnecte B, mais `CancelOwner` ignore cette tâche dont le propriétaire est encore vide. A libère le verrou et la tâche installe le fichier. Quand le gestionnaire reprend, il renvoie `401 disconnected` alors que la carte a été modifiée. Sous le même verrou d’accès que la révocation, revalider la génération et soumettre la tâche avec son propriétaire déjà renseigné. Appliquer la correction aux téléchargements et aux patchs.
- [internal/server/writes.go:106](/Users/tfournier/projets/github.com/tfournier/reminislink-m13-go/internal/server/writes.go:106) **Une ancienne requête peut annuler une tâche créée après reconnexion.** B lance un téléchargement et son gestionnaire est suspendu avant `ownJob`. La console déconnecte B. B se reconnecte avec la même identité et lance un second téléchargement, accepté avec `202`. Pendant que cette nouvelle tâche attend le verrou d’écriture, l’ancien gestionnaire reprend. Son contrôle de génération échoue et `CancelOwner` annule les deux tâches, y compris celle créée après la reconnexion. Supprimer cette annulation globale tardive en rendant atomiques la validation et la soumission. Couvrir la séquence déconnexion, reconnexion et reprise de l’ancienne requête par un test synchronisé.

## Points précédents vérifiés

- Les écritures HTTP sérialisées, l’import de cheats et la finalisation d’un envoi revérifient la génération d’accès après acquisition du verrou global.
- La finalisation relâche le mutex de l’envoi pendant cette attente, puis vérifie de nouveau l’envoi. Un test couvre l’annulation pendant l’attente et vérifie que le fichier est absent.
- Les scripts RL2 utilisent `mktemp` pour les fichiers de signature. L’expansion des arguments optionnels fonctionne sous Bash 3.2 avec `set -u`, avec et sans corps.
- Si le certificat TLS manque, est illisible ou ne correspond plus à la clé privée existante, le pak recrée le certificat avec cette clé. Le test vérifie la stabilité du pin. La correction de la feuille de route est présente dans `main` au commit `cdacbd7b`.

## Tests lancés

- `git diff --check` sur `main...HEAD`, `e84a003d...HEAD` et `aa6263ab~1...HEAD`, `gofmt -l cmd internal protocol scripts` et `scripts/sync-test-vectors.sh --check` ont réussi.
- `/bin/bash -n` a réussi sur la bibliothèque RL2 et les trois scripts smoke concernés. Le contrôle de l’expansion des arguments sous Bash 3.2 a également réussi.
- Les suites `make lint test`, `make contract`, les suites iOS et les smoke tests complets ont été écartés en raison de leurs écritures. Les deux scénarios bloquants ont été établis par lecture du code.

La contre-revue porte sur `dbeb27c5`. La section M13 de la feuille de route a servi de référence en l’absence de prompt dédié. La description de PR est restée inaccessible depuis cet environnement. Aucun fichier n’a été modifié et aucun commit n’a été créé.