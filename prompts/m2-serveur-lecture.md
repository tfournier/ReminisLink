# M2 — Serveur du pak en lecture seule (Go)

## À lire

`docs/specs/02-architecture.md`, `docs/specs/03-protocole.md` (§1, 2, 5, 6, 9), `docs/specs/04-api-v1.md` (sections Console et Bibliothèque), `docs/specs/05-console-pak.md` (§1 à §4, §6, §8, §9), `docs/specs/08-tests-qualite.md`. Code existant de `internal/auth` et `internal/library` (jalon M1).

## Livrables

1. **Squelette** `cmd/reminislink` :
   - options `--headless` (serveur + Bonjour, sans UI), `--desktop` (réservé à M7, peut afficher « not implemented »), `--sd <dossier>` (racine de carte SD, sinon variables d'environnement NextUI), `--port`, `--name` ;
   - configuration (`internal/store`, `config.json` : `deviceId` créé au premier lancement, nom, port) ;
   - journal `slog` comme Cheat Manager ;
   - arrêt propre sur SIGINT/SIGTERM (`503 shutting_down`, attente 10 s, retrait de l'annonce).
2. **Serveur** `internal/server` : routeur `net/http` avec motifs Go 1.22+, middlewares (journal sans secrets, récupération de panique, limite de corps 1 Mio, auth de M1, limitation de débit sur `/hello` et `/pair`), encodage JSON des erreurs selon `03 §6`, délais du serveur de `07 §4`.
3. **Découverte** `internal/discovery` : annonce `_reminislink._tcp` avec l'enregistrement TXT complet de `03 §2`. Bibliothèque mDNS autorisée par `CLAUDE.md`. Choix du port : 47821 puis les 9 suivants.
4. **Routes** : `GET /hello`, `POST /pair` (avec la `PairingSession` de M1 ; en mode headless, affiche le lien `reminislink://pair?...` et le PIN dans le terminal, et un QR code ASCII si la bibliothèque QR le permet), `DELETE /clients/me`, `GET /device`, `GET /systems`, `GET /systems/{systemId}/games`, `GET /games/{gameId}`, `GET /games/{gameId}/art`.
5. **Adaptateur** `internal/osadapter/nextui` : lecture des systèmes, des jeux et de leurs fichiers selon le tableau de `05 §3.1`. Réutilise la logique de `ListSystems` / `ListEntries` de Cheat Manager (copie avec mention d'origine). Comparaisons insensibles à la casse. Pour chaque point encore marqué **[À VÉRIFIER M0]**, une constante nommée et un `TODO(M0)`.
6. **Informations console** `internal/device` : stockage (`statfs`), IPv4, modèle et plateforme depuis l'environnement ; batterie et version de l'OS selon les relevés M0, sinon `null`.
7. **Make** : cibles `headless` (lance le pak sur une copie temporaire de `testdata/sdcard`) et mise à jour de `make test`.

## Tests

- Intégration `httptest` pour chaque route, avec la carte SD factice copiée dans `t.TempDir()` et un client signé (utilitaire de test réutilisable par les jalons suivants : `internal/testutil`).
- Cas d'erreur : identifiant invalide, jeu absent, requête non signée, client inconnu.
- Performance : `GET /systems/{id}/games` sur un dossier factice de 500 jeux sous 300 ms en local (test marqué `-short` exclu si trop lent en CI).
- Les réponses sont validées contre `protocol/openapi.yaml`.

## Hors périmètre

Toute écriture sur la carte SD autre que `config.json` et `clients.json`. L'interface gabagool (M7).

## Fait quand

- `make lint test` passe.
- `make headless` démarre, s'annonce (vérifiable avec `dns-sd -B _reminislink._tcp` sur Mac), et un appairage manuel suivi d'un `GET /systems` signé fonctionne (fournis un petit script `scripts/smoke.sh` qui le fait avec `curl` et `openssl`).
