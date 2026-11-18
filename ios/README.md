# ReminisLink for iOS

| Path | Content |
|---|---|
| `ReminisLink.xcodeproj` | App, share extension and UI tests (hand-written project, see below) |
| `Config/Config.xcconfig` | Identifier prefix and versions shared by every target |
| `Config/*-Info.plist`, `Config/*.entitlements` | Info.plist keys and capabilities of docs/specs/06-app-ios.md §3 |
| `ReminisLink/` | SwiftUI app: `App/` (state, pairing, connections, errors), `Views/`, `Resources/` (String Catalogs EN + FR, assets) |
| `ReminisLinkShare/` | Share extension (skeleton until M4) |
| `ReminisLinkUITests/` | XCUITest of the main path |
| `ReminisLinkKit/` | Swift package: `ReminisLinkKit` (signing, pairing, API client, discovery, stores) and `ReminisLinkDemo` (fake pak) |

## Build and test

```sh
cd ios/ReminisLinkKit && swift test
cd ios && xcodebuild test -scheme ReminisLink -destination 'platform=iOS Simulator,name=iPhone 16'
```

No Apple Developer account is needed for the simulator. To run on a device,
set `RL_BUNDLE_PREFIX` (your reverse-DNS prefix) and `DEVELOPMENT_TEAM` in
`Config/Config.xcconfig`: the bundle ids, the app group and the shared
Keychain group all derive from the prefix.

## The Xcode project

`project.pbxproj` is written by hand on purpose (no XcodeGen or Tuist to
install). The source folders are file-system synchronized groups: adding or
removing a file under `ReminisLink/`, `ReminisLinkShare/` or
`ReminisLinkUITests/` needs no project change. Build settings common to all
targets are in `Config/Config.xcconfig`.

## Demo mode (no console, no network)

Launch with the environment variable `RL_DEMO=1` (already in the scheme,
disabled: tick it in *Edit Scheme > Run > Arguments*) or the argument
`-RLDemo YES`. The app then talks to the fake pak of `ReminisLinkDemo`
through a `URLProtocol`, keeps its keys in memory and sees one "Demo Brick".
Pair it with *Add a Console > Pair with the Demo Console*, or open its link:

```sh
xcrun simctl openurl booted 'reminislink://pair?id=5f1e2d3c-4b5a-4968-8776-a5b4c3d2e1f0&n=Demo%20Brick&s=AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8&v=1'
```

The fake pak checks signatures like the real one; its game names are made up.

## Against the pak on the Mac (`make headless`)

1. At the repository root, run `make headless` (milestone M2). It announces
   `_reminislink._tcp` with Bonjour and prints the pairing link.
2. Run the `ReminisLink` scheme on the simulator **without** `RL_DEMO`. The
   simulator uses the Mac's network: the app finds the pak with Bonjour.
3. Open the pairing link printed by the pak:
   `xcrun simctl openurl booted '<link>'`.

The UI test against the real pak is skipped unless the link is given
(`TEST_RUNNER_` variables are passed to the test runner):

```sh
TEST_RUNNER_RL_UITEST_PAIR_URL='reminislink://pair?...' \
  xcodebuild test -scheme ReminisLink -destination 'platform=iOS Simulator,name=iPhone 16'
```

It launches the app with `RL_RESET_STORE=1`, which forgets the consoles
paired before.
