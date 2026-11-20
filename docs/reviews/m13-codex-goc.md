Verdict : à corriger

## Bloquant

- [internal/transfer/download.go:119](/Users/tfournier/projets/github.com/tfournier/reminislink-m13-go/internal/transfer/download.go:119) **Une requête déjà révoquée peut encore démarrer une tâche.** B envoie un `POST /downloads` valide. Le serveur accepte la requête, puis son gestionnaire est suspendu juste avant la mise en file. La console déconnecte B et `CancelStale` passe alors que la tâche n’existe pas encore. Le gestionnaire reprend et soumet la tâche avec l’ancienne génération. Si la file est libre et que le serveur distant répond `404` avant que le gestionnaire atteigne `jobStarted`, la console a déjà émis le GET et la tâche finit `failed`. Le gestionnaire renvoie ensuite `401 disconnected`, tandis que `CancelStale` laisse la tâche terminée en `failed`. La même fenêtre de soumission existe pour les patchs. Revalider la génération et mettre la tâche en file sous le même verrou d’accès que la révocation, dans les deux chemins. Conserver le contrôle à l’installation.

## À corriger

- [internal/server/writes_test.go:386](/Users/tfournier/projets/github.com/tfournier/reminislink-m13-go/internal/server/writes_test.go:386) Le premier test attend 150 ms puis vérifie l’état `running`, qui est aussi celui d’un téléchargement encore en cours. Synchroniser le test sur l’attente du verrou d’écriture et vérifier l’absence du fichier cible dans la bibliothèque. Ajouter un cas où la déconnexion intervient avant la mise en file.

## Vérification des deux scénarios précédents

- Une tâche déjà soumise porte l’identité et la génération de B. Si B est déconnecté pendant l’attente du verrou d’écriture, la révocation annule la tâche et le contrôle à l’installation rejette son ancienne génération. Cela vaut pour le téléchargement et le patch.
- Après reconnexion, la nouvelle tâche porte la génération courante. Quand l’ancien gestionnaire reprend et renvoie `401`, son appel à `CancelStale` préserve la nouvelle tâche.

## Tests lancés

- `git diff --check` sur `main...HEAD`, `e84a003d...HEAD` et `HEAD~1...HEAD`, `gofmt -l cmd internal protocol scripts`, `scripts/sync-test-vectors.sh --check` et `bash -n` sur la bibliothèque RL2 et les trois scripts smoke ont réussi.
- Les suites `make lint test`, `make contract`, Swift et Xcode nécessitent des écritures et ont été écartées. Les conclusions sur la concurrence reposent sur la lecture du code.

Contre-revue ciblée du commit `3d575b38`. La feuille de route M13 a servi de référence en l’absence de prompt dédié. La description de la PR est restée inaccessible, car `gh` n’était pas authentifié. Le premier appel à `git` a déclenché une tentative de création d’un cache Xcode, refusée par le bac à sable. Les contrôles suivants ont utilisé directement le binaire Git de Xcode. Aucun fichier n’a été modifié et aucun commit n’a été créé.