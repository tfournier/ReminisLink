import ReminisLinkKit
import SwiftUI

/// Game sheet (docs/specs/06-app-ios.md §4.5): header (tap the image to
/// change it, M11), the save, states and cheats screens, the patch, the
/// files of the game (each can be downloaded to the iPhone, M11) and its
/// deletion.
struct GameDetailView: View {
    @Environment(AppModel.self) private var app
    let route: GameRoute
    @State private var detail: GameDetail?
    @State private var error: AppError?
    @State private var toDelete: DeleteTarget?
    @State private var deleted = false
    @State private var patch: PatchContext?
    @State private var artNotice: String?
    @State private var artError: AppError?
    /// The file being downloaded (one at a time), cancelled when the
    /// screen goes away.
    @State private var download: FileDownload?
    @State private var downloadTask: Task<Void, Never>?
    @State private var downloadError: AppError?
    @State private var exported: ExportedFile?
    /// M19: the export of the game's data as an archive.
    @State private var archiveDownload: FileDownload?
    @State private var archiveTask: Task<Void, Never>?
    @State private var archiveError: AppError?
    @State private var askArchive = false
    @State private var archiveImport: ArchiveImportContext?
    /// M23: the rename of the game.
    @State private var isRenaming = false
    @State private var newName = ""
    @State private var renameError: AppError?
    @State private var isRenamingNow = false

    /// The Patch screen closed by itself on this game (docs/specs/06-app-ios.md §4.5).
    private var patchNotice: PatchNotice? {
        guard let notice = app.patchNotice, notice.deviceId == route.deviceId,
              notice.gameId == route.gameId else { return nil }
        return notice
    }

    /// Whether the game has an image now (it may have been changed here).
    private var hasArt: Bool {
        detail.map { $0.files.contains { $0.role == .art } } ?? route.hasArt
    }

    var body: some View {
        List {
            Section {
                HStack(alignment: .top, spacing: 16) {
                    GameArtButton(
                        deviceId: route.deviceId, gameId: route.gameId, hasArt: hasArt,
                        isGameGone: error == .gameRemoved,
                        notice: $artNotice, error: $artError
                    )
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
                if let artError {
                    ErrorView(error: artError)
                }
                if let artNotice {
                    NoticeView(text: artNotice)
                }
                if let patchNotice {
                    NoticeView(text: patchNotice.text)
                        .transition(.opacity)
                }
            }

            if let error {
                Section { ErrorView(error: error, retry: { await load() }) }
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
                            // M23: the number, from a pak that gives it.
                            if let count = detail.cheatCount {
                                Text(count, format: .number)
                            } else {
                                Text(detail.hasCheats ? "Yes" : "No")
                            }
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
                    if let downloadError {
                        ErrorView(error: downloadError)
                    }
                    ForEach(Array(detail.files.enumerated()), id: \.offset) { _, file in
                        GameFileRow(
                            file: file,
                            download: file.fileId != nil && download?.fileId == file.fileId ? download : nil,
                            canDownload: download == nil,
                            onDownload: { startDownload(file) },
                            onCancel: cancelDownload
                        )
                    }
                } header: {
                    Text("Files")
                } footer: {
                    Text("Download a file to keep a copy on this iPhone: Save to Files, AirDrop… The console keeps its file.")
                }
                archiveSection(detail)
                Section {
                    if let renameError {
                        ErrorView(error: renameError)
                    }
                    Button {
                        newName = GameNameRule.editableName(fileName: detail.fileName, isFolder: detail.kind.isFolder)
                        isRenaming = true
                    } label: {
                        HStack {
                            Label("Rename…", systemImage: "pencil")
                            if isRenamingNow {
                                Spacer()
                                ProgressView()
                            }
                        }
                    }
                    .disabled(isRenamingNow)
                    .accessibilityIdentifier("game.rename")
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
        .onDisappear {
            // Leaving the screen cancels the download; a change of layout
            // (M15: the Library columns collapsing or expanding) only moves
            // it, and the game stays the last screen of the path.
            if app.path.last != .game(route) {
                cancelDownload()
                cancelArchive()
            }
            if patchNotice != nil { app.patchNotice = nil }
        }
        // Said for a few seconds, then gone.
        .task(id: patchNotice?.id) {
            guard let id = patchNotice?.id else { return }
            try? await Task.sleep(for: app.patchNoticeDuration)
            guard !Task.isCancelled, app.patchNotice?.id == id else { return }
            withAnimation { app.patchNotice = nil }
        }
        .exportSheet($exported)
        .task { if detail == nil { await load() } }
        .refreshable { await load() }
        // Back from the save, states or cheats screens: counters may have changed.
        .onChange(of: app.session(for: route.deviceId).libraryRevision) {
            // M14: a change of another system leaves this game as it is.
            guard app.session(for: route.deviceId).libraryChangeAffects(detail?.systemId) else { return }
            Task { await load() }
        }
        .sheet(item: $patch) { context in
            PatchView(context: context)
        }
        .sheet(item: $archiveImport) { context in
            ArchiveImportView(context: context)
        }
        .confirmationDialog("Export the data of \(detail?.name ?? route.name)", isPresented: $askArchive, titleVisibility: .visible) {
            Button("Without the Game File") { startArchive(includeROM: false) }
                .accessibilityIdentifier("archive.export.data")
            if detail?.kind == .file {
                Button("With the Game File") { startArchive(includeROM: true) }
                    .accessibilityIdentifier("archive.export.rom")
            }
        } message: {
            Text("A zip with the save, its clock file, the states and their thumbnails, the cheats and the art, to keep on this iPhone or import on another console.")
        }
        .alert("Rename the Game", isPresented: $isRenaming) {
            TextField("Name", text: $newName)
                .accessibilityIdentifier("game.rename.name")
            Button("Rename") { Task { await rename() } }
                .accessibilityIdentifier("game.rename.confirm")
            Button("Cancel", role: .cancel) {}
        } message: {
            Text(renameMessage)
        }
        .sheet(item: $toDelete, onDismiss: {
            // The game is gone: back to its folder.
            if deleted, app.path.last == .game(route) { app.path.removeLast() }
        }) { target in
            DeleteGameView(target: target, onDeleted: { deleted = true })
        }
    }

    /// What the rename keeps and does (the extension, the files that follow).
    private var renameMessage: String {
        let ext = detail.map { GameNameRule.keptExtension(fileName: $0.fileName, isFolder: $0.kind.isFolder) } ?? ""
        let follows = String(localized: "Its save, states, cheats and art are renamed with it, never over another file.")
        return ext.isEmpty ? follows : String(localized: "The extension \(ext) is kept.") + " " + follows
    }

    /// M23: renames the game, then shows it under its new identifier.
    private func rename() async {
        guard let detail else { return }
        let ext = GameNameRule.keptExtension(fileName: detail.fileName, isFolder: detail.kind.isFolder)
        if let problem = GameNameRule.problem(newName, fileExtension: ext) {
            renameError = .gameNameProblem(problem)
            return
        }
        isRenamingNow = true
        defer { isRenamingNow = false }
        do {
            let result = try await app.session(for: route.deviceId).renameGame(id: route.gameId, name: newName)
            renameError = nil
            let renamed = GameRoute(
                deviceId: route.deviceId, gameId: result.game.id, systemLabel: route.systemLabel,
                name: result.game.name, hasArt: result.game.hasArt, fileName: result.game.fileName
            )
            var path = app.path
            if let index = path.lastIndex(of: .game(route)) {
                // Screens pushed above the game named the old one.
                path = Array(path[..<index]) + [.game(renamed)]
                app.path = path
            }
        } catch {
            let error = AppError(error)
            if !error.isCancellation { renameError = error }
        }
    }

    /// Downloads a file to the export directory, then opens the share
    /// sheet. A `404` means the file changed on the console: the list is
    /// loaded again.
    private func startDownload(_ file: GameFile) {
        guard let fileId = file.fileId else { return }
        downloadTask?.cancel()
        let progress = FileDownload(fileId: fileId, expected: file.sizeBytes)
        download = progress
        downloadError = nil
        let session = app.session(for: route.deviceId)
        let gameId = route.gameId
        downloadTask = Task {
            do {
                let file = try await session.downloadGameFile(gameId: gameId, file: file, progress: progress)
                guard !Task.isCancelled, download === progress else { return ExportedFiles.remove(file.file) }
                download = nil
                exported = file
            } catch {
                guard download === progress else { return }
                download = nil
                let error = AppError(error)
                if error.isCancellation { return }
                if error == .gameFileChanged {
                    // Said once the list is really reloaded; a game that is
                    // gone says so instead (and its rows go).
                    if await load() { downloadError = error }
                } else {
                    downloadError = error
                }
            }
        }
    }

    /// The Archive section (M19): export the game's data as a zip, or
    /// import one into this game.
    @ViewBuilder
    private func archiveSection(_ detail: GameDetail) -> some View {
        Section {
            if let archiveError {
                ErrorView(error: archiveError)
            }
            if let archiveDownload {
                HStack {
                    ProgressView()
                    Text("Preparing the archive… \(archiveDownload.received.byteString)")
                        .foregroundStyle(.secondary)
                    Spacer()
                    Button(action: cancelArchive) {
                        Image(systemName: "xmark.circle.fill").foregroundStyle(.secondary)
                    }
                    .buttonStyle(.borderless)
                    .accessibilityLabel(Text("Cancel the export"))
                }
                .accessibilityIdentifier("archive.exporting")
            } else {
                Button {
                    askArchive = true
                } label: {
                    Label("Export the Game Data…", systemImage: "square.and.arrow.up")
                }
                .accessibilityIdentifier("game.archive.export")
            }
            Button {
                archiveImport = ArchiveImportContext(deviceId: route.deviceId, gameId: route.gameId, gameName: detail.name)
            } label: {
                Label("Import an Archive…", systemImage: "square.and.arrow.down")
            }
            .accessibilityIdentifier("game.archive.import")
        } header: {
            Text("Archive")
        } footer: {
            Text("Move this game to another console, or keep its data: the save, the states, the cheats and the art in one file. On import, whatever is replaced goes to the console's trash first.")
        }
    }

    private func startArchive(includeROM: Bool) {
        archiveTask?.cancel()
        let progress = FileDownload(fileId: "archive", expected: nil)
        archiveDownload = progress
        archiveError = nil
        let session = app.session(for: route.deviceId)
        let gameId = route.gameId
        archiveTask = Task {
            do {
                let file = try await session.downloadGameArchive(gameId: gameId, includeROM: includeROM, progress: progress)
                guard !Task.isCancelled, archiveDownload === progress else { return ExportedFiles.remove(file.file) }
                archiveDownload = nil
                exported = file
            } catch {
                guard archiveDownload === progress else { return }
                archiveDownload = nil
                let error = AppError(error)
                if !error.isCancellation { archiveError = error }
            }
        }
    }

    private func cancelArchive() {
        archiveTask?.cancel()
        archiveTask = nil
        archiveDownload = nil
    }

    private func cancelDownload() {
        downloadTask?.cancel()
        downloadTask = nil
        download = nil
    }

    /// Whether the game sheet is up to date. A game no longer on the
    /// console loses its rows and actions.
    @discardableResult
    private func load() async -> Bool {
        do {
            detail = try await app.session(for: route.deviceId).gameDetail(id: route.gameId)
            error = nil
            return true
        } catch {
            let error = AppError(error)
            if error == .gameRemoved {
                cancelDownload()
                detail = nil
                downloadError = nil
            }
            if !error.isCancellation { self.error = error }
            return false
        }
    }
}

/// A file of the game sheet (M11): what it is, its name and size, and a
/// button to download it to the iPhone (progress and cancel while it runs).
struct GameFileRow: View {
    let file: GameFile
    let download: FileDownload?
    let canDownload: Bool
    let onDownload: () -> Void
    let onCancel: () -> Void

    var body: some View {
        HStack(spacing: 12) {
            VStack(alignment: .leading, spacing: 4) {
                HStack {
                    GameFileRoleTitle(role: file.role, slot: file.slot).font(.headline)
                    Spacer()
                    Text(file.sizeBytes.byteString).foregroundStyle(.secondary)
                }
                Text(file.name)
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
                    .textSelection(.enabled)
                if let download {
                    ProgressView(value: download.fraction ?? 0)
                        .accessibilityLabel(Text("Downloading \(file.name)"))
                        .accessibilityIdentifier("file.progress")
                }
            }
            .accessibilityElement(children: .combine)
            if download != nil {
                Button(action: onCancel) {
                    Image(systemName: "xmark.circle.fill")
                        .font(.title2)
                        .foregroundStyle(.secondary)
                }
                .buttonStyle(.borderless)
                .accessibilityLabel(Text("Cancel the download of \(file.name)"))
                .accessibilityIdentifier("file.cancel")
            } else if file.fileId != nil {
                // A pak older than M11 cannot serve the file: no button.
                Button(action: onDownload) {
                    Image(systemName: "arrow.down.circle")
                        .font(.title2)
                }
                .buttonStyle(.borderless)
                .disabled(!canDownload)
                .accessibilityLabel(Text("Download \(file.name)"))
                .accessibilityIdentifier("file.download")
            }
        }
    }
}

/// What a file of a game is: Game, Save, State 2…
struct GameFileRoleTitle: View {
    let role: GameFileRole
    var slot: Int?

    var body: some View {
        switch role {
        case .rom: Text("Game")
        case .save: Text("Save")
        case .rtc: Text("Clock of the save")
        case .state:
            if let slot { Text("State \(slot)") } else { Text("State") }
        case .cheats: Text("Cheats")
        case .art: Text("Art")
        case .other: Text("Other File")
        default: Text(verbatim: role.rawValue)
        }
    }
}

struct FileRow: View {
    let role: GameFileRole
    var slot: Int?
    let path: String
    let sizeBytes: Int64

    /// A file of a deletion preview (M8).
    init(item: DeletionItem) {
        role = item.role
        path = item.name
        sizeBytes = item.size
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                GameFileRoleTitle(role: role, slot: slot).font(.headline)
                Spacer()
                Text(sizeBytes.byteString).foregroundStyle(.secondary)
            }
            Text(path)
                .font(.caption.monospaced())
                .foregroundStyle(.secondary)
                .textSelection(.enabled)
        }
        .accessibilityElement(children: .combine)
    }
}
