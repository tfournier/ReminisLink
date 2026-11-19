Verdict : à corriger

## Bloquant

- [internal/server/writer.go:105](/Users/tfournier/projets/github.com/tfournier/reminislink-m10-go/internal/server/writer.go:105) **Une écriture peut être acceptée sans bail.** Deux écritures du même appareil se chevauchent. La première réserve le bail. La seconde vérifie son corps, puis `TryAcquire` lui indique que l’appareil détient déjà le bail. Si le corps de la première échoue à la vérification à cet instant, son `Rollback` libère le bail entre `TryAcquire` et `Renew`. `Renew` renvoie `false`, mais la seconde écriture atteint quand même son handler. Un autre appareil peut alors prendre le bail et écrire en parallèle. Utiliser une seule opération atomique d’acquisition ou de confirmation dans `Commit`.
- [internal/auth/writer.go:104](/Users/tfournier/projets/github.com/tfournier/reminislink-m10-go/internal/auth/writer.go:104) **Une lecture peut rendre permanent le bail provisoire d’une écriture rejetée.** Pendant qu’un `PUT /save` attend son corps, un `GET /device` du même appareil appelle `Renew`, qui confirme la réservation. Si le `PUT` reçoit ensuite `401 bad_signature`, le rollback laisse le bail en place et le prochain écrivain reçoit `423`. Les lectures doivent renouveler uniquement un bail déjà confirmé. Une écriture validée ou une prise de main explicite peut confirmer la réservation.
- [internal/server/writer.go:116](/Users/tfournier/projets/github.com/tfournier/reminislink-m10-go/internal/server/writer.go:116) **Le contrôle de révocation vérifie l’état courant, sans détecter une révocation intervenue pendant la lecture du corps.** Un `PUT /save` attend son corps pendant que la console déconnecte l’appareil. Si celui-ci se reconnecte avant la fin de la lecture, l’ancien `PUT` peut reprendre le bail et écrire. Après « Oublier » suivi d’un réappairage avec le même identifiant, une requête en attente signée avec l’ancienne clé peut également passer. Associer l’admission à une génération d’accès invalidée par la révocation ou le remplacement de la clé, puis la vérifier sous `access` au commit et lors de la prise de main explicite.
- [internal/auth/middleware.go:243](/Users/tfournier/projets/github.com/tfournier/reminislink-m10-go/internal/auth/middleware.go:243) **La fermeture de connexion couvre les rejets M10 du gate avec un corps annoncé, mais plusieurs autres rejets avant lecture peuvent encore attendre le corps.** Par exemple, sur une connexion HTTP/1.1 persistante, un `PUT /save` avec un petit corps annoncé et une signature invalide passe directement par `failWith`. Sans `Connection: close`, `net/http` peut attendre ce corps avant d’envoyer le `401`. Le `413` décidé sur `Content-Length` présente le même problème. Centraliser la fermeture pour les rejets alors que le corps annoncé est encore intact, puis étendre le test sur connexion réelle à ces deux cas.

## À corriger

- [internal/server/presence.go:74](/Users/tfournier/projets/github.com/tfournier/reminislink-m10-go/internal/server/presence.go:74) La reconnexion enregistre désormais la présence immédiatement. Il reste une course avec « Déconnecter » sur les autres requêtes. Si la console efface la présence entre le contrôle `disconnected.has` et `presence.touch`, la requête réinscrit l’appareil dans `connectedClients` pendant 30 secondes. Enregistrer la présence sous `access` lors de l’acceptation et conserver sa mise à jour dans le handler de reconnexion.

Aucun interblocage relevé dans les chemins inspectés. La course du bail vient du fait que `Rollback` peut intervenir entre deux opérations du bail alors que `Commit` tient `access`.

## Tests lancés

- `git diff --check` sur `main...HEAD`, `m10-writer...HEAD` et `b2de5c10...HEAD` — aucune erreur de diff. Le lanceur Git de macOS a tenté de créer un cache temporaire et le sandbox l’a refusé.
- `gofmt -l cmd internal protocol scripts` — aucun fichier signalé.
- `scripts/sync-test-vectors.sh --check` — les copies Swift correspondent aux vecteurs Go.
- `make lint test` et `make contract` — non lancés, car leurs recettes écrivent dans des caches ou des répertoires temporaires. Les scénarios ci-dessus ont été établis par lecture du code.
- `swift test` et `xcodebuild test` — non lancés. Le diff ne touche aucun fichier iOS.

Aucun prompt M10 dans `prompts/`. La revue s’appuie sur les spécifications M10 du dépôt. La description de la PR est restée inaccessible, faute de session GitHub authentifiée.