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

    /// The console shown under each game, by system id: its label, with
    /// its tag when several systems with games share that label (two
    /// games of the same name in GBA and MGBA must not look the same):
    /// "Game Boy Advance (MGBA)". The filter keeps them merged.
    public static func rowLabels(systems: [GameSystem], games: [LibraryGame]) -> [String: String] {
        let withGames = Set(games.map(\.systemId))
        var systemsPerLabel: [String: Int] = [:]
        for system in systems where withGames.contains(system.id) {
            systemsPerLabel[system.label, default: 0] += 1
        }
        var labels: [String: String] = [:]
        for system in systems where labels[system.id] == nil {
            labels[system.id] = systemsPerLabel[system.label, default: 0] > 1
                ? "\(system.label) (\(system.tag))" : system.label
        }
        return labels
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

/// Everything the flat library derives from its games, its systems and
/// its query: the consoles of the filter, the labels and the games shown,
/// in order. Building it sorts every game (`localizedStandardCompare`),
/// which takes a noticeable time on a large library.
public struct LibraryPresentation: Sendable, Equatable {
    public let groups: [SystemGroup]
    /// The label of each system id.
    public let labels: [String: String]
    /// The console shown under each game, by system id.
    public let rowLabels: [String: String]
    /// The games matching the query, in its order.
    public let games: [LibraryGame]

    public init(systems: [GameSystem], games: [LibraryGame], query: LibraryQuery) {
        let groups = LibraryFiltering.groups(systems: systems, games: games)
        self.groups = groups
        labels = LibraryFiltering.labels(systems)
        rowLabels = LibraryFiltering.rowLabels(systems: systems, games: games)
        self.games = LibraryFiltering.apply(query, to: games, groups: groups)
    }
}

/// Remembers the last ``LibraryPresentation``, so a view drawn again with
/// the same games, systems and query reuses it instead of filtering and
/// sorting the whole library on the main thread again. The Library list is
/// drawn again on every navigation of its tab (selecting a game, going
/// back, opening its save: the selection is the tab's path). Comparing the
/// inputs is cheap: the arrays of an unchanged library share their storage.
@MainActor
public final class LibraryPresentationCache {
    private struct Inputs: Equatable {
        let systems: [GameSystem]
        let games: [LibraryGame]
        let query: LibraryQuery
    }

    private var inputs: Inputs?
    private var cached: LibraryPresentation?
    /// How many presentations were built (tests).
    public private(set) var builds = 0

    public init() {}

    public func presentation(systems: [GameSystem], games: [LibraryGame], query: LibraryQuery) -> LibraryPresentation {
        let inputs = Inputs(systems: systems, games: games, query: query)
        if let cached, self.inputs == inputs { return cached }
        let presentation = LibraryPresentation(systems: systems, games: games, query: query)
        self.inputs = inputs
        cached = presentation
        builds += 1
        return presentation
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
