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

    /// Another device has write access (M10).
    private var isReadOnly: Bool { app.session(for: route.deviceId).isReadOnly }

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
                    .disabled(PatchNaming.refusal(kind: detail.kind, fileName: detail.fileName) != nil || isReadOnly)
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
                Section {
                    Button("Delete…", role: .destructive) {
                        toDelete = DeleteTarget(deviceId: route.deviceId, gameId: route.gameId, name: detail.name)
                    }
                    .disabled(isReadOnly)
                    .accessibilityIdentifier("game.deleteButton")
                }
            }
        }
        .overlay {
            if detail == nil && error == nil { ProgressView() }
        }
        .navigationTitle(route.name)
        .navigationBarTitleDisplayMode(.inline)
        .onDisappear { cancelDownload() }
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
        .sheet(item: $toDelete, onDismiss: {
            // The game is gone: back to its folder.
            if deleted, app.path.last == .game(route) { app.path.removeLast() }
        }) { target in
            DeleteGameView(target: target, onDeleted: { deleted = true })
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
