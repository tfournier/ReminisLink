import ReminisLinkKit
import SwiftUI
import UniformTypeIdentifiers

/// Import of a game archive (docs/specs/06-app-ios.md §4.5, milestone M19):
/// choose the archive in Files (or share it to the app), see what the
/// console will add, replace or skip, choose another target if needed,
/// then import. Everything replaced goes to the console's trash first, and
/// the save to its backup copies.
struct ArchiveImportView: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    let context: ArchiveImportContext

    @State private var deviceId: String?
    @State private var flow: ArchiveImportFlow?
    @State private var showImporter = false
    @State private var showGamePicker = false
    @State private var systems: [GameSystem]?
    @State private var fileName: String?
    @State private var loadError: AppError?
    @State private var confirmReplace = false
    @State private var tookShare = false
    /// A shared archive waiting for the console to be chosen.
    @State private var pendingFile: PickedFile?

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
                        .disabled(flow?.step.isBusy == true || flow?.plan != nil)
                    }
                }
                if let loadError {
                    Section { ErrorView(error: loadError) }
                }
                fileSection
                if let flow {
                    ArchivePlanSections(
                        flow: flow, systems: systems ?? [],
                        onChooseGame: { showGamePicker = true }
                    )
                }
            }
            .navigationTitle("Import Game Data")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    if case .succeeded = flow?.step {
                        Button("Done") { close() }.accessibilityIdentifier("archive.done")
                    } else {
                        Button("Cancel") { close() }
                    }
                }
                ToolbarItem(placement: .confirmationAction) {
                    if flow?.step == .starting || flow?.step == .importing {
                        ProgressView()
                    } else if flow?.step == .ready {
                        Button("Import") {
                            if flow?.plan?.replaced.isEmpty == false { confirmReplace = true } else { flow?.confirm() }
                        }
                        .disabled(flow?.plan?.importsAnything != true)
                        .accessibilityIdentifier("archive.import")
                    }
                }
            }
            .fileImporter(isPresented: $showImporter, allowedContentTypes: [.zip]) { result in
                switch result {
                case .success(let url): Task { await pick(url) }
                case .failure: loadError = .file
                }
            }
            .sheet(isPresented: $showGamePicker) {
                if let deviceId {
                    GamePickerView(deviceId: deviceId) { game in
                        showGamePicker = false
                        flow?.retarget(.game(gameId: game.id))
                    }
                }
            }
            .alert("Replace the data of this game?", isPresented: $confirmReplace) {
                Button("Import") { flow?.confirm() }
                    .accessibilityIdentifier("archive.import.confirm")
                Button("Cancel", role: .cancel) {}
            } message: {
                Text("\(flow?.plan?.replaced.count ?? 0) files of \(flow?.plan?.targetName ?? "") will be replaced. The console first moves them to its trash, and keeps a copy of the save in its backup copies.")
            }
        }
        .interactiveDismissDisabled(flow?.step.isBusy == true)
        .task {
            if deviceId == nil { deviceId = context.deviceId ?? (app.store.devices.count == 1 ? app.store.devices.first?.id : nil) }
            await takeSharedFile()
        }
        .task(id: deviceId) { await loadSystems() }
        .onChange(of: deviceId) {
            if let file = pendingFile, deviceId != nil {
                pendingFile = nil
                start(file)
            }
        }
        // The job is followed by events; polled every second meanwhile.
        .task(id: flow?.step) {
            while flow?.step == .importing, !Task.isCancelled {
                await flow?.tick()
                try? await Task.sleep(for: .seconds(1))
            }
        }
    }

    @ViewBuilder
    private var fileSection: some View {
        Section {
            if let fileName {
                LabeledContent("Archive", value: fileName)
            }
            if case .sending(let fraction) = flow?.step {
                ProgressView(value: fraction) { Text("Sending the archive to the console…") }
                    .accessibilityIdentifier("archive.sending")
            } else if flow?.step == .inspecting {
                HStack {
                    ProgressView()
                    Text("The console checks the archive…")
                }
            }
            if flow == nil || flow?.step.isBusy == false && !isDone {
                Button {
                    showImporter = true
                } label: {
                    Label(fileName == nil ? "Choose an Archive…" : "Choose Another Archive…", systemImage: "doc.zipper")
                }
                .disabled(deviceId == nil)
                .accessibilityIdentifier("archive.choose")
            }
        } footer: {
            if fileName == nil {
                Text("A .reminislink.zip archive exported from a game of this console or another one. The console checks it before anything is written.")
            }
        }
    }

    private var isDone: Bool {
        if case .succeeded = flow?.step { return true }
        return false
    }

    private func makeFlow() -> ArchiveImportFlow? {
        guard let deviceId else { return nil }
        flow?.abandon() // another archive: the previous one is dropped
        let target: ArchiveTarget? = context.gameId.map { .game(gameId: $0) }
        let next = ArchiveImportFlow(session: app.session(for: deviceId), target: target)
        flow = next
        return next
    }

    private func pick(_ url: URL) async {
        loadError = nil
        do {
            let file = try await PickedFile.copy(url, into: app.transferDirectory)
            start(file)
        } catch {
            loadError = .file
        }
    }

    private func start(_ file: PickedFile) {
        guard let flow = makeFlow() else { return file.remove() }
        fileName = file.name
        flow.start(with: file)
    }

    /// A shared archive is moved into the app at once.
    private func takeSharedFile() async {
        guard !tookShare, let request = context.request, let name = request.files.first else { return }
        tookShare = true
        let inbox = app.shareInbox
        let root = app.transferDirectory
        do {
            let file = try await app.work.run {
                try await Task.detached(priority: .userInitiated) {
                    let directory = root.appending(path: UUID().uuidString, directoryHint: .isDirectory)
                    try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
                    let destination = directory.appending(path: name)
                    try inbox.takeFile(name, from: request, to: destination)
                    return PickedFile(url: destination, name: name, sizeBytes: try FileDigest.size(of: destination))
                }.value
            }
            if deviceId != nil { start(file) } else { pendingFile = file }
        } catch {
            loadError = .file
        }
    }

    private func loadSystems() async {
        guard let deviceId else { return }
        systems = try? await app.session(for: deviceId).perform { try await $0.systems() }
    }

    private func close() {
        flow?.abandon()
        pendingFile?.remove()
        dismiss()
    }
}

/// What the import of the archive will do, and its result.
struct ArchivePlanSections: View {
    let flow: ArchiveImportFlow
    let systems: [GameSystem]
    let onChooseGame: () -> Void

    var body: some View {
        if case .failed(let error) = flow.step {
            Section {
                ErrorView(error: error)
                if let gameId = flow.existingGameId {
                    Button("Import into That Game") { flow.retarget(.game(gameId: gameId)) }
                        .accessibilityIdentifier("archive.useExisting")
                }
            }
        }
        if case .succeeded(let result) = flow.step {
            Section {
                NoticeView(text: String(localized: "Imported: \(result.imported) files."))
                if result.saveBackupId != nil {
                    Text("The previous save is in the backup copies of the game on the console.")
                        .font(.footnote).foregroundStyle(.secondary)
                }
                if result.trashEntryId != nil {
                    Text("The replaced files are in the console's trash.")
                        .font(.footnote).foregroundStyle(.secondary)
                }
                ForEach(Array(result.skipped.enumerated()), id: \.offset) { _, skipped in
                    LabeledContent {
                        ArchiveSkipText(reason: skipped.reason)
                    } label: {
                        ArchiveRoleTitle(role: skipped.role, slot: skipped.slot)
                    }
                }
            }
            .accessibilityIdentifier("archive.result")
        }
        if let plan = flow.plan, !isDone {
            Section {
                LabeledContent("Game", value: plan.manifest.source.gameName)
                LabeledContent("System", value: plan.manifest.source.systemLabel)
                if let device = plan.manifest.source.deviceName {
                    LabeledContent("From", value: device)
                }
                LabeledContent("Exported", value: plan.manifest.createdAt.dateTimeString)
            } header: {
                Text("Archive")
            }
            Section {
                switch plan.target {
                case .game?:
                    LabeledContent("Into", value: plan.targetName ?? "")
                        .accessibilityIdentifier("archive.target")
                case .newGame?:
                    LabeledContent("Into", value: String(localized: "A new game in \(plan.targetName ?? "")"))
                        .accessibilityIdentifier("archive.target")
                case nil:
                    Text("No game of this console matches the archive. Choose the game its data go to.")
                        .foregroundStyle(.secondary)
                }
                if !flow.step.isBusy {
                    Button("Choose Another Game…", action: onChooseGame)
                        .accessibilityIdentifier("archive.chooseGame")
                    if plan.manifest.hasROM {
                        let tagged = systems.filter { $0.tag.caseInsensitiveCompare(plan.manifest.source.systemTag) == .orderedSame }
                        Menu("Import as a New Game…") {
                            ForEach(tagged.isEmpty ? systems : tagged) { system in
                                Button(system.label) { flow.retarget(.newGame(systemId: system.id, folderId: nil)) }
                            }
                        }
                        .disabled(systems.isEmpty)
                    }
                }
            } header: {
                Text("Target")
            } footer: {
                if plan.suggested && plan.target != nil {
                    Text("Suggested by the console from the name of the game.")
                }
            }
            Section {
                ForEach(Array(plan.entries.enumerated()), id: \.offset) { _, entry in
                    LabeledContent {
                        VStack(alignment: .trailing, spacing: 2) {
                            switch entry.action {
                            case .create: Text("Added")
                            case .replace: Text("Replaces the current file")
                            default: ArchiveSkipText(reason: entry.reason)
                            }
                            Text(entry.sizeBytes.byteString).font(.caption).foregroundStyle(.secondary)
                        }
                    } label: {
                        ArchiveRoleTitle(role: entry.role, slot: entry.slot)
                    }
                    .accessibilityElement(children: .combine)
                }
            } header: {
                Text("Files")
            } footer: {
                Text("Files are matched by what they are, never by their path, and named as this console names them. A replaced file goes to the console's trash first; the save is also kept in its backup copies.")
            }
        }
    }

    private var isDone: Bool {
        if case .succeeded = flow.step { return true }
        return false
    }
}

/// What a file of an archive is.
struct ArchiveRoleTitle: View {
    let role: ArchiveRole
    let slot: Int?

    var body: some View {
        switch role {
        case .stateThumbnail:
            if let slot { Text("Thumbnail of state \(slot)") } else { Text("State thumbnail") }
        default:
            GameFileRoleTitle(role: GameFileRole(rawValue: role.rawValue), slot: slot)
        }
    }
}

/// Why a file of an archive is not imported.
struct ArchiveSkipText: View {
    let reason: ArchiveSkipReason?

    var body: some View {
        switch reason {
        case .gameExists?: Text("Skipped: the game is already there")
        case .locked?: Text("Skipped: the state is locked")
        case .unsupportedFormat?: Text("Skipped: not supported by this console")
        case .noStateFolder?: Text("Skipped: no emulator for this system")
        case .noState?: Text("Skipped: its state is not imported")
        case .noTarget?: Text("Skipped: no game chosen")
        default: Text("Skipped")
        }
    }
}
