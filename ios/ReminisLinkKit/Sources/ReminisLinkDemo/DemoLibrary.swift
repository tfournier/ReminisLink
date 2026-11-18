import Foundation
import ReminisLinkKit

/// A small fake library served by ``FakePak``. Every game name is made up:
/// no commercial game, no ROM content.
public struct DemoLibrary: Sendable {
    public struct Game: Sendable {
        /// Path relative to the system folder, `/`-separated.
        public let path: String
        public let kind: GameKind
        public let sizeBytes: Int64
        public let modifiedAt: Date
        public let hasSave: Bool
        public let stateSlots: [Int]
        public let lockedStateCount: Int
        public let cheatCount: Int
        public let hasArt: Bool

        public init(
            path: String, kind: GameKind = .file, sizeBytes: Int64, modifiedAt: Date,
            hasSave: Bool = false, stateSlots: [Int] = [], lockedStateCount: Int = 0,
            cheatCount: Int = 0, hasArt: Bool = false
        ) {
            self.path = path
            self.kind = kind
            self.sizeBytes = sizeBytes
            self.modifiedAt = modifiedAt
            self.hasSave = hasSave
            self.stateSlots = stateSlots
            self.lockedStateCount = lockedStateCount
            self.cheatCount = cheatCount
            self.hasArt = hasArt
        }

        public var fileName: String { String(path.split(separator: "/").last ?? "") }

        /// File name without its extension (the folder name for `folder-m3u`).
        public var name: String {
            if kind == .folderM3U { return fileName }
            guard let dot = fileName.lastIndex(of: "."), dot != fileName.startIndex else { return fileName }
            return String(fileName[..<dot])
        }

        /// The parent folder inside the system ("" at the root).
        public var parent: String {
            guard let slash = path.lastIndex(of: "/") else { return "" }
            return String(path[..<slash])
        }
    }

    public struct System: Sendable {
        public let folder: String
        public let label: String
        public let tag: String
        public let games: [Game]

        public init(folder: String, label: String, tag: String, games: [Game]) {
            self.folder = folder
            self.label = label
            self.tag = tag
            self.games = games
        }

        public var id: String { DemoLibrary.id(folder) }
    }

    public var systems: [System]

    public init(systems: [System]) {
        self.systems = systems
    }

    /// Opaque ids are base64url of the path relative to `Roms/`, like the pak.
    public static func id(_ path: String) -> String {
        Base64URL.encode(Data(path.utf8))
    }

    public func system(id: String) -> System? {
        systems.first { $0.id == id }
    }

    public func game(id: String) -> (System, Game)? {
        for system in systems {
            if let game = system.games.first(where: { Self.id(system.folder + "/" + $0.path) == id }) {
                return (system, game)
            }
        }
        return nil
    }

    /// The folder path (inside the system) of a folder id, "" for the root.
    public func folderPath(system: System, folderId: String?) -> String? {
        guard let folderId else { return "" }
        let all = Set(system.games.flatMap { game -> [String] in
            var out: [String] = []
            var parent = game.parent
            while !parent.isEmpty {
                out.append(parent)
                parent = parent.contains("/") ? String(parent[..<parent.lastIndex(of: "/")!]) : ""
            }
            return out
        })
        return all.first { Self.id(system.folder + "/" + $0) == folderId }
    }

    private static let base = Date(timeIntervalSince1970: 1_790_000_000)

    public static let sample = DemoLibrary(systems: [
        System(folder: "Game Boy Advance (GBA)", label: "Game Boy Advance", tag: "GBA", games: [
            Game(path: "Sky Garden Quest.gba", sizeBytes: 16_777_216, modifiedAt: base,
                 hasSave: true, stateSlots: [0, 1, 2], lockedStateCount: 1, cheatCount: 12, hasArt: true),
            Game(path: "Pixel Runner.gba", sizeBytes: 4_194_304, modifiedAt: base.addingTimeInterval(86_400 * 3)),
            Game(path: "Homebrew/Tiny Tactics (Demo).gba", sizeBytes: 1_048_576, modifiedAt: base.addingTimeInterval(86_400),
                 hasSave: true, hasArt: true),
            Game(path: "Homebrew/Cartographe Égaré.gba", sizeBytes: 2_097_152, modifiedAt: base.addingTimeInterval(86_400 * 2),
                 stateSlots: [0], cheatCount: 2),
        ]),
        System(folder: "01) Game Boy (GB)", label: "Game Boy", tag: "GB", games: [
            Game(path: "Moonlit Maze 🌙.gb", sizeBytes: 262_144, modifiedAt: base, hasSave: true, stateSlots: [0], hasArt: true),
        ]),
        System(folder: "Sony PlayStation (PS)", label: "Sony PlayStation", tag: "PS", games: [
            Game(path: "Starlight Saga", kind: .folderM3U, sizeBytes: 734_003_200, modifiedAt: base,
                 hasSave: true, stateSlots: [0, 1], lockedStateCount: 2),
        ]),
    ])
}
