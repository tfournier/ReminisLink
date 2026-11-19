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
| Un appareil inconnu sur le Wi-Fi appelle l'API | Toutes les routes métier sont signées avec une clé connue des seuls clients appairés |
| Écoute du Wi-Fi pendant l'appairage | La clé est dérivée des deux côtés (HKDF) à partir du secret du QR code, qui ne passe jamais sur le réseau |
| Écoute du Wi-Fi ensuite | La clé ne circule jamais. Le contenu des fichiers transite en clair : risque accepté en v1 (ROMs et saves) |
| Rejeu d'une requête capturée | Nonce unique (cache d'au moins 10 min, en mémoire) + horodatage (±300 s) dans la signature. **Risque résiduel accepté** : le cache est perdu quand le pak se ferme ; une requête capturée peut être rejouée si le pak est relancé moins de 300 s après elle (décision du 18/11/2026) |
| Faire lire de gros corps par des requêtes forgées ou rejouées | Signature vérifiée et nonce réservé avant toute lecture du corps ; limite de corps par route ; budget mémoire global (voir [03 §4.3](03-protocole.md#43-vérification-côté-pak)) |
| Modification d'une requête en transit | Méthode, chemin, paramètres et SHA-256 du corps sont signés |
| Morceaux d'envoi modifiés (non signés) | SHA-256 du fichier complet annoncé dans la requête de création, signée, et vérifié avant d'écrire la destination |
| Force brute de l'appairage | Secret QR de 256 bits ; PIN : 5 essais puis blocage de 30 s et nouveau PIN ; secret et PIN valables 10 min, à usage unique |
| Force brute hors ligne du PIN après écoute | Possible (10⁶ combinaisons). Le PIN est un mode de secours, signalé comme moins sûr dans l'app |
| Traversée de répertoire (`../`) | Identifiants opaques résolus et vérifiés côté serveur ; noms nettoyés |
| Remplir la carte SD | Contrôle de l'espace libre à la création d'un envoi ; tailles maximales |
| Téléchargement vers une adresse interne (SSRF) | Seul un client appairé peut lancer un téléchargement ; schémas `http`/`https` uniquement. Risque résiduel accepté |
| Deux clients qui modifient la carte en même temps (par exemple une suppression de jeu pendant qu'un autre client écrit dans ses données) | Un seul client à la fois tient le bail d'écriture (M10, décision du 19/11/2026) : les autres reçoivent `423 read_only` avant toute lecture du corps et sans effet ; la confirmation d'une suppression affiche toujours l'aperçu calculé par le pak (`GET /games/{gameId}/deletion`). **Risque résiduel accepté**, réduit à un client qui prend la main au milieu : une requête déjà acceptée de l'ancien détenteur va jusqu'au bout ; tout fichier supprimé ou remplacé reste récupérable 30 jours dans la corbeille |
| Un client appairé qui prend la main sans arrêt (`PUT /writer` avec `force`) et empêche les autres d'écrire | **Accepté** (décision du 19/11/2026) : tous les clients appairés sont de confiance. La console peut le déconnecter ou l'oublier (Réglages > Appareils) |
| Perte de données par erreur | Tout fichier supprimé ou remplacé part dans une corbeille de 30 jours ; copies automatiques avant toute opération destructive ; l'effacement définitif (vider la corbeille) exige une confirmation explicite (`X-RL-Confirm`) |
| Copie de la carte SD (retirée de la console, exFAT sans protection) | **Non couvert en v1** : `clients.json` contient les clés HMAC des iPhones, qui permettent de signer à leur place. Corrigé par M13 (clés Ed25519 : seules les clés publiques restent sur la carte) — constat du 19/11/2026 |
| Fuite par le journal | Jamais de clé, secret, PIN ni signature dans les journaux |

## 4. Bonnes pratiques d'implémentation

- Aléa : `crypto/rand` (Go) et `SecRandomCopyBytes` ou CryptoKit (Swift).
- Comparaisons de MAC en temps constant : `hmac.Equal` (Go), et CryptoKit côté Swift.
- Limites : corps JSON 1 Mio, morceau 16 Mio, `/hello` et `/pair` limités par IP.
- Délais du serveur HTTP : `ReadHeaderTimeout` 10 s, `IdleTimeout` 60 s ; pas de `ReadTimeout` global sur les routes d'envoi, mais un délai d'inactivité.
- Le serveur ne sert aucun fichier statique et n'expose aucune route de débogage en version publiée.

## 5. Évolutions possibles

- **TLS en option** : certificat auto-signé généré par le pak, empreinte placée dans le QR code, épinglée par l'app. À évaluer selon le débit mesuré au jalon M0 (préférer ChaCha20-Poly1305 si le processeur n'a pas d'instructions AES).
- **PAKE pour le PIN** (SPAKE2 ou équivalent) si le mode PIN s'avère beaucoup utilisé.
