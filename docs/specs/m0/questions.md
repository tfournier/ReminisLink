# M0 — Questions ouvertes

Rempli par l'agent d'analyse pour chaque point **[À VÉRIFIER M0]** que l'analyse n'a pas permis de trancher. Source des faits : [source-analysis.md](source-analysis.md) (code NextUI au commit `b6ec1343`, firmware TrimUI v1.1.1). Les manipulations sur la console sont regroupées dans la procédure réduite de [README.md](README.md).

Format :

```markdown
## <Point à vérifier> (spec et section concernées)

- Ce que montrent les relevés :
- Ce qui manque :
- Manipulation à faire sur la console pour trancher :
- Hypothèse prudente retenue en attendant :
```

## Veille et Wi-Fi pendant un pak gabagool (05 §2, « Mise en veille »)

- Ce que montrent les relevés : le code de NextUI ne met jamais la console en veille pendant un pak (la veille passe par `PWR_update()`, appelé seulement par `nextui.elf` et `minarch` ; `keymon` ne gère pas le bouton d'alimentation). gabagool v2.26.6 ne contient aucune gestion de veille, d'écran ou du bouton d'alimentation. Le script `bin/suspend` coupe `wpa_supplicant` avant la veille.
- Ce qui manque : l'effet réel du bouton d'alimentation (appui court, appui long) quand un pak gabagool tourne (le noyau ou le PMIC peuvent agir seuls, par exemple extinction forcée sur appui très long) ; le maintien de `wlan0` plusieurs minutes sans interaction (économie d'énergie du pilote Wi-Fi).
- Manipulation à faire sur la console pour trancher : procédure réduite, étape 3 (ping depuis le Mac pendant 10 minutes avec Cheat Manager ouvert, puis appui court et appui long sur le bouton d'alimentation).
- Hypothèse prudente retenue en attendant : la console ne se met pas en veille seule pendant le pak ; le pak ne déclenche aucune veille lui-même. Voir la décision D2 ci-dessous.

## Coexistence du répondeur mDNS avec avahi en Wi-Fi réel (02, « Choix techniques », ligne mDNS)

- Ce que montrent les relevés : `avahi-daemon` 0.6.31 démarre avec le firmware d'origine (`/etc/rc.d/S10dbus`, `S61avahi-daemon`) et écoute UDP 5353. Essai du 19/11/2026 par l'agent principal, dans le rootfs v1.1.1 en conteneur Docker linux/arm64 : avahi lancé (D-Bus désactivé dans une copie de sa configuration), puis le pak `--headless` (binaire Go statique) ; le pak s'annonce (`_reminislink._tcp`), un client zeroconf du même conteneur le trouve avec son TXT complet, avahi reste actif. Coexistence sur le même hôte : OK. Ce n'est plus une décision, seulement une vérification.
- Ce qui manque : le multicast réel sur `wlan0` et la découverte depuis un iPhone (et depuis le Mac).
- Manipulation à faire sur la console pour trancher : procédure réduite, étape 4, dès qu'une version du pak est installable sur la console : vérifier que avahi tourne (`ps | grep avahi`), lancer le pak, puis `dns-sd -B _reminislink._tcp` sur le Mac et la découverte dans l'app.
- Hypothèse prudente retenue en attendant : on garde le répondeur `zeroconf` intégré (décision actuelle). La publication par avahi (D-Bus) ne serait étudiée que si l'essai réel échoue.

## RTC console éteinte (05 §4, « Âge crédité »)

- Ce que montrent les relevés : RTC matérielle `/dev/rtc0` lue au démarrage par `sysfixtime` ; l'extinction de NextUI (`poweroff_next`) ne réécrit pas l'heure dans la RTC ; NTP (busybox `ntpd` via `sysntpd`) seulement avec le Wi-Fi, sans recopie dans la RTC. Dans la configuration d'origine, la clé uci est `enable 1` alors que `sysntpd` lit `enabled` (défaut 1) : l'option « Network time sync » de NextUI n'a probablement pas d'effet sur `sysntpd`.
- Ce qui manque : savoir si la RTC continue d'avancer console éteinte (pile ou supercondensateur), et si `ntpd` tourne réellement.
- Manipulation à faire sur la console pour trancher : procédure réduite, étapes 2 et 5 (le script relève `date`, `hwclock -r`, les processus `ntpd` ; éteindre Wi-Fi coupé, attendre quelques heures, rallumer et relancer le script).
- Hypothèse prudente retenue en attendant : l'horloge murale peut être fausse au lancement du pak ; la borne de 7 jours de l'âge crédité est maintenue (aucun changement).

## Points à reconfirmer sans mention dans les specs

Ils sont tranchés par le code (confiance haute ou moyenne) et intégrés aux specs ; le script les relève pour confirmation.

- **Séparateur de `.system/version.txt`** (05 §6, `osVersion`) : `echo "…\n…"` dans le makefile de NextUI donne un vrai retour à la ligne ou un `\n` littéral selon le shell de la CI. La spec demande une lecture qui accepte les deux. Le script affiche `od -c` du fichier.
- **`axp2202-battery/status`** (05 §6, `battery`) : NextUI ne l'utilise pas ; la spec suit la méthode de NextUI (`axp2202-usb/online` et `time_to_full_now`). Le script relève les fichiers de `axp2202-*`, sur batterie (relevé 1) et sur secteur (relevé 2).
- **Vignette** : signature PNG et taille 1024×768 d'un `.minui/<TAG>/*.0.bmp` (le script affiche les 24 premiers octets : signature et en-tête IHDR).
- **Dossiers `<TAG>-<cœur>`, `.st9` et `minuisettings.txt`** : présents après la partie de l'étape 1.

---

# Décisions à prendre

Ces faits changent une décision. Rien n'a été modifié dans l'architecture : la personne choisit, puis la spec est mise à jour.

## D1 — Certificats racine *(tranché le 19/11/2026 : aucune nouvelle dépendance ; gabagool, déjà autorisé, importe `certifiable` qui embarque 143 certificats racine et les installe dans `http.DefaultTransport` — c'est ainsi que le Pak Store télécharge en HTTPS ; les téléchargements du pak partent de `http.DefaultTransport.Clone()`. Vérifié : la base NSS `/etc/pki/nssdb/cert9.db` du firmware est vide.)*

*Sans objet depuis le 20/11/2026 : le pak ne télécharge plus rien (téléchargement depuis un lien retiré de la v1, voir [09](../09-feuille-de-route.md#après-la-v1-idées-non-engagées)).*

### Détail pour les téléchargements HTTPS (05 §5.6)

Fait : le firmware v1.1.1 n'a aucun magasin de certificats (pas de `/etc/ssl`, aucun `.crt`/`.pem`) et NextUI n'en fournit pas pour tg5040. Le client HTTP de Go ne trouvera rien. Cheat Manager fonctionne parce que gabagool importe `github.com/BrandonKowalski/certifiable`, qui embarque `certificates.crt` (`go:embed`) et le pose dans `http.DefaultTransport` à l'initialisation. Dans ReminisLink, ce code n'est lié que si `internal/ui` l'est : pas en `--headless`, pas dans les tests `CGO_ENABLED=0` de `internal/transfer`, et le client de 05 §5.6 devrait cloner `http.DefaultTransport` (et non en créer un neuf) pour en profiter.

Options :
1. **Dépendre directement de `certifiable`** (déjà présent indirectement via gabagool). Simple, même magasin que Cheat Manager. Dépendance hors de la liste de `CLAUDE.md` (à autoriser) ; effet de bord global sur `http.DefaultTransport` ; mise à jour du magasin liée à ses versions.
2. **Embarquer notre propre bundle PEM** (`go:embed` dans `internal/transfer`, par exemple le bundle Mozilla publié par curl) et construire un `x509.CertPool` pour notre seul client. Pas de dépendance ni d'effet global ; un fichier à mettre à jour (cible Makefile) et sa licence (MPL 2.0) à mentionner.
3. **Fichier dans le pak** (`resources/certs/ca-certificates.crt`) et `export SSL_CERT_FILE=…` dans `launch.sh` (Go le lit sous Linux). Magasin remplaçable sans recompiler ; ne marche que lancé par `launch.sh` (en mode desktop, magasin du système).
4. **Compter sur l'import de gabagool** sans rien ajouter : fragile (ordre d'initialisation, `--headless` sans HTTPS), déconseillé.

## D2 — Veille pendant le pak *(tranché le 19/11/2026 par la personne : comportement normal de la console, rien à gérer — l'absence d'interaction sur la console ne veut pas dire qu'aucun fichier n'arrive de l'iPhone ; NextUI ne met pas la console en veille pendant un pak.)*

### Détail (05 §2)

Fait : ni NextUI ni gabagool ne mettent la console en veille pendant un pak ; empêcher la veille pendant un transfert est donc inutile. Mais un pak laissé ouvert garde l'écran allumé et vide la batterie, et une veille par `bin/suspend` couperait le Wi-Fi.

Options (combinables, sauf la 1) :
1. **Ne rien gérer** : la console reste allumée tant que le pak est ouvert ; on sort avec B. Le plus simple ; risque de batterie vide.
2. **Éteindre l'écran (rétroéclairage) après un délai sans action**, serveur maintenu, réveil par une touche. Économise la batterie sans couper le Wi-Fi ; mécanisme à étudier (`/sys/class/backlight` ou réglage de luminosité de NextUI).
3. **Gérer le bouton d'alimentation** : refus (avec message) pendant un transfert ou une tâche, sinon fermeture propre du pak (ou veille par `bin/suspend` après retrait de l'annonce).
4. **Fermeture automatique** après N minutes sans requête, transfert ni touche.

## D3 — Formats *(tranché le 19/11/2026 : format par défaut seulement en v1 ; autres formats : écritures refusées en `422` — déjà appliqué en M5.)*

### Détail de save et de state non par défaut (05 §3.1, 04 States)

Fait : `minuisettings.txt` peut fixer `saveFormat` (1 à 3 : `.srm` rzip, `<rom>.sav`, `.srm`), `stateFormat` (1 à 4 : `.state.<N>`, ou `.state`/`.state<N>`, reprise `.state.auto`, certains compressés rzip) et `useExtractedFileName`. Le dossier des states dépend aussi du cœur (`<TAG>-<cœur>`), qui change si l'utilisateur change de pak émulateur.

Options :
1. **Défauts seulement** : le pak lit `minuisettings.txt` ; si un format non par défaut est actif, il l'indique (écran et `/device`) et les fonctions saves/states passent en lecture seule ou sont désactivées.
2. **Tous les formats** dans l'adaptateur : nommage selon le réglage, fichiers copiés tels quels (la compression rzip ne gêne ni la copie de sécurité ni la restauration), reprise `.state.auto` exposée comme emplacement 9 (`resumeSlot = 9` toujours).
3. **Défaut complet + autres formats en lecture seule** (liste et téléchargement, pas de verrou ni de restauration).

À trancher aussi : quel dossier `<TAG>-<cœur>` utiliser quand il y en a plusieurs (celui du pak émulateur actif, lu dans `EMU_EXE=` de son `launch.sh`, ou tous), et si les fichiers d'un format inactif doivent apparaître.

## D4 — Format de `osVersion` *(tranché le 19/11/2026 : chaîne de release NextUI, ex. `NextUI-20261013-039a06de`.)*

### Détail (04 `/hello` et `/device`, 03 §2 TXT `osv`, `protocol/openapi.yaml`)

Fait : `.system/version.txt` contient `NextUI-AAAAMMJJ-N` et un hash, jamais `v6.x.y`. Les exemples `6.14.0` de la spec et du contrat ne correspondent à rien de lisible sur la console.

Options : `NextUI-20261013-0` seul ; `NextUI-20261013-0 (<hash>)` ; en plus, la version du système d'origine (`/etc/version`, ex. `1.1.1`) dans un champ séparé. Le contrat (`openapi.yaml`, exemples) et 03 §2 sont à aligner une fois choisi (le TXT `osv` doit rester court et ASCII).

## D5 — Taille des vignettes *(tranché le 19/11/2026 : servies telles quelles, réduites par l'app — déjà appliqué.)*

### Détail de state (04 `GET /games/{gameId}/states/{slot}/thumbnail`)

Fait : la vignette est une capture plein écran PNG de 1024×768 (plus en HDMI), pas une miniature.

Options : servir le fichier tel quel (simple, l'app réduit) ; réduire côté pak (décodage PNG en Go pur, coût CPU sur la console, cache éventuel) ; ajouter un paramètre de taille (changement du contrat).

## D6 — Systèmes que NextUI n'affiche pas *(tranché le 19/11/2026 : tous les dossiers à tag listés, avec un champ `emulatorInstalled`.)*

### Détail (04 `/systems`)

Fait : NextUI n'affiche un dossier de `Roms/` que si un pak émulateur `<TAG>.pak` existe, et fusionne à l'affichage les dossiers de même préfixe (`Game Boy Advance (GBA)` et `(MGBA)`).

Options : lister tous les dossiers avec un tag (actuel) ; ne lister que ceux qu'affiche NextUI ; tout lister avec un champ indiquant si NextUI les affiche (changement du contrat). La fusion d'affichage resterait côté app ; les données restent séparées par tag.
