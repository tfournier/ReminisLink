import ReminisLinkKit
import SwiftUI

/// Restore sheet of a trash entry (M18, docs/specs/06-app-ios.md §4.3):
/// shows exactly what comes back and where, read from the console
/// (`GET /trash/{entryId}/restore`), then puts it back. A file already on
/// the SD card is never replaced: when a place is taken, the sheet says so
/// and offers to restore every file under the same free name instead.
struct TrashRestoreView: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    let deviceId: String
    let entry: TrashEntry

    @State private var preview: TrashRestorePreview?
    @State private var result: TrashRestoreResult?
    @State private var error: AppError?
    @State private var isWorking = false
    /// The console answered `409` to a restore in place: a file appeared
    /// since the preview, which was read again.
    @State private var conflictAppeared = false
    @State private var confirmReplace = false

    var body: some View {
        NavigationStack {
            List {
                if let error {
                    Section { ErrorView(error: error, retry: { await load() }, deviceId: deviceId) }
                }
                if let result {
                    doneSection(result)
                } else if let preview {
                    previewSections(preview)
                }
            }
            .overlay {
                if preview == nil && error == nil && result == nil { ProgressView() }
            }
            .readableWidth()
            .navigationTitle(result == nil ? "Restore \(entry.displayLabel)?" : "Restored")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: result == nil ? .cancellationAction : .confirmationAction) {
                    Button(result == nil ? "Cancel" : "Done") { dismiss() }
                        .accessibilityIdentifier(result == nil ? "trashRestore.cancel" : "trashRestore.done")
                }
            }
        }
        .interactiveDismissDisabled(isWorking)
        .task { if preview == nil { await load() } }
    }

    @ViewBuilder
    private func previewSections(_ preview: TrashRestorePreview) -> some View {
        Section {
            ForEach(preview.items, id: \.name) { item in
                RestoreItemRow(item: item, renamed: preview.canRename)
            }
        } header: {
            Text("Back to the SD card")
        } footer: {
            VStack(alignment: .leading, spacing: 4) {
                Text("Total: \(preview.restoredBytes.byteString)")
                Text("The locks and the resume history of a game come back with it.")
            }
        }
        .accessibilityIdentifier("trashRestore.items")
        if preview.conflict {
            Section {
                Label {
                    Text(conflictAppeared
                        ? "A file appeared on the SD card where this entry goes back. Nothing was changed."
                        : "A file with the same name is already on the SD card. ReminisLink never replaces it without asking.")
                } icon: {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .foregroundStyle(.orange)
                        .accessibilityHidden(true)
                }
                .accessibilityIdentifier("trashRestore.conflict")
                if preview.canRename {
                    Button {
                        Task { await restore(.rename) }
                    } label: {
                        working("Restore Under a Free Name")
                    }
                    .disabled(isWorking)
                    .accessibilityIdentifier("trashRestore.rename")
                } else {
                    // No free name exists: never offer a doomed action.
                    Text(preview.noFreeName == .pathBlocked
                        ? "A file blocks the folder where this entry goes back: no free name can be used. Move or delete that file, then try again."
                        : "Every free name is already taken on the SD card.")
                        .foregroundStyle(.secondary)
                        .accessibilityIdentifier("trashRestore.noFreeName")
                }
            } footer: {
                if preview.canRename {
                    Text("Every file comes back with the same number added to its name, so the files of a game stay together.")
                }
            }
            replaceSection(preview)
        } else if preview.replaceable {
            Section {
                Button {
                    Task { await restore(.fail) }
                } label: {
                    working("Restore")
                }
                .disabled(isWorking)
                .accessibilityIdentifier("trashRestore.confirm")
            } footer: {
                Text(preview.keptAs == .backup
                    ? "The copy goes back to the backup copies of the game; the current save does not change."
                    : "Every file goes back to its original place.")
            }
            replaceSection(preview)
        } else {
            Section {
                Button {
                    Task { await restore(.fail) }
                } label: {
                    working("Restore")
                }
                .disabled(isWorking)
                .accessibilityIdentifier("trashRestore.confirm")
            } footer: {
                Text("Every file goes back to its original place.")
            }
        }
    }

    /// "Replace the Current Save" (a save, or its backup copy, of a game
    /// still on the console), with a confirmation saying where the current
    /// save goes.
    @ViewBuilder
    private func replaceSection(_ preview: TrashRestorePreview) -> some View {
        if !preview.replaceable, preview.replaceUnavailable == "place-blocked" || preview.replaceUnavailable == "backup-taken" {
            Section {
                Text("The current save cannot be replaced now: something else holds the place of the save or of its copy on the SD card.")
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("trashRestore.replaceUnavailable")
            }
        }
        if preview.replaceable {
            // Nothing in place (the game lost its save and clock file):
            // the action only installs this save.
            let installOnly = preview.replaceDisplaced.isEmpty
            let title: LocalizedStringKey = installOnly ? "Make It the Game's Save" : "Replace the Current Save"
            Section {
                Button(role: installOnly ? nil : .destructive) {
                    confirmReplace = true
                } label: {
                    working(title)
                }
                .disabled(isWorking)
                .accessibilityIdentifier("trashRestore.replace")
            } footer: {
                Text(replaceExplanation(preview))
            }
            .confirmationDialog(installOnly ? "Make it the game's save?" : "Replace the current save?",
                                isPresented: $confirmReplace, titleVisibility: .visible) {
                Button(title, role: installOnly ? nil : .destructive) {
                    Task { await restore(.replace) }
                }
                .accessibilityIdentifier("trashRestore.replaceConfirm")
            } message: {
                Text(replaceExplanation(preview))
            }
        }
    }

    /// Says exactly where the current files go (the pak's answer, and the
    /// clock file of the copy: a pair goes in as one journaled replacement,
    /// which also puts the current pair in the trash).
    private func replaceExplanation(_ preview: TrashRestorePreview) -> LocalizedStringKey {
        let withClock = preview.items.contains { $0.name.lowercased().hasSuffix(".rtc") }
        switch preview.keptAs {
        case .backup? where withClock:
            return "This save becomes the save of the game. The current save is kept first as a backup copy of the game (Save screen); as part of the safe replacement, the current save and its clock file are also placed in the trash of the console."
        case .backup?:
            return "This save becomes the save of the game. The current save is kept first as a backup copy of the game (Save screen)."
        case nil:
            return "The game has no save on the console now: this save becomes its save."
        default:
            return "This save becomes the save of the game. The current save goes to the trash of the console first."
        }
    }

    private func doneSection(_ result: TrashRestoreResult) -> some View {
        Section {
            ForEach(result.restored, id: \.self) { name in
                Text(name)
                    .font(.caption.monospaced())
                    .textSelection(.enabled)
            }
        } header: {
            Text("Back on the SD card")
        } footer: {
            if result.keptAs == .backup {
                Text("This save is now the save of the game. The previous save is kept as a backup copy of the game (Save screen).")
            } else if result.keptAs != nil {
                Text("This save is now the save of the game. The previous save is in the trash of the console.")
            } else if result.renamed {
                Text("The files came back under a free name: the files already on the SD card were not changed.")
            } else {
                Text("Every file is back in its original place.")
            }
        }
        .accessibilityIdentifier("trashRestore.done.list")
    }

    private func working(_ title: LocalizedStringKey) -> some View {
        HStack {
            Text(title)
            if isWorking {
                Spacer()
                ProgressView()
            }
        }
    }

    private func load() async {
        do {
            preview = try await app.session(for: deviceId).trashRestorePreview(entry.id)
            error = nil
        } catch {
            let error = AppError.trashRestore(error)
            if !error.isCancellation { self.error = error }
        }
    }

    private func restore(_ onConflict: TrashRestoreConflict) async {
        isWorking = true
        defer { isWorking = false }
        do {
            result = try await app.session(for: deviceId).restoreTrashEntry(entry.id, onConflict: onConflict)
            error = nil
        } catch {
            if case .api(.server(let server)) = AppError(error), server.trashRestoreConflicts != nil {
                // A file appeared since the preview (or the free names ran
                // out): show what is taken now.
                conflictAppeared = onConflict == .fail
                await load()
                return
            }
            let error = AppError.trashRestore(error)
            if !error.isCancellation { self.error = error }
        }
    }
}

/// A file of a restore preview: its original place and, when the entry
/// goes back under a free name, the name it gets.
private struct RestoreItemRow: View {
    let item: TrashRestoreItem
    let renamed: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(alignment: .firstTextBaseline) {
                Text(fileName(item.name)).font(.headline)
                Spacer()
                Text(item.size.byteString).foregroundStyle(.secondary)
            }
            Text(item.name)
                .font(.caption.monospaced())
                .foregroundStyle(.secondary)
                .textSelection(.enabled)
            if item.conflict {
                Text("Already on the SD card")
                    .font(.footnote)
                    .foregroundStyle(.orange)
            }
            if renamed, let free = item.freeName, free != item.name {
                Text("Comes back as \(free)")
                    .font(.footnote)
            }
        }
        .accessibilityElement(children: .combine)
    }

    private func fileName(_ path: String) -> String {
        path.split(separator: "/").last.map(String.init) ?? path
    }
}
