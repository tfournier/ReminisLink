# ReminisLink — Privacy Policy / Politique de confidentialité

[English](#en) · [Français](#fr)

Published version / Version publiée : <https://reminis.link/privacy/> ·
<https://reminis.link/fr/privacy/>. Keep this file and `site/privacy/`,
`site/fr/privacy/` identical / Garder ce fichier et les pages du site
identiques.

Last updated / Dernière mise à jour : 22 November 2026 / 22 novembre 2026

---

<a id="en"></a>

## English

ReminisLink is an app for iPhone, iPad, Mac (with Apple silicon) and Android
that manages the games of your retro handheld over your local Wi-Fi network.
Your data stays on your devices, on your handheld and, with the Apple app,
in your own iCloud account (copies of your saves and save states in iCloud
Drive, pairing in iCloud Keychain). ReminisLink on the handheld never
connects to the internet: it only answers your devices on your local network.

### What we collect

**Nothing.** ReminisLink has no account, no server, no analytics, no
advertising and no tracking. The Apple app uses no third-party SDK; the
Android app uses no Google Play Services and only open-source libraries
that work on the device (AndroidX, CameraX, OkHttp, ZXing, Tink). The
developer receives no data about you or about how you use the app.

### On your iPhone, iPad or Mac

- **The preferences of the app**, the list of handhelds paired with an
  older version that must be paired again, the last address on your local
  network where each handheld answered (kept on this device only), and,
  when the identity of your Apple devices has not arrived on this one, since
  when it is missing.
- **A cache of box art** shown in the library, which iOS may clear at any
  time.
- **Files you share to ReminisLink** from another app: the share
  extension copies them into storage shared with the app until you send them.
  The app deletes them once they are used; unused ones are deleted the next
  time the app checks its inbox (when it becomes active) once they are older
  than 7 days.
- **Temporary copies of the files you send** (game archives you import
  included), kept until the transfer ends. The app does not keep its
  transfer queue across launches: they are deleted at the next launch at the
  latest.
- **Files you download** from your handheld (a file of a game, a file of the
  SD card, a game archive, screenshots) or from your copies of saves and
  save states: written to a temporary folder of the app, then saved only
  where you choose (Files, AirDrop, another app, Photos). The temporary
  folder is emptied at each launch of the app.
- **Game archives.** When you export a game, the handheld makes a zip file
  with its save and clock file, its save states and their thumbnails, its
  cheats and its picture, and the game itself if you ask for it, plus a
  `manifest.json` that lists the name of the handheld, its system, platform
  and version of ReminisLink, the system and name of the game, the name,
  size and checksum of each file, and the date of the export. The archive
  goes only where you choose with the share sheet. An archive you import is
  chosen in Files and sent to your handheld (see below).
- **Screenshots of your handheld**: their thumbnails are kept in memory
  only, while the app runs. A screenshot you open is downloaded to a
  temporary file, deleted once it is shown. Sharing or saving screenshots
  downloads them to the temporary folder above; **Save to Photos** adds them
  to your photo library.
- **A picture of the QR code** you import from Photos or Files (for example
  on a Mac without a camera) is read on the device to find the QR code, and
  never kept: a picture from Photos is copied to a temporary file, deleted
  once read. Pictures larger than 50 MB or 200 megapixels are refused.
- **The demo mode** (Try Without a Console) works with a made-up handheld
  inside the app: it never talks to a handheld or to any server, and puts
  nothing in the Keychain, iCloud Drive or Photos. Files downloaded from the
  made-up handheld stay in the app (the share sheet is not offered). Files or
  pictures you choose in Files or Photos to try a feature are copied to a
  temporary folder of the demo (iOS may first download the original you
  chose, for example from iCloud); like everything the demo writes, they are
  erased when you quit it, or at the next launch.

Deleting the app removes all of this. It does not remove the copies of your
saves and save states in iCloud Drive (see below) nor the screenshots you
added to Photos, and iOS may keep the pairing in the Keychain after the app
is deleted: unpair your handhelds in the app first if you want it removed.

### Pairing of your handhelds (iCloud Keychain)

To pair, the app creates its own identity: an identifier and a private
signing key. It also keeps which identity new pairings use, and the name you
chose to show on your handhelds. For each paired handheld, it keeps the
identifier, name and model of the handheld, the name you gave it, the
identity it was paired with, the fingerprint of its certificate’s public
key, the address on your local network where it was reached when you paired,
and the pairing date.

All of this is kept in the iOS Keychain, readable only by ReminisLink and its
share extension, and synced by **iCloud Keychain** to your other Apple
devices (iPhone, iPad, Mac) signed in with the same Apple Account, so that
they share one pairing. iCloud Keychain is Apple’s service, end-to-end
encrypted, under your Apple account and Apple’s privacy policy. If iCloud
Keychain is off, these items stay on this device. The developer never
receives them.

Unpairing a handheld in the app removes the pairing from the handheld and
from all these devices. The identity itself is kept, so that your other
pairings keep working.

If the identity that new pairings use has still not arrived on a device 10
minutes after it was first missed there (its key may no longer exist
anywhere), the app offers to reset the identity of this device: a new
identity is created and used for new pairings. No identity is deleted, and
the handhelds already paired keep working.

### Pairing refresh (iCloud key-value store)

iCloud Keychain does not tell the app when another of your Apple devices
changes a pairing. So when a device pairs, forgets or renames a handheld,
creates or chooses an identity, or changes the name shown on your
handhelds, it writes one value, `pairingsChanged`, to the iCloud key-value
store of the app: a counter and a random token, with nothing about you, your
devices or your handhelds, and no date. iCloud syncs it to your other Apple
devices, which then read the Keychain again. The iCloud key-value store is
Apple’s service, under your Apple account and Apple’s privacy policy: the
developer never receives it.

### Copies of your saves and save states (iCloud Drive)

ReminisLink can copy the saves (with their clock files) and the save states
(with their thumbnails) of your handhelds to your device: with the **Sync
Saves** button of a handheld, and automatically when it connects (you can
turn this off in the settings of the app). Up to 10 versions of each save,
and of each save state slot, are kept. Next to the copies, small files
record the identifier and name of the handheld, the identifier, name and
system of the game, and for each version its number (and the slot, for a
save state), the names, sizes and checksums of its files, the date of the
copy and, when known, the date the save or save state last changed on the
handheld.

The copies go to **your own iCloud Drive**, in the ReminisLink folder of the
Files app, where you can see, move or delete them. iCloud is Apple’s service,
under your Apple account and Apple’s privacy policy: the developer has no
server and never receives these copies. When iCloud Drive is not available to
the app (not signed in, or iCloud Drive turned off for ReminisLink), the
copies stay on this device (on an iPhone: Files > On My iPhone >
ReminisLink), are not moved to iCloud Drive later, and are deleted with the
app.

### On your Android device

The Android app keeps everything in its private storage, which other apps
cannot read:

- **Its identity**: an identifier and a private signing key. The key is
  created in the Android Keystore, which never lets it out; where the
  Keystore cannot make this kind of key (Ed25519), the key is a random value
  kept encrypted (AES-256-GCM) by a key of the Android Keystore that cannot
  be exported.
- **The paired handhelds** (`consoles.json`): for each one, its identifier,
  name and model, the fingerprint of its certificate’s public key, the last
  address on your local network where it answered, and the pairing date;
  and the name you chose to show on your handhelds (by default the model of
  your device).
- **Copies of the files you send**, picked with the system file picker or
  shared from another app (games, saves, save states, patches, cheat files,
  apps for the handheld): copied into the app so that the transfer can
  resume, and deleted when the transfer or the installation ends or is
  abandoned. The app does not keep its transfer queue when the system closes
  it: copies left behind are deleted at the next launch at the latest.
- **Files you download** are written only into the document you create with
  the system’s save dialog. A download that fails or is cancelled deletes
  that document.
- **A picture you choose for a game** is reduced on the device, sent to the
  handheld, and not kept. Box art is not stored on the device.
- A pairing link you scan, paste or open from another app, and the list of
  files shared from another app, are kept in memory only.
- **The demo mode** (Try Without a Console) works with a made-up handheld
  kept in memory: it never uses the network, puts nothing in the Keystore or
  in the list of handhelds, and keeps nothing once you quit it. Files you
  pick to try a feature are copied and deleted like for a real transfer; a
  download is written to the document you choose.

The app opts out of Android backup and of device-to-device transfer: none of
this goes to your Google account or to a new device. Each Android device
therefore has its own identity and pairs with each handheld on its own. The
Android app makes no copies of your saves and has no cloud sync.
Uninstalling the app removes all of this, its Keystore keys included.

### Between the app and your handheld

ReminisLink talks only to handhelds on your local network, found with
Bonjour (mDNS), over HTTPS (encrypted). The app accepts only the certificate
whose public-key fingerprint is in the QR code you scanned.

- **Pairing.** The QR code holds the identifier and name of the handheld, a
  one-time secret, its local address and port, and that fingerprint. The app
  looks for the handheld with Bonjour, or else connects to the address in the
  QR code. Two requests are not signed: the one that checks that the handheld
  is there and open for pairing, and the pairing request, which instead
  proves that the app scanned the QR code. The app then sends the name you
  chose (by default the model of the device, such as “iPhone”), the
  identifier and public key of the app, and the platform (Apple or Android).
- **After pairing**, every other request is signed with the private key of
  the app. Requests carry the type of device (iPhone, iPad, Mac or Android).
- **The files you choose to send**: games, saves, save states, patches, cheat
  files, game images, game archives and apps for the handheld.

### On the SD card of your handheld

ReminisLink runs on NextUI (platforms `tg5040`, `tg5050`, `h700` and
`my355`) and on Knulli (experimental).

- **The paired devices** (`clients.json`): for each one, its identifier,
  name, platform, the types of device seen, its public key (which cannot be
  used to sign) and the date of pairing. You can remove a device in Settings
  > Devices on the handheld.
- **The log of ReminisLink** (`ReminisLink.txt`, in
  `/.userdata/<platform>/logs/` on NextUI, such as
  `/.userdata/tg5040/logs/`, and in `/userdata/system/logs/` on Knulli):
  what the handheld did, with for each request the time, the action, the
  result, the identifier of the device and its address on your local
  network, plus the names of devices when they pair and file names. Requests
  that are not signed or are rejected are only logged in samples. Never a
  key, a secret or a signature. At launch, a log larger than 5 MB is renamed
  `ReminisLink.old.txt`, replacing the previous old log. Next to it,
  `ReminisLink.stderr.txt` receives the error output of the program, if any.
- **Your files**, where the system of the handheld keeps them (screenshots
  included), and in the ReminisLink folder
  (`/.userdata/shared/ReminisLink/` on NextUI,
  `/userdata/system/ReminisLink/` on Knulli): backup copies of saves
  (including the save a restore or an archive import replaces), locked
  copies of save states, the trash, the staging area and temporary files of
  transfers.
  - The trash holds deleted or replaced files (games, saves, save states,
    screenshots, apps…), including the files an archive import replaces. You can
    restore them from the app; a file a restore would replace is first kept
    as a backup copy or moved to the trash. The trash is purged at launch
    once files reach 30 days of counted age, or when you empty it.
  - The staging area holds the files sent for a patch, a cheat import, an
    app installation or an archive import. An archive is deleted once it is
    imported or when you abandon the import; anything left is deleted at the
    next launch of ReminisLink once older than 24 hours.
  - An upload left idle for 24 hours is deleted when the handheld next
    notices that it expired, or at the next launch of ReminisLink.
- **The certificate of the handheld** and its private key.

The SD card has no protection of its own: anyone holding it can read these
files.

### Permissions

On iPhone, iPad and Mac:

- **Local network**: to find your handheld and talk to it.
- **Camera**: only to scan the QR code shown by your handheld (on a Mac,
  its camera or Continuity Camera). No picture is taken or saved.
- **Photos, add only**: asked the first time you choose **Save to Photos**
  for screenshots of your handheld. It lets the app add pictures to your
  photo library, never read it.

The app never reads your photo library: when you choose a picture for a game
or a picture of the QR code, the iOS picker gives it only the picture you
pick.

On Android:

- **Network access** (granted at installation): to find your handheld with
  the system’s service discovery (NSD) and talk to it on your local network.
- **Camera**: only to scan the QR code shown by your handheld, decoded on
  the device. No picture is taken or saved. You can paste the pairing link
  instead.
- **Notifications** (Android 13 and later, asked when you send files): to
  show the progress of transfers, with the name of the file, while they run
  in the background. If you refuse, transfers still run.
- **No storage permission**: the app only reads the files you pick and
  writes the documents you create with the system dialogs.

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
<https://github.com/tfournier/ReminisLink/issues>, or write to
<contact@reminis.link>.

---

<a id="fr"></a>

## Français

ReminisLink est une app pour iPhone, iPad, Mac (à puce Apple) et Android qui
gère les jeux de ta console rétro par ton réseau Wi-Fi local. Tes données
restent sur tes appareils, sur ta console et, avec l’app Apple, dans ton
propre compte iCloud (copies de tes sauvegardes et de tes états de jeu dans
iCloud Drive, appairage dans le trousseau iCloud). ReminisLink sur la console
ne se connecte jamais à internet : il ne répond qu’à tes appareils sur ton
réseau local.

### Ce que nous collectons

**Rien.** ReminisLink n’a ni compte, ni serveur, ni mesure d’audience, ni
publicité, ni pistage. L’app Apple n’utilise aucun SDK tiers ; l’app Android
n’utilise pas les services Google Play et seulement des bibliothèques open
source qui fonctionnent sur l’appareil (AndroidX, CameraX, OkHttp, ZXing,
Tink). Le développeur ne reçoit aucune donnée sur toi ni sur ton utilisation
de l’app.

### Sur ton iPhone, ton iPad ou ton Mac

- **Les préférences de l’app**, la liste des consoles appairées avec une
  ancienne version qui doivent être appairées de nouveau, la dernière
  adresse sur ton réseau local où chaque console a répondu (gardée sur cet
  appareil seulement) et, quand l’identité de tes appareils Apple n’est pas
  encore arrivée sur celui-ci, depuis quand elle manque.
- **Un cache des jaquettes** affichées dans la bibliothèque, qu’iOS peut
  vider à tout moment.
- **Les fichiers que tu partages vers ReminisLink** depuis une autre
  app : l’extension de partage les copie dans un stockage partagé avec l’app
  jusqu’à leur envoi. L’app les supprime une fois utilisés ; ceux qui ne
  servent pas sont supprimés la prochaine fois que l’app vérifie sa boîte de
  réception (quand elle devient active), s’ils ont plus de 7 jours.
- **Des copies temporaires des fichiers que tu envoies** (archives de jeu
  importées comprises), gardées jusqu’à la fin du transfert. L’app ne garde
  pas sa file de transferts d’un lancement à l’autre : elles sont supprimées
  au plus tard au lancement suivant.
- **Les fichiers que tu télécharges** depuis ta console (un fichier d’un jeu,
  un fichier de la carte SD, une archive de jeu, des captures d’écran) ou
  depuis les copies de tes sauvegardes et de tes états de jeu : écrits dans
  un dossier temporaire de l’app, puis enregistrés seulement là où tu le
  choisis (Fichiers, AirDrop, une autre app, Photos). Le dossier temporaire
  est vidé à chaque lancement de l’app.
- **Les archives de jeu.** Quand tu exportes un jeu, la console fabrique un
  fichier zip avec sa sauvegarde et son fichier d’horloge, ses états de jeu
  et leurs vignettes, ses codes de triche et son image, et le jeu lui-même si
  tu le demandes, ainsi qu’un `manifest.json` qui indique le nom de la
  console, son système, sa plateforme et sa version de ReminisLink, le
  système et le nom du jeu, le nom, la taille et la somme de contrôle de
  chaque fichier, et la date de l’export. L’archive ne va que là où tu le
  choisis avec la feuille de partage. Une archive que tu importes est
  choisie dans Fichiers et envoyée à ta console (voir plus bas).
- **Les captures d’écran de ta console** : leurs vignettes ne sont gardées
  qu’en mémoire, pendant que l’app tourne. Une capture que tu ouvres est
  téléchargée dans un fichier temporaire, supprimé une fois qu’elle est
  affichée. Partager ou enregistrer des captures les télécharge dans le
  dossier temporaire ci-dessus ; **Enregistrer dans Photos** les ajoute à ta
  photothèque.
- **Une image du QR code** que tu importes depuis Photos ou Fichiers (par
  exemple sur un Mac sans caméra) est lue sur l’appareil pour y trouver le
  QR code, et jamais gardée : une image de Photos est copiée dans un fichier
  temporaire, supprimé une fois lu. Les images de plus de 50 Mo ou de plus de
  200 mégapixels sont refusées.
- **Le mode démo** (Essayer sans console) fonctionne avec une console
  inventée, dans l’app : il ne communique jamais avec une console ni avec
  aucun serveur, et ne met rien dans le trousseau, dans iCloud Drive ni dans
  Photos. Les fichiers téléchargés depuis la console inventée restent dans
  l’app (la feuille de partage n’est pas proposée). Les fichiers ou images
  que tu choisis dans Fichiers ou Photos pour essayer une fonction sont
  copiés dans un dossier temporaire de la démo (iOS peut d’abord télécharger
  l’original choisi, par exemple depuis iCloud) ; comme tout ce que la démo
  écrit, ils sont effacés quand tu la quittes, ou au lancement suivant.

Supprimer l’app efface tout cela. Cela n’efface pas les copies de tes
sauvegardes et de tes états de jeu dans iCloud Drive (voir plus bas) ni les
captures que tu as ajoutées à Photos, et iOS peut garder l’appairage dans le
trousseau après la suppression de l’app : désappaire d’abord tes consoles
dans l’app si tu veux qu’il disparaisse.

### Appairage de tes consoles (trousseau iCloud)

Pour s’appairer, l’app crée sa propre identité : un identifiant et une clé
privée de signature. Elle garde aussi quelle identité les nouveaux
appairages utilisent, et le nom que tu as choisi d’afficher sur tes
consoles. Pour chaque console appairée, elle garde l’identifiant, le nom et
le modèle de la console, le nom que tu lui as donné, l’identité avec
laquelle elle a été appairée, l’empreinte de la clé publique de son
certificat, l’adresse sur ton réseau local où elle a été jointe à
l’appairage, et la date d’appairage.

Tout cela est gardé dans le trousseau iOS, lisible seulement par ReminisLink
et son extension de partage, et synchronisé par le **trousseau iCloud** avec
tes autres appareils Apple (iPhone, iPad, Mac) connectés au même compte
Apple, pour qu’ils partagent un seul appairage. Le trousseau iCloud est le
service d’Apple, chiffré de bout en bout, sous ton compte Apple et la
politique de confidentialité d’Apple. Si le trousseau iCloud est désactivé,
ces éléments restent sur cet appareil. Le développeur ne les reçoit jamais.

Se désappairer d’une console dans l’app retire l’appairage de la console et
de tous ces appareils. L’identité elle-même est gardée, pour que tes autres
appairages continuent de fonctionner.

Si l’identité qu’utilisent les nouveaux appairages n’est toujours pas
arrivée sur un appareil 10 minutes après qu’il l’a vue manquer (sa clé
n’existe peut-être plus nulle part), l’app propose de réinitialiser
l’identité de cet appareil : une nouvelle identité est créée et utilisée
pour les nouveaux appairages. Aucune identité n’est effacée, et les consoles
déjà appairées continuent de fonctionner.

### Actualisation des appairages (stockage clé-valeur iCloud)

Le trousseau iCloud ne prévient pas l’app quand un autre de tes appareils
Apple change un appairage. Alors, quand un appareil appaire, oublie ou
renomme une console, crée ou choisit une identité, ou change le nom affiché
sur tes consoles, il écrit une seule valeur, `pairingsChanged`, dans le
stockage clé-valeur iCloud de l’app : un compteur et un jeton aléatoire,
rien sur toi, tes appareils ou tes consoles, et aucune date. iCloud la
synchronise avec tes autres appareils Apple, qui relisent alors le
trousseau. Le stockage clé-valeur iCloud est le service d’Apple, sous ton
compte Apple et la politique de confidentialité d’Apple : le développeur ne
le reçoit jamais.

### Copies de tes sauvegardes et de tes états de jeu (iCloud Drive)

ReminisLink peut copier les sauvegardes (avec leurs fichiers d’horloge) et
les états de jeu (avec leurs vignettes) de tes consoles sur ton appareil :
avec le bouton **Synchroniser les sauvegardes** d’une console, et
automatiquement à chaque connexion (désactivable dans les réglages de
l’app). Jusqu’à 10 versions de chaque sauvegarde, et de chaque emplacement
d’état de jeu, sont gardées. À côté des copies, de petits fichiers notent
l’identifiant et le nom de la console, l’identifiant, le nom et le système
du jeu, et pour chaque version son numéro (et l’emplacement, pour un état de
jeu), le nom, la taille et la somme de contrôle de ses fichiers, la date de
la copie et, si elle est connue, la date de dernière modification de la
sauvegarde ou de l’état de jeu sur la console.

Les copies vont dans **ton propre iCloud Drive**, dans le dossier ReminisLink
de l’app Fichiers, où tu peux les voir, les déplacer ou les supprimer. iCloud
est le service d’Apple, sous ton compte Apple et la politique de
confidentialité d’Apple : le développeur n’a aucun serveur et ne reçoit
jamais ces copies. Quand iCloud Drive n’est pas disponible pour l’app (compte
non connecté, ou iCloud Drive désactivé pour ReminisLink), les copies restent
sur cet appareil (sur un iPhone : Fichiers > Sur mon iPhone > ReminisLink),
ne sont pas déplacées plus tard vers iCloud Drive, et sont supprimées avec
l’app.

### Sur ton appareil Android

L’app Android garde tout dans son stockage privé, que les autres apps ne
peuvent pas lire :

- **Son identité** : un identifiant et une clé privée de signature. La clé
  est créée dans l’Android Keystore, qui ne la laisse jamais sortir ; là où
  le Keystore ne sait pas créer ce type de clé (Ed25519), la clé est une
  valeur aléatoire gardée chiffrée (AES-256-GCM) par une clé de l’Android
  Keystore qui ne peut pas être exportée.
- **Les consoles appairées** (`consoles.json`) : pour chacune, son
  identifiant, son nom et son modèle, l’empreinte de la clé publique de son
  certificat, la dernière adresse sur ton réseau local où elle a répondu, et
  la date d’appairage ; ainsi que le nom que tu as choisi d’afficher sur tes
  consoles (par défaut le modèle de ton appareil).
- **Des copies des fichiers que tu envoies**, choisis avec le sélecteur de
  fichiers du système ou partagés depuis une autre app (jeux, sauvegardes,
  états de jeu, patchs, fichiers de codes de triche, apps pour la console) :
  copiés dans l’app pour que le transfert puisse reprendre, et supprimés
  quand le transfert ou l’installation se termine ou est abandonné. L’app ne
  garde pas sa file de transferts quand le système la ferme : les copies
  restantes sont supprimées au plus tard au lancement suivant.
- **Les fichiers que tu télécharges** ne sont écrits que dans le document
  que tu crées avec la fenêtre d’enregistrement du système. Un
  téléchargement qui échoue ou est annulé supprime ce document.
- **Une image que tu choisis pour un jeu** est réduite sur l’appareil,
  envoyée à la console, et pas gardée. Les jaquettes ne sont pas enregistrées
  sur l’appareil.
- Un lien d’appairage que tu scannes, colles ou ouvres depuis une autre app,
  et la liste des fichiers partagés depuis une autre app, ne sont gardés
  qu’en mémoire.
- **Le mode démo** (Essayer sans console) fonctionne avec une console
  inventée gardée en mémoire : il n’utilise jamais le réseau, ne met rien
  dans le Keystore ni dans la liste des consoles, et ne garde rien une fois
  que tu le quittes. Les fichiers que tu choisis pour essayer une fonction
  sont copiés et supprimés comme pour un vrai transfert ; un téléchargement
  est écrit dans le document que tu choisis.

L’app refuse la sauvegarde Android et le transfert d’appareil à appareil :
rien de tout cela ne va dans ton compte Google ni sur un nouvel appareil.
Chaque appareil Android a donc sa propre identité et s’appaire lui-même avec
chaque console. L’app Android ne fait aucune copie de tes sauvegardes et n’a
aucune synchronisation dans le cloud. Désinstaller l’app efface tout cela,
ses clés du Keystore comprises.

### Entre l’app et ta console

ReminisLink ne communique qu’avec des consoles de ton réseau local, trouvées
par Bonjour (mDNS), en HTTPS (chiffré). L’app n’accepte que le certificat
dont l’empreinte de la clé publique figure dans le QR code scanné.

- **Appairage.** Le QR code contient l’identifiant et le nom de la console,
  un secret à usage unique, son adresse locale et son port, et cette
  empreinte. L’app cherche la console par Bonjour, ou à défaut se connecte à
  l’adresse du QR code. Deux requêtes ne sont pas signées : celle qui vérifie
  que la console est là et ouverte à l’appairage, et la requête d’appairage,
  qui prouve à la place que l’app a scanné le QR code. L’app envoie alors le
  nom que tu as choisi (par défaut le modèle de l’appareil, par exemple «
  iPhone »), l’identifiant et la clé publique de l’app, et la plateforme
  (Apple ou Android).
- **Après l’appairage**, toutes les autres requêtes sont signées avec la clé
  privée de l’app. Les requêtes indiquent le type d’appareil (iPhone, iPad,
  Mac ou Android).
- **Les fichiers que tu choisis d’envoyer** : jeux, sauvegardes, états de
  jeu, patchs, fichiers de codes de triche, images de jeux, archives de jeu
  et apps pour la console.

### Sur la carte SD de ta console

ReminisLink fonctionne sur NextUI (plateformes `tg5040`, `tg5050`, `h700` et
`my355`) et sur Knulli (expérimental).

- **Les appareils appairés** (`clients.json`) : pour chacun, son identifiant,
  son nom, sa plateforme, les types d’appareils vus, sa clé publique (qui ne
  permet pas de signer) et la date d’appairage. Tu peux retirer un appareil
  dans Réglages > Appareils sur la console.
- **Le journal de ReminisLink** (`ReminisLink.txt`, dans
  `/.userdata/<plateforme>/logs/` sur NextUI, par exemple
  `/.userdata/tg5040/logs/`, et dans `/userdata/system/logs/` sur Knulli) :
  ce que la console a fait, avec pour chaque requête l’heure, l’action, le
  résultat, l’identifiant de l’appareil et son adresse sur ton réseau local,
  ainsi que le nom des appareils quand ils s’appairent et des noms de
  fichiers. Les requêtes non signées ou refusées ne sont journalisées que par
  échantillons. Jamais de clé, de secret ni de signature. Au lancement, un
  journal de plus de 5 Mo est renommé `ReminisLink.old.txt`, en remplaçant
  l’ancien journal précédent. À côté, `ReminisLink.stderr.txt` reçoit la
  sortie d’erreur du programme, s’il y en a une.
- **Tes fichiers**, là où le système de la console les range (captures
  d’écran comprises), et dans le dossier de ReminisLink
  (`/.userdata/shared/ReminisLink/` sur NextUI,
  `/userdata/system/ReminisLink/` sur Knulli) : copies de secours des
  sauvegardes (y compris la sauvegarde qu’une restauration ou un import
  d’archive remplace), copies verrouillées des états de jeu, la corbeille,
  la zone d’attente et les fichiers temporaires des transferts.
  - La corbeille garde les fichiers supprimés ou remplacés (jeux,
    sauvegardes, états de jeu, captures d’écran, apps…), y compris les
    fichiers qu’un import d’archive remplace. Tu peux les restaurer depuis l’app ; un
    fichier qu’une restauration remplacerait est d’abord gardé en copie de
    secours ou mis à la corbeille. La corbeille est purgée au lancement une
    fois 30 jours d’âge comptabilisé atteints, ou quand tu la vides.
  - La zone d’attente garde les fichiers envoyés pour un patch, un import de
    codes de triche, l’installation d’une app ou un import d’archive. Une
    archive est supprimée une fois importée ou quand tu abandonnes l’import ;
    ce qui reste est supprimé au prochain lancement de ReminisLink, une fois
    plus vieux que 24 heures.
  - Un envoi inactif pendant 24 heures est supprimé quand la console
    constate ensuite son expiration, ou au prochain lancement de
    ReminisLink.
- **Le certificat de la console** et sa clé privée.

La carte SD n’a aucune protection propre : quiconque l’a en main peut lire
ces fichiers.

### Autorisations

Sur iPhone, iPad et Mac :

- **Réseau local** : pour trouver ta console et communiquer avec elle.
- **Appareil photo** : uniquement pour scanner le QR code affiché par ta
  console (sur un Mac, sa caméra ou la caméra de continuité). Aucune photo
  n’est prise ni enregistrée.
- **Photos, ajout seulement** : demandée la première fois que tu choisis
  **Enregistrer dans Photos** pour des captures d’écran de ta console. Elle
  permet à l’app d’ajouter des images à ta photothèque, jamais de la lire.

L’app ne lit jamais ta photothèque : quand tu choisis une image pour un jeu
ou une image du QR code, le sélecteur d’iOS ne lui donne que l’image
choisie.

Sur Android :

- **Accès au réseau** (accordé à l’installation) : pour trouver ta console
  avec la découverte de services du système (NSD) et communiquer avec elle
  sur ton réseau local.
- **Appareil photo** : uniquement pour scanner le QR code affiché par ta
  console, décodé sur l’appareil. Aucune photo n’est prise ni enregistrée.
  Tu peux coller le lien d’appairage à la place.
- **Notifications** (Android 13 et plus récent, demandée quand tu envoies
  des fichiers) : pour afficher la progression des transferts, avec le nom
  du fichier, pendant qu’ils tournent en arrière-plan. Si tu refuses, les
  transferts continuent.
- **Aucune autorisation de stockage** : l’app ne lit que les fichiers que tu
  choisis et n’écrit que les documents que tu crées avec les fenêtres du
  système.

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
<https://github.com/tfournier/ReminisLink/issues>, ou écris à
<contact@reminis.link>.
