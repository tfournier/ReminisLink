import ReminisLinkKit
import SwiftUI
import UniformTypeIdentifiers

/// One folder of the SD card (milestone M12): folders first, then files,
/// with their size and date. A file can be downloaded to the iPhone (share
/// sheet, as the files of a game, M11). The root is the Files tab (M23),
/// and the view is reused as the picker of "Install from the SD card"
/// (`onPick`): there a zip archive or a `.pak` folder is chosen, other
/// files are shown dimmed.
///
/// M23: where the pak says so (`writable`, `deletable`), files are added
/// (resumable uploads), folders made, and items moved to the trash of the
/// console after a confirmation that names them, one by one (swipe, menu)
/// or several at once (Select).
///
/// Only opaque identifiers go back to the console; the data of
/// ReminisLink is never listed by the pak.
struct SdFolderView: View {
    @Environment(AppModel.self) private var app
    let route: SdFolderRoute
    /// Picker mode: called with the chosen zip or `.pak` folder.
    var onPick: ((SdEntry) -> Void)?

    @State private var folder: SdFolder?
    @State private var error: AppError?
    @State private var loads = LoadGeneration()
    @State private var download: FileDownload?
    @State private var downloadTask: Task<Void, Never>?
    @State private var downloadError: AppError?
    @State private var exported: ExportedFile?
    // M23: the writes.
    @State private var isSelecting = false
    @State private var selection = SdSelection()
    @State private var pendingTrash: [SdEntry] = []
    @State private var isAddingFiles = false
    @State private var isNamingFolder = false
    @State private var newFolderName = ""
    @State private var actionError: AppError?
    @State private var queuedNotice: Int?

    private var isPicking: Bool { onPick != nil }
    private var canWrite: Bool { !isPicking && folder?.writable == true }
    private var deletableEntries: [SdEntry] { folder?.entries.filter(\.deletable) ?? [] }
    /// The entries as they were when selected (their versions are sent).
    private var selectedEntries: [SdEntry] { selection.entries }

    var body: some View {
        List {
            if let error {
                Section { ErrorView(error: error, retry: { await load() }) }
            }
            if let downloadError {
                Section { ErrorView(error: downloadError) }
            }
            if let actionError {
                Section { ErrorView(error: actionError) }
            }
            if let queuedNotice {
                Section {
                    Label(String(localized: "\(queuedNotice) files are being sent. Follow them in Transfers, on the Console tab."),
                          systemImage: "arrow.up.circle")
                        .accessibilityIdentifier("sd.queued")
                }
            }
            if let folder {
                Section {
                    ForEach(folder.entries) { entry in
                        if isSelecting {
                            selectableRow(entry)
                        } else {
                            row(entry)
                                .swipeActions {
                                    if !isPicking && entry.deletable {
                                        Button("Move to Trash", systemImage: "trash", role: .destructive) { pendingTrash = [entry] }
                                    }
                                }
                                .contextMenu {
                                    if !isPicking && entry.deletable {
                                        Button("Move to Trash…", systemImage: "trash", role: .destructive) { pendingTrash = [entry] }
                                    }
                                }
                        }
                    }
                } footer: {
                    VStack(alignment: .leading, spacing: 6) {
                        if folder.truncated {
                            Text("This folder holds more than 5,000 items: only the first 5,000 are shown.")
                                .accessibilityIdentifier("sd.truncated")
                        }
                        if isPicking {
                            Text("Choose a zip archive or a folder whose name ends with .pak.")
                        } else if !folder.writable {
                            Text("Read only: the console needs this folder.")
                        } else if folder.isRoot {
                            Text("Items you delete go to the trash of the console. ReminisLink's own data is not shown.")
                        }
                    }
                }
            }
        }
        .overlay {
            if folder == nil && error == nil {
                ProgressView()
            } else if folder?.entries.isEmpty == true {
                ContentUnavailableView("This folder is empty", systemImage: "folder")
            }
        }
        .navigationTitle(route.folderId == nil ? String(localized: "SD Card") : route.title)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar { if !isPicking { writeToolbar } }
        // The selection bar replaces the tab bar while selecting (iOS 26:
        // the floating tab bar of an iPhone covers the bottom bar).
        .toolbar(isSelecting ? .hidden : .automatic, for: .tabBar)
        .onDisappear { cancelDownload() }
        .exportSheet($exported)
        .task { if folder == nil { await load() } }
        .refreshable { await load() }
        // M14: a change of the card by any client (an upload installed).
        .onChange(of: app.session(for: route.deviceId).libraryRevision) { _, _ in
            Task { await load() }
        }
        .fileImporter(isPresented: $isAddingFiles, allowedContentTypes: [.item], allowsMultipleSelection: true) { result in
            if case .success(let urls) = result { Task { await addFiles(urls) } }
        }
        .alert("New Folder", isPresented: $isNamingFolder) {
            TextField("Name", text: $newFolderName)
                .accessibilityIdentifier("sd.newFolder.name")
            Button("Create") { Task { await createFolder() } }
                .accessibilityIdentifier("sd.newFolder.create")
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("The folder is made on the SD card of the console.")
        }
        .confirmationDialog(
            pendingTrash.count == 1 ? String(localized: "Move to the trash?") : String(localized: "Move \(pendingTrash.count) items to the trash?"),
            isPresented: Binding(get: { !pendingTrash.isEmpty }, set: { if !$0 { pendingTrash = [] } }),
            titleVisibility: .visible
        ) {
            Button("Move to Trash", role: .destructive) {
                let entries = pendingTrash
                Task { await trash(entries) }
            }
            .accessibilityIdentifier("sd.trash.confirm")
        } message: {
            Text(trashMessage(pendingTrash))
        }
    }

    @ToolbarContentBuilder
    private var writeToolbar: some ToolbarContent {
        if isSelecting {
            ToolbarItem(placement: .confirmationAction) {
                Button("Done") { finishSelection() }
                    .accessibilityIdentifier("sd.select.done")
            }
            ToolbarItem(placement: .bottomBar) {
                Button("Move to Trash", systemImage: "trash", role: .destructive) { pendingTrash = selectedEntries }
                    .disabled(selectedEntries.isEmpty)
                    .accessibilityIdentifier("sd.select.trash")
            }
        } else {
            if canWrite {
                ToolbarItem(placement: .primaryAction) {
                    Menu {
                        Button("Add Files…", systemImage: "doc.badge.plus") { isAddingFiles = true }
                            .accessibilityIdentifier("sd.addFiles")
                        Button("New Folder…", systemImage: "folder.badge.plus") {
                            newFolderName = ""
                            isNamingFolder = true
                        }
                        .accessibilityIdentifier("sd.newFolder")
                    } label: {
                        Label("Add", systemImage: "plus")
                    }
                    .accessibilityIdentifier("sd.add")
                }
            }
            if !deletableEntries.isEmpty {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Select", systemImage: "checkmark.circle") { isSelecting = true }
                        .accessibilityIdentifier("sd.select")
                }
            }
        }
    }

    /// A row in selection mode: deletable entries can be checked.
    private func selectableRow(_ entry: SdEntry) -> some View {
        let selected = selection.contains(entry.sdEntryId)
        return Button {
            // One request, all or nothing (Codex review of M23): at most 100.
            if selection.toggle(entry) {
                if actionError == .sdTooManySelected { actionError = nil }
            } else {
                actionError = .sdTooManySelected
            }
        } label: {
            HStack {
                Image(systemName: selected ? "checkmark.circle.fill" : "circle")
                    .foregroundStyle(entry.deletable ? Color.accentColor : .secondary)
                    .font(.title3)
                    .accessibilityHidden(true)
                SdEntryLabel(entry: entry)
            }
        }
        .tint(.primary)
        .disabled(!entry.deletable)
        .accessibilityAddTraits(selected ? .isSelected : [])
        .accessibilityIdentifier("sd.\(entry.name)")
    }

    /// What goes to the trash, named (at most 5 names).
    private func trashMessage(_ entries: [SdEntry]) -> String {
        let names = entries.prefix(5).map(\.name).joined(separator: "\n")
        let more = entries.count > 5 ? "\n" + String(localized: "and \(entries.count - 5) more") : ""
        let folders = entries.contains(where: \.isFolder)
            ? String(localized: "Folders go with everything in them.") + " "
            : ""
        return names + more + "\n\n" + folders
            + String(localized: "They stay in the trash of the console for 30 days, and can be restored from the Trash screen.")
    }

    private func finishSelection() {
        isSelecting = false
        selection.clear()
    }

    private func trash(_ entries: [SdEntry]) async {
        pendingTrash = []
        do {
            try await app.session(for: route.deviceId).trashSdEntries(entries)
            actionError = nil
            finishSelection()
        } catch {
            let error = AppError(error)
            if !error.isCancellation { actionError = error }
        }
        await load()
    }

    private func createFolder() async {
        let name = newFolderName.trimmingCharacters(in: .whitespaces)
        guard !name.isEmpty else { return }
        do {
            _ = try await app.session(for: route.deviceId).createSdFolder(parentId: route.folderId, name: name)
            actionError = nil
        } catch {
            let error = AppError(error)
            if !error.isCancellation { actionError = error }
        }
        await load()
    }

    private func addFiles(_ urls: [URL]) async {
        let title = route.folderId == nil ? String(localized: "SD Card") : route.title
        do {
            let count = try await app.session(for: route.deviceId)
                .addSdFiles(urls, folderId: route.folderId, folderTitle: title, securityScoped: true)
            queuedNotice = count
            actionError = nil
        } catch {
            let error = AppError(error)
            if !error.isCancellation { actionError = error }
        }
    }

    @ViewBuilder
    private func row(_ entry: SdEntry) -> some View {
        let child = SdFolderRoute(deviceId: route.deviceId, folderId: entry.sdEntryId, title: entry.name)
        if isPicking && entry.isInstallableAsPak {
            Button {
                onPick?(entry)
            } label: {
                SdEntryLabel(entry: entry, accessory: .install)
            }
            .tint(.primary)
            .accessibilityIdentifier("sd.\(entry.name)")
        } else if entry.isFolder {
            Group {
                if isPicking {
                    NavigationLink(value: child) { SdEntryLabel(entry: entry) }
                } else {
                    NavigationLink(value: Route.sdFolder(child)) { SdEntryLabel(entry: entry) }
                }
            }
            .accessibilityIdentifier("sd.\(entry.name)")
        } else if isPicking || !entry.isFile {
            SdEntryLabel(entry: entry)
                .foregroundStyle(.secondary)
                .accessibilityIdentifier("sd.\(entry.name)")
        } else {
            let progress = download?.fileId == entry.sdEntryId ? download : nil
            HStack {
                SdEntryLabel(entry: entry, progress: progress)
                if progress != nil {
                    Button(action: cancelDownload) {
                        Image(systemName: "xmark.circle.fill").font(.title2).foregroundStyle(.secondary)
                    }
                    .buttonStyle(.borderless)
                    .accessibilityLabel(Text("Cancel the download of \(entry.name)"))
                    .accessibilityIdentifier("sd.cancel")
                } else {
                    Button { startDownload(entry) } label: {
                        Image(systemName: "arrow.down.circle").font(.title2)
                    }
                    .buttonStyle(.borderless)
                    .disabled(download != nil)
                    .accessibilityLabel(Text("Download \(entry.name)"))
                    .accessibilityIdentifier("sd.download")
                }
            }
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("sd.\(entry.name)")
        }
    }

    private func load() async {
        let generation = loads.begin()
        let folderId = route.folderId
        do {
            let folder = try await app.session(for: route.deviceId).perform { try await $0.browseSd(folderId: folderId) }
            guard loads.isCurrent(generation) else { return }
            self.folder = folder
            // An entry that changed or left is no longer the one selected.
            selection.prune(listing: folder.entries)
            error = nil
        } catch {
            guard loads.isCurrent(generation) else { return }
            let error = AppError.pak(error, routeMissing: route.folderId == nil)
            if !error.isCancellation { self.error = error }
        }
    }

    /// Downloads a file to the export directory, then opens the share
    /// sheet. A `404` means the file is gone or changed: the folder is
    /// loaded again.
    private func startDownload(_ entry: SdEntry) {
        downloadTask?.cancel()
        let progress = FileDownload(fileId: entry.sdEntryId, expected: entry.sizeBytes)
        download = progress
        downloadError = nil
        let session = app.session(for: route.deviceId)
        let report = progress.reporter()
        let id = entry.sdEntryId
        let name = entry.name
        downloadTask = Task {
            do {
                let file = try await session.export { client, directory in
                    try await client.downloadSdFile(id: id, fallbackName: name, into: directory, progress: report)
                }
                guard !Task.isCancelled, download === progress else { return ExportedFiles.remove(file.file) }
                download = nil
                exported = file
            } catch {
                guard download === progress else { return }
                download = nil
                let error = AppError.gameFile(error)
                if error.isCancellation { return }
                downloadError = error
                if error == .gameFileChanged { await load() }
            }
        }
    }

    private func cancelDownload() {
        downloadTask?.cancel()
        downloadTask = nil
        download = nil
    }
}

/// A folder or file: icon, name, size and date.
struct SdEntryLabel: View {
    enum Accessory {
        case none
        /// Picker mode: the entry can be installed.
        case install
    }

    let entry: SdEntry
    var accessory: Accessory = .none
    var progress: FileDownload?

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: symbol)
                .foregroundStyle(entry.isFolder ? Color.accentColor : .secondary)
                .frame(width: 28)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 4) {
                Text(entry.name)
                    .lineLimit(2)
                Text(details)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                if let progress {
                    ProgressView(value: progress.fraction ?? 0)
                        .accessibilityLabel(Text("Downloading \(entry.name)"))
                        .accessibilityIdentifier("sd.progress")
                }
            }
            if accessory == .install {
                Spacer()
                Text("Install")
                    .font(.subheadline.bold())
                    .foregroundStyle(Color.accentColor)
            }
        }
        .accessibilityElement(children: .combine)
    }

    private var symbol: String {
        if entry.isPakFolder { return "shippingbox" }
        if entry.isFolder { return "folder.fill" }
        if entry.isZip { return "doc.zipper" }
        return "doc"
    }

    private var details: String {
        let date = entry.modifiedAt.dateTimeString
        guard let size = entry.sizeBytes, !entry.isFolder else { return date }
        return "\(size.byteString) · \(date)"
    }
}

/// The SD browser as a picker (Install from the SD card): its own
/// navigation, from the root of the card.
struct SdPickerSheet: View {
    @Environment(\.dismiss) private var dismiss
    let deviceId: String
    let onPick: (SdEntry) -> Void
    @State private var path: [SdFolderRoute] = []

    var body: some View {
        NavigationStack(path: $path) {
            SdFolderView(route: SdFolderRoute(deviceId: deviceId, folderId: nil, title: ""), onPick: pick)
                .navigationDestination(for: SdFolderRoute.self) { SdFolderView(route: $0, onPick: pick) }
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) {
                        Button("Cancel") { dismiss() }
                    }
                }
        }
    }

    private func pick(_ entry: SdEntry) {
        dismiss()
        onPick(entry)
    }
}
