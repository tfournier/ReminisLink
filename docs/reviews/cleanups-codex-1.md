Verdict : à corriger

## Bloquant

- `scripts/run-headless-uitests.sh:268` — Le lanceur peut afficher `PASS` pour zéro test exécuté. Avec `SKIP_BUILD=1`, un build réutilisé où la méthode a été renommée peut produire un bilan vide. Si `xcodebuild` sort à 0 avec `TEST EXECUTE SUCCEEDED`, le script accepte ce résultat. Un saut reconnu par le motif `Test Case … skipped` fait bien sortir le script en échec. Pour couvrir les deux cas, exiger dans le résultat XCTest que la méthode demandée ait effectivement réussi.
- `scripts/smoke-events.sh:103` et `scripts/smoke-events.sh:125` — Avec `bash -x` ou le traçage shell activé en CI, l’affectation du lien et l’appel à `rl2_use_link` écrivent le lien complet sur stderr. Le traçage se poursuit dans la bibliothèque RL2 et expose le secret décodé. Désactiver le traçage avant la première lecture du lien. Le lanceur UI a le même risque dans son journal à `scripts/run-headless-uitests.sh:237`.

## À corriger

- `scripts/run-headless-uitests.sh:223` — Le PID mémorisé pour `EVENTS_LIBRARY` est celui du sous-shell qui attend puis lance `smoke-events.sh`. Un `SIGTERM` envoyé au lanceur peut tuer ce sous-shell en laissant le helper tourner, avec son dossier temporaire et sa clé privée. Faire remplacer le sous-shell par le helper avec `exec` après l’attente, puis gérer explicitement `INT` et `TERM` dans `smoke-events.sh:78`. Sous Bash 3.2, j’ai vérifié qu’un `SIGTERM` sans gestionnaire ne déclenche pas le piège `EXIT`.
- `scripts/run-headless-uitests.sh:245` — Si le runner échoue au démarrage d’`EVENTS_LIBRARY`, le script attend le second client avant d’examiner la possibilité de relancer. Il peut attendre environ deux minutes le deuxième lien que l’app n’a jamais consommé le premier pour faire renouveler. Si le helper est déjà bloqué dans un appel RL2 sans délai maximal, l’attente peut durer davantage. Arrêter le helper dès l’échec de `xcodebuild`, puis décider de la relance. Le compteur limite bien la relance à une, mais le motif `encountered an error` et l’absence d’une ligne `^Test Case` ne prouvent pas à eux seuls que le test n’a jamais démarré.
- `scripts/run-headless-uitests.sh:150` — Sous Bash 3.2, `card=$(new_card)` peut masquer un échec de `cp -R`. Si la copie échoue faute d’espace, le `printf` suivant renvoie quand même le chemin de la carte partielle. Propager explicitement l’échec de la copie avant d’imprimer le chemin.

## Suggestions

- `ios/ReminisLinkTests/ClientAccessAppTests.swift:216` — Le test FakePak couvre déjà la séquence `429` puis succès de `complete`. Un test avec une horloge injectée pourrait aussi vérifier qu’un `Retry-After` supérieur au délai local est respecté et qu’un refus `device_busy` ne rend pas l’issue de l’envoi inconnue.
- `scripts/smoke-events.sh:317` — Vérifier la valeur contractuelle du `Retry-After` du cinquième flux. Le contrôle actuel accepte n’importe quelle valeur non vide.

Le contrat OpenAPI, les specs, Go, Swift et FakePak concordent sur `429 device_busy`. Go renvoie `Retry-After: 2` pour les écritures ordinaires et la finalisation d’un upload. Swift reconnaît aussi l’ancien couple `too_many_attempts` et `Device busy`, garde les autres `429` distincts et traite la finalisation refusée comme transitoire. Le smoke test utilise RL2 avec épinglage TLS, sans appel au bail M10, et suit directement le PID des `curl` qui tiennent les flux. Le lanceur recense les 14 méthodes et démarre un pak par tentative. `RESTORED` redémarre sur la carte de `STATES`, conformément au scénario.

Relecture en lecture seule. Syntaxe et `--list` vérifiés avec Bash 3.2. Aucune compilation ni exécution des suites Go ou iOS.