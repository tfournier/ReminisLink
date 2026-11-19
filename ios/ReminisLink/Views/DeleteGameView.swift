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
    /// What the pak would move and keep with the current option, read
    /// again when the option changes (nil while loading).
    @State private var preview: DeletionPreview?
    @State private var previewError: AppError?

    /// The Keep option is offered when the pak would keep something with
    /// it (a save, a state, a lock, a resume history...), read from its
    /// preview; once offered, it stays while the option changes.
    @State private var hasSaveData = false

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
        .task(id: keepSave) { await loadPreview() }
    }

    @ViewBuilder
    private func confirmSections(_ detail: GameDetail) -> some View {
        // The exact selection of the pak (M8): never computed here.
        if let preview {
            let copies = preview.moved.filter(DeletionPreview.isPakCopy)
            Section {
                ForEach(Array(preview.moved.filter { !DeletionPreview.isPakCopy($0) }.enumerated()), id: \.offset) { _, item in
                    FileRow(item: item)
                }
                if !copies.isEmpty {
                    // Locks and resume history: one row, not a file each.
                    HStack {
                        Text("ReminisLink Copies (\(copies.count))").font(.headline)
                        Spacer()
                        Text(copies.reduce(0) { $0 + $1.size }.byteString).foregroundStyle(.secondary)
                    }
                    .accessibilityElement(children: .combine)
                    .accessibilityIdentifier("delete.pakCopies")
                }
            } header: {
                Text("Moved to the trash of the console")
            } footer: {
                Text("Total: \(preview.movedBytes.byteString)")
            }
            .accessibilityIdentifier("delete.files")
            if !preview.kept.isEmpty {
                Section {
                    ForEach(Array(preview.kept.enumerated()), id: \.offset) { _, item in
                        FileRow(item: item)
                    }
                } header: {
                    Text("Kept in place")
                } footer: {
                    Text("Discs that another playlist of the system still lists stay on the SD card, and so do the save and the states when you keep them.")
                }
                .accessibilityIdentifier("delete.kept")
            }
        } else if let previewError {
            Section { ErrorView(error: previewError) }
        } else {
            Section {
                HStack {
                    Text("Reading what will be moved…").foregroundStyle(.secondary)
                    Spacer()
                    ProgressView()
                }
                .accessibilityIdentifier("delete.loading")
            }
        }
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
            .disabled(isWorking || preview == nil)
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

    /// `GET /games/{id}/deletion` with the current option. The Delete
    /// button stays disabled until it answers.
    private func loadPreview() async {
        let gameId = target.gameId
        let keepSave = keepSave
        preview = nil
        previewError = nil
        do {
            let preview = try await app.session(for: target.deviceId).perform {
                try await $0.deletionPreview(gameId: gameId, keepSave: keepSave)
            }
            guard keepSave == self.keepSave, !Task.isCancelled else { return }
            self.preview = preview
            if preview.hasDataKeptWithSave(keepSave: keepSave) { hasSaveData = true }
        } catch {
            let error = AppError(error)
            if !error.isCancellation && keepSave == self.keepSave { previewError = error }
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
