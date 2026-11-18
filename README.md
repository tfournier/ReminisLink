# ReminisLink

ReminisLink lets you manage the games on your retro handheld from your iPhone: ROMs, saves, save states, cheats and ROM patches.

It has two parts:

- **The pak**: the ReminisLink program for the handheld (NextUI first), shown as "ReminisLink" in the Tools menu. You open it, manage your games from the phone, close it and play.
- **ReminisLink for iOS**: the iPhone app. It finds the handheld on the local Wi-Fi network (Bonjour), pairs with a QR code and talks to the pak over HTTP.

> Status: specification phase. See [`docs/specs/`](docs/specs/README.md).

## Repository layout

```
ReminisLink/
├── go.mod                 # Go module of the pak
├── cmd/reminislink/       # Pak entry point (device and desktop mode)
├── internal/              # Pak packages (server, protocol, OS adapters, storage...)
├── pak/                   # launch.sh and pak resources
├── pak.json               # Pak Store manifest
├── ios/                   # Xcode project of the iOS app (SwiftUI)
├── protocol/              # OpenAPI spec and shared test vectors (Go <-> Swift)
├── scripts/               # toolchain setup, device checks
├── docs/specs/            # functional and technical specifications (French)
└── prompts/               # prompts for the AI coding agents (French)
```

## Documentation

- [Specifications](docs/specs/README.md)
- [Agent prompts](prompts/README.md)
- [Conventions for coding agents](CLAUDE.md)

## License

To be decided (MIT, like Cheat Manager, is the default proposal).
