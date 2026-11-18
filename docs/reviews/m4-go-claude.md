# Revues M4 Go — agents Claude neufs

## Revue complète (`59f5bf69`) — à corriger
- **Bloquant (confirmé)** : après une coupure de courant, l'état SHA-256 sauvegardé pouvait être en avance sur les données réellement écrites du `.part` → un fichier corrompu était installé avec un contrôle réussi (pour une save : bonne version en corbeille, version corrompue installée).
- À corriger : purge de la corbeille selon l'horloge de la console, URL complète (jeton) dans le journal, spec contradictoire sur la survie des envois, `onConflict` ignoré pour save/state, entrée de corbeille non recomptée après annulation partielle.
- Suggestions : `DeleteGame` hors verrou d'installation, bornes des envois et tâches, noms (point initial, Unicode Cf, noms Windows), staging `.tmp`, ROM sans extension, annulation pendant l'installation, `keepSave` et verrous.

## Revue ciblée n° 1 (`a9129406`) — à corriger, aucun bloquant
- Garde d'horloge : entrées datées trop tôt après une horloge en retard ; saut aberrant enregistré qui bloque la purge.
- Recalcul du hash bloquant la requête (> 15 s côté app pour > 1 Gio).
- Contrôle des noms de ROM seulement pour les téléchargements ; bornes absentes du contrat.

## Revue ciblée n° 2 (`6c1243d4`) — à corriger
- **Bloquant (confirmé)** : une horloge fausse mais plausible (heure figée puis NTP, faute de frappe sur l'année, date erronée persistante) faisait encore purger des entrées récentes. Escaladé à la personne → décision : purge par **âge crédité** (05 §4).
- À corriger : candidat périmé bloquant la purge, `expiresAt` faux, `Save(Config)` faisant reculer `lastSeenTime`, écrivain initial perdant les champs inconnus, échec de recalcul définitif.

## Aller-retour 3 (`07ff8174`)
Points hors purge corrigés (propriétaire unique de `config.json`, recalcul réessayable, entrée illisible jamais purgée, drapeaux) ; envois qui survivent au redémarrage et `keepSave` qui garde verrous et historique (décisions de la personne) ; garde d'horloge remplacée par `CreditClock` (âge crédité, monotone en session, ≤ 7 jours par extinction, aucun crédit après arrêt brutal). Relu par l'agent principal ; `make lint test`, `make contract`, `go test -race`, `smoke-transfers.sh` OK.
