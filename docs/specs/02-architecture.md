# 02 — Architecture

## Vue d'ensemble

```
┌──────────────── iPhone ────────────────┐            ┌──────────── Console (NextUI) ────────────┐
│ App ReminisLink (SwiftUI)              │            │ ReminisLink.pak (Go, au premier plan)    │
│  ├─ Découverte (NWBrowser, Bonjour)    │  mDNS      │  ├─ Annonce Bonjour _reminislink._tcp    │
│  ├─ Appairage (scan QR)                │◀──────────▶│  ├─ Écran : QR, PIN, état, transferts    │
│  ├─ ReminisLinkKit (client API, HMAC)  │  HTTP/JSON │  ├─ Serveur HTTP API v1 (port 47821)     │
│  ├─ Transferts reprenables             │◀──────────▶│  ├─ Auth (appairage, HMAC, nonces)       │
│  └─ Extension de partage               │            │  ├─ Bibliothèque / saves / states /      │
└────────────────────────────────────────┘            │  │   cheats / patchs / transferts        │
                                                      │  ├─ Adaptateur OS (nextui)               │
                                                      │  └─ Carte SD (exFAT)                     │
                                                      └──────────────────────────────────────────┘
```

## Responsabilités

### Le pak (console)

- DOIT tourner **au premier plan** : pas de démon. Tant que le pak est ouvert, aucun jeu ne tourne, donc aucune écriture concurrente de l'émulateur.
- Au lancement : vérifie que le Wi-Fi est connecté (une adresse IPv4 sur l'interface sans fil) ; sinon, affiche une erreur et propose de quitter.
- Démarre le serveur HTTP, puis l'annonce Bonjour.
- Exécute à chaque lancement les tâches de maintenance : archivage du state de reprise, contrôle des verrous (voir [05](05-console-pak.md)).
- Affiche le QR code d'appairage, le PIN de secours, les clients connectés et la progression des transferts.
- À la fermeture (bouton B ou signal) : arrête d'accepter les requêtes, laisse les écritures en cours se terminer (10 s maximum), retire l'annonce Bonjour, appelle `sync`.

### App iOS

- Découvre les consoles, gère l'appairage et les clés.
- Affiche la bibliothèque et exécute les actions via l'API.
- Gère les envois de fichiers (reprenables) et le suivi des tâches côté console.
- Reçoit des fichiers depuis les autres apps (extension de partage).

### Le contrat

`protocol/openapi.yaml` décrit l'API. C'est la source de vérité. `protocol/test-vectors/` contient des vecteurs de test (signature, appairage, encodage des identifiants) exécutés par les deux suites de tests.

## Composants Go (le pak)

| Paquet | Rôle |
|---|---|
| `cmd/reminislink` | Point d'entrée. Options : `--desktop` (fenêtre sur Mac, carte SD factice), `--headless` (serveur seul, sans UI, pour les tests), `--port` |
| `internal/server` | Routeur `net/http` (Go 1.22+ : motifs `GET /api/v1/...`), middlewares (journal, auth, limites de taille, récupération de panique), encodage JSON des erreurs |
| `internal/auth` | Appairage (secret QR, PIN), dérivation de clé, vérification HMAC, cache de nonces, stockage des clients |
| `internal/discovery` | Annonce mDNS/DNS-SD |
| `internal/osadapter` | Interface `Adapter` et implémentation `nextui` |
| `internal/library` | Systèmes, jeux, identifiants opaques, fichiers associés à un jeu |
| `internal/saves`, `internal/states`, `internal/cheats`, `internal/patch` | Fonctions métier |
| `internal/transfer` | Envois reprenables, gestionnaire de tâches (patchs, installations de paks) |
| `internal/store` | Données ReminisLink sur la carte SD : sauvegardes, verrous, clients, configuration |
| `internal/ui` | Écrans gabagool |
| `internal/device` | Batterie, stockage, réseau, version de l'OS |

Le code de lecture et d'écriture des `.cht` de Cheat Manager (`internal/cheats` de `nextui-cheat-manager`) DEVRAIT être réutilisé. Comme un paquet `internal` ne s'importe pas d'un autre module, deux options : le copier avec mention de l'origine (v1), puis l'extraire plus tard dans un module public partagé par les deux projets.

## Composants iOS

| Cible | Rôle |
|---|---|
| `ReminisLinkKit` (Swift package) | Modèles, client API, signature HMAC, appairage, transferts, découverte. Sans UI, testable avec `swift test` |
| `ReminisLink` (app) | Écrans SwiftUI |
| `ReminisLinkShare` (extension de partage) | Reçoit des fichiers, les confie à l'app via un groupe d'apps (*App Group*) partagé |

## Flux principaux

**Première connexion**

1. L'utilisateur ouvre ReminisLink sur la console. La console affiche un QR code.
2. Il le scanne avec l'appareil photo de l'iPhone (lien `reminislink://pair?...`) ou depuis l'app.
3. L'app cherche la console par Bonjour (par son `id`). Si elle ne la trouve pas, elle affiche une erreur (aucun repli sur l'IP du QR code, voir [03 §3.1](03-protocole.md#31-contenu-du-qr-code)).
4. Échange d'appairage → les deux côtés dérivent la même clé sans la transmettre (voir [03](03-protocole.md)).
5. L'app enregistre la console et sa clé dans le Trousseau.

**Connexions suivantes** : l'app trouve la console par Bonjour (`id` dans les données TXT) et signe ses requêtes avec la clé. Si Bonjour échoue, l'app affiche une erreur explicite (pas de balayage réseau en v1).

**Ajout d'un jeu depuis l'iPhone** : l'app crée un envoi (`POST /uploads`), envoie les morceaux, termine l'envoi ; la console vérifie le SHA-256 puis déplace le fichier dans `Roms/<système>/`.

**Pas de téléchargement par la console** : le pak ne se connecte jamais à Internet (téléchargement depuis un lien retiré de la v1 le 20/11/2026, voir [09, Après la v1](09-feuille-de-route.md#après-la-v1-idées-non-engagées)). Un fichier du web est téléchargé sur l'iPhone, puis envoyé comme les autres.

## Choix techniques et raisons

| Choix | Raison |
|---|---|
| Wi-Fi + HTTP, pas de Bluetooth | Bluetooth 4.2 : ~3 h pour une ROM NDS de 512 Mo, contre ~4 min en Wi-Fi |
| Bonjour seulement | Seule méthode de découverte sans entitlement Apple spécial ; les IP changent |
| mDNS intégré au binaire Go | Un répondeur intégré marche sur tous les OS. `avahi-daemon` 0.6.31 **tourne déjà** sur le firmware d'origine (lancé par `/etc/rc.d/S61avahi-daemon`, avec D-Bus par `S10dbus`, à l'écoute sur UDP 5353) ; essai du 19/11/2026 dans le rootfs v1.1.1 en conteneur linux/arm64 : le pak (`--headless`) s'annonce à côté d'avahi et un client mDNS le trouve avec son TXT complet, avahi reste actif. Reste à confirmer sur la vraie console en Wi-Fi, depuis l'iPhone **[À VÉRIFIER M0]** |
| HTTP + HMAC, pas de TLS | Processeur modeste, réseau local, serveur ouvert seulement quand le pak l'est. La clé ne circule jamais. TLS pourra devenir une option |
| Pak au premier plan | Aucune écriture concurrente avec l'émulateur, pas de consommation en arrière-plan |
| Go | Comme Cheat Manager et Grout ; un binaire ARM64 unique |
