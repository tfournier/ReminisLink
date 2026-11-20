import ReminisLinkKit
import SwiftUI

/// What the Patch screen opens with.
struct PatchContext: Identifiable, Equatable {
    let id = UUID()
    /// The console; nil lets the user choose (a shared patch).
    var deviceId: String?
    /// The game; nil lets the user choose it (a shared patch).
    var game: PatchGame?
    /// A share request holding one patch file.
    var request: ShareRequest?
}

/// Patch screen of a game (docs/specs/06-app-ios.md §4.5): choose the
/// patch (Files, link, SD card), choose the output (a new file with a
/// suggested name, or replace the game), follow the job, then open the
/// new game. Opened from the game sheet, or by a shared `.ips`, `.bps` or
/// `.ups` (the console, then the game, are chosen first). The app
/// proposes no source of patches.
struct PatchView: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    @Environment(\.scenePhase) private var scenePhase
    let context: PatchContext

    @State private var deviceId: String?
    @State private var flow: PatchFlow?
    /// The shared file, once moved out of the inbox.
    @State private var sharedFile: PickedFile?
    @State private var didLoad = false
    @State private var loadError: AppError?

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
                        // Once something was sent to a console, it stays.
                        .disabled(flow?.isBoundToConsole ?? false)
                        .accessibilityIdentifier("patch.console")
                    }
                }
                if let loadError {
                    Section { ErrorView(error: loadError) }
                }
                if let flow {
                    PatchFormSections(flow: flow, choosesGame: context.game == nil, close: close)
                }
            }
            .navigationTitle("Apply a Patch")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { toolbar }
        }
        .interactiveDismissDisabled(flow.map { $0.step.holdsTheScreen && !$0.isCancelling } ?? false)
        .task {
            guard !didLoad else { return }
            didLoad = true
            if deviceId == nil {
                deviceId = context.deviceId ?? (app.store.devices.count == 1 ? app.store.devices.first?.id : nil)
            }
            await takeSharedFile()
            makeFlow()
            // The flow may exist already (made when the console was set).
            if let sharedFile, let flow, flow.file == nil { flow.file = sharedFile }
        }
        .onChange(of: deviceId) { makeFlow() }
        // Every second while the screen is visible and the app active.
        .task(id: scenePhase) {
            guard scenePhase == .active else { return }
            while !Task.isCancelled {
                await flow?.tick()
                try? await Task.sleep(for: .seconds(1))
            }
        }
        .onChange(of: scenePhase) { _, phase in
            if phase == .background { flow?.suspend() }
            if phase == .active { flow?.resumeIfPaused() }
        }
        .onDisappear {
            flow?.discardFile()
            if flow == nil { sharedFile?.remove() }
        }
    }

    @ToolbarContentBuilder
    private var toolbar: some ToolbarContent {
        ToolbarItem(placement: .cancellationAction) {
            // Always a way out: Cancel while the app side runs, then Close
            // (a cancellation in flight goes on without the screen).
            if let flow, flow.step.holdsTheScreen, !flow.isCancelling {
                Button("Cancel") { Task { await flow.cancel() } }
                    // Disabled while a cancel waits for the console's answer.
                    .disabled(flow.step == .cancellingUpload)
                    .accessibilityIdentifier("patch.cancel")
            } else {
                Button(flow.map { $0.step.isBusy || $0.isCancelling } ?? false ? "Close" : "Cancel") { close() }
                    .accessibilityIdentifier("patch.close")
            }
        }
        ToolbarItem(placement: .confirmationAction) {
            if let flow {
                if flow.step.isBusy {
                    ProgressView()
                } else if case .succeeded = flow.step {
                    EmptyView()
                } else {
                    PatchApplyButton(flow: flow)
                }
            }
        }
    }

    /// A shared patch is moved into the app at once: the share request is
    /// consumed when this screen closes, however it closes.
    private func takeSharedFile() async {
        guard let request = context.request, let name = request.files.first, sharedFile == nil else { return }
        let inbox = app.shareInbox
        let root = app.transferDirectory
        do {
            sharedFile = try await Task.detached(priority: .userInitiated) {
                let directory = root.appending(path: UUID().uuidString, directoryHint: .isDirectory)
                try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
                let destination = directory.appending(path: name)
                try inbox.takeFile(name, from: request, to: destination)
                return PickedFile(url: destination, name: name, sizeBytes: try FileDigest.size(of: destination))
            }.value
        } catch {
            loadError = .file
        }
    }

    /// One flow per console (a game belongs to one console).
    private func makeFlow() {
        guard let deviceId, flow?.deviceId != deviceId else { return }
        let previous = flow
        let next = PatchFlow(session: app.session(for: deviceId), game: context.game)
        if let file = previous?.file ?? sharedFile {
            next.file = file
        }
        flow = next
    }

    private func close() {
        flow?.discardFile()
        if flow == nil { sharedFile?.remove() }
        dismiss()
    }
}

/// "Apply", with the confirmation of a replacement.
private struct PatchApplyButton: View {
    let flow: PatchFlow
    @State private var confirmReplace = false

    var body: some View {
        Button("Apply") {
            if flow.mode == .replace { confirmReplace = true } else { flow.start() }
        }
        .disabled(!flow.canStart)
        .accessibilityIdentifier("patch.apply")
        .confirmationDialog("Replace the game?", isPresented: $confirmReplace, titleVisibility: .visible) {
            Button("Replace the Game", role: .destructive) { flow.start() }
                .accessibilityIdentifier("patch.replaceConfirm")
        } message: {
            Text("The original ROM goes to the trash of the console. The save, the states and the cheats are kept.")
        }
    }
}

/// The sections of the Patch screen for one console.
private struct PatchFormSections: View {
    @Environment(AppModel.self) private var app
    @Bindable var flow: PatchFlow
    /// The game is chosen here (a shared patch).
    let choosesGame: Bool
    let close: () -> Void

    @State private var showImporter = false
    @State private var showGamePicker = false
    @State private var sdFiles: [SdFile]?
    @State private var sdError: AppError?
    @State private var fileError: AppError?
    @State private var openError: AppError?
    @State private var isOpening = false

    private var locked: Bool { flow.step.isBusy || flow.isCancelling || isSucceeded }

    private var isSucceeded: Bool {
        if case .succeeded = flow.step { return true }
        return false
    }

    var body: some View {
        gameSection
        if let game = flow.game {
            if let refusal = game.refusal {
                Section {
                    Label {
                        Text(refusal.text)
                            .fixedSize(horizontal: false, vertical: true)
                    } icon: {
                        Image(systemName: "info.circle").accessibilityHidden(true)
                    }
                    .accessibilityElement(children: .combine)
                    .accessibilityIdentifier("patch.refused")
                }
            } else {
                statusSection
                sourceSection
                outputSection
            }
        }
    }

    // MARK: Game

    @ViewBuilder
    private var gameSection: some View {
        Section {
            if choosesGame {
                Button {
                    showGamePicker = true
                } label: {
                    LabeledContent("Game") {
                        Text(flow.game?.name ?? String(localized: "Choose"))
                    }
                }
                .disabled(locked)
                .accessibilityIdentifier("patch.game")
                .sheet(isPresented: $showGamePicker) {
                    GamePickerView(deviceId: flow.deviceId, patchOnly: true) { picked in
                        flow.game = PatchGame(id: picked.id, name: picked.name, fileName: picked.fileName,
                                              kind: picked.kind, systemLabel: picked.systemLabel)
                        showGamePicker = false
                    }
                }
            } else if let game = flow.game {
                LabeledContent("Game", value: game.name)
                    .accessibilityIdentifier("patch.game")
            }
        } footer: {
            if choosesGame, flow.game == nil {
                Text("Choose the game to apply the patch to.")
            }
        }
        .task {
            // A shared patch: ask for the game at once.
            if choosesGame && flow.game == nil { showGamePicker = true }
        }
    }

    // MARK: Source

    @ViewBuilder
    private var sourceSection: some View {
        Section {
            Picker("Patch", selection: $flow.source) {
                Text("Files").tag(PatchFlow.Source.files)
                Text("Link").tag(PatchFlow.Source.link)
                Text("SD Card").tag(PatchFlow.Source.sdCard)
            }
            .pickerStyle(.segmented)
            .disabled(locked)
            .accessibilityIdentifier("patch.source")
            switch flow.source {
            case .files: filesRows
            case .link: linkRows
            case .sdCard: sdRows
            }
        } header: {
            Text("Patch")
        } footer: {
            switch flow.source {
            case .files:
                Text("An IPS, BPS or UPS patch. It is sent to the staging area of the console, then applied.")
            case .link:
                if !flow.linkText.isEmpty && flow.validatedLink == nil {
                    Text("Enter a link starting with http:// or https://.")
                        .foregroundStyle(.red)
                } else {
                    Text("The console downloads the patch itself, on its own Wi-Fi connection.")
                }
            case .sdCard:
                Text("The .ips, .bps and .ups files of the Roms folder and of the ReminisLink/patches folder of the SD card.")
            }
        }
        .fileImporter(isPresented: $showImporter, allowedContentTypes: [.item]) { result in
            Task { await picked(result) }
        }
        .task(id: flow.source) {
            if flow.source == .sdCard && sdFiles == nil { await loadSdFiles() }
        }
    }

    @ViewBuilder
    private var filesRows: some View {
        if let file = flow.file {
            LabeledContent(file.name, value: file.sizeBytes.byteString)
                .accessibilityIdentifier("patch.file")
        }
        if let fileError {
            Label(fileError.message, systemImage: "exclamationmark.triangle.fill")
        }
        Button {
            showImporter = true
        } label: {
            Label(flow.file == nil ? "Choose a File…" : "Choose Another File…", systemImage: "folder")
        }
        .disabled(locked)
        .accessibilityIdentifier("patch.choose")
    }

    private var linkRows: some View {
        TextField(text: $flow.linkText, prompt: Text(verbatim: "https://")) {
            Text("Link")
        }
        .keyboardType(.URL)
        .textContentType(.URL)
        .textInputAutocapitalization(.never)
        .autocorrectionDisabled()
        .disabled(locked)
        .accessibilityIdentifier("patch.link")
    }

    @ViewBuilder
    private var sdRows: some View {
        if let sdError {
            ErrorView(error: sdError, retry: loadSdFiles)
        } else if let sdFiles {
            if sdFiles.isEmpty {
                Text("No patch on the SD card.")
                    .foregroundStyle(.secondary)
            }
            ForEach(sdFiles) { file in
                Button {
                    flow.sdFileId = file.id
                } label: {
                    HStack {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(file.fileName).foregroundStyle(.primary)
                            Text(file.path)
                                .font(.caption.monospaced())
                                .foregroundStyle(.secondary)
                        }
                        Spacer()
                        if flow.sdFileId == file.id {
                            Image(systemName: "checkmark").foregroundStyle(.tint)
                        }
                    }
                    .accessibilityElement(children: .combine)
                    .accessibilityAddTraits(flow.sdFileId == file.id ? .isSelected : [])
                }
                .disabled(locked)
                .accessibilityIdentifier("patch.sdFile")
            }
        } else {
            ProgressView()
        }
    }

    // MARK: Output

    @ViewBuilder
    private var outputSection: some View {
        Section {
            Picker("Output", selection: $flow.mode) {
                Text("New File").tag(PatchOutputMode.newFile)
                Text("Replace the Game").tag(PatchOutputMode.replace)
            }
            .pickerStyle(.inline)
            .labelsHidden()
            .disabled(locked)
            .accessibilityIdentifier("patch.mode")
            if flow.mode == .newFile {
                HStack {
                    TextField("File Name", text: $flow.fileName)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .disabled(locked)
                        .accessibilityIdentifier("patch.fileName")
                    if !flow.fileName.isEmpty && !locked {
                        Button {
                            flow.fileName = ""
                        } label: {
                            Image(systemName: "xmark.circle.fill")
                                .foregroundStyle(.secondary)
                        }
                        .buttonStyle(.borderless)
                        .accessibilityLabel(Text("Clear"))
                        .accessibilityIdentifier("patch.clearName")
                    }
                }
            } else {
                Label {
                    Text("The original ROM goes to the trash of the console. The save, the states and the cheats are kept.")
                        .fixedSize(horizontal: false, vertical: true)
                } icon: {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .foregroundStyle(.orange)
                        .accessibilityHidden(true)
                }
                .accessibilityElement(children: .combine)
                .accessibilityIdentifier("patch.replaceWarning")
            }
        } header: {
            Text("Output")
        } footer: {
            if flow.mode == .newFile {
                switch flow.outputName {
                case .invalid:
                    Text("Enter a file name without / or \\, not starting with a dot.")
                        .foregroundStyle(.red)
                        .accessibilityIdentifier("patch.nameError")
                case .valid(let name):
                    Text("Will be created: \(name). If the name is taken, the console adds a number. The extension of the game is added when the name does not end with it.")
                        .accessibilityIdentifier("patch.finalName")
                }
            }
        }
    }

    // MARK: Status

    @ViewBuilder
    private var statusSection: some View {
        switch flow.step {
        case .idle:
            EmptyView()
        case .preparing:
            Section { progressRow(Text("Preparing the patch…"), fraction: nil) }
        case .sending(let fraction):
            Section {
                progressRow(Text("Sending the patch… \(fraction.formatted(.percent.precision(.fractionLength(0))))"), fraction: fraction)
                if flow.isWaitingForReconnection {
                    // M10: resumes by itself once reconnected.
                    Label("Paused: the console disconnected this app. The transfer resumes when you reconnect.", systemImage: "pause.circle")
                        .font(.footnote)
                        .accessibilityIdentifier("patch.waitingForReconnection")
                }
            }
        case .downloading:
            Section { progressRow(Text("The console is downloading the patch…"), fraction: flow.job?.job?.progress.fraction) }
        case .starting:
            Section { progressRow(Text("Starting…"), fraction: nil) }
        case .cancellingUpload:
            Section {
                progressRow(Text("Cancelling… Waiting for the console to confirm."), fraction: nil)
                    .accessibilityIdentifier("patch.cancellingUpload")
            }
        case .patching:
            Section {
                progressRow(patchingText, fraction: flow.job?.job?.progress.fraction)
                if !cancelInProgress {
                    Button("Cancel the Patch", role: .destructive) { Task { await flow.cancel() } }
                        .accessibilityIdentifier("patch.cancelJob")
                }
            } footer: {
                Text("You can close this screen: the console goes on. Follow it in Transfers.")
            }
        case .downloaded:
            Section {
                Text("The console downloaded the patch before the cancellation arrived. Nothing was applied: tap Apply to use it.")
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier("patch.downloaded")
            }
        case .succeeded(let gameId, let late):
            Section {
                NoticeView(text: successText(late: late))
                if flow.verified == false {
                    Label("IPS: the source ROM could not be verified.", systemImage: "info.circle")
                        .font(.footnote)
                        .accessibilityIdentifier("patch.unverified")
                }
                if let openError {
                    ErrorView(error: openError)
                }
                if let gameId, gameId != flow.game?.id {
                    Button {
                        Task { await open(gameId) }
                    } label: {
                        if isOpening { ProgressView() } else { Text("Open the New Game") }
                    }
                    .disabled(isOpening)
                    .accessibilityIdentifier("patch.openGame")
                } else {
                    Button("Done") { close() }
                        .accessibilityIdentifier("patch.done")
                }
            }
        case .failed(let error):
            Section {
                if error == .upload(.paused) {
                    Label(error.message, systemImage: "pause.circle")
                    Button("Resume") { flow.start() }
                } else if error == .upload(.cancelNotConfirmed) {
                    Label(error.message, systemImage: "exclamationmark.triangle.fill")
                        .accessibilityIdentifier("patch.cancelNotConfirmed")
                    Button("Resume") { flow.start() }
                    Button("Cancel Again", role: .destructive) { Task { await flow.cancel() } }
                } else {
                    Label {
                        Text(error.message).fixedSize(horizontal: false, vertical: true)
                    } icon: {
                        Image(systemName: "exclamationmark.triangle.fill")
                            .foregroundStyle(.orange)
                            .accessibilityHidden(true)
                    }
                    .accessibilityElement(children: .combine)
                    .accessibilityIdentifier("patch.error")
                    if let details = flow.mismatchDetails {
                        Self.mismatchRows(details)
                    }
                }
            }
        case .cancelled:
            Section {
                Text("Cancelled. Nothing was changed on the console.")
                    .accessibilityIdentifier("patch.cancelled")
            }
        }
        if cancelInProgress {
            Section {
                progressRow(Text("Cancelling… Waiting for the console to confirm."), fraction: nil)
            }
        }
        if let error = flow.cancelError {
            Section {
                Label(String(localized: "The cancellation could not be sent: \(error.message)"), systemImage: "exclamationmark.triangle.fill")
                    .accessibilityIdentifier("patch.cancelError")
                Button("Try Again") { Task { await flow.cancel() } }
            }
        }
        if let error = flow.pollError, flow.step.isBusy {
            Section {
                Label(error.message, systemImage: "exclamationmark.triangle.fill")
                    .accessibilityIdentifier("patch.pollError")
                Button("Try Again") { Task { await flow.tick() } }
                    .accessibilityIdentifier("patch.pollRetry")
            } footer: {
                Text("The console goes on with the task; its state shows again once it answers.")
            }
        }
    }

    /// What the patch expects and what the console has.
    @ViewBuilder
    static func mismatchRows(_ details: PatchMismatchDetails) -> some View {
        if details.expectedSize != nil || details.expectedCrc32 != nil {
            LabeledContent("Expected ROM", value: describe(size: details.expectedSize, crc: details.expectedCrc32))
                .font(.footnote)
        }
        if details.actualSize != nil || details.actualCrc32 != nil {
            LabeledContent("ROM on the console", value: describe(size: details.actualSize, crc: details.actualCrc32))
                .font(.footnote)
        }
    }

    static func describe(size: Int64?, crc: String?) -> String {
        [size.map { $0.byteString }, crc.map { "CRC32 \($0)" }].compactMap { $0 }.joined(separator: " · ")
    }

    /// Asked here, or seen on the job (asked from the Transfers screen).
    private var cancelInProgress: Bool {
        flow.isCancelling || (flow.step.isBusy && flow.job?.job?.cancelRequested == true)
    }

    private func successText(late: Bool) -> String {
        if late {
            return String(localized: "Patch applied: the cancellation arrived too late.")
        }
        return flow.mode == .replace
            ? String(localized: "Patch applied. The original ROM is in the trash of the console.")
            : String(localized: "Patch applied. The new game is in the library.")
    }

    private var patchingText: Text {
        if let fraction = flow.job?.job?.progress.fraction {
            return Text("Applying the patch… \(fraction.formatted(.percent.precision(.fractionLength(0))))")
        }
        return Text("Applying the patch…")
    }

    private func progressRow(_ text: Text, fraction: Double?) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            text
            if let fraction {
                ProgressView(value: fraction).accessibilityHidden(true)
            } else {
                ProgressView().frame(maxWidth: .infinity, alignment: .leading).accessibilityHidden(true)
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("patch.progress")
    }

    // MARK: Logic

    private func loadSdFiles() async {
        sdError = nil
        guard let game = flow.game else { return }
        do {
            let files = try await flow.session.perform { try await $0.sdFiles(type: .patch) }
            sdFiles = SdFile.sortedForGame(files, fileName: game.fileName, name: game.name)
        } catch {
            let error = AppError(error)
            if !error.isCancellation { sdError = error }
        }
    }

    private func picked(_ result: Result<URL, Error>) async {
        fileError = nil
        switch result {
        case .success(let url):
            do {
                flow.file = try await PickedFile.copy(url, into: app.transferDirectory)
            } catch {
                fileError = .file
            }
        case .failure:
            fileError = .file
        }
    }

    /// Opens the sheet of the produced game, above the current one.
    private func open(_ gameId: String) async {
        guard let game = flow.game else { return }
        isOpening = true
        defer { isOpening = false }
        openError = nil
        let deviceId = flow.deviceId
        do {
            let detail = try await flow.session.perform { try await $0.game(id: gameId) }
            let route = GameRoute(deviceId: deviceId, gameId: gameId, systemLabel: game.systemLabel,
                                  name: detail.name, hasArt: false, fileName: detail.fileName)
            close()
            if case .game(let current)? = app.path.last, current.deviceId == deviceId {
                app.path.append(.game(route))
            } else {
                app.path = [.console(deviceId), .library(deviceId), .game(route)]
            }
        } catch {
            let error = AppError(error)
            if !error.isCancellation { openError = error }
        }
    }
}

extension PatchRefusal {
    var text: String {
        switch self {
        case .multiDisc:
            String(localized: "This game has several discs: a patch applies to a single ROM file, so multi-disc games cannot be patched here.")
        case .archive:
            String(localized: "Extract the ROM first: the console does not patch an archive (.zip, .7z).")
        }
    }
}
