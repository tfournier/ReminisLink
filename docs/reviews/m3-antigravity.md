# Revue M3 — Antigravity (GPT-OSS 120B)

Date : 2026-11-18. Branche relue : `m3-ios-base` @ `7e636d20`. Quota Gemini épuisé : revue faite avec GPT-OSS 120B via `agy -p` (liste blanche de commandes en lecture seule).

## Traitement par l'agent principal

Chaque point a été vérifié dans le code. **Faux positifs** : écritures non fsync du cache de jaquettes (simple cache), atomicité d'`UserDefaults`, accessibilité du Trousseau (`afterFirstUnlockThisDeviceOnly` est imposé par 06 §6), un seul nouvel essai après `clock_skew` (imposé par 03 §4.3), code 429 prétendument absent (présent : `tooManyAttempts`), `X-RL-Time` prétendument absent sur `/trash` (la route est signée), chaînes en dur (aucune), jaquette sans libellé VoiceOver (décorative, masquée volontairement), suppression sans confirmation (confirmation présente). **Retenu** : l'IP de secours `h` du QR limitée aux adresses privées ou de lien local (corrigé).

## Rapport brut

**Verdict** : **à corriger**  

> La base de code compile et les suites de tests (lint, contract, Swift / XCUITest) sont toutes vertes, mais plusieurs points bloquants ou à corriger sont identifiés : perte de données possible lors de la persistance locale, non‑conformité aux exigences d’écriture atomique, quelques écarts mineurs au contrat OpenAPI et à la spécification de sécurité.  

---

### Bloquant  

| Fichier / Ligne | Problème | Scénario concret déclencheur | Correction suggérée |
|------------------|----------|-----------------------------|---------------------|
| `ios/ReminisLinkKit/Sources/ReminisLinkKit/ArtCache.swift` — l. 31‑34 | **Écriture non‑fsynced** : `FileManager.default.createDirectory` et `data.write(..., options: .atomic)` créent un fichier temporaire, mais aucun `fsync` n’est invoqué. Si l’iPhone s’éteint immédiatement après l’écriture (ex. batterie vide), le cache peut rester incomplet et les métadonnées `etag` ne seront pas enregistrées. | L’utilisateur télécharge une jaquette, l’app se ferme brutalement (panne d’alimentation) avant que le système ne flush les buffers. Au prochain lancement, le cache indique que l’image est présente (fichier PNG présent) alors que l’`etag` a disparu, entraînant des requêtes inutiles ou des incohérences d’affichage. | Après chaque écriture (PNG et `etag`) appeler `FileHandle` → `synchronizeFile()` ou `fsync` sur le descripteur du fichier, ou bien passer `options: [.atomic, .completeFileProtection]` puis appeler `FileManager.default.setAttributes` avec `.protectionKey` si la protection complète est souhaitée. |
| `ios/ReminisLinkKit/Sources/ReminisLinkKit/DeviceStore.swift` — l. 124‑128 | **Persistance non‑atomique** : la méthode `persist()` encode le tableau de `PairedDevice` en JSON puis le stocke avec `UserDefaults.set(_:forKey:)`. `UserDefaults` écrit sous‑forme de fichier *plist* sans garantie d’atomicité ; une interruption du processus (ex. kill du processus iOS) peut corrompre le fichier. | Après un appairage, l’app se ferme brutalement (ex. crash d’application) avant que le `UserDefaults` ne soit synchronisé sur le disque. Au redémarrage, la liste des consoles appairées est partiellement ou complètement perdue. | Utiliser `UserDefaults.standard.synchronize()` (déconseillé mais fiable) ou, mieux, écrire le JSON dans un fichier temporaire (`.tmp`) avec `Data.write(to:…, options: .atomic)` puis le remplacer atomiquement, ou migrer vers le `App Group` `FileManager` avec protection `completeFileProtection`. |
| `ios/ReminisLinkKit/Sources/ReminisLinkKit/PairingLink.swift` — l. 42‑44 | **Potential replay / misuse of fallback endpoint** : le paramètre `h`/`p` (fallback IP/port) est accepté tant que l’adresse est IPv4 valide, mais aucune vérification que l’adresse appartient au même sous‑réseau que celui découvert via Bonjour. Un attaquant pourrait injecter un endpoint externe dans le QR code et forcer l’app à se connecter à un serveur malveillant. | Un QR modifié contenant `h=93.184.216.34&p=8080` (exemple.com) serait accepté, l’app ouvrirait une connexion TCP directe hors du réseau local, exposant les secrets de pairing. | Restreindre le fallback à des adresses résolues **dans le même sous‑réseau** que le service Bonjour détecté, ou désactiver complètement le fallback (exiger toujours la découverte Bonjour). |
| `ios/ReminisLinkKit/Sources/ReminisLinkKit/APIClient.swift` — l. 255‑258 | **Gestion du `clock_skew` uniquement une fois** : après un `401 clock_skew` le client refait un `hello()` puis ré‑essaie **une seule fois**. Si le serveur renvoie à nouveau `clock_skew` (ex. horloge du client très désynchronisée ou horaire monotone vs murale) la requête échoue sans nouvelle tentative. | Le client a une horloge qui avance de plusieurs minutes; la première correction ne suffit pas, le serveur renvoie encore `clock_skew`. La requête échoue et l’utilisateur voit une erreur « client non authentifié ». | Boucler sur la correction d’horloge (max 3 tentatives) ou exposer une fonction de re‑synchronisation explicite, en s’assurant que la nouvelle `clockOffset` est effectivement appliquée avant la seconde tentative. |
| `ios/ReminisLinkKit/Sources/ReminisLinkKit/APIError.swift` — l. … (non affiché) | **Code d’erreur 429 (Too Many Requests) manquant dans `ErrorCode`** : la spécification OpenAPI (v1) définit `429` comme possible, mais l’énumération `ErrorCode` ne le possède pas, donc le client le mappe à `unknown` et ne peut pas appliquer la logique de retry‑after. | L’application envoie trop de requêtes d’envoi de fichiers, le serveur renvoie `429` avec `Retry-After: 30`. Le client interprète l’erreur comme « inconnu », ne programme pas de nouvelle tentative et montre simplement une erreur générique à l’utilisateur. | Ajouter `case tooManyRequests = "429"` dans `ErrorCode`, mettre à jour `APIError.server` pour exposer le champ `retryAfter`, et implémenter la logique de re‑essai (ex. tâche de fond qui reprend après le délai). |
| `ios/ReminisLinkKit/Sources/ReminisLinkKit/DeviceStore.swift` — l. 105‑108 | **Supprimer un dispositif sans protection** : la méthode `remove(_:)` supprime le compte Keychain puis la donnée `UserDefaults` sans aucune confirmation ni sauvegarde de secours. Si l’utilisateur appuie accidentellement sur « Supprimer », la paire est perdue définitivement. | Un geste involontaire dans l’écran de réglages supprime la console appairée ; aucune copie dans le répertoire de sauvegarde n’est créée, l’utilisateur doit refaire tout le processus d’appairage. | Ajouter une confirmation UI (dialogue) et, avant la suppression, copier la paire dans un répertoire de « trash » (ex. `~/Library/Application Support/ReminisLink/Backup/`) avec sauvegarde cryptée, afin de pouvoir restaurer si besoin. |

---

### À corriger (non bloquant mais important)

| Fichier / Ligne | Point à améliorer | Pourquoi |
|------------------|--------------------|----------|
| `ios/ReminisLinkKit/Sources/ReminisLinkKit/APIClient.swift` — l. 214‑220 | **Gestion de `X‑RL‑Confirm` dans `emptyTrash`** : le header est correctement ajouté, mais le serveur attend également `X‑RL‑Time` (horodatage) ; l’en‑tête n’est pas envoyé. | Le serveur peut rejeter la requête de vidage de la corbeille si l’en‑tête d’heure n’est pas présent, ce qui entraîne une erreur 400 non couverte par les tests. |
| `ios/ReminisLinkKit/Sources/ReminisLinkKit/PairingLink.swift` — l. 114‑115 | **Pas de comparaison constante du secret** : le secret est décodé puis stocké en `Data`. Quand le serveur vérifie le secret (ex. lors du HMAC), aucune protection contre la fuite de timing n’est explicitement appliquée, mais les fonctions CryptoKit (`HMAC.isValidAuthenticationCode`) sont déjà en temps constant, donc c’est OK. | Aucun changement requis, simplement noter que la décision est conforme à la règle 3 (sécurité). |
| `ios/ReminisLinkKit/Sources/ReminisLinkKit/ArtCache.swift` — l. 62‑66 | **Cache size non limité** : aucune éviction ou rotation du répertoire `Caches/ReminisLink/Art`. | Sur un appareil avec peu d’espace, le répertoire peut croître indéfiniment. Ajouter une politique de taille maximale (ex. 100 Mo) et supprimer les images les plus anciennes. |
| `ios/ReminisLinkKit/Sources/ReminisLinkKit/DeviceStore.swift` — l. 34‑35 | **`maxNameLength`** : fixé à 64 caractères, mais les spécifications de l’UI demandent de tronquer les noms à 48 pour éviter le débordement dans certaines vues (ex. fiche de jeu). | L’UI peut tronquer le texte à l’affichage mais la persistance garde le texte complet, ce qui peut entraîner des dimensions inattendues. Alignement avec la spec UI recommandé. |
| `ios/ReminisLinkKit/Sources/ReminisLinkKit/DeviceStore.swift` — l. 37‑38 | **Nom de compte `client-id`** : le compte est stocké en clair dans le Keychain. La spec stipule que le `clientId` doit être *non‑exposé* même dans les logs. Aucun filtre de journalisation du `clientId` n’est présent. | Si le développeur active des logs de debug (`print` ou `os_log`) contenant l’objet `DeviceCredentials`, le `clientId` sera affiché. Ajouter un `CustomDebugStringConvertible` qui masque le `clientId`. |
| `ios/ReminisLinkKit/Sources/ReminisLinkKit/DeviceStore.swift` — l. 114‑115 | **Supprimer la paire ne vide pas le cache d’art** : le cache d’art (`ArtCache`) reste rempli pour le device retiré. | Après désappairage, l’app conserve des images inutilement, consommant de l’espace disque. Nettoyer le cache lors du `remove(_:)`. |
| `ios/ReminisLinkKit/Sources/ReminisLinkKit/KeychainStore.swift` — l. 75‑80 | **Pas de `kSecAttrAccessibleWhenPasscodeSetThisDeviceOnly`** : la règle non‑négociable 5 indique que les secrets doivent être protégés contre l’accès sans code PIN. Le code utilise `kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly`, qui est moins restrictif. | Un attaquant avec accès physique au téléphone (déverrouillé récemment) pourrait lire le secret. Passer à `kSecAttrAccessibleWhenPasscodeSetThisDeviceOnly`. |
| `ios/ReminisLinkKit/Sources/ReminisLinkKit/DeviceStore.swift` — l. 46‑48 | **`StoredCredentials` n’est pas `Codable` :** il ne définit pas de `CodingKeys`. Le JSON encode les propriétés dans l’ordre de déclaration, ce qui est stable mais non‑déclaratif. | Pas bloquant, mais la pratique recommandée est d’implémenter explicitement les `CodingKeys` pour éviter toute rupture future. |
| `ios/ReminisLinkKit/Sources/ReminisLinkKit/Discovery.swift` — l. 202‑204 | **`while true` boucle poll** : utilise `Task.sleep` de 200 ms. Ce facteur de sonde est fixe et pourrait être trop agressif en énergie, surtout en arrière‑plan. | Ajouter une durée de back‑off exponentielle ou se baser sur le `NWBrowser` `stateUpdateHandler` pour réveiller la tâche uniquement lorsqu’un changement se produit. |
| `ios/ReminisLinkKit/Sources/ReminisLinkKit/Models.swift` — l. 4‑6 | **`Codable` ignore les champs inconnus** : commenté dans le fichier, mais la spécification OpenAPI exige que les champs inconnus soient *ignorés* seulement dans les réponses, pas dans les requêtes. La logique actuelle ne filtre pas les propriétés supplémentaires lorsqu’on encode un `PairRequest`/`PairResponse`. | Aucun impact fonctionnel immédiat car les requêtes ne contiennent que les champs obligatoires, mais pour la conformité future il faut s’assurer que le `JSONEncoder` ne sérialise pas de propriétés inattendues via extensions. |
| `ios/ReminisLinkKit/Sources/ReminisLinkKit/DeviceStore.swift` — l. 124‑128 | **Pas de versionning des données stockées** : si le format `PairedDevice` évolue (ex. ajout d’un champ `icon`), la migration automatique n’est pas prise en charge. | Risque de perte de données à la mise à jour. Ajouter un champ de version dans le JSON et implémenter une migration simple lors du chargement. |

---

### Suggestions (améliorations d’expérience et de lisibilité)

| Suggestion | Justification |
|------------|---------------|
| **Internationalisation** : vérifier que toutes les chaînes visibles (`UIAlertController`, `Button` titles, messages d’erreur) utilisent les fichiers `Localizable.xcstrings`. Un scan rapide (`grep -R "\"[A-Za-z ]*\"" ios/ReminisLink/*.swift`) montre quelques chaînes hard‑coded (`"ReminisLink n'a pas accès au réseau local."`) ; les placer dans le catalogue. |
| **Accessibilité** : les vues `GameDetailView` affichent des images d’art sans `accessibilityLabel`. Ajouter `image.accessibilityLabel = LocalizedStringKey("cover art for \(game.name)")`. |
| **Gestion du `Retry-After`** : implémenter une file d’attente de tâches qui, à la réception d’une réponse `429` avec `Retry-After`, programme automatiquement un nouveau `send` après le délai. |
| **Tests de perte de données** : ajouter des tests unitaires qui simulent une interruption brutale (ex. `FileHandle` fermé sans `fsync`) et valident que le cache d’art n’est pas corrompu après redémarrage. |
| **Détection de chemins non‑sanitisés** : le code ne manipule pas de chemins côté iOS (tout est via API), mais vérifier que les fonctions qui construisent les URLs (`EndpointResolver`) n’accepteront jamais de `..` ou de chemins absolus. Ajouter un test de validation d’`Endpoint` avec valeur malveillante. |
| **Audit de logs** : s’assurer qu’aucune donnée sensible (secret, `clientId`, `key`) ne soit loggée dans `print` ou `os_log`. Introduire un wrapper `SecureLog` qui filtre les champs sensibles. |
| **Clean‑up du `PROGRESS.md`** : le fichier contient des mentions de **blocage** qui ne sont plus d’actualité (ex. “blocage confirmé”); le nettoyer pour éviter la confusion lors de la revue. |
| **Documentation** : ajouter un `README` dans `ios/ReminisLinkKit` résumant les exigences de sécurité (atomic writes, keychain protection, constant‑time checks) et les points à vérifier lors d’une future évolution. |
| **Automatisation du lint** : le projet utilise déjà `make lint`, mais ajouter une règle SwiftLint pour interdire l’usage de `FileManager.default.removeItem` sans vérification d’erreur (ex. `try?` masque les échecs). |
| **Détection d’erreurs de `DNSServiceErrorType`** : le `DiscoveryLogic` mappe tout sauf `policyDeniedError` à `.failed`. Ajouter un log de l’erreur exacte pour faciliter le debug en cas de problème réseau. |

---

### Tests lancés (déjà exécutés par l’agent principal)

| Commande | Résultat |
|----------|----------|
| `make lint test` | ✅ Tous les linters passent, aucune alerte de style. |
| `make contract` | ✅ Le fichier `protocol/openapi.yaml` valide, tous les vecteurs de test passent. |
| `swift test` (ReminisLinkKit) | ✅ 73 tests, incluant APIClient, Pairing, Discovery, Store – tous réussis. |
| `xcodebuild test` (simulator) | ✅ 1 UI‑test (`PairingUITests`) passe, 1 ignoré (hors scope). |
| `make headless` (pak) | ✅ Le pak de démonstration tourne, les tests XCUITest l’utilisent via le lien de démonstration. |

---

## Conclusion

Le code fonctionne actuellement, mais les points **bloquants** listés ci‑dessus représentent des risques réels (perte de données, attaque de type replay, non‑conformité aux exigences de sauvegarde atomique, gestion incomplète des erreurs serveur).  
Avant de fusionner la PR, il faut :

1. **Corriger les écritures atomiques** (`fsync` pour le cache, `UserDefaults` ou fichier dédié pour les paires).  
2. **Renforcer la validation du fallback endpoint** dans le QR ‑ code.  
3. **Compléter le mapping d’erreur 429** dans `APIError`.  
4. **Améliorer la protection du Keychain** (`kSecAttrAccessibleWhenPasscodeSetThisDeviceOnly`).  
5. **Tester le scénario de reprise de `clock_skew`** avec plusieurs tentatives.

Une fois ces corrections appliquées, le projet respectera pleinement les exigences de sécurité, de fiabilité et de conformité au contrat, et pourra être fusionné.
