import ReminisLinkKit
import SwiftUI

/// One folder of the SD card, read only (milestone M12): folders first,
/// then files, with their size and date. A file can be downloaded to the
/// iPhone (share sheet, as the files of a game, M11). Opened from the
/// Storage section of the Console tab, and reused as the picker of
/// "Install from the SD card" (`onPick`): there a zip archive or a `.pak`
/// folder is chosen, other files are shown dimmed.
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

    private var isPicking: Bool { onPick != nil }

    var body: some View {
        List {
            if let error {
                Section { ErrorView(error: error, retry: { await load() }) }
            }
            if let downloadError {
                Section { ErrorView(error: downloadError) }
            }
            if let folder {
                Section {
                    ForEach(folder.entries) { entry in
                        row(entry)
                    }
                } footer: {
                    VStack(alignment: .leading, spacing: 6) {
                        if folder.truncated {
                            Text("This folder holds more than 5,000 items: only the first 5,000 are shown.")
                                .accessibilityIdentifier("sd.truncated")
                        }
                        if isPicking {
                            Text("Choose a zip archive or a folder whose name ends with .pak.")
                        } else if folder.isRoot {
                            Text("Read only. ReminisLink's own data is not shown.")
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
        .onDisappear { cancelDownload() }
        .exportSheet($exported)
        .task { if folder == nil { await load() } }
        .refreshable { await load() }
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
