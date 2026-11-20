# Security policy

ReminisLink talks to your handheld over your local network and keeps keys on your devices, so security reports are welcome.

## Reporting a vulnerability

Please do **not** open a public issue for a security problem. Instead:

- use GitHub's private vulnerability reporting: the **Security** tab of this repository, then **Report a vulnerability**; or
- write to <contact@reminis.link>.

Include what you found, how to reproduce it and the versions involved (pak version from Settings > Version and log on the handheld, app version from the app's settings). You should get an answer within a week. Please give us a reasonable time to fix the problem before you publish anything.

Never send private keys or pairing links: the `tls/` folder of the handheld holds its private key, and a pairing link (or its QR code) is a one-time secret.

## Supported versions

Only the latest release of the pak and the latest version of the iOS app receive security fixes.

## Scope

In scope: the pak (HTTP API, pairing, request signing, file handling on the SD card) and the iOS app (pairing, Keychain, network, file handling). Out of scope: NextUI itself, the handheld's firmware, and attacks that need physical access to an unlocked handheld or phone.
