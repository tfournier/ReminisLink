# Prompts pour les agents IA

Ces prompts pilotent des agents de code (Claude Code ou équivalent) jalon par jalon. Ils sont écrits pour être copiés tels quels.

## Agent principal

Pour confier tout le projet à un seul agent qui lance les sous-agents, fait relire leur travail par Antigravity (Gemini) et intègre jalon par jalon, colle [`orchestrateur.md`](orchestrateur.md) dans une session Claude Code ouverte à la racine du dépôt. Les sections ci-dessous décrivent l'usage manuel, jalon par jalon.

## Utilisation

1. Ouvre une session d'agent **à la racine du dépôt** (le fichier `CLAUDE.md` est lu automatiquement par Claude Code ; pour un autre agent, demande-lui de le lire d'abord).
2. Colle le contenu de [`00-contexte-commun.md`](00-contexte-commun.md), puis le prompt du jalon.
3. Une session = un jalon = une branche (`m2-serveur-lecture`, etc.) = une pull request.
4. À la fin, lance une **autre** session avec [`revue.md`](revue.md) sur la pull request, pour que le travail ne soit pas relu par l'agent qui l'a écrit.

## Ordre

```
M0 (toi, sur la console) ──▶ m0-analyse-releves
                                   │
                                   ▼
                              m1-contrat-crypto
                              │              │
                              ▼              ▼
                 m2-serveur-lecture    m3-ios-base        (en parallèle)
                              │              │
                              └──────┬───────┘
                                     ▼
                              m4-transferts
                                     ▼
                              m5-saves-states-cheats
                                     ▼
                              m6-patchs
                                     ▼
                              m7-ui-console-publication
```

- M2 et M3 peuvent tourner en parallèle (deux sessions, deux branches) : M3 utilise le pak en mode `--headless` dès qu'il existe, et des réponses factices avant.
- À partir de M4, chaque prompt couvre **les deux côtés** (Go et iOS). Si tu préfères deux agents, découpe le prompt en deux à la ligne « Côté iOS ».

## Fichiers

| Fichier | Jalon | Agent |
|---|---|---|
| [`orchestrateur.md`](orchestrateur.md) | Tous | Agent principal : planifie, délègue, fait vérifier par Antigravity, intègre |
| [`00-contexte-commun.md`](00-contexte-commun.md) | Tous | À coller en premier |
| [`m0-analyse-releves.md`](m0-analyse-releves.md) | M0 | Met à jour les specs avec les relevés de la console |
| [`m1-contrat-crypto.md`](m1-contrat-crypto.md) | M1 | OpenAPI, vecteurs de test, auth Go et Swift |
| [`m2-serveur-lecture.md`](m2-serveur-lecture.md) | M2 | Serveur Go, découverte, bibliothèque |
| [`m3-ios-base.md`](m3-ios-base.md) | M3 | App iOS : appairage et navigation |
| [`m4-transferts.md`](m4-transferts.md) | M4 | Envois, téléchargements, suppression |
| [`m5-saves-states-cheats.md`](m5-saves-states-cheats.md) | M5 | Saves, states, verrous, cheats |
| [`m6-patchs.md`](m6-patchs.md) | M6 | IPS, BPS, UPS |
| [`m7-ui-console-publication.md`](m7-ui-console-publication.md) | M7 | Écrans du pak, packaging, TestFlight |
| [`revue.md`](revue.md) | Tous | Relecture indépendante d'une PR |
