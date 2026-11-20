import ReminisLinkKit
import SwiftUI

/// Imports cheats into a game (docs/specs/04-api-v1.md, `cheats/import`):
/// from Files (sent to the staging area first) or from a `.cht` of the SD
/// card; merged into the list or replacing it. The app proposes no source
/// of cheats, and the console never downloads a link (removed from v1).
struct CheatImportView: View {
    enum Source: Hashable {
        case files
        case sdCard
    }

    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    let route: GameRoute
    let onImported: (CheatImportResult) -> Void

    @State private var source: Source = .files
    @State private var mode: CheatImportMode = .merge
    @State private var file: PickedFile?
    @State private var showImporter = false
    @State private var sdFiles: [SdFile]?
    @State private var sdFileId: String?
    @State private var error: AppError?
    @State private var errorText: String?
    @State private var isImporting = false
    @State private var confirmReplace = false

    private var session: ConsoleSession { app.session(for: route.deviceId) }

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    Picker("Source", selection: $source) {
                        Text("Files").tag(Source.files)
                        Text("SD Card").tag(Source.sdCard)
                    }
                    .pickerStyle(.segmented)
                    .accessibilityIdentifier("import.source")
                }
                if let errorText {
                    Section {
                        Label(errorText, systemImage: "exclamationmark.triangle.fill")
                            .accessibilityIdentifier("import.error")
                    }
                } else if let error {
                    Section { ErrorView(error: error) }
                }
                if isImporting && session.reconnectionWaits > 0 {
                    // M10: the file waits on this iPhone, then goes on.
                    Section {
                        Label("Paused: the console disconnected this app. The transfer resumes when you reconnect.", systemImage: "pause.circle")
                            .accessibilityIdentifier("import.paused")
                    }
                }
                switch source {
                case .files: filesSection
                case .sdCard: sdSection
                }
                Section {
                    Picker("Import", selection: $mode) {
                        Text("Add to the List").tag(CheatImportMode.merge)
                        Text("Replace the List").tag(CheatImportMode.replace)
                    }
                    .pickerStyle(.inline)
                    .labelsHidden()
                    .accessibilityIdentifier("import.mode")
                } footer: {
                    switch mode {
                    case .merge: Text("The imported cheats are added after the current ones.")
                    case .replace: Text("The current cheats are replaced. The previous file is kept on the console (.cht.bak).")
                    }
                }
            }
            .navigationTitle("Import Cheats")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { close() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    if isImporting {
                        ProgressView()
                    } else {
                        Button("Import") {
                            if mode == .replace { confirmReplace = true } else { Task { await runImport() } }
                        }
                        .disabled(!canImport)
                        .accessibilityIdentifier("import.run")
                    }
                }
            }
            .fileImporter(isPresented: $showImporter, allowedContentTypes: [.item]) { result in
                Task { await picked(result) }
            }
            .confirmationDialog("Replace the list?", isPresented: $confirmReplace, titleVisibility: .visible) {
                Button("Replace the List", role: .destructive) { Task { await runImport() } }
                    .accessibilityIdentifier("import.replaceConfirm")
            } message: {
                Text("The current cheats are replaced by the imported ones. The previous file is kept on the console (.cht.bak).")
            }
            .task(id: source) {
                if source == .sdCard && sdFiles == nil { await loadSdFiles() }
            }
        }
        .interactiveDismissDisabled(isImporting)
    }

    // MARK: Sources

    private var filesSection: some View {
        Section {
            if let file {
                LabeledContent(file.name, value: file.sizeBytes.byteString)
                    .accessibilityIdentifier("import.file")
            }
            Button {
                showImporter = true
            } label: {
                Label(file == nil ? "Choose a File…" : "Choose Another File…", systemImage: "folder")
            }
            .accessibilityIdentifier("import.choose")
        } footer: {
            Text("A RetroArch cheat file (.cht). It is sent to the console, then imported.")
        }
    }

    @ViewBuilder
    private var sdSection: some View {
        Section {
            if let sdFiles {
                if sdFiles.isEmpty {
                    Text("No .cht file in the Cheats folder of the SD card.")
                        .foregroundStyle(.secondary)
                }
                ForEach(sdFiles) { file in
                    Button {
                        sdFileId = file.id
                    } label: {
                        HStack {
                            VStack(alignment: .leading, spacing: 2) {
                                Text(file.fileName).foregroundStyle(.primary)
                                Text(file.path)
                                    .font(.caption.monospaced())
                                    .foregroundStyle(.secondary)
                            }
                            Spacer()
                            if sdFileId == file.id {
                                Image(systemName: "checkmark").foregroundStyle(.tint)
                            }
                        }
                        .accessibilityElement(children: .combine)
                        .accessibilityAddTraits(sdFileId == file.id ? .isSelected : [])
                    }
                    .accessibilityIdentifier("import.sdFile")
                }
            } else {
                ProgressView()
            }
        } footer: {
            Text("The .cht files of the Cheats folder of the SD card. Those named after this game come first.")
        }
    }

    // MARK: Logic

    private var canImport: Bool {
        guard !isImporting else { return false }
        switch source {
        case .files: return file != nil
        case .sdCard: return sdFileId != nil
        }
    }

    private func loadSdFiles() async {
        do {
            let files = try await session.perform { try await $0.sdFiles(type: .cht) }
            sdFiles = SdFile.sortedForGame(files, fileName: route.fileName ?? route.name, name: route.name)
        } catch {
            let error = AppError(error)
            if !error.isCancellation { self.error = error }
        }
    }

    private func picked(_ result: Result<URL, Error>) async {
        switch result {
        case .success(let url):
            do {
                file?.remove()
                file = try await PickedFile.copy(url, into: app.transferDirectory)
            } catch {
                self.error = .file
            }
        case .failure:
            error = .file
        }
    }

    private func runImport() async {
        isImporting = true
        defer { isImporting = false }
        error = nil
        errorText = nil
        let gameId = route.gameId
        do {
            let result: CheatImportResult
            switch source {
            case .files:
                guard let file else { return }
                self.file = nil
                result = try await session.importCheats(gameId: gameId, from: file, mode: mode)
            case .sdCard:
                guard let sdFileId else { return }
                result = try await session.importCheats(gameId: gameId, CheatImportRequest(source: .sd(sdFileId: sdFileId), mode: mode))
            }
            onImported(result)
            dismiss()
        } catch {
            let error = AppError(error)
            guard !error.isCancellation else { return }
            if case .api(let api) = error, api.code == .unsupportedFormat {
                errorText = String(localized: "No cheat was found in this file. Choose a RetroArch cheat file (.cht). Nothing was changed.")
            } else {
                self.error = error
            }
        }
    }

    private func close() {
        file?.remove()
        dismiss()
    }
}
