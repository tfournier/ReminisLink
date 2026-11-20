# ReminisLink — Privacy Policy / Politique de confidentialité

[English](#en) · [Français](#fr)

Published version / Version publiée : <https://reminis.link/privacy/> ·
<https://reminis.link/fr/privacy/>. Keep this file and `site/privacy/`,
`site/fr/privacy/` identical / Garder ce fichier et les pages du site
identiques.

Last updated / Dernière mise à jour : 20 November 2026 / 20 novembre 2026

---

<a id="en"></a>

## English

ReminisLink is an iPhone app that manages the games of your retro handheld
over your local Wi-Fi network. Your data stays on your iPhone, on your
handheld and in your own iCloud account (copies of your saves in iCloud
Drive, pairing in iCloud Keychain). The only exception is a download from a
link, which the handheld makes itself (see below).

### What we collect

**Nothing.** ReminisLink has no account, no server, no analytics, no
advertising, no tracking and no third-party SDK. The developer receives no
data about you or about how you use the app.

### On your iPhone

- **The preferences of the app**, and the list of handhelds paired with an
  older version that must be paired again.
- **A cache of box art** shown in the library, which iOS may clear at any
  time.
- **Files and links you share to ReminisLink** from another app: the share
  extension copies them into storage shared with the app until you send them.
  The app deletes them once they are used, or when they are more than 7 days
  old.
- **Temporary copies of the files you send**, kept until the transfer ends,
  and deleted at the next launch of the app at the latest.
- **Files you download** from your handheld (a file of a game, a file of the
  SD card) or from your save copies: written to a temporary folder of the
  app, then saved only where you choose (Files, AirDrop, another app). The
  temporary folder is emptied at each launch of the app.

Deleting the app removes all of this. It does not remove the copies of your
saves in iCloud Drive (see below), and iOS may keep the pairing in the
Keychain after the app is deleted: unpair your handhelds in the app first if
you want it removed.

### Pairing of your handhelds (iCloud Keychain)

To pair, the app creates its own identity: an identifier and a private
signing key. For each paired handheld, it keeps the identifier, name and
model of the handheld, the name you gave it, the fingerprint of its
certificate’s public key and its last address on your local network, and the
name you chose to show on your handhelds.

All of this is kept in the iOS Keychain, readable only by ReminisLink and its
share extension, and synced by **iCloud Keychain** to your other Apple
devices signed in with the same Apple Account, so that they share one
pairing. iCloud Keychain is Apple’s service, end-to-end encrypted, under your
Apple account and Apple’s privacy policy. If iCloud Keychain is off, these
items stay on this device. The developer never receives them.

Unpairing a handheld in the app removes the pairing from the handheld and
from all these devices. The identity itself is kept, so that your other
pairings keep working.

### Copies of your saves (iCloud Drive)

ReminisLink can copy the saves of your handhelds (with their clock files) to
your iPhone: with the **Sync Saves** button of a handheld, and automatically
when it connects (you can turn this off in the settings of the app). Up to 10
versions of each save are kept. Next to the copies, small files record the
identifier and name of the handheld, the identifier, name and system of the
game, and for each version its number, date, size and checksum.

The copies go to **your own iCloud Drive**, in the ReminisLink folder of the
Files app, where you can see, move or delete them. iCloud is Apple’s service,
under your Apple account and Apple’s privacy policy: the developer has no
server and never receives these copies. When iCloud Drive is not available to
the app (not signed in, or iCloud Drive turned off for ReminisLink), the
copies stay on your iPhone (Files > On My iPhone > ReminisLink), are not
moved to iCloud Drive later, and are deleted with the app.

### Between the app and your handheld

ReminisLink talks only to handhelds on your local network, found with
Bonjour, over HTTPS (encrypted). The app accepts only the certificate whose
public-key fingerprint is in the QR code you scanned.

- **Pairing.** The QR code holds the identifier and name of the handheld, a
  one-time secret, its local address and port, and that fingerprint. The app
  looks for the handheld with Bonjour, or else connects to the address in the
  QR code. Two requests are not signed: the one that checks that the handheld
  is there and open for pairing, and the pairing request, which instead
  proves that the app scanned the QR code. The app then sends the name you
  chose (by default the model of the device, such as “iPhone”), the
  identifier and public key of the app, and the platform (Apple).
- **After pairing**, every other request is signed with the private key of
  the app, and carries the type of device (iPhone, iPad or Mac).
- **The files you choose to send**: games, saves, save states, patches, cheat
  files, game images and apps for the handheld.

### On the SD card of your handheld

- **The paired devices** (`clients.json`): for each one, its identifier,
  name, platform, the types of device seen, its public key (which cannot be
  used to sign) and the date of pairing. You can remove a device in Settings
  > Devices on the handheld.
- **The log of ReminisLink** (`/.userdata/tg5040/logs/`): what the handheld
  did, with for each request the time, the action, the result, the identifier
  of the device and its address on your local network, plus the names of
  devices when they pair, and file names. Never a key, a secret or a
  signature. At launch, a log larger than 5 MB is renamed, replacing the
  previous old log.
- **Your files**, where NextUI keeps them, and in the ReminisLink folder
  (`/.userdata/shared/ReminisLink/`): backup copies of saves, locked copies
  of save states, the trash (deleted or replaced files, removed after 30 days
  counted by the handheld, or when you empty it), and temporary files of
  transfers.
- **The certificate of the handheld** and its private key.

The SD card has no protection of its own: anyone holding it can read these
files.

### Downloads from a link

When you type or share a link, the app sends it to your handheld, which
downloads the file itself (a game, a patch, a cheat file or an app for the
handheld), from its own connection. The server of the link receives the
request of the handheld, as for any download: the full address of the link
(with its path and parameters), the public IP address of your network, and
“ReminisLink” as the name of the program. The app itself does not contact
that server.

### Permissions

- **Local network**: to find your handheld and talk to it.
- **Camera**: only to scan the QR code shown by your handheld. No picture is
  taken or saved.

The app does not ask for access to your photo library: when you choose a
picture for a game, the iOS picker gives it only the picture you pick.

### Children

ReminisLink collects no data from anyone, children included.

### This website

reminis.link uses no cookies, no analytics, no trackers and loads nothing
from other sites. It is hosted on GitHub Pages, which may keep technical
logs of visits (such as IP addresses) for security, under GitHub’s own
privacy statement.

### Changes

Any change to this policy will be published on this page, with a new date.

### Contact

Open an issue on the GitHub repository of the project:
<https://github.com/tfournier/ReminisLink/issues>.

---

<a id="fr"></a>

## Français

ReminisLink est une app iPhone qui gère les jeux de ta console rétro par ton
réseau Wi-Fi local. Tes données restent sur ton iPhone, sur ta console et
dans ton propre compte iCloud (copies de tes sauvegardes dans iCloud Drive,
appairage dans le trousseau iCloud). Seule exception : un téléchargement
depuis un lien, que la console fait elle-même (voir plus bas).

### Ce que nous collectons

**Rien.** ReminisLink n’a ni compte, ni serveur, ni mesure d’audience, ni
publicité, ni pistage, ni SDK tiers. Le développeur ne reçoit aucune donnée
sur toi ni sur ton utilisation de l’app.

### Sur ton iPhone

- **Les préférences de l’app**, et la liste des consoles appairées avec une
  ancienne version qui doivent être appairées de nouveau.
- **Un cache des jaquettes** affichées dans la bibliothèque, qu’iOS peut
  vider à tout moment.
- **Les fichiers et liens que tu partages vers ReminisLink** depuis une autre
  app : l’extension de partage les copie dans un stockage partagé avec l’app
  jusqu’à leur envoi. L’app les supprime une fois utilisés, ou quand ils ont
  plus de 7 jours.
- **Des copies temporaires des fichiers que tu envoies**, gardées jusqu’à la
  fin du transfert, et supprimées au plus tard au lancement suivant de l’app.
- **Les fichiers que tu télécharges** depuis ta console (un fichier d’un jeu,
  un fichier de la carte SD) ou depuis tes copies de sauvegardes : écrits
  dans un dossier temporaire de l’app, puis enregistrés seulement là où tu le
  choisis (Fichiers, AirDrop, une autre app). Le dossier temporaire est vidé
  à chaque lancement de l’app.

Supprimer l’app efface tout cela. Cela n’efface pas les copies de tes
sauvegardes dans iCloud Drive (voir plus bas), et iOS peut garder l’appairage
dans le trousseau après la suppression de l’app : désappaire d’abord tes
consoles dans l’app si tu veux qu’il disparaisse.

### Appairage de tes consoles (trousseau iCloud)

Pour s’appairer, l’app crée sa propre identité : un identifiant et une clé
privée de signature. Pour chaque console appairée, elle garde l’identifiant,
le nom et le modèle de la console, le nom que tu lui as donné, l’empreinte de
la clé publique de son certificat et sa dernière adresse sur ton réseau
local, ainsi que le nom que tu as choisi d’afficher sur tes consoles.

Tout cela est gardé dans le trousseau iOS, lisible seulement par ReminisLink
et son extension de partage, et synchronisé par le **trousseau iCloud** avec
tes autres appareils Apple connectés au même compte Apple, pour qu’ils
partagent un seul appairage. Le trousseau iCloud est le service d’Apple,
chiffré de bout en bout, sous ton compte Apple et la politique de
confidentialité d’Apple. Si le trousseau iCloud est désactivé, ces éléments
restent sur cet appareil. Le développeur ne les reçoit jamais.

Se désappairer d’une console dans l’app retire l’appairage de la console et
de tous ces appareils. L’identité elle-même est gardée, pour que tes autres
appairages continuent de fonctionner.

### Copies de tes sauvegardes (iCloud Drive)

ReminisLink peut copier les sauvegardes de tes consoles (avec leurs fichiers
d’horloge) sur ton iPhone : avec le bouton **Synchroniser les saves** d’une
console, et automatiquement à chaque connexion (désactivable dans les
réglages de l’app). Jusqu’à 10 versions de chaque sauvegarde sont gardées. À
côté des copies, de petits fichiers notent l’identifiant et le nom de la
console, l’identifiant, le nom et le système du jeu, et pour chaque version
son numéro, sa date, sa taille et sa somme de contrôle.

Les copies vont dans **ton propre iCloud Drive**, dans le dossier ReminisLink
de l’app Fichiers, où tu peux les voir, les déplacer ou les supprimer. iCloud
est le service d’Apple, sous ton compte Apple et la politique de
confidentialité d’Apple : le développeur n’a aucun serveur et ne reçoit
jamais ces copies. Quand iCloud Drive n’est pas disponible pour l’app (compte
non connecté, ou iCloud Drive désactivé pour ReminisLink), les copies restent
sur ton iPhone (Fichiers > Sur mon iPhone > ReminisLink), ne sont pas
déplacées plus tard vers iCloud Drive, et sont supprimées avec l’app.

### Entre l’app et ta console

ReminisLink ne communique qu’avec des consoles de ton réseau local, trouvées
par Bonjour, en HTTPS (chiffré). L’app n’accepte que le certificat dont
l’empreinte de la clé publique figure dans le QR code scanné.

- **Appairage.** Le QR code contient l’identifiant et le nom de la console,
  un secret à usage unique, son adresse locale et son port, et cette
  empreinte. L’app cherche la console par Bonjour, ou à défaut se connecte à
  l’adresse du QR code. Deux requêtes ne sont pas signées : celle qui vérifie
  que la console est là et ouverte à l’appairage, et la requête d’appairage,
  qui prouve à la place que l’app a scanné le QR code. L’app envoie alors le
  nom que tu as choisi (par défaut le modèle de l’appareil, par exemple «
  iPhone »), l’identifiant et la clé publique de l’app, et la plateforme
  (Apple).
- **Après l’appairage**, toutes les autres requêtes sont signées avec la clé
  privée de l’app, et indiquent le type d’appareil (iPhone, iPad ou Mac).
- **Les fichiers que tu choisis d’envoyer** : jeux, sauvegardes, states,
  patchs, fichiers de cheats, images de jeux et apps pour la console.

### Sur la carte SD de ta console

- **Les appareils appairés** (`clients.json`) : pour chacun, son identifiant,
  son nom, sa plateforme, les types d’appareils vus, sa clé publique (qui ne
  permet pas de signer) et la date d’appairage. Tu peux retirer un appareil
  dans Réglages > Appareils sur la console.
- **Le journal de ReminisLink** (`/.userdata/tg5040/logs/`) : ce que la
  console a fait, avec pour chaque requête l’heure, l’action, le résultat,
  l’identifiant de l’appareil et son adresse sur ton réseau local, ainsi que
  le nom des appareils quand ils s’appairent, et des noms de fichiers. Jamais
  de clé, de secret ni de signature. Au lancement, un journal de plus de 5 Mo
  est renommé, en remplaçant l’ancien journal précédent.
- **Tes fichiers**, là où NextUI les range, et dans le dossier de ReminisLink
  (`/.userdata/shared/ReminisLink/`) : copies de secours des sauvegardes,
  copies verrouillées des states, la corbeille (fichiers supprimés ou
  remplacés, effacés après 30 jours comptés par la console, ou quand tu la
  vides), et fichiers temporaires des transferts.
- **Le certificat de la console** et sa clé privée.

La carte SD n’a aucune protection propre : quiconque l’a en main peut lire
ces fichiers.

### Téléchargements depuis un lien

Quand tu saisis ou partages un lien, l’app l’envoie à ta console, qui
télécharge elle-même le fichier (un jeu, un patch, un fichier de cheats ou
une app pour la console), avec sa propre connexion. Le serveur du lien reçoit
la requête de la console, comme pour tout téléchargement : l’adresse complète
du lien (avec son chemin et ses paramètres), l’adresse IP publique de ton
réseau, et « ReminisLink » comme nom de programme. L’app elle-même ne
contacte pas ce serveur.

### Autorisations

- **Réseau local** : pour trouver ta console et communiquer avec elle.
- **Appareil photo** : uniquement pour scanner le QR code affiché par ta
  console. Aucune photo n’est prise ni enregistrée.

L’app ne demande pas l’accès à ta photothèque : quand tu choisis une image
pour un jeu, le sélecteur d’iOS ne lui donne que l’image choisie.

### Enfants

ReminisLink ne collecte aucune donnée, sur personne, enfants compris.

### Ce site

reminis.link n’utilise ni cookie, ni mesure d’audience, ni traqueur, et ne
charge rien depuis d’autres sites. Il est hébergé par GitHub Pages, qui peut
garder des journaux techniques des visites (comme les adresses IP) pour sa
sécurité, selon la déclaration de confidentialité de GitHub.

### Modifications

Toute modification de cette politique sera publiée sur cette page, avec une
nouvelle date.

### Contact

Ouvre un ticket sur le dépôt GitHub du projet :
<https://github.com/tfournier/ReminisLink/issues>.
