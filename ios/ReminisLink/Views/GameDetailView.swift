import ReminisLinkKit
import SwiftUI

/// Game sheet (docs/specs/06-app-ios.md §4.5): header, the save, states
/// and cheats screens, the patch, the files of the game and its deletion.
struct GameDetailView: View {
    @Environment(AppModel.self) private var app
    let route: GameRoute
    @State private var detail: GameDetail?
    @State private var error: AppError?
    @State private var toDelete: DeleteTarget?
    @State private var deleted = false
    @State private var patch: PatchContext?

    var body: some View {
        List {
            Section {
                HStack(alignment: .top, spacing: 16) {
                    GameArtView(deviceId: route.deviceId, gameId: route.gameId, hasArt: route.hasArt, size: 96)
                    VStack(alignment: .leading, spacing: 6) {
                        Text(detail?.name ?? route.name)
                            .font(.title3.bold())
                            .accessibilityAddTraits(.isHeader)
                            .accessibilityIdentifier("game.title")
                        Text(route.systemLabel)
                            .foregroundStyle(.secondary)
                        if let detail {
                            Text(detail.totalBytes.byteString)
                                .foregroundStyle(.secondary)
                                .accessibilityLabel(Text("Total size: \(detail.totalBytes.byteString)"))
                        }
                    }
                }
                .padding(.vertical, 4)
            }

            if let error {
                Section { ErrorView(error: error, retry: load) }
            }

            if let detail {
                Section {
                    NavigationLink(value: Route.save(route)) {
                        LabeledContent {
                            Text(detail.hasSave ? "Yes" : "No")
                        } label: {
                            Label("Save", systemImage: "externaldrive")
                        }
                    }
                    .accessibilityIdentifier("game.save")
                    NavigationLink(value: Route.states(route)) {
                        LabeledContent {
                            Text(detail.stateCount, format: .number)
                        } label: {
                            Label("States", systemImage: "square.stack.3d.up")
                        }
                    }
                    .accessibilityIdentifier("game.states")
                    NavigationLink(value: Route.cheats(route)) {
                        LabeledContent {
                            Text(detail.hasCheats ? "Yes" : "No")
                        } label: {
                            Label("Cheats", systemImage: "wand.and.stars")
                        }
                    }
                    .accessibilityIdentifier("game.cheats")
                } header: {
                    Text("Contents")
                }
                Section {
                    Button {
                        patch = PatchContext(deviceId: route.deviceId, game: PatchGame(
                            id: route.gameId, name: detail.name, fileName: detail.fileName,
                            kind: detail.kind, systemLabel: route.systemLabel
                        ))
                    } label: {
                        Label("Apply a Patch…", systemImage: "bandage")
                    }
                    .disabled(PatchNaming.refusal(kind: detail.kind, fileName: detail.fileName) != nil)
                    .accessibilityIdentifier("game.patch")
                } header: {
                    Text("Patch")
                } footer: {
                    if let refusal = PatchNaming.refusal(kind: detail.kind, fileName: detail.fileName) {
                        Text(refusal.text)
                    } else {
                        Text("IPS, BPS or UPS, applied by the console to a new file or to this game.")
                    }
                }
                Section {
                    ForEach(Array(detail.files.enumerated()), id: \.offset) { _, file in
                        FileRow(file: file)
                    }
                } header: {
                    Text("Files")
                } footer: {
                    Text("Paths on the SD card, for information.")
                }
                .accessibilityIdentifier("game.files")
                Section {
                    Button("Delete…", role: .destructive) {
                        toDelete = DeleteTarget(deviceId: route.deviceId, gameId: route.gameId, name: detail.name)
                    }
                    .accessibilityIdentifier("game.deleteButton")
                }
            }
        }
        .overlay {
            if detail == nil && error == nil { ProgressView() }
        }
        .navigationTitle(route.name)
        .navigationBarTitleDisplayMode(.inline)
        .task { if detail == nil { await load() } }
        .refreshable { await load() }
        // Back from the save, states or cheats screens: counters may have changed.
        .onChange(of: app.session(for: route.deviceId).libraryRevision) { Task { await load() } }
        .sheet(item: $patch) { context in
            PatchView(context: context)
        }
        .sheet(item: $toDelete, onDismiss: {
            // The game is gone: back to its folder.
            if deleted, app.path.last == .game(route) { app.path.removeLast() }
        }) { target in
            DeleteGameView(target: target, onDeleted: { deleted = true })
        }
    }

    private func load() async {
        let gameId = route.gameId
        do {
            detail = try await app.session(for: route.deviceId).perform { try await $0.game(id: gameId) }
            error = nil
        } catch {
            let error = AppError(error)
            if !error.isCancellation { self.error = error }
        }
    }
}

struct FileRow: View {
    let file: GameFile

    private var title: Text {
        switch file.role {
        case .rom: Text("Game")
        case .save: Text("Save")
        case .rtc: Text("Clock of the save")
        case .state:
            if let slot = file.slot { Text("State \(slot)") } else { Text("State") }
        case .cheats: Text("Cheats")
        case .art: Text("Art")
        default: Text(file.role.rawValue)
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                title.font(.headline)
                Spacer()
                Text(file.sizeBytes.byteString).foregroundStyle(.secondary)
            }
            Text(file.path)
                .font(.caption.monospaced())
                .foregroundStyle(.secondary)
                .textSelection(.enabled)
        }
        .accessibilityElement(children: .combine)
    }
}
