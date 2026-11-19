// Debug builds only: the fake pak never ships in a release build.
#if DEBUG
import Foundation
import ReminisLinkKit

// Milestone M9 of the fake pak: `GET /saves` (the save of every game with
// its SHA-256 and its clock file). Downloads go through
// `GET /games/{gameId}/save` and `/save/rtc` (FakePakGameData.swift).

extension FakePak {
    /// UI tests and demo: the `n`-th `GET /saves` (1 for the first) finds
    /// the save of the first game with a save changed, as if it had been
    /// played on the console in between.
    public var changeSaveOnListing: Int? {
        get { lock.withLock { _changeSaveOnListing } }
        set { lock.withLock { _changeSaveOnListing = newValue } }
    }

    /// Replaces the save of a game as playing it on the console would
    /// (no backup copy: NextUI writes the save in place).
    public func play(gameId: String, content: Data) {
        guard let (system, game) = library.game(id: gameId) else { return }
        _ = currentData(system: system, game: game)
        let key = Self.dataKey(system: system, game: game)
        lock.withLock {
            gameData[key]?.save = .of(content)
            gameData[key]?.saveModifiedAt = Date()
        }
    }

    func listSaves() -> FakeResponse {
        let listing = lock.withLock { () -> Int in
            _saveListings += 1
            return _saveListings
        }
        let library = self.library
        if listing == changeSaveOnListing,
           let (system, game) = library.systems.lazy.flatMap({ s in s.games.map { (s, $0) } }).first(where: { $0.1.hasSave }) {
            play(gameId: DemoLibrary.id(system.folder + "/" + game.path), content: Data("played-\(listing)".utf8))
        }
        var saves: [[String: Any]] = []
        for system in library.systems {
            for game in system.games {
                let data = currentData(system: system, game: game)
                guard let save = data.save else { continue }
                let path = Self.savePath(system, game)
                let rtc: Any = data.rtc.map { rtc -> [String: Any] in
                    [
                        "path": String(path.dropLast(4)) + ".rtc", "sizeBytes": rtc.content.count,
                        "modifiedAt": ProtocolJSON.formatDate(data.saveModifiedAt),
                        "sha256": Signer.contentSHA256(rtc.content),
                    ]
                } ?? NSNull()
                saves.append([
                    "gameId": DemoLibrary.id(system.folder + "/" + game.path), "systemId": system.id,
                    "gameName": game.name, "fileName": game.fileName, "path": path,
                    "sizeBytes": save.content.count, "modifiedAt": ProtocolJSON.formatDate(data.saveModifiedAt),
                    "sha256": Signer.contentSHA256(save.content), "rtc": rtc,
                ])
            }
        }
        return .json(200, ["saves": saves, "truncated": false, "generatedAt": ProtocolJSON.formatDate(Date())])
    }
}
#endif
