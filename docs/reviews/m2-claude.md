# Revues M2 — agents Claude neufs

Revues non croisées, faites en complément de la revue GPT-OSS (de faible qualité).

## Revue complète (`fff9f61c`) — à corriger
- **Bloquant (confirmé)** : un client non authentifié pouvait remplir le journal de la carte SD (`X-RL-Client` brut, chemin non borné, pas de `MaxHeaderBytes`) : 900 Ko de journal par requête.
- À corriger : coût de `cheatCount` (lecture de chaque `.cht`), nom d'instance Bonjour non échappé (« Brick v1.2 » introuvable), identifiants différant seulement par la casse acceptés, erreur fatale jamais journalisée.
- Suggestions : limiteur par IP (éviction, IPv6 /64), `;` dans la requête, tags `.`/`..`, second Ctrl-C, `wlan0`/SSID hors adaptateur, `.m3u` et disques, jaquettes > 8 Mio, secrets dans les arguments de `smoke.sh`.
- Vérifié conforme : routes signées via `Require`, ordre des contrôles, confinement, aucun secret journalisé, écritures limitées à `config.json`/`clients.json`, arrêt propre, dépendances.

## Revue ciblée des corrections (`7e7fa7d7`) — à corriger
- **Bloquant (confirmé)** : l'échantillonnage du journal se contournait par les redirections `307` du `ServeMux` (chemins `//`, `/./`, `/../`).
- À corriger : refus de `;` et échantillonnage de `/hello`/`/pair` absents de la spec (reportés dans 03 §4.2 et 05 §8 par l'agent principal), taille du fichier journal sans plafond.
- Suggestions : `ErrorLog`, IPv6 /64, coût de `m3uSiblings`, casse des disques d'un `.m3u`, `miekg/dns` en dépendance directe (test seulement), erreur fatale affichée deux fois.
- Vérifié conforme : `MaxHeaderBytes`, bornage, `cheats = N` borné, échappement Bonjour, correspondance exacte des noms, `connectedClients` (en mémoire, clients authentifiés seulement), noms commerciaux retirés.

## Aller-retour 2 (`b0f2b83d`)
Seules les requêtes acceptées par le vérificateur vont au journal complet ; rotation à 5 Mio au lancement et plafond de 20 Mio ; `ErrorLog` échantillonné ; disques d'un `.m3u` résolus sans tenir compte de la casse ; erreur fatale affichée une fois. Diff du bloquant relu par l'agent principal ; `make lint test`, `make contract`, `go test -race`, `make headless` + `smoke.sh` OK.
