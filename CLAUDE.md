# ReminisLink — conventions for coding agents

Read this file before any change. The specifications live in `docs/specs/` (French). When the code and the spec disagree, stop and report it: do not silently pick one.

## The product in one paragraph

ReminisLink is an iPhone companion for Linux retro handhelds (first target: TrimUI Brick on NextUI). The handheld runs the **ReminisLink pak** (shown as "ReminisLink" in the NextUI Tools menu), a foreground Go program that serves an HTTP API on the local Wi-Fi network and announces itself with Bonjour. The iOS app (SwiftUI) discovers it, pairs with a QR code, and manages ROMs, saves, save states, cheats and patches. Le pak only runs when no game is running.

## Languages

- Code, identifiers, comments, commit messages, README, pak.json: **English**.
- Specs (`docs/specs/`) and prompts (`prompts/`): French.
- User-facing strings: localized (English + French at least), never hard-coded.

## Repository layout

| Path | Content |
|---|---|
| `cmd/reminislink/` | entry point; `--desktop` runs against a fake SD card on macOS/Linux |
| `internal/server/` | HTTP routing, handlers, middleware |
| `internal/auth/` | pairing, HMAC request signing, nonce cache, client store |
| `internal/discovery/` | Bonjour/mDNS announcement |
| `internal/osadapter/` | one adapter per OS (paths of ROMs, saves, states, cheats, art); `nextui` first |
| `internal/library/` | systems, games, the files that belong to a game |
| `internal/saves/`, `internal/states/`, `internal/cheats/`, `internal/patch/` | features |
| `internal/transfer/` | resumable uploads, device-side downloads, jobs |
| `internal/store/` | ReminisLink data on the SD card (backups, locks, clients, config) |
| `internal/ui/` | the pak screens (gabagool) |
| `ios/` | Xcode project `ReminisLink` (app + share extension + `ReminisLinkKit` Swift package) |
| `protocol/openapi.yaml` | the API contract: source of truth for both sides |
| `protocol/test-vectors/` | JSON test vectors used by the Go **and** Swift test suites |

## Go (the pak)

- Same toolchain and style as `nextui-cheat-manager` (Makefile targets `test`, `lint`, `build`, `pak`, `release`, `docker-*`; Debian 11 cross toolchain; CGO for SDL only).
- UI: `github.com/BrandonKowalski/gabagool/v2`, as in Cheat Manager.
- Everything outside `internal/ui` and `cmd/` must build and test with `CGO_ENABLED=0` on macOS/Linux.
- Standard library first. Allowed extra dependencies: gabagool, an mDNS library (e.g. `github.com/grandcat/zeroconf`), a QR encoder. Ask before adding anything else.
- Paths come from environment variables with NextUI defaults (`SDCARD_PATH`, `ROMS_PATH`, `CHEATS_PATH`, `SAVES_PATH`, `USERDATA_PATH`, `PLATFORM`, `LOGS_PATH`), like Cheat Manager.
- Logs: `log/slog` to `$LOGS_PATH/ReminisLink.txt`.

## Swift (iOS)

- iOS 17 minimum, SwiftUI, Observation (`@Observable`), Swift Concurrency (async/await), no third-party dependency without asking.
- Networking, signing and models in the `ReminisLinkKit` Swift package, testable with `swift test`.
- Keys and tokens only in the Keychain.

## Non-negotiable rules

1. **Never delete user data without a backup.** Every destructive operation (delete, replace, restore, patch in place) first moves or copies the previous file into the ReminisLink backup area. Writes are atomic: write to `<file>.tmp`, `fsync`, then rename.
2. **Never trust a path from the network.** The API only takes opaque IDs or names; the server resolves them and checks the result stays inside the expected root (no `..`, no absolute paths, no symlink escape).
3. **The contract is `protocol/openapi.yaml`.** Change it first, then both implementations. Signing and pairing must pass the shared test vectors on both sides.
4. **The SD card is exFAT**: no permissions, no symlinks, case-insensitive. Do not rely on `chmod`.
5. **Small, reviewable changes.** One milestone task per branch/PR, with tests.

## Commands

```sh
make test      # Go unit tests (no toolchain)
make lint      # gofmt + go vet
make contract  # validate protocol/openapi.yaml (Docker) and check the test vectors
make vectors   # regenerate protocol/test-vectors/ and copy them to the Swift tests
make desktop   # run the pak against ./testdata/sdcard on the Mac
make docker-release   # build the pak zip
# iOS
cd ios && xcodebuild test -scheme ReminisLink -destination 'platform=iOS Simulator,name=iPhone 16'
cd ios/ReminisLinkKit && swift test
```
