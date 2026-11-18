import ReminisLinkKit
import SwiftUI
import UniformTypeIdentifiers

/// A file chosen on the Add screen.
struct AddFile: Identifiable, Equatable {
    let id = UUID()
    let url: URL
    let name: String
    let sizeBytes: Int64
    /// From the document picker: needs `startAccessingSecurityScopedResource`.
    let isSecurityScoped: Bool
    var kind: AddKind
    var systemId: String?
    var game: PickedGame?
}

/// A game chosen as the destination of a save.
struct PickedGame: Hashable {
    let id: String
    let name: String
}

/// Add screen (docs/specs/06-app-ios.md §4.6): files from Files (several at
/// once, system suggested from the extension) or a link downloaded by the
/// console. Prefilled by the share extension. The app proposes no source
/// of games.
struct AddView: View {
    enum Mode: Hashable {
        case files
        case link
    }

    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    let context: AddContext

    @State private var deviceId: String?
    @State private var mode: Mode = .files
    @State private var files: [AddFile] = []
    @State private var showImporter = false
    @State private var systems: [GameSystem]?
    @State private var linkText = ""
    @State private var linkKind: AddKind = .rom
    @State private var linkSystemId: String?
    @State private var canPaste = false
    @State private var error: AppError?
    @State private var isSending = false
    @State private var didLoadRequest = false

    var body: some View {
        NavigationStack {
            Form {
                if context.deviceId == nil && app.store.devices.count > 1 {
                    Section {
                        Picker("Console", selection: $deviceId) {
                            Text("Choose").tag(String?.none)
                            ForEach(app.store.devices) { device in
                                Text(device.name).tag(Optional(device.id))
                            }
                        }
                    }
                }
                Section {
                    Picker("Source", selection: $mode) {
                        Text("Files").tag(Mode.files)
                        Text("Link").tag(Mode.link)
                    }
                    .pickerStyle(.segmented)
                    .accessibilityIdentifier("add.mode")
                }
                if let error {
                    Section { ErrorView(error: error) }
                }
                switch mode {
                case .files: filesSection
                case .link: linkSection
                }
            }
            .navigationTitle("Add")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { close() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    if isSending {
                        ProgressView()
                    } else {
                        Button(mode == .files ? "Send" : "Download") { Task { await send() } }
                            .disabled(!canSend)
                            .accessibilityIdentifier("add.send")
                    }
                }
            }
            .fileImporter(isPresented: $showImporter, allowedContentTypes: [.item], allowsMultipleSelection: true) { result in
                switch result {
                case .success(let urls): addPicked(urls)
                case .failure: error = .file
                }
            }
        }
        .interactiveDismissDisabled(isSending)
        .task {
            if deviceId == nil { deviceId = context.deviceId }
            loadRequest()
            canPaste = UIPasteboard.general.hasURLs
        }
        .task(id: deviceId) { await loadSystems() }
        .onReceive(NotificationCenter.default.publisher(for: UIPasteboard.changedNotification)) { _ in
            canPaste = UIPasteboard.general.hasURLs
        }
    }

    // MARK: Files

    @ViewBuilder
    private var filesSection: some View {
        ForEach($files) { $file in
            Section {
                LabeledContent("Size", value: file.sizeBytes.byteString)
                Picker("Type", selection: $file.kind) {
                    ForEach(AddKind.allCases, id: \.self) { Text($0.title).tag($0) }
                }
                .accessibilityIdentifier("add.kind")
                if file.kind == .rom || file.kind == .save {
                    systemPicker(selection: $file.systemId)
                }
                if file.kind == .save {
                    if let deviceId, let systemId = file.systemId {
                        NavigationLink {
                            GamePickerView(deviceId: deviceId, systemId: systemId, selection: $file.game)
                        } label: {
                            LabeledContent("Game", value: file.game?.name ?? String(localized: "Choose"))
                        }
                    }
                }
                Text(file.kind.help)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                Button("Remove", role: .destructive) { files.removeAll { $0.id == file.id } }
            } header: {
                Text(file.name)
                    .textCase(nil)
                    .accessibilityIdentifier("add.file")
            }
        }
        Section {
            Button {
                showImporter = true
            } label: {
                Label(files.isEmpty ? "Choose Files…" : "Add More Files…", systemImage: "folder")
            }
            .accessibilityIdentifier("add.choose")
        } footer: {
            if files.isEmpty {
                Text("Choose games, saves (.sav), patches (.ips, .bps, .ups) or cheats (.cht) in Files.")
            }
        }
    }

    private func systemPicker(selection: Binding<String?>) -> some View {
        Picker("System", selection: selection) {
            Text("Choose").tag(String?.none)
            ForEach(systems ?? []) { system in
                Text(system.label).tag(Optional(system.id))
            }
        }
        .accessibilityIdentifier("add.system")
    }

    // MARK: Link

    @ViewBuilder
    private var linkSection: some View {
        Section {
            TextField(text: $linkText, prompt: Text(verbatim: "https://")) {
                Text("Link")
            }
            .keyboardType(.URL)
            .textContentType(.URL)
            .textInputAutocapitalization(.never)
            .autocorrectionDisabled()
            .accessibilityIdentifier("add.link")
            if canPaste {
                PasteButton(payloadType: URL.self) { urls in
                    if let url = urls.first { linkText = url.absoluteString }
                }
                .labelStyle(.titleAndIcon)
            }
        } footer: {
            if !linkText.isEmpty && DownloadURL.validated(linkText) == nil {
                Text("Enter a link starting with http:// or https://.")
                    .foregroundStyle(.red)
            } else {
                Text("The console downloads the file itself, on its own Wi-Fi connection.")
            }
        }
        Section {
            Picker("Type", selection: $linkKind) {
                ForEach([AddKind.rom, .patch, .cheats], id: \.self) { Text($0.title).tag($0) }
            }
            if linkKind == .rom {
                systemPicker(selection: $linkSystemId)
            }
        } footer: {
            Text(linkKind.help)
        }
    }

    // MARK: Logic

    private var canSend: Bool {
        guard deviceId != nil, !isSending else { return false }
        switch mode {
        case .files:
            return !files.isEmpty && files.allSatisfy { file in
                switch file.kind {
                case .rom: file.systemId != nil
                case .save: file.game != nil
                case .patch, .cheats: true
                }
            }
        case .link:
            return DownloadURL.validated(linkText) != nil && (linkKind != .rom || linkSystemId != nil)
        }
    }

    private func loadRequest() {
        guard !didLoadRequest, let request = context.request else { return }
        didLoadRequest = true
        for name in request.files {
            let url = app.shareInbox.fileURL(name, in: request)
            guard let size = try? FileDigest.size(of: url) else { continue }
            files.append(AddFile(url: url, name: name, sizeBytes: size, isSecurityScoped: false,
                                 kind: AddKind.suggested(forFileName: name)))
        }
        if files.isEmpty, let link = request.urls.first {
            mode = .link
            linkText = link
            if let url = URL(string: link) { linkKind = AddKind.suggested(forFileName: url.lastPathComponent) == .patch ? .patch : linkKind }
        }
        suggestSystems()
    }

    private func addPicked(_ urls: [URL]) {
        error = nil
        for url in urls {
            let scoped = url.startAccessingSecurityScopedResource()
            defer { if scoped { url.stopAccessingSecurityScopedResource() } }
            guard let size = try? FileDigest.size(of: url) else {
                error = .file
                continue
            }
            let name = url.lastPathComponent
            files.append(AddFile(url: url, name: name, sizeBytes: size, isSecurityScoped: scoped,
                                 kind: AddKind.suggested(forFileName: name)))
        }
        suggestSystems()
    }

    private func loadSystems() async {
        guard let deviceId else { return }
        do {
            systems = try await app.session(for: deviceId).perform { try await $0.systems() }
            suggestSystems()
        } catch {
            let error = AppError(error)
            if !error.isCancellation { self.error = error }
        }
    }

    /// Fills the system of the files (and of the link) still without one.
    private func suggestSystems() {
        guard let systems else { return }
        for index in files.indices where files[index].systemId == nil {
            files[index].systemId = SystemSuggestion.system(forFileName: files[index].name, in: systems)?.id
        }
        if linkSystemId == nil, let url = DownloadURL.validated(linkText) {
            linkSystemId = SystemSuggestion.system(forFileName: url.lastPathComponent, in: systems)?.id
        }
    }

    private func send() async {
        guard let deviceId else { return }
        isSending = true
        defer { isSending = false }
        error = nil
        let session = app.session(for: deviceId)
        do {
            switch mode {
            case .files:
                for file in files {
                    let copy = try await Self.makeLocalCopy(of: file, in: app.transferDirectory)
                    try session.transfers.enqueue(
                        fileURL: copy, fileName: file.name, target: target(for: file),
                        destination: destination(for: file), ownsFile: true
                    )
                }
            case .link:
                guard let url = DownloadURL.validated(linkText) else { return }
                let target: TransferTarget = linkKind == .rom && linkSystemId != nil
                    ? .rom(systemId: linkSystemId ?? "", fileName: nil)
                    : .staging(fileName: nil)
                let request = DownloadRequest(url: url.absoluteString, target: target)
                let accepted = try await session.perform { try await $0.createDownload(request) }
                session.transfers.track(jobId: accepted.jobId, title: DownloadURL.displayName(url))
            }
            if let request = context.request { app.finishShare(request) }
            dismiss()
            if app.path.last != .transfers(deviceId) {
                app.path = [.console(deviceId), .transfers(deviceId)]
            }
        } catch {
            let error = AppError(error)
            if !error.isCancellation { self.error = error }
        }
    }

    private func close() {
        if let request = context.request { app.finishShare(request) }
        dismiss()
    }

    private func target(for file: AddFile) -> TransferTarget {
        switch file.kind {
        case .rom: .rom(systemId: file.systemId ?? "", fileName: file.name)
        case .save: .save(gameId: file.game?.id ?? "")
        case .patch, .cheats: .staging(fileName: file.name)
        }
    }

    private func destination(for file: AddFile) -> String {
        switch file.kind {
        case .rom: systems?.first { $0.id == file.systemId }?.label ?? ""
        case .save: file.game?.name ?? ""
        case .patch, .cheats: String(localized: "Staging area of the console")
        }
    }

    /// Copies a file into the app's transfer directory, so an upload can be
    /// resumed later without the document picker's access (a directory per
    /// copy, deleted when the upload ends). The copy is made by the file
    /// system, never through memory.
    nonisolated static func makeLocalCopy(of file: AddFile, in root: URL) async throws -> URL {
        let source = file.url
        let scoped = file.isSecurityScoped
        let name = file.name
        return try await Task.detached(priority: .userInitiated) {
            let directory = root.appending(path: UUID().uuidString, directoryHint: .isDirectory)
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
            let destination = directory.appending(path: name)
            let accessing = scoped && source.startAccessingSecurityScopedResource()
            defer { if accessing { source.stopAccessingSecurityScopedResource() } }
            try FileManager.default.copyItem(at: source, to: destination)
            return destination
        }.value
    }
}

extension AddKind {
    var title: LocalizedStringKey {
        switch self {
        case .rom: "Game"
        case .patch: "Patch"
        case .cheats: "Cheats"
        case .save: "Save"
        }
    }

    var help: LocalizedStringKey {
        switch self {
        case .rom: "Added to the library of the chosen system."
        case .patch: "Kept in the staging area of the console for 24 hours, to apply it to a game."
        case .cheats: "Kept in the staging area of the console for 24 hours, to import it into a game."
        case .save: "Replaces the save of the game. The current save is kept as a backup copy."
        }
    }
}

/// Games of a system (and of its folders), to choose the destination of a
/// save.
struct GamePickerView: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    let deviceId: String
    let systemId: String
    @Binding var selection: PickedGame?

    @State private var groups: [(folder: String, games: [GameSummary])]?
    @State private var error: AppError?

    var body: some View {
        List {
            if let error {
                Section { ErrorView(error: error, retry: load) }
            }
            ForEach(groups ?? [], id: \.folder) { group in
                Section {
                    ForEach(group.games) { game in
                        Button {
                            selection = PickedGame(id: game.id, name: game.name)
                            dismiss()
                        } label: {
                            HStack {
                                Text(game.name).foregroundStyle(.primary)
                                Spacer()
                                if selection?.id == game.id {
                                    Image(systemName: "checkmark")
                                        .accessibilityLabel(Text("Selected"))
                                }
                            }
                        }
                    }
                } header: {
                    if !group.folder.isEmpty { Text(group.folder).textCase(nil) }
                }
            }
        }
        .overlay {
            if groups == nil && error == nil {
                ProgressView()
            } else if groups?.isEmpty == true {
                ContentUnavailableView("No games in this folder", systemImage: "gamecontroller")
            }
        }
        .navigationTitle("Game")
        .task { if groups == nil { await load() } }
    }

    /// The system root, then its folders (three levels at most).
    private func load() async {
        let systemId = systemId
        do {
            let found = try await app.session(for: deviceId).perform { client in
                var result: [(folder: String, games: [GameSummary])] = []
                var pending: [(id: String?, name: String, depth: Int)] = [(nil, "", 0)]
                while !pending.isEmpty {
                    let next = pending.removeFirst()
                    let listing = try await client.games(systemId: systemId, folderId: next.id)
                    if !listing.games.isEmpty {
                        result.append((next.name, listing.games.sorted { $0.name.localizedStandardCompare($1.name) == .orderedAscending }))
                    }
                    if next.depth < 3 {
                        for folder in listing.folders {
                            let name = next.name.isEmpty ? folder.name : next.name + "/" + folder.name
                            pending.append((folder.id, name, next.depth + 1))
                        }
                    }
                }
                return result
            }
            groups = found
            error = nil
        } catch {
            let error = AppError(error)
            if !error.isCancellation { self.error = error }
        }
    }
}
