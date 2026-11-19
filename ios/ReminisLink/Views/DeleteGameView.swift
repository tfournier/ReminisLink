import ReminisLinkKit
import SwiftUI

/// What the delete sheet is about.
struct DeleteTarget: Identifiable, Hashable {
    let deviceId: String
    let gameId: String
    let name: String
    var id: String { gameId }
}

/// Delete sheet (docs/specs/06-app-ios.md §4.5): lists exactly what will be
/// moved to the trash of the console, offers to keep the save and the
/// states, then offers to empty the trash at once.
struct DeleteGameView: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    let target: DeleteTarget
    var onDeleted: () -> Void = {}

    @State private var detail: GameDetail?
    @State private var keepSave = false
    @State private var result: TrashMoveResult?
    @State private var error: AppError?
    @State private var isWorking = false
    @State private var confirmEmpty = false
    /// The trash total read just before the confirmation, nil if unknown.
    @State private var freshTrashTotal: Int64?
    @State private var emptied: Int64?

    /// The files `DELETE /games/{id}` moves, with the option applied: the
    /// same selection as the pak (the save, its `.rtc` and the states stay
    /// with `keepSave`).
    static func filesToMove(_ files: [GameFile], keepSave: Bool) -> [GameFile] {
        files.filter { !keepSave || !GameDetail.isKeptWithSave($0.role) }
    }

    /// What else the pak moves without being listed by `GET /games/{id}`.
    static func unlistedNote(_ files: [GameFile], keepSave: Bool) -> LocalizedStringKey? {
        guard !keepSave else { return nil }
        if files.contains(where: { $0.role == .state }) {
            return "The thumbnails and other files of its states, their locks and its resume history go to the trash too."
        }
        return "The locks of its states and its resume history go to the trash too."
    }

    private var hasSaveData: Bool {
        detail?.files.contains { GameDetail.isKeptWithSave($0.role) } ?? false
    }

    var body: some View {
        NavigationStack {
            List {
                if let error {
                    Section { ErrorView(error: error) }
                }
                if let result {
                    doneSections(result)
                } else if let detail {
                    confirmSections(detail)
                }
            }
            .overlay {
                if detail == nil && error == nil && result == nil { ProgressView() }
            }
            .navigationTitle(result == nil ? "Delete \(target.name)?" : "Moved to the Trash")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: result == nil ? .cancellationAction : .confirmationAction) {
                    Button(result == nil ? "Cancel" : "Done") { dismiss() }
                        .accessibilityIdentifier(result == nil ? "delete.cancel" : "delete.done")
                }
            }
            .confirmationDialog("Empty the trash?", isPresented: $confirmEmpty, titleVisibility: .visible) {
                Button("Empty Trash", role: .destructive) { Task { await emptyTrash() } }
                    .accessibilityIdentifier("delete.emptyConfirm")
            } message: {
                // Never the size of this game alone: the whole trash goes.
                if let total = freshTrashTotal {
                    Text("\(total.byteString) will be permanently deleted from the SD card. This cannot be undone.")
                } else {
                    Text("Everything in the trash of the console will be permanently deleted. This cannot be undone.")
                }
            }
        }
        .interactiveDismissDisabled(isWorking)
        .task { if detail == nil { await load() } }
    }

    @ViewBuilder
    private func confirmSections(_ detail: GameDetail) -> some View {
        let files = Self.filesToMove(detail.files, keepSave: keepSave)
        Section {
            ForEach(Array(files.enumerated()), id: \.offset) { _, file in
                FileRow(file: file)
            }
        } header: {
            Text("Moved to the trash of the console")
        } footer: {
            VStack(alignment: .leading, spacing: 4) {
                Text("Total: \(files.reduce(0) { $0 + $1.sizeBytes }.byteString)")
                if detail.kind == .folderM3U {
                    Text("The whole folder of this multi-disc game is moved.")
                }
                if let note = Self.unlistedNote(detail.files, keepSave: keepSave) {
                    Text(note)
                }
            }
        }
        .accessibilityIdentifier("delete.files")
        if hasSaveData {
            Section {
                Toggle("Keep the Save and the States", isOn: $keepSave)
                    .accessibilityIdentifier("delete.keepSave")
            } footer: {
                Text("The save, its clock file, the states, their locks and the resume history stay in place on the SD card.")
            }
        }
        Section {
            Button(role: .destructive) {
                Task { await delete() }
            } label: {
                HStack {
                    Text("Move to Trash")
                    if isWorking {
                        Spacer()
                        ProgressView()
                    }
                }
            }
            .disabled(isWorking)
            .accessibilityIdentifier("delete.confirm")
        } footer: {
            Text("Everything stays in the trash of the console for at least 30 days. The space is only freed when the trash is emptied.")
        }
    }

    @ViewBuilder
    private func doneSections(_ result: TrashMoveResult) -> some View {
        Section {
            ForEach(result.moved, id: \.self) { path in
                Text(path)
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
            }
        } header: {
            Text("Moved to the trash of the console")
        } footer: {
            Text("\(result.movedBytes.byteString) stay on the SD card for at least 30 days, until the trash is emptied.")
        }
        .accessibilityIdentifier("delete.result")
        Section {
            if let emptied {
                Label("\(emptied.byteString) freed", systemImage: "checkmark.circle")
                    .accessibilityIdentifier("delete.emptied")
            } else {
                Button("Empty Trash Now", role: .destructive) {
                    // The trash may hold more than this game: show its
                    // current size in the confirmation.
                    Task {
                        let session = app.session(for: target.deviceId)
                        freshTrashTotal = await session.loadTrash() ? session.trash?.totalBytes : nil
                        confirmEmpty = true
                    }
                }
                    .disabled(isWorking)
                    .accessibilityIdentifier("delete.emptyNow")
            }
        } footer: {
            if emptied == nil {
                Text("Frees the space now. Everything in the trash of the console is permanently deleted.")
            }
        }
    }

    private func load() async {
        let gameId = target.gameId
        do {
            detail = try await app.session(for: target.deviceId).perform { try await $0.game(id: gameId) }
            error = nil
        } catch {
            let error = AppError(error)
            if !error.isCancellation { self.error = error }
        }
    }

    private func delete() async {
        isWorking = true
        defer { isWorking = false }
        do {
            result = try await app.session(for: target.deviceId).deleteGame(id: target.gameId, keepSave: keepSave)
            error = nil
            onDeleted()
        } catch {
            let error = AppError(error)
            if !error.isCancellation { self.error = error }
        }
    }

    private func emptyTrash() async {
        isWorking = true
        defer { isWorking = false }
        let session = app.session(for: target.deviceId)
        do {
            emptied = try await session.perform { try await $0.emptyTrash() }.freedBytes
            await session.refresh()
            error = nil
        } catch {
            let error = AppError(error)
            if !error.isCancellation { self.error = error }
        }
    }
}
