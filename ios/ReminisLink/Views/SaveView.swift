import ReminisLinkKit
import SwiftUI

/// The save of a game (docs/specs/06-app-ios.md §4.5): download it,
/// replace it from Files, and its backup copies (create with a label,
/// restore, download, delete). The pak copies the current save before
/// every replacement or restoration.
struct SaveView: View {
    @Environment(AppModel.self) private var app
    let route: GameRoute

    @State private var save: GameFile?
    @State private var backups: [SaveBackup]?
    @State private var error: AppError?
    @State private var notice: String?
    @State private var isWorking = false
    @State private var exported: ExportedFile?
    @State private var showImporter = false
    @State private var replacement: PickedFile?
    @State private var showCreate = false
    @State private var selected: SaveBackup?
    @State private var toRestore: SaveBackup?
    @State private var toDelete: SaveBackup?
    /// A download in progress, cancelled when the screen goes away.
    @State private var downloadTask: Task<Void, Never>?

    private var session: ConsoleSession { app.session(for: route.deviceId) }

    var body: some View {
        List {
            if let error {
                Section { ErrorView(error: error, retry: load) }
            }
            if let notice {
                Section { NoticeView(text: notice) }
            }
            currentSection
            if let backups {
                backupsSection(backups)
            }
        }
        .overlay {
            if backups == nil && error == nil { ProgressView() }
        }
        .disabled(isWorking)
        .overlay(alignment: .bottom) {
            if isWorking {
                ProgressView().padding().background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12)).padding()
            }
        }
        .navigationTitle("Save")
        .onDisappear { downloadTask?.cancel() }
        .navigationBarTitleDisplayMode(.inline)
        .task { if backups == nil { await load() } }
        .refreshable { await load() }
        .onChange(of: session.libraryRevision) { Task { await load() } }
        .fileImporter(isPresented: $showImporter, allowedContentTypes: [.item]) { result in
            Task { await picked(result) }
        }
        .exportSheet($exported)
        .sheet(isPresented: $showCreate) {
            CreateBackupView { label in await createBackup(label: label) }
        }
        .confirmationDialog(
            selected.map(Self.title) ?? "",
            isPresented: Binding(get: { selected != nil }, set: { if !$0 { selected = nil } }),
            titleVisibility: .visible, presenting: selected
        ) { backup in
            Button("Restore…") { toRestore = backup }
                .accessibilityIdentifier("backup.restore")
            Button("Download") { downloadTask = Task { await download(backup) } }
                .accessibilityIdentifier("backup.download")
            Button("Delete…", role: .destructive) { toDelete = backup }
                .accessibilityIdentifier("backup.delete")
        } message: { backup in
            Text("\(backup.createdAt.dateTimeString) · \(backup.sizeBytes.byteString)")
        }
        .alert(
            "Replace the save?",
            isPresented: Binding(get: { replacement != nil }, set: { if !$0 { replacement = nil } }),
            presenting: replacement
        ) { file in
            Button("Replace") { Task { await replace(with: file) } }
                .accessibilityIdentifier("save.replaceConfirm")
            Button("Cancel", role: .cancel) { file.remove() }
        } message: { file in
            if save != nil {
                Text("“\(file.name)” replaces the save of \(route.name). The current save is first copied (backup copy).")
            } else {
                Text("“\(file.name)” becomes the save of \(route.name), which has none yet.")
            }
        }
        .alert(
            "Restore this copy?",
            isPresented: Binding(get: { toRestore != nil }, set: { if !$0 { toRestore = nil } }),
            presenting: toRestore
        ) { backup in
            Button("Restore") { Task { await restore(backup) } }
                .accessibilityIdentifier("backup.restoreConfirm")
            Button("Cancel", role: .cancel) {}
        } message: { backup in
            Text("The current save is first copied (backup copy), then replaced by the copy of \(backup.createdAt.dateTimeString).")
        }
        .alert(
            "Delete this copy?",
            isPresented: Binding(get: { toDelete != nil }, set: { if !$0 { toDelete = nil } }),
            presenting: toDelete
        ) { backup in
            Button("Delete", role: .destructive) { Task { await delete(backup) } }
                .accessibilityIdentifier("backup.deleteConfirm")
            Button("Cancel", role: .cancel) {}
        } message: { _ in
            Text("The copy goes to the trash of the console. The current save does not change.")
        }
    }

    // MARK: Sections

    @ViewBuilder
    private var currentSection: some View {
        Section {
            if let save {
                LabeledContent("Size", value: save.sizeBytes.byteString)
                LabeledContent("Modified", value: save.modifiedAt.dateTimeString)
                Text(save.path)
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
                Button {
                    downloadTask = Task { await downloadSave() }
                } label: {
                    Label("Download", systemImage: "square.and.arrow.up")
                }
                .accessibilityIdentifier("save.download")
            } else if backups != nil {
                Text("This game has no save on the console.")
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("save.none")
            }
            Button {
                showImporter = true
            } label: {
                Label("Replace from Files…", systemImage: "folder")
            }
            .accessibilityIdentifier("save.replace")
            if save != nil {
                Button {
                    showCreate = true
                } label: {
                    Label("Make a Backup Copy…", systemImage: "doc.on.doc")
                }
                .accessibilityIdentifier("save.createBackup")
            }
        } header: {
            Text("Current Save")
        } footer: {
            Text("Before replacing or restoring the save, the console always copies the current one (backup copy).")
        }
    }

    private func backupsSection(_ backups: [SaveBackup]) -> some View {
        Section {
            if backups.isEmpty {
                Text("No backup copy yet.")
                    .foregroundStyle(.secondary)
            }
            ForEach(backups) { backup in
                Button {
                    selected = backup
                } label: {
                    BackupRow(backup: backup)
                }
                .foregroundStyle(.primary)
                .accessibilityIdentifier("backup.row")
            }
        } header: {
            Text("Backup Copies")
        } footer: {
            Text("Automatic copies are limited to 10 per game: the oldest ones are deleted. Copies you make yourself are kept.")
        }
    }

    // MARK: Actions

    static func title(_ backup: SaveBackup) -> String {
        BackupRow.title(backup)
    }

    private func load() async {
        let gameId = route.gameId
        do {
            async let detail = session.perform { try await $0.game(id: gameId) }
            async let list = session.perform { try await $0.saveBackups(gameId: gameId) }
            let (game, copies) = try await (detail, list)
            save = game.files.first { $0.role == .save }
            backups = copies
            error = nil
        } catch {
            show(error)
        }
    }

    private func show(_ error: Error) {
        let error = AppError.gameData(error)
        if !error.isCancellation { self.error = error }
    }

    private func run(_ action: () async throws -> Void) async {
        isWorking = true
        defer { isWorking = false }
        error = nil
        notice = nil
        do {
            try await action()
        } catch {
            show(error)
        }
    }

    private func downloadSave() async {
        let gameId = route.gameId
        await run {
            share(try await session.export { try await $0.downloadSave(gameId: gameId, into: $1) })
        }
    }

    private func download(_ backup: SaveBackup) async {
        let gameId = route.gameId
        let backupId = backup.id
        await run {
            share(try await session.export { try await $0.downloadSaveBackup(gameId: gameId, backupId: backupId, into: $1) })
        }
    }

    /// Opens the share sheet, unless the screen went away meanwhile.
    private func share(_ file: ExportedFile) {
        guard !Task.isCancelled else { return ExportedFiles.remove(file.file) }
        exported = file
    }

    private func picked(_ result: Result<URL, Error>) async {
        switch result {
        case .success(let url):
            await run {
                replacement = try await app.work.run { try await PickedFile.copy(url, into: app.transferDirectory) }
            }
        case .failure:
            error = .file
        }
    }

    private func replace(with file: PickedFile) async {
        replacement = nil
        await run {
            switch try await session.replaceSave(gameId: route.gameId, gameName: route.name, with: file) {
            case .replaced(let backup):
                notice = backup == nil
                    ? String(localized: "Save replaced.")
                    : String(localized: "Save replaced. The previous save was kept as a backup copy.")
                await load()
            case .queued:
                notice = String(localized: "The save is larger than 64 MB: it is being sent in Transfers. The current save is copied first.")
            }
        }
    }

    private func createBackup(label: String?) async -> Bool {
        let gameId = route.gameId
        var created = false
        await run {
            _ = try await session.perform { try await $0.createSaveBackup(gameId: gameId, label: label) }
            created = true
            notice = String(localized: "Backup copy created.")
            await load()
        }
        return created
    }

    private func restore(_ backup: SaveBackup) async {
        let gameId = route.gameId
        let backupId = backup.id
        await run {
            let result = try await session.perform { try await $0.restoreSaveBackup(gameId: gameId, backupId: backupId) }
            notice = result.backup == nil
                ? String(localized: "Copy restored.")
                : String(localized: "Copy restored. The previous save was kept as a backup copy.")
            session.contentChanged()
            await load()
        }
    }

    private func delete(_ backup: SaveBackup) async {
        let gameId = route.gameId
        let backupId = backup.id
        await run {
            try await session.perform { try await $0.deleteSaveBackup(gameId: gameId, backupId: backupId) }
            notice = String(localized: "Copy moved to the trash of the console.")
            await load()
        }
    }
}

/// One backup copy: what it is, when, how big.
struct BackupRow: View {
    let backup: SaveBackup

    /// Automatic copies are named after their reason (the pak's label is
    /// not translated); manual ones after their label.
    static func title(_ backup: SaveBackup) -> String {
        switch backup.reason {
        case .manual: backup.label ?? String(localized: "Backup copy")
        case .beforeReplace: String(localized: "Before replacement")
        case .beforeRestore: String(localized: "Before restoration")
        case .beforeDelete: String(localized: "Before deletion")
        case .beforePatch: String(localized: "Before patch")
        default: backup.label ?? String(localized: "Automatic copy")
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text(Self.title(backup)).font(.headline)
                if backup.reason != .manual {
                    TagView(text: Text("Automatic"), tint: .secondary)
                }
                Spacer()
                Text(backup.sizeBytes.byteString).foregroundStyle(.secondary)
            }
            Text(backup.createdAt.dateTimeString)
                .font(.subheadline)
                .foregroundStyle(.secondary)
        }
        .accessibilityElement(children: .combine)
        .accessibilityHint(Text("Shows the actions for this copy"))
    }
}

/// Asks for the optional label of a new backup copy (80 characters).
struct CreateBackupView: View {
    @Environment(\.dismiss) private var dismiss
    let create: (String?) async -> Bool
    @State private var label = ""
    @State private var isCreating = false

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField("Label", text: $label, prompt: Text("Optional"))
                        .accessibilityIdentifier("backup.label")
                        .onChange(of: label) { _, new in
                            let limited = BackupLabel.limited(new)
                            if limited != new { label = limited }
                        }
                } footer: {
                    Text("\(label.unicodeScalars.count)/\(BackupLabel.maxLength) · A copy of the current save is made on the console.")
                }
            }
            .navigationTitle("New Backup Copy")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    if isCreating {
                        ProgressView()
                    } else {
                        Button("Create") {
                            Task {
                                isCreating = true
                                let created = await create(BackupLabel.normalized(label))
                                isCreating = false
                                if created { dismiss() }
                            }
                        }
                        .accessibilityIdentifier("backup.create")
                    }
                }
            }
        }
        .presentationDetents([.medium])
        .interactiveDismissDisabled(isCreating)
    }
}
