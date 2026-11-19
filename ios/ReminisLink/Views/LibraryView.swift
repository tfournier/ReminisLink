import ReminisLinkKit
import SwiftUI

/// The Library tab of a console (milestone M11): every game in one list
/// (`GET /games`), with search (name and file name), filters (console,
/// save, states, cheats) and sort. A library over 20 000 games
/// (`truncated`), or a pak without the route (`404`), is browsed per
/// system instead.
struct LibraryView: View {
    @Environment(AppModel.self) private var app
    let deviceId: String
    @State private var content: LibraryContent?
    @State private var error: AppError?
    @State private var query = LibraryQuery()
    @State private var toDelete: DeleteTarget?
    @AppStorage("gameSort") private var sort: GameSort = .name

    private var session: ConsoleSession { app.session(for: deviceId) }

    var body: some View {
        Group {
            switch content?.mode {
            case .flat(let all)?:
                FlatLibraryList(
                    deviceId: deviceId, all: all, systems: content?.systems ?? [],
                    query: $query, toDelete: $toDelete, error: error, retry: load
                )
            case .bySystem(let truncated)?:
                SystemsList(deviceId: deviceId, systems: content?.systems ?? [], error: error, retry: load) {
                    if truncated {
                        Text("The console has too many games to list them at once: browse them by console.")
                    }
                }
            case nil:
                List {
                    if let error {
                        Section { ErrorView(error: error, retry: load) }
                    }
                }
                .overlay {
                    if error == nil { ProgressView() }
                }
            }
        }
        .navigationTitle("Library")
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button {
                    app.showAdd(deviceId: deviceId)
                } label: {
                    Label("Add Files or a Link", systemImage: "plus")
                }
                // Another device has write access (M10).
                .disabled(session.isReadOnly)
                .accessibilityIdentifier("library.add")
            }
        }
        .task { if content == nil { await load() } }
        .refreshable { await load() }
        .onChange(of: session.libraryRevision) { Task { await load() } }
        .onChange(of: sort, initial: true) { _, sort in query.sort = sort.librarySort }
        .onChange(of: query.sort) { _, sort in self.sort = GameSort(sort) }
        .sheet(item: $toDelete) { target in
            DeleteGameView(target: target)
        }
    }

    private func load() async {
        let session = session
        do {
            async let systems = session.perform { try await $0.systems() }
            let mode: LibraryMode
            do {
                mode = LibraryMode.from(try await session.perform { try await $0.allGames() })
            } catch where LibraryMode.meansUnsupported(error) {
                // A pak older than M11: browse per system.
                mode = .bySystem(truncated: false)
            }
            content = LibraryContent(systems: try await systems, mode: mode)
            error = nil
        } catch {
            let error = AppError(error)
            if !error.isCancellation { self.error = error }
        }
    }
}

/// What the Library tab loaded.
struct LibraryContent: Equatable {
    let systems: [GameSystem]
    let mode: LibraryMode
}

/// Every game in one list, searched, filtered and sorted on the iPhone.
struct FlatLibraryList: View {
    @Environment(AppModel.self) private var app
    let deviceId: String
    let all: AllGames
    let systems: [GameSystem]
    @Binding var query: LibraryQuery
    @Binding var toDelete: DeleteTarget?
    let error: AppError?
    let retry: () async -> Void

    var body: some View {
        let groups = LibraryFiltering.groups(systems: systems, games: all.games)
        let labels = LibraryFiltering.labels(systems)
        let rowLabels = LibraryFiltering.rowLabels(systems: systems, games: all.games)
        let games = LibraryFiltering.apply(query, to: all.games, groups: groups)
        List {
            if let error {
                Section { ErrorView(error: error, retry: retry) }
            }
            if query.isFiltered {
                Section {
                    HStack {
                        Text(filterSummary)
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                        Spacer()
                        Button("Clear Filters") { query.clearFilters() }
                            .font(.footnote)
                            .accessibilityIdentifier("library.clearFilters")
                    }
                }
            }
            Section {
                ForEach(games) { game in
                    let systemLabel = labels[game.systemId] ?? ""
                    NavigationLink(value: Route.game(GameRoute(
                        deviceId: deviceId, gameId: game.id, systemLabel: systemLabel,
                        name: game.summary.name, hasArt: game.summary.hasArt, fileName: game.summary.fileName
                    ))) {
                        GameRow(deviceId: deviceId, game: game.summary, subtitle: subtitle(game, systemLabel: rowLabels[game.systemId] ?? systemLabel))
                    }
                    .accessibilityIdentifier("game.\(game.summary.name)")
                    .swipeActions(edge: .trailing) {
                        // Another device has write access (M10).
                        if !app.session(for: deviceId).isReadOnly {
                            Button("Delete", role: .destructive) {
                                toDelete = DeleteTarget(deviceId: deviceId, gameId: game.id, name: game.summary.name)
                            }
                            .accessibilityIdentifier("game.delete")
                        }
                    }
                }
            } header: {
                if !games.isEmpty {
                    Text("\(games.count) games")
                }
            }
        }
        .overlay {
            if games.isEmpty {
                if all.games.isEmpty {
                    ContentUnavailableView("No games on the console", systemImage: "square.grid.2x2")
                } else if !query.text.trimmingCharacters(in: .whitespaces).isEmpty {
                    ContentUnavailableView.search(text: query.text)
                } else {
                    ContentUnavailableView {
                        Label("No games match the filters", systemImage: "line.3.horizontal.decrease.circle")
                    } actions: {
                        Button("Clear Filters") { query.clearFilters() }
                    }
                }
            }
        }
        .searchable(text: $query.text, prompt: Text("Name or file name"))
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Menu {
                    Picker(selection: $query.systemLabel) {
                        Text("All Consoles").tag(String?.none)
                        ForEach(groups) { group in
                            Text(group.label).tag(Optional(group.label))
                        }
                    } label: {
                        Label("Filter by Console", systemImage: "gamecontroller")
                    }
                    .pickerStyle(.menu)
                    Section {
                        Toggle("Has a Save", isOn: $query.hasSave)
                        Toggle("Has States", isOn: $query.hasStates)
                        Toggle("Has Cheats", isOn: $query.hasCheats)
                    }
                    Section {
                        Picker(selection: $query.sort) {
                            Text("Name").tag(LibrarySort.name)
                            Text("Date").tag(LibrarySort.date)
                            Text("Size").tag(LibrarySort.size)
                        } label: {
                            Label("Sort by", systemImage: "arrow.up.arrow.down")
                        }
                        .pickerStyle(.menu)
                    }
                    if query.isFiltered {
                        Button("Clear Filters", role: .destructive) { query.clearFilters() }
                    }
                    Section {
                        Button {
                            app.tabPaths[.library, default: []].append(.systems(deviceId))
                        } label: {
                            Label("Browse by Console", systemImage: "folder")
                        }
                        .accessibilityIdentifier("library.bySystem")
                    }
                } label: {
                    Label(
                        "Filter and Sort",
                        systemImage: query.isFiltered
                            ? "line.3.horizontal.decrease.circle.fill" : "line.3.horizontal.decrease.circle"
                    )
                }
                .accessibilityIdentifier("library.filter")
            }
        }
    }

    /// The console (with the folder, if any) under the name of a game.
    private func subtitle(_ game: LibraryGame, systemLabel: String) -> String {
        guard let folder = game.folderPath, !folder.isEmpty else { return systemLabel }
        return systemLabel + " · " + folder
    }

    private var filterSummary: String {
        var parts: [String] = []
        if let label = query.systemLabel { parts.append(label) }
        if query.hasSave { parts.append(String(localized: "Has a Save")) }
        if query.hasStates { parts.append(String(localized: "Has States")) }
        if query.hasCheats { parts.append(String(localized: "Has Cheats")) }
        return parts.formatted(.list(type: .and))
    }
}

/// The systems of a console, to browse its library per system (docs/specs
/// 06 §4.4): from the Library tab's menu, or instead of the flat list.
struct SystemsView: View {
    @Environment(AppModel.self) private var app
    let deviceId: String
    @State private var systems: [GameSystem]?
    @State private var error: AppError?

    var body: some View {
        SystemsList(deviceId: deviceId, systems: systems, error: error, retry: load) { EmptyView() }
            .navigationTitle("Consoles")
            .task { if systems == nil { await load() } }
            .refreshable { await load() }
            .onChange(of: app.session(for: deviceId).libraryRevision) { Task { await load() } }
    }

    private func load() async {
        do {
            systems = try await app.session(for: deviceId).perform { try await $0.systems() }
            error = nil
        } catch {
            let error = AppError(error)
            if !error.isCancellation { self.error = error }
        }
    }
}

/// Systems with their number of games, each opening its folder.
struct SystemsList<Footer: View>: View {
    let deviceId: String
    let systems: [GameSystem]?
    let error: AppError?
    let retry: () async -> Void
    @ViewBuilder let footer: () -> Footer

    var body: some View {
        List {
            if let error {
                Section { ErrorView(error: error, retry: retry) }
            }
            Section {
                ForEach(systems ?? []) { system in
                    NavigationLink(value: Route.folder(FolderRoute(
                        deviceId: deviceId, systemId: system.id, systemLabel: system.label, folderId: nil, title: system.label
                    ))) {
                        HStack {
                            VStack(alignment: .leading, spacing: 2) {
                                Text(system.label)
                                if !system.emulatorInstalled {
                                    Text("Emulator not installed on the console")
                                        .font(.footnote)
                                        .foregroundStyle(.secondary)
                                }
                            }
                            Spacer()
                            Text("\(system.gameCount) games")
                                .foregroundStyle(.secondary)
                        }
                        .accessibilityElement(children: .combine)
                    }
                    .accessibilityIdentifier("system.\(system.tag)")
                }
            } footer: {
                footer()
            }
        }
        .overlay {
            if systems == nil && error == nil {
                ProgressView()
            } else if systems?.isEmpty == true {
                ContentUnavailableView("No games on the console", systemImage: "square.grid.2x2")
            }
        }
    }
}

/// Sort orders of a folder (docs/specs/06-app-ios.md §4.4).
enum GameSort: String, CaseIterable, Identifiable {
    case name
    case date
    case size

    var id: String { rawValue }

    var title: LocalizedStringKey {
        switch self {
        case .name: "Name"
        case .date: "Date"
        case .size: "Size"
        }
    }

    init(_ sort: LibrarySort) {
        switch sort {
        case .name: self = .name
        case .date: self = .date
        case .size: self = .size
        }
    }

    /// The same order for the flat library (M11).
    var librarySort: LibrarySort {
        switch self {
        case .name: .name
        case .date: .date
        case .size: .size
        }
    }

    func sorted(_ games: [GameSummary]) -> [GameSummary] {
        switch self {
        case .name: games.sorted { $0.name.localizedStandardCompare($1.name) == .orderedAscending }
        case .date: games.sorted { $0.modifiedAt > $1.modifiedAt }
        case .size: games.sorted { $0.sizeBytes > $1.sizeBytes }
        }
    }
}

/// A folder of a system: subfolders and games, with search and sort.
struct FolderView: View {
    @Environment(AppModel.self) private var app
    let route: FolderRoute
    @State private var listing: GameListing?
    @State private var error: AppError?
    @State private var query = ""
    @State private var toDelete: DeleteTarget?
    @AppStorage("gameSort") private var sort: GameSort = .name

    private var games: [GameSummary] {
        let all = listing?.games ?? []
        let filtered = query.isEmpty ? all : all.filter {
            $0.name.localizedStandardContains(query) || $0.fileName.localizedStandardContains(query)
        }
        return sort.sorted(filtered)
    }

    private var folders: [LibraryFolder] {
        let all = listing?.folders ?? []
        return query.isEmpty ? all : all.filter { $0.name.localizedStandardContains(query) }
    }

    var body: some View {
        List {
            if let error {
                Section { ErrorView(error: error, retry: load) }
            }
            if !folders.isEmpty {
                Section("Folders") {
                    ForEach(folders) { folder in
                        NavigationLink(value: Route.folder(FolderRoute(
                            deviceId: route.deviceId, systemId: route.systemId, systemLabel: route.systemLabel,
                            folderId: folder.id, title: folder.name
                        ))) {
                            LabeledContent {
                                Text("\(folder.gameCount) games")
                            } label: {
                                Label(folder.name, systemImage: "folder")
                            }
                        }
                    }
                }
            }
            Section {
                ForEach(games) { game in
                    NavigationLink(value: Route.game(GameRoute(
                        deviceId: route.deviceId, gameId: game.id, systemLabel: route.systemLabel,
                        name: game.name, hasArt: game.hasArt, fileName: game.fileName
                    ))) {
                        GameRow(deviceId: route.deviceId, game: game)
                    }
                    .accessibilityIdentifier("game.\(game.name)")
                    .swipeActions(edge: .trailing) {
                        // Another device has write access (M10).
                        if !app.session(for: route.deviceId).isReadOnly {
                            Button("Delete", role: .destructive) {
                                toDelete = DeleteTarget(deviceId: route.deviceId, gameId: game.id, name: game.name)
                            }
                            .accessibilityIdentifier("game.delete")
                        }
                    }
                }
            }
        }
        .overlay {
            if listing == nil && error == nil {
                ProgressView()
            } else if listing != nil && games.isEmpty && folders.isEmpty {
                if query.isEmpty {
                    ContentUnavailableView("No games in this folder", systemImage: "gamecontroller")
                } else {
                    ContentUnavailableView.search(text: query)
                }
            }
        }
        .navigationTitle(route.title)
        .searchable(text: $query, prompt: Text("Search"))
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Menu {
                    Picker(selection: $sort) {
                        ForEach(GameSort.allCases) { Text($0.title).tag($0) }
                    } label: {
                        Text("Sort by")
                    }
                } label: {
                    Label("Sort", systemImage: "arrow.up.arrow.down")
                }
            }
        }
        .task { if listing == nil { await load() } }
        .refreshable { await load() }
        .onChange(of: app.session(for: route.deviceId).libraryRevision) { Task { await load() } }
        .sheet(item: $toDelete) { target in
            DeleteGameView(target: target)
        }
    }

    private func load() async {
        let systemId = route.systemId
        let folderId = route.folderId
        do {
            listing = try await app.session(for: route.deviceId).perform {
                try await $0.games(systemId: systemId, folderId: folderId)
            }
            error = nil
        } catch {
            let error = AppError(error)
            if !error.isCancellation { self.error = error }
        }
    }
}

/// A game in a list: art, name, size and badges.
struct GameRow: View {
    let deviceId: String
    let game: GameSummary
    /// The console of the game, in the flat library (M11).
    var subtitle: String?

    var body: some View {
        HStack(spacing: 12) {
            GameArtView(deviceId: deviceId, gameId: game.id, hasArt: game.hasArt)
            VStack(alignment: .leading, spacing: 4) {
                Text(game.name)
                    .font(.body)
                if let subtitle, !subtitle.isEmpty {
                    Text(subtitle)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                HStack(spacing: 10) {
                    Text(game.sizeBytes.byteString)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    if game.hasSave {
                        Badge(systemImage: "externaldrive.fill", accessibilityLabel: Text("Has a save"))
                    }
                    if game.stateCount > 0 {
                        Badge(systemImage: "square.stack.3d.up.fill", count: game.stateCount,
                              accessibilityLabel: Text("\(game.stateCount) states"))
                    }
                    if game.lockedStateCount > 0 {
                        Badge(systemImage: "lock.fill",
                              accessibilityLabel: Text("\(game.lockedStateCount) locked states"))
                    }
                    if game.cheatCount > 0 {
                        Badge(systemImage: "wand.and.stars", count: game.cheatCount,
                              accessibilityLabel: Text("\(game.cheatCount) cheats"))
                    }
                }
            }
        }
        .accessibilityElement(children: .combine)
    }
}
