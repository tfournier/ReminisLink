import ReminisLinkKit
import SwiftUI
import UniformTypeIdentifiers

/// The apps of a console (milestone M12, docs/specs/06-app-ios.md): its
/// tool and emulator paks, grouped, with their version and size.
/// ReminisLink itself is marked and cannot be deleted. Delete moves a pak
/// to the trash of the console after a confirmation. Install offers three
/// sources: Files (a zip, or a `.pak` folder zipped here), a link
/// (downloaded by the console) and the SD card (its browser as a picker).
struct PaksView: View {
    @Environment(AppModel.self) private var app
    @Environment(\.scenePhase) private var scenePhase
    let deviceId: String

    @State private var toDelete: Pak?
    @State private var actionError: AppError?
    @State private var notice: String?
    @State private var draft: PakInstallDraft?
    @State private var showImporter = false
    @State private var showSdPicker = false
    /// The SD entry chosen in the picker: its options open once the picker
    /// is gone (one sheet at a time).
    @State private var pickedEntry: SdEntry?
    @State private var isPreparing = false

    var body: some View {
        let session = app.session(for: deviceId)
        let manager = session.paks
        List {
            if let actionError {
                Section { ErrorView(error: actionError) }
            }
            if let notice {
                Section { NoticeView(text: notice) }
            }
            if isPreparing {
                Section {
                    HStack(spacing: 12) {
                        ProgressView()
                        Text("Preparing the file…").foregroundStyle(.secondary)
                    }
                }
            }
            if !manager.installs.isEmpty {
                Section {
                    ForEach(manager.installs) { install in
                        PakInstallRow(install: install, manager: manager)
                    }
                } header: {
                    Text("Installations")
                }
            }
            if let error = manager.loadError {
                Section { ErrorView(error: error, retry: { await manager.load() }) }
            }
            ForEach(PakList.grouped(manager.paks ?? []), id: \.kind) { group in
                Section {
                    ForEach(group.paks) { pak in
                        PakRow(pak: pak)
                            .swipeActions {
                                if !pak.isSelf {
                                    Button("Delete", role: .destructive) { toDelete = pak }
                                        .accessibilityIdentifier("pak.delete")
                                }
                            }
                            .contextMenu {
                                if !pak.isSelf {
                                    Button("Delete…", systemImage: "trash", role: .destructive) { toDelete = pak }
                                }
                            }
                    }
                } header: {
                    PakKindTitle(kind: group.kind)
                } footer: {
                    if group.paks.contains(where: \.isSelf) {
                        Text("ReminisLink cannot delete or replace itself while it runs.")
                    }
                }
            }
        }
        .overlay {
            if manager.paks == nil && manager.loadError == nil {
                ProgressView()
            } else if manager.paks?.isEmpty == true {
                ContentUnavailableView("No apps", systemImage: "app.dashed")
            }
        }
        .navigationTitle("Apps")
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Menu {
                    Button("From Files…", systemImage: "folder") { showImporter = true }
                        .accessibilityIdentifier("paks.install.files")
                    Button("From a Link…", systemImage: "link") { draft = PakInstallDraft(source: .link) }
                        .accessibilityIdentifier("paks.install.link")
                    Button("From the SD Card…", systemImage: "sdcard") { showSdPicker = true }
                        .accessibilityIdentifier("paks.install.sd")
                } label: {
                    Label("Install", systemImage: "plus")
                }
                .disabled(manager.loadError == .needsNewerPak)
                .accessibilityIdentifier("paks.install")
            }
        }
        .fileImporter(isPresented: $showImporter, allowedContentTypes: [.zip, .folder]) { result in
            switch result {
            case .success(let url): prepare(url)
            case .failure: actionError = .file
            }
        }
        .sheet(item: $draft, onDismiss: nil) { draft in
            PakInstallOptionsView(deviceId: deviceId, draft: draft)
        }
        .sheet(isPresented: $showSdPicker, onDismiss: {
            if let entry = pickedEntry {
                pickedEntry = nil
                draft = PakInstallDraft(source: .sdCard(entry))
            }
        }) {
            SdPickerSheet(deviceId: deviceId) { pickedEntry = $0 }
        }
        .confirmationDialog(
            "Delete this app?",
            isPresented: Binding(get: { toDelete != nil }, set: { if !$0 { toDelete = nil } }),
            titleVisibility: .visible,
            presenting: toDelete
        ) { pak in
            Button("Delete \(pak.name)", role: .destructive) {
                Task { await delete(pak) }
            }
            .accessibilityIdentifier("pak.delete.confirm")
        } message: { pak in
            Text("\(pak.name) goes to the trash of the console: it can be recovered for at least 30 days.")
        }
        .pakReplacementConfirmation(manager)
        .task { await manager.load() }
        .refreshable { await manager.load() }
        // The jobs of the installations, every second while visible
        // (nothing is sent while the event stream follows them, M14).
        .task(id: scenePhase) {
            guard scenePhase == .active else { return }
            while !Task.isCancelled {
                await session.transfers.refreshJobs()
                try? await Task.sleep(for: .seconds(1))
            }
        }
    }

    private func delete(_ pak: Pak) async {
        notice = nil
        do {
            try await app.session(for: deviceId).paks.delete(pak)
            actionError = nil
            notice = String(localized: "\(pak.name) was moved to the trash of the console.")
        } catch {
            let error = AppError(error)
            if !error.isCancellation { actionError = error }
        }
    }

    /// A zip is copied into the app, a `.pak` folder zipped, then the
    /// options are asked.
    private func prepare(_ url: URL) {
        actionError = nil
        isPreparing = true
        let root = app.transferDirectory
        Task {
            defer { isPreparing = false }
            do {
                let file = try await PakArchiver.prepare(url, into: root)
                draft = PakInstallDraft(source: .file(file))
            } catch {
                let error = AppError(error)
                if !error.isCancellation { actionError = error == .unexpected ? .file : error }
            }
        }
    }
}

/// "Tools" or "Emulators".
struct PakKindTitle: View {
    let kind: PakKind

    var body: some View {
        switch kind {
        case .tool: Text("Tools")
        case .emu: Text("Emulators")
        default: Text(verbatim: kind.rawValue)
        }
    }
}

/// An installed pak: name, version, size; ReminisLink is marked.
struct PakRow: View {
    let pak: Pak

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: pak.kind == .emu ? "gamecontroller" : "wrench.and.screwdriver")
                .foregroundStyle(.secondary)
                .frame(width: 28)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 4) {
                HStack {
                    Text(pak.name).font(.headline)
                    if pak.isSelf {
                        TagView(text: Text("This app"))
                            .accessibilityIdentifier("pak.self")
                    }
                }
                HStack(spacing: 6) {
                    if let version = pak.version {
                        Text(verbatim: version)
                    }
                    Text(pak.sizeBytes.byteString)
                }
                .font(.footnote)
                .foregroundStyle(.secondary)
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("pak.\(pak.name)")
    }
}

/// An installation in progress or finished.
struct PakInstallRow: View {
    let install: PakInstall
    let manager: PakManager

    var body: some View {
        let phase = install.displayPhase
        VStack(alignment: .leading, spacing: 6) {
            Text(install.name).font(.headline)
            switch phase {
            case .sending:
                ProgressView(value: install.upload?.fractionCompleted ?? 0)
                    .accessibilityHidden(true)
            case .installing:
                ProgressView().accessibilityHidden(true)
            default:
                EmptyView()
            }
            Text(statusText(phase))
                .font(.footnote)
                .foregroundStyle(isFailure(phase) ? .red : .secondary)
                .fixedSize(horizontal: false, vertical: true)
                .accessibilityIdentifier("pakInstall.status")
            if let mismatch = install.mismatch, let confirmed = install.confirmedName {
                Label {
                    Text(mismatch.replacedOne
                        ? "\(mismatch.installed) was replaced instead of \(confirmed). The previous \(mismatch.installed) is in the trash of the console."
                        : "\(mismatch.installed) was installed instead of \(confirmed), which was not replaced.")
                        .fixedSize(horizontal: false, vertical: true)
                } icon: {
                    Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                }
                .font(.footnote)
                .accessibilityIdentifier("pakInstall.mismatch")
            }
            if case .needsReplacement = phase {
                HStack {
                    Button("Replace…") { manager.pendingReplacement = install }
                        .accessibilityIdentifier("pakInstall.replace")
                    Button("Keep the Installed One") { manager.keepInstalled(install) }
                }
                .buttonStyle(.bordered)
            }
        }
        .padding(.vertical, 2)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("pakInstall.\(install.name)")
    }

    private func isFailure(_ phase: PakInstall.Phase) -> Bool {
        if case .failed = phase { return true }
        return false
    }

    private func statusText(_ phase: PakInstall.Phase) -> String {
        switch phase {
        case .sending:
            if let upload = install.upload, upload.phase == .paused {
                return String(localized: "Paused. The transfer will resume.")
            }
            return String(localized: "Sending to the console…")
        case .installing:
            return String(localized: "Installing on the console…")
        case .needsReplacement(let name):
            return String(localized: "\(name) is already installed on the console.")
        case .installed:
            return String(localized: "Installed")
        case .failed(let error):
            return error.message
        case .cancelled:
            return String(localized: "Cancelled")
        }
    }
}

/// What the Install sheet starts from.
struct PakInstallDraft: Identifiable {
    enum Source {
        case file(PickedFile)
        /// Typed on the sheet.
        case link
        case sdCard(SdEntry)
    }

    let id = UUID()
    let source: Source
}

/// The options of an installation: the source, and where it goes
/// (automatic by default: the console decides from the archive, its SD
/// folder or its `pak.json`).
struct PakInstallOptionsView: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    let deviceId: String
    let draft: PakInstallDraft

    @State private var kind: PakInstallKind = .auto
    @State private var linkText = ""
    @State private var isSending = false
    @State private var error: AppError?
    @State private var started = false

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    switch draft.source {
                    case .file(let file):
                        LabeledContent("File", value: file.name)
                        LabeledContent("Size", value: file.sizeBytes.byteString)
                    case .sdCard(let entry):
                        LabeledContent("On the SD card", value: entry.name)
                    case .link:
                        TextField(text: $linkText, prompt: Text(verbatim: "https://")) {
                            Text("Link")
                        }
                        .keyboardType(.URL)
                        .textContentType(.URL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .accessibilityIdentifier("pakInstall.link")
                    }
                } footer: {
                    switch draft.source {
                    case .link:
                        if !linkText.isEmpty && DownloadURL.validated(linkText) == nil {
                            Text("Enter a link starting with http:// or https://.").foregroundStyle(.red)
                        } else {
                            Text("The console downloads the zip archive itself, on its own Wi-Fi connection.")
                        }
                    case .file, .sdCard:
                        Text("A zip archive holding one Name.pak folder, or a .pak folder.")
                    }
                }
                Section {
                    Picker("Install As", selection: $kind) {
                        Text("Automatic").tag(PakInstallKind.auto)
                        Text("Tool").tag(PakInstallKind.tool)
                        Text("Emulator").tag(PakInstallKind.emu)
                    }
                    .accessibilityIdentifier("pakInstall.kind")
                } footer: {
                    Text("Automatic lets the console choose from the archive or its pak.json. A pak of the same name is never replaced without asking.")
                }
                if let error {
                    Section { ErrorView(error: error) }
                }
                Section {
                    Text("ReminisLink does not check what a pak does: install only paks you trust. It runs on the console like any other app.")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
            }
            .navigationTitle("Install an App")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    if isSending {
                        ProgressView()
                    } else {
                        Button("Install") { Task { await install() } }
                            .disabled(!canInstall)
                            .accessibilityIdentifier("pakInstall.confirm")
                    }
                }
            }
        }
        .interactiveDismissDisabled(isSending)
        .onDisappear {
            // Cancelled: the copy of the file is deleted.
            if !started, case .file(let file) = draft.source { file.remove() }
        }
    }

    private var canInstall: Bool {
        switch draft.source {
        case .link: DownloadURL.validated(linkText) != nil
        // A file refused at once was deleted with its installation.
        case .file: !started
        case .sdCard: true
        }
    }

    private func install() async {
        let manager = app.session(for: deviceId).paks
        let source: PakInstall.Source
        let name: String
        switch draft.source {
        case .file(let file):
            source = .file(file)
            name = file.name
        case .sdCard(let entry):
            source = .sdCard(entry)
            name = entry.name
        case .link:
            guard let url = DownloadURL.validated(linkText) else { return }
            source = .link(url)
            name = DownloadURL.displayName(url)
        }
        isSending = true
        defer { isSending = false }
        started = true
        let install = await manager.install(source, name: name, kind: kind)
        // Refused at once (busy, a link the console refuses…): said here.
        if case .failed(let error) = install.phase {
            self.error = error
            manager.remove(install)
            return
        }
        dismiss()
    }
}

/// "Replace <name>? The current version goes to the trash." for the
/// installation waiting for it.
struct PakReplacementModifier: ViewModifier {
    let manager: PakManager

    func body(content: Content) -> some View {
        content.confirmationDialog(
            Text("Replace \(manager.pendingReplacement.flatMap(Self.name) ?? "")?"),
            isPresented: Binding(
                get: { manager.pendingReplacement != nil },
                set: { if !$0 { manager.pendingReplacement = nil } }
            ),
            titleVisibility: .visible,
            presenting: manager.pendingReplacement
        ) { install in
            Button("Replace", role: .destructive) {
                Task { await manager.replace(install) }
            }
            .accessibilityIdentifier("pakReplace.confirm")
            Button("Keep the Installed One", role: .cancel) { manager.keepInstalled(install) }
        } message: { _ in
            Text("The current version goes to the trash of the console.")
        }
    }

    private static func name(_ install: PakInstall) -> String? {
        if case .needsReplacement(let name) = install.phase { return name }
        return nil
    }
}

extension View {
    func pakReplacementConfirmation(_ manager: PakManager) -> some View {
        modifier(PakReplacementModifier(manager: manager))
    }
}
