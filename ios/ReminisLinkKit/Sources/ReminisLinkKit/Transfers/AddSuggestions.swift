import Foundation

/// What a file added from the app is (docs/specs/06-app-ios.md §4.6).
public enum AddKind: String, Codable, Sendable, CaseIterable {
    case rom
    /// `.ips`, `.bps`, `.ups`: sent to the staging area of the console.
    case patch
    /// `.cht`: sent to the staging area of the console.
    case cheats
    /// `.sav`: replaces the save of a game.
    case save
    /// M12: a zip archive installed as a tool or emulator pak. Never
    /// suggested (most zips are games): the user picks it.
    case pak

    public static let patchExtensions: Set<String> = ["ips", "bps", "ups"]
    public static let cheatExtensions: Set<String> = ["cht"]
    public static let saveExtensions: Set<String> = ["sav", "srm"]

    /// The kind suggested by the extension of a file name.
    public static func suggested(forFileName name: String) -> AddKind {
        let ext = fileExtension(name)
        if patchExtensions.contains(ext) { return .patch }
        if cheatExtensions.contains(ext) { return .cheats }
        if saveExtensions.contains(ext) { return .save }
        return .rom
    }

    static func fileExtension(_ name: String) -> String {
        guard let dot = name.lastIndex(of: "."), dot != name.startIndex else { return "" }
        return name[name.index(after: dot)...].lowercased()
    }
}

/// Suggests the system of a ROM from its extension. Only a suggestion: the
/// user always confirms or changes it.
public enum SystemSuggestion {
    /// Extension → NextUI tags of the systems that take it, best first.
    /// TODO(M0): check the tags against the folders of a real NextUI card;
    /// an unknown tag only means no suggestion.
    public static let tagsByExtension: [String: [String]] = [
        "gba": ["GBA", "MGBA"],
        "gb": ["GB", "GBC"],
        "gbc": ["GBC", "GB"],
        "nes": ["FC"],
        "fds": ["FDS"],
        "sfc": ["SFC", "SUPA"],
        "smc": ["SFC", "SUPA"],
        "md": ["MD"],
        "gen": ["MD"],
        "smd": ["MD"],
        "sms": ["SMS"],
        "gg": ["GG"],
        "pce": ["PCE"],
        "ngp": ["NGP", "NGPC"],
        "ngc": ["NGPC", "NGP"],
        "ws": ["WS", "WSC"],
        "wsc": ["WSC", "WS"],
        "lnx": ["LYNX"],
        "vb": ["VB"],
        "min": ["PKM"],
        "a26": ["A2600"],
        "pbp": ["PS"],
        "chd": ["PS"],
        "cue": ["PS"],
        "m3u": ["PS"],
        "p8": ["P8", "PICO"],
    ]

    /// The first system whose tag matches the extension of `fileName`.
    /// `extraTagsByExtension` adds tags after those of ``tagsByExtension``
    /// (the made-up systems of the demo mode).
    public static func system(
        forFileName fileName: String, in systems: [GameSystem], extraTagsByExtension: [String: [String]] = [:]
    ) -> GameSystem? {
        let ext = AddKind.fileExtension(fileName)
        let tags = (tagsByExtension[ext] ?? []) + (extraTagsByExtension[ext] ?? [])
        for tag in tags {
            if let system = systems.first(where: { $0.tag.caseInsensitiveCompare(tag) == .orderedSame }) {
                return system
            }
        }
        return nil
    }
}
