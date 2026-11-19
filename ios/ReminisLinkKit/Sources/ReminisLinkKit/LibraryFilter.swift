import Foundation

// The flat library of the app (milestone M11): every game of `GET /games`,
// searched, filtered and sorted on the iPhone.

/// Sort orders of the library.
public enum LibrarySort: String, CaseIterable, Sendable {
    /// By name (natural order, case- and accent-insensitive).
    case name
    /// Most recently modified first.
    case date
    /// Largest first.
    case size
}

/// A console of the library filter: the systems that share a label, as
/// NextUI shows them as one (for example GBA and MGBA, both "Game Boy
/// Advance").
public struct SystemGroup: Hashable, Sendable, Identifiable {
    public let label: String
    public let systemIds: Set<String>
    /// The tags of its systems, sorted (e.g. `["GBA", "MGBA"]`).
    public let tags: [String]
    public let gameCount: Int

    public var id: String { label }

    public init(label: String, systemIds: Set<String>, tags: [String], gameCount: Int) {
        self.label = label
        self.systemIds = systemIds
        self.tags = tags
        self.gameCount = gameCount
    }
}

/// What the library shows: search text, filters and sort.
public struct LibraryQuery: Equatable, Sendable {
    /// Matched against the game name and its file name.
    public var text: String
    /// The label of a ``SystemGroup``; nil for every console.
    public var systemLabel: String?
    public var hasSave: Bool
    public var hasStates: Bool
    public var hasCheats: Bool
    public var sort: LibrarySort

    public init(
        text: String = "", systemLabel: String? = nil, hasSave: Bool = false, hasStates: Bool = false,
        hasCheats: Bool = false, sort: LibrarySort = .name
    ) {
        self.text = text
        self.systemLabel = systemLabel
        self.hasSave = hasSave
        self.hasStates = hasStates
        self.hasCheats = hasCheats
        self.sort = sort
    }

    /// A filter other than the search text is set.
    public var isFiltered: Bool { systemLabel != nil || hasSave || hasStates || hasCheats }

    /// Every filter off (the search text and the sort stay).
    public mutating func clearFilters() {
        systemLabel = nil
        hasSave = false
        hasStates = false
        hasCheats = false
    }
}

public enum LibraryFiltering {
    /// The consoles of the filter: the systems that have games, merged by
    /// label, sorted by label.
    public static func groups(systems: [GameSystem], games: [LibraryGame]) -> [SystemGroup] {
        var counts: [String: Int] = [:]
        for game in games { counts[game.systemId, default: 0] += 1 }
        var byLabel: [String: [GameSystem]] = [:]
        for system in systems where counts[system.id, default: 0] > 0 {
            byLabel[system.label, default: []].append(system)
        }
        return byLabel.map { label, systems in
            SystemGroup(
                label: label,
                systemIds: Set(systems.map(\.id)),
                tags: systems.map(\.tag).sorted(),
                gameCount: systems.reduce(0) { $0 + counts[$1.id, default: 0] }
            )
        }
        .sorted { $0.label.localizedStandardCompare($1.label) == .orderedAscending }
    }

    /// The label of each system id.
    public static func labels(_ systems: [GameSystem]) -> [String: String] {
        Dictionary(systems.map { ($0.id, $0.label) }, uniquingKeysWith: { first, _ in first })
    }

    /// The games matching `query`, in its order.
    public static func apply(_ query: LibraryQuery, to games: [LibraryGame], groups: [SystemGroup]) -> [LibraryGame] {
        let text = query.text.trimmingCharacters(in: .whitespacesAndNewlines)
        let systemIds = query.systemLabel.map { label in groups.first { $0.label == label }?.systemIds ?? [] }
        let matching = games.filter { game in
            let summary = game.summary
            if let systemIds, !systemIds.contains(game.systemId) { return false }
            if query.hasSave && !summary.hasSave { return false }
            if query.hasStates && summary.stateCount == 0 { return false }
            if query.hasCheats && summary.cheatCount == 0 { return false }
            if !text.isEmpty {
                return summary.name.localizedStandardContains(text) || summary.fileName.localizedStandardContains(text)
            }
            return true
        }
        return sorted(matching, by: query.sort)
    }

    /// Ties keep a stable order (the pak's: system folder, then path).
    public static func sorted(_ games: [LibraryGame], by sort: LibrarySort) -> [LibraryGame] {
        let indexed = games.enumerated().map { ($0.offset, $0.element) }
        return indexed.sorted { a, b in
            let x = a.1.summary
            let y = b.1.summary
            switch sort {
            case .name:
                let order = x.name.localizedStandardCompare(y.name)
                if order != .orderedSame { return order == .orderedAscending }
            case .date:
                if x.modifiedAt != y.modifiedAt { return x.modifiedAt > y.modifiedAt }
            case .size:
                if x.sizeBytes != y.sizeBytes { return x.sizeBytes > y.sizeBytes }
            }
            return a.0 < b.0
        }
        .map(\.1)
    }
}

/// Whether the library can be shown as one flat list, or the app browses
/// per system (a library over 20 000 games, or a pak without `GET /games`).
public enum LibraryMode: Equatable, Sendable {
    case flat(AllGames)
    /// `truncated`: the pak has more games than it lists at once.
    case bySystem(truncated: Bool)

    /// The mode for an answer of `GET /games`.
    public static func from(_ games: AllGames) -> LibraryMode {
        games.truncated ? .bySystem(truncated: true) : .flat(games)
    }

    /// Whether an error of `GET /games` means the pak predates the route
    /// (M11): `404`, whatever its body, or `405`.
    public static func meansUnsupported(_ error: Error) -> Bool {
        switch error as? APIError {
        case .server(let server): server.status == 404 || server.status == 405
        case .unexpectedStatus(let status): status == 404 || status == 405
        default: false
        }
    }
}
