# 07 — Sécurité

## 1. Ce qu'on protège

- Les fichiers de la carte SD : saves, states, ROMs (intégrité et disponibilité).
- L'accès à l'API : seuls les appareils appairés peuvent agir.

## 2. Hypothèses

- Le réseau est un Wi-Fi local, en général domestique, parfois public (hôtel, école).
- Le pak n'écoute que lorsqu'il est ouvert, donc quelques minutes à la fois.
- Une personne qui a la console ou la carte SD en main a déjà accès à tout. Ce cas est hors du modèle.

## 3. Menaces et mesures

| Menace | Mesure |
|---|---|
| Un appareil inconnu sur le Wi-Fi appelle l'API | Toutes les routes métier sont signées (Ed25519) par la clé privée d'un client appairé ; le pak ne garde que les clés publiques |
| Écoute du Wi-Fi pendant l'appairage | Tout passe en TLS épinglé sur l'empreinte du certificat lue dans le QR code ; aucune clé secrète n'est échangée (seule la clé publique du client passe), et le secret du QR code ne circule jamais (seule une preuve HMAC) |
| Faux pak sur le réseau (usurpation de la console) | L'app n'accepte que le certificat dont l'empreinte est dans le QR code (M13) ; un faux pak ne peut ni se faire appairer ni recevoir les requêtes d'un client appairé. Repli sur l'adresse du QR code permis pour la même raison (03 §3.1) |
| Écoute du Wi-Fi ensuite | Tout le trafic est chiffré par TLS (1.2 au minimum, 1.3 de préférence) ; aucune clé privée ne circule |
| Rejeu d'une requête capturée | Nonce unique (cache d'au moins 10 min, en mémoire) + horodatage (±300 s) dans la signature. **Risque résiduel accepté** : le cache est perdu quand le pak se ferme ; une requête capturée peut être rejouée si le pak est relancé moins de 300 s après elle (décision du 18/11/2026) |
| Faire lire de gros corps par des requêtes forgées ou rejouées | Signature vérifiée et nonce réservé avant toute lecture du corps ; limite de corps par route ; budget mémoire global (voir [03 §4.3](03-protocole.md#43-vérification-côté-pak)) |
| Modification d'une requête en transit | Méthode, chemin, paramètres et SHA-256 du corps sont signés |
| Morceaux d'envoi modifiés (non signés) | SHA-256 du fichier complet annoncé dans la requête de création, signée, et vérifié avant d'écrire la destination |
| Force brute de l'appairage | Secret QR de 256 bits, valable 10 min, à usage unique ; 5 essais puis blocage de 30 s et nouveau secret. Pas de PIN en M13 |
| Traversée de répertoire (`../`) | Identifiants opaques résolus et vérifiés côté serveur ; noms nettoyés ; chemins des archives de paks vérifiés avant toute écriture (zip-slip, M12) |
| Remplir la carte SD | Contrôle de l'espace libre à la création d'un envoi ; tailles maximales |
| Téléchargement vers une adresse interne (SSRF) | Seul un client appairé peut lancer un téléchargement ; schémas `http`/`https` uniquement. Risque résiduel accepté |
| Deux clients qui modifient la carte en même temps (par exemple une suppression de jeu pendant qu'un autre client écrit dans ses données) | Le pak exécute les écritures une à une (M13, décision du 19/11/2026, qui remplace le bail d'écriture de M10) ; la confirmation d'une suppression affiche toujours l'aperçu calculé par le pak (`GET /games/{gameId}/deletion`). **Risque résiduel accepté** : un autre client peut changer les fichiers d'un jeu entre l'aperçu et la suppression ; tout fichier supprimé ou remplacé reste récupérable 30 jours dans la corbeille |
| Un client appairé qui agit sans limite (écritures en boucle, suppressions) | **Accepté** (décision du 19/11/2026) : tous les clients appairés sont de confiance, sans rôles. La console peut le déconnecter ou l'oublier (Réglages > Appareils) ; une écriture qui attend plus de 10 s reçoit `429` |
| Un appareil appairé installe du code exécuté sur la console (pak, M12) | **Accepté** (décision du 19/11/2026) : tous les appareils appairés sont de confiance ; ReminisLink ne juge pas le contenu d'un pak (pas plus que le Pak Store). Seules restent les protections de la carte : extraction dans la zone d'attente, aucun chemin absolu ni `..`, aucun lien, une seule racine `<Nom>.pak` avec `launch.sh`, 10 000 entrées et 1 Gio décompressé au plus, rapport de compression borné (bombe zip) ; l'ancien pak part à la corbeille ; ReminisLink ne peut ni se supprimer ni se remplacer |
| Lecture des données de ReminisLink par le navigateur SD (M12) | Le dossier `.userdata/shared/ReminisLink/` (clés des clients, clé TLS, corbeille, zone d'attente) et les journaux du pak ne sont ni listés ni téléchargeables ; aucun lien suivi ; identifiants résolus et confinés à la carte à chaque requête, casse ignorée (exFAT) |
| Perte de données par erreur | Tout fichier supprimé ou remplacé part dans une corbeille de 30 jours ; copies automatiques avant toute opération destructive ; l'effacement définitif (vider la corbeille) exige une confirmation explicite (`X-RL-Confirm`) |
| Copie de la carte SD (retirée de la console, exFAT sans protection) | **Couvert pour les clients** (M13) : `clients.json` ne contient que des clés publiques Ed25519, inutilisables pour signer. La clé privée TLS du pak est sur la carte : une copie permet de se faire passer pour la console sur le réseau local, pas pour les clients — **accepté** (constat du 19/11/2026) |
| Fuite par le journal | Jamais de clé, secret, PIN ni signature dans les journaux |

## 4. Bonnes pratiques d'implémentation

- Aléa : `crypto/rand` (Go) et `SecRandomCopyBytes` ou CryptoKit (Swift).
- Comparaisons de MAC en temps constant : `hmac.Equal` (Go), et CryptoKit côté Swift. Signatures : `crypto/ed25519` (Go), `Curve25519.Signing` de CryptoKit (Swift).
- TLS : `crypto/tls` (TLS 1.2 minimum), certificat ECDSA P-256 auto-signé ; côté app, épinglage de la clé publique (SPKI) dans `URLSession` (délégué d'authentification), sans confiance système.
- Limites : corps JSON 1 Mio, morceau 16 Mio, `/hello` et `/pair` limités par IP.
- Délais du serveur HTTP : `ReadHeaderTimeout` 10 s, `IdleTimeout` 60 s ; pas de `ReadTimeout` global sur les routes d'envoi, mais un délai d'inactivité.
- Le serveur ne sert aucun fichier statique et n'expose aucune route de débogage en version publiée.

## 5. Évolutions possibles

- **Retour du PIN** (secours si l'appareil photo est abîmé), protégé par un PAKE (SPAKE2 ou équivalent) ou par un échange X25519 confirmé par le PIN (choix à faire alors, 09).
- Débit TLS à mesurer sur la console (M13).
