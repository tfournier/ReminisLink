# Revue M4 Go — Antigravity (GPT-OSS 120B)

Branche relue : `m4-transferts-go` @ `59f5bf69`. Verdict « prêt à fusionner » ; n'a pas vu le bloquant du hash trouvé par la revue Claude. Ses points « à corriger » (vérification du bundle CA, erreur de `os.Rename` du `.hash`, retours en arrière multiples, fsync avant rename) sont couverts ou devenus sans objet (le `.hash` a été supprimé, le `.part` est synchronisé avant l'installation).

## Rapport brut

**Verdict :** prêt à fusionner  

---

### Bloquant  
*(aucun blocage identifié ; aucun scénario ne provoque perte de données, faille de sécurité ou écart au contrat.)*  

---

### À corriger  

| Fichier / Ligne | Problème | Scénario déclencheur | Correction proposée |
|-----------------|----------|----------------------|--------------------|
| `internal/transfer/download.go:42‑44` | **TODO(M0)** : le commentaire indique qu’il faudrait vérifier l’existence de `DefaultCABundle` sur la console, mais aucune vérification n’est implémentée. | À l’exécution sur la console, l’absence du bundle pourrait entraîner l’échec du chargement du CA et donc des téléchargements HTTPS. | Ajouter une vérification (ex. : `if _, err := os.Stat(DefaultCABundle); err != nil { … }`) et fournir un fallback ou un message d’erreur clair. |
| `internal/transfer/uploads.go:390‑393` | Le hash‑state file est écrit sans vérifier l’erreur de `os.Rename`. Si le rename échoue, le fichier `.hash.tmp` reste en place et l’état peut devenir incohérent. | Crash ou problème de disque empêchant le renommage. | Vérifier l’erreur de `os.Rename` et, en cas d’échec, restaurer le fichier précédent ou consigner une erreur fatale. |
| `internal/store/trash.go:286‑324` | En cas d’échec du `os.Rename` : le code remet les fichiers déplacés en arrière, mais si `restoreItem` échoue pour plusieurs fichiers, les chemins restent dans le trash et sont loggés en **error**. | Power‑loss ou permission / E/S insuffisante pendant le rollback. | Ajouter une boucle de retry ou marquer l’entrée comme « partial » afin que la prochaine purge la traite. |
| `internal/transfer/service.go:312‑319` | Dans `installROM` le `os.Rename(tmp, dst)` n’est pas suivi d’un `fsync` du fichier source; seul le répertoire est synchronisé. | Coupure d’alimentation juste après le `rename` pourrait laisser le fichier incomplet sur le disque (déjà écrit dans le tmp). | Après le `Rename`, appeler `file.Sync()` sur le fichier temporaire avant de le renommer ou utiliser `WriteFileAtomic` pour la copie finale. |

> **Note** : les points ci‑dessus sont des améliorations de robustesse ; ils ne sont pas bloquants car le code actuel garantit déjà l’intégrité via les vérifications de SHA‑256 et les mécanismes de rollback.

---

### Suggestions  

| Sujet | Suggestion |
|-------|------------|
| **Documentation** | Ajouter dans `README.md` ou dans la spec un rappel que le service purge les fichiers `uploads/*.part` et `uploads/dl_*.part` après `UploadIdleExpiry` / `DownloadIdleExpiry`. |
| **Logs** | S’assurer que les logs d’erreur (`log.Error`) ne contiennent pas la valeur brute du SHA‑256 ou d’autres secrets. Un filtre de masquage : `log.With(slog.Int64("hash_len", len(hash)))`. |
| **Tests additionnels** | - Ajouter un test qui simule une interruption pendant `installROM` (crash après `Rename` mais avant `SyncDir`) et vérifie que le fichier final n’est pas corrompu. <br> - Couvrir le cas où `LoadRootCAs` ne trouve ni store ni bundle et s’assure que l’erreur retournée est correctement propagée. |
| **Sécurité des noms** | La fonction `CleanFileName` est utilisée, mais il serait utile d’ajouter une vérification explicite contre les caractères non‑ASCII ou les caractères réservés sur exFAT afin d’éviter les problèmes de compatibilité. |
| **Performance** | Dans `WriteChunk`, le `hash` est mis à jour à chaque écriture. Pour de très gros fichiers, envisager un buffer de 64 KB (déjà le `ChunkSize` par défaut) pour réduire les appels système. |
| **Code style** | Toutes les fonctions publiques sont commentées ; cependant, `withoutPathErr` (appelée dans `trash.go` ligne 326) n’est pas décrite dans le fichier. Ajouter un commentaire. |
| **Conformité au contrat** | Le mapping `CodeInvalidID` → `invalid_id` correspond bien à `protocol/openapi.yaml`. Vérifier que toutes les nouvelles constantes d’erreur (`CodeStateLocked`, `CodeChecksumMismatch`, etc.) apparaissent bien dans le fichier OpenAPI. |

---

### Tests lancés  

| Commande | Résultat |
|----------|----------|
| `make lint` | **OK** (aucune violation de `gofmt`/`go vet`). |
| `make test` | **OK** (tous les tests unitaires passent). |
| `make contract` | **OK** (validation du `protocol/openapi.yaml` et des vecteurs de test). |
| `go test -race ./...` | **OK** (aucune data race détectée). |
| `scripts/smoke-transfers.sh` | **OK** (scénarios d’upload complet, reprise, offsets incorrects, SHA‑256 erroné, espace insuffisant, conflits, téléchargement via `httptest.Server`, annulation, suppression d’un jeu simple et multi‑disques, purge). |

Toutes les suites passent, aucune erreur n’a été rencontrée.

---

**Conclusion**  
Le code implémente correctement les exigences de perte de données, de sécurité, de conformité au contrat et de concurrence. Les points à corriger sont mineurs et concernent surtout la robustesse de certaines situations d’erreur extrême. Après les petites améliorations suggérées, la PR peut être fusionnée.
