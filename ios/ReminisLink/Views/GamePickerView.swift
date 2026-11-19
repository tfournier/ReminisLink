import ReminisLinkKit
import SwiftUI

/// Chooses a game of a console: systems, then folders and games (for the
/// save sent from the Add screen, or a shared patch).
struct GamePickerView: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    let deviceId: String
    /// For a patch: multi-disc games are shown but cannot be chosen.
    var patchOnly = false
    let onPick: (PickedGame) -> Void

    @State private var systems: [GameSystem]?
    @State private var error: AppError?

    var body: some View {
        NavigationStack {
            List {
                if let error {
                    Section { ErrorView(error: error, retry: load) }
                }
                ForEach(systems ?? []) { system in
                    NavigationLink {
                        GameFolderPickerView(deviceId: deviceId, systemId: system.id, systemLabel: system.label, folderId: nil,
                                             title: system.label, patchOnly: patchOnly, onPick: onPick)
                    } label: {
                        LabeledContent(system.label) {
                            Text(system.gameCount, format: .number)
                        }
                    }
                    .accessibilityIdentifier("picker.system.\(system.tag)")
                }
            }
            .overlay {
                if systems == nil && error == nil { ProgressView() }
            }
            .navigationTitle("Choose the Game")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
            }
            .task { if systems == nil { await load() } }
        }
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

/// One folder of a system in the game picker.
struct GameFolderPickerView: View {
    @Environment(AppModel.self) private var app
    let deviceId: String
    let systemId: String
    let systemLabel: String
    let folderId: String?
    let title: String
    var patchOnly = false
    let onPick: (PickedGame) -> Void

    @State private var listing: GameListing?
    @State private var error: AppError?

    var body: some View {
        List {
            if let error {
                Section { ErrorView(error: error, retry: load) }
            }
            if let listing {
                ForEach(listing.folders) { folder in
                    NavigationLink {
                        GameFolderPickerView(deviceId: deviceId, systemId: systemId, systemLabel: systemLabel, folderId: folder.id,
                                             title: folder.name, patchOnly: patchOnly, onPick: onPick)
                    } label: {
                        Label(folder.name, systemImage: "folder")
                    }
                }
                ForEach(listing.games) { game in
                    let refusal = patchOnly ? PatchNaming.refusal(kind: game.kind, fileName: game.fileName) : nil
                    let refused = refusal != nil
                    Button {
                        onPick(PickedGame(id: game.id, name: game.name, hasSave: game.hasSave, fileName: game.fileName,
                                          kind: game.kind, systemLabel: systemLabel))
                    } label: {
                        HStack {
                            VStack(alignment: .leading, spacing: 2) {
                                Text(game.name).foregroundStyle(refused ? .secondary : .primary)
                                if let refusal {
                                    (refusal == .archive ? Text("Archive: extract the ROM first") : Text("Several discs: cannot be patched"))
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                }
                            }
                            Spacer()
                            if game.hasSave && !patchOnly {
                                Image(systemName: "externaldrive")
                                    .foregroundStyle(.secondary)
                                    .accessibilityLabel(Text("Has a save"))
                            }
                        }
                    }
                    .disabled(refused)
                    .accessibilityIdentifier("picker.game.\(game.name)")
                }
                if listing.folders.isEmpty && listing.games.isEmpty {
                    Text("No game in this folder.").foregroundStyle(.secondary)
                }
            }
        }
        .overlay {
            if listing == nil && error == nil { ProgressView() }
        }
        .navigationTitle(title)
        .navigationBarTitleDisplayMode(.inline)
        .task { if listing == nil { await load() } }
    }

    private func load() async {
        let systemId = systemId
        let folderId = folderId
        do {
            listing = try await app.session(for: deviceId).perform { try await $0.games(systemId: systemId, folderId: folderId) }
            error = nil
        } catch {
            let error = AppError(error)
            if !error.isCancellation { self.error = error }
        }
    }
}
