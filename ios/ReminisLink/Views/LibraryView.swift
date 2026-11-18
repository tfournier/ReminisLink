import ReminisLinkKit
import SwiftUI

/// Systems of the console with their number of games.
struct LibraryView: View {
    @Environment(AppModel.self) private var app
    let deviceId: String
    @State private var systems: [GameSystem]?
    @State private var error: AppError?

    var body: some View {
        List {
            if let error {
                Section { ErrorView(error: error, retry: load) }
            }
            ForEach(systems ?? []) { system in
                NavigationLink(value: Route.folder(FolderRoute(
                    deviceId: deviceId, systemId: system.id, systemLabel: system.label, folderId: nil, title: system.label
                ))) {
                    LabeledContent {
                        Text("\(system.gameCount) games")
                    } label: {
                        Text(system.label)
                    }
                }
                .accessibilityIdentifier("system.\(system.tag)")
            }
        }
        .overlay {
            if systems == nil && error == nil {
                ProgressView()
            } else if systems?.isEmpty == true {
                ContentUnavailableView("No games on the console", systemImage: "square.grid.2x2")
            }
        }
        .navigationTitle("Library")
        .task { if systems == nil { await load() } }
        .refreshable { await load() }
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
                        name: game.name, hasArt: game.hasArt
                    ))) {
                        GameRow(deviceId: route.deviceId, game: game)
                    }
                    .accessibilityIdentifier("game.\(game.name)")
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

    var body: some View {
        HStack(spacing: 12) {
            GameArtView(deviceId: deviceId, gameId: game.id, hasArt: game.hasArt)
            VStack(alignment: .leading, spacing: 4) {
                Text(game.name)
                    .font(.body)
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
