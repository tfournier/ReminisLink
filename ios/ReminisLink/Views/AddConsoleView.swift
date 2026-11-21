import PhotosUI
import ReminisLinkKit
import SwiftUI
import UniformTypeIdentifiers

/// Add a console: scan the QR code (docs/specs/06-app-ios.md §4.2). M13:
/// no PIN; the name the consoles show for this app is typed here. M15:
/// VisionKit's scanner where supported, else the camera read with Vision
/// (an iPad app on a Mac), and a picture of the QR code from Photos or
/// Files; every path goes through the same pairing link check.
struct AddConsoleView: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    /// The console to pair again ("Pair Again"): only the title changes.
    var repairing: String?
    /// The name of this app's identity on the consoles.
    @State private var pairingName = ""
    /// The name put in the field when the screen opened: written back only
    /// if the user changed it (M15: an untouched field never hides a name
    /// that arrived meanwhile).
    @State private var loadedName = ""
    /// A QR code that is not a pairing link this app can use.
    @State private var scanError: AppError?
    @State private var scanner = ScannerKind.current()
    /// The camera fallback could not start.
    @State private var cameraFailure: CameraQRScanner.Failure?
    @State private var photo: PhotosPickerItem?
    @State private var importsFile = false
    /// The import in progress (one at a time), run by a task tied to the
    /// screen: closing it cancels the import.
    @State private var pendingImport: ImportRequest?
    /// Camera and imports: one link accepted, once, never after closing.
    @State private var acceptance = PairingCodeAcceptance()
    /// Changes to start the scanner again (back from Settings, a camera
    /// that came back).
    @State private var scannerGeneration = 0
    @Environment(\.scenePhase) private var scenePhase

    private var isMac: Bool { ProcessInfo.processInfo.isiOSAppOnMac }

    var body: some View {
        NavigationStack {
            List {
                Section {
                    scannerView
                    if let scanError {
                        ErrorView(error: scanError)
                    }
                } header: {
                    Text("Scan the QR Code")
                } footer: {
                    scannerFooter
                }

                Section {
                    PhotosPicker(selection: $photo, matching: .images) {
                        Label("Choose a Photo of the QR Code", systemImage: "photo")
                    }
                    .disabled(pendingImport != nil)
                    .accessibilityIdentifier("pairing.importPhoto")
                    Button {
                        importsFile = true
                    } label: {
                        Label("Choose a Picture File", systemImage: "folder")
                    }
                    .disabled(pendingImport != nil)
                    .accessibilityIdentifier("pairing.importFile")
                    if pendingImport != nil {
                        HStack(spacing: 12) {
                            ProgressView()
                            Text("Reading the picture…").foregroundStyle(.secondary)
                        }
                        .accessibilityIdentifier("pairing.importing")
                    }
                } header: {
                    Text("Import a Picture")
                } footer: {
                    Text("A picture of the QR code shown by the console, taken with another device and sent with AirDrop, for example.")
                }

                Section {
                    TextField(AppModel.defaultClientName, text: $pairingName)
                        .textInputAutocapitalization(.words)
                        .submitLabel(.done)
                        .onSubmit { savePairingName() }
                        .accessibilityIdentifier("pairing.clientName")
                } header: {
                    Text("Name Shown on the Console")
                } footer: {
                    Text("This name identifies all your Apple devices signed in with the same Apple Account: they share one pairing through iCloud Keychain.")
                }

                #if DEBUG
                if let link = app.demoPairingLink {
                    Section {
                        Button("Pair with the Demo Console") {
                            savePairingName()
                            app.requestPairing(link)
                        }
                        .accessibilityIdentifier("demo.pair")
                    } footer: {
                        Text("Demo mode: no real console is used.")
                    }
                }
                #endif
            }
            .onAppear {
                loadedName = app.store.pairingName() ?? ""
                pairingName = loadedName
            }
            .onDisappear {
                acceptance.close()
                savePairingName()
            }
            .onChange(of: photo) { _, item in
                guard let item else { return }
                photo = nil
                if pendingImport == nil { pendingImport = ImportRequest(source: .photo(item)) }
            }
            .fileImporter(isPresented: $importsFile, allowedContentTypes: [.image]) { result in
                guard case .success(let url) = result, pendingImport == nil else { return }
                pendingImport = ImportRequest(source: .file(url))
            }
            // Cancelled when the screen goes away (or another import starts).
            .task(id: pendingImport) {
                guard let request = pendingImport else { return }
                let outcome = await Self.read(request)
                guard !Task.isCancelled, pendingImport == request else { return }
                pendingImport = nil
                importFinished(outcome)
            }
            .onChange(of: scenePhase) { _, phase in
                // Back from Settings (camera allowed), or VisionKit or a
                // camera that changed: choose and start the scanner again.
                guard phase == .active else { return }
                let now = ScannerKind.current()
                if cameraFailure != nil || now != scanner {
                    cameraFailure = nil
                    scanner = now
                    scannerGeneration += 1
                }
            }
            .navigationTitle(repairing == nil ? "Add a Console" : "Pair Again")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
            }
        }
    }

    @ViewBuilder
    private var scannerView: some View {
        switch scanner {
        case .visionKit:
            QRScannerView(onLink: { scanned($0) }, onUnavailable: {
                // VisionKit stopped (or could not start): the camera path.
                scanner = ScannerKind.choose(
                    dataScannerSupported: false, dataScannerAvailable: false,
                    hasCamera: CameraQRScanner.preferredCamera() != nil
                )
            })
                .id(scannerGeneration)
                .frame(height: 280)
                .listRowInsets(EdgeInsets())
                .accessibilityLabel(Text("QR code scanner"))
        case .camera where cameraFailure == nil:
            CameraQRScannerView(onLink: { scanned($0) }, onUnavailable: { cameraFailure = $0 })
                .id(scannerGeneration)
                .frame(height: 280)
                .listRowInsets(EdgeInsets())
                .accessibilityLabel(Text("QR code scanner"))
                .accessibilityIdentifier("pairing.cameraScanner")
        case .camera:
            if cameraFailure == .denied {
                Text("ReminisLink is not allowed to use the camera. Allow it in Settings, or import a picture of the QR code.")
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("pairing.cameraDenied")
                Button("Open Settings") {
                    if let url = URL(string: UIApplication.openSettingsURLString) { UIApplication.shared.open(url) }
                }
            } else {
                noCameraText
            }
        case .none:
            noCameraText
        }
    }

    private var noCameraText: some View {
        Text("No camera is available. Import a picture of the QR code, or pair the console from your iPhone or iPad: the pairing reaches this device through iCloud Keychain.")
            .foregroundStyle(.secondary)
            .accessibilityIdentifier("pairing.noCamera")
    }

    @ViewBuilder
    private var scannerFooter: some View {
        if isMac {
            Text("Hold the console in front of this Mac's camera, with the QR code facing it. You can also import a picture of the QR code, or pair from your iPhone or iPad: the pairing reaches this Mac through iCloud Keychain.")
        } else {
            Text("Point the camera at the QR code shown by ReminisLink on the console. If it is not shown, open Settings > Pair a New Device on the console.")
        }
    }

    /// The one place a link found by the camera or in a picture leads to a
    /// pairing: the first only, never after the screen closed.
    private func scanned(_ result: Result<PairingLink, PairingLinkError>) {
        switch result {
        case .success(let link):
            guard acceptance.accept() else { return }
            savePairingName()
            app.requestPairing(link)
        case .failure(let error):
            guard !acceptance.isClosed else { return }
            scanError = AppError(pairingLink: error)
        }
    }

    struct ImportRequest: Equatable {
        enum Source: Equatable {
            case photo(PhotosPickerItem)
            case file(URL)
        }

        let id = UUID()
        let source: Source
    }

    enum ImportOutcome: Equatable {
        case found(PairingCodeSearch)
        case failed(AppError)
        case cancelled
    }

    /// Loads and reads a picture, off the main actor; bounded (bytes, then
    /// pixels, downsampled before Vision: ``QRCodeReader``).
    private static func read(_ request: ImportRequest) async -> ImportOutcome {
        do {
            let payloads: [String]
            switch request.source {
            case .photo(let item):
                // A file, never the whole picture in memory: its size is
                // checked before ImageIO reads it.
                guard let picture = try await item.loadTransferable(type: ImportedPicture.self) else { return .failed(.file) }
                defer { try? FileManager.default.removeItem(at: picture.url) }
                payloads = try await decode(picture.url, securityScoped: false)
            case .file(let url):
                payloads = try await decode(url, securityScoped: true)
            }
            try Task.checkCancellation()
            return .found(PairingCodeSearch(payloads: payloads))
        } catch is CancellationError {
            return .cancelled
        } catch QRCodeReader.ImageError.tooLarge {
            return .failed(.pictureTooLarge)
        } catch {
            return Task.isCancelled ? .cancelled : .failed(.file)
        }
    }

    /// Reads a picture off the main actor, in a child of the import task:
    /// closing the screen cancels it, and a cancelled import never starts
    /// ImageIO or Vision.
    @concurrent
    private nonisolated static func decode(_ url: URL, securityScoped: Bool) async throws -> [String] {
        try Task.checkCancellation()
        let access = securityScoped && url.startAccessingSecurityScopedResource()
        defer { if access { url.stopAccessingSecurityScopedResource() } }
        try Task.checkCancellation()
        return try QRCodeReader.payloads(inImageAt: url)
    }

    private func importFinished(_ outcome: ImportOutcome) {
        scanError = nil
        switch outcome {
        case .found(.link(let link)):
            scanned(.success(link))
        case .found(.refused(let error)):
            scanned(.failure(error))
        case .found(.notAPairingCode), .found(.noCode):
            if !acceptance.isClosed { scanError = .noPairingCodeInPicture }
        case .failed(let error):
            if !acceptance.isClosed { scanError = error }
        case .cancelled:
            break
        }
    }

    /// Keeps the typed name (sent at the next pairing); an empty field
    /// falls back on the model of this device.
    private func savePairingName() {
        guard pairingName != loadedName, pairingName != (app.store.pairingName() ?? "") else { return }
        try? app.store.setPairingName(pairingName)
        loadedName = pairingName
    }
}

/// A picture picked in Photos, received as a file (copied to a temporary
/// place, removed once read).
struct ImportedPicture: Transferable {
    let url: URL

    static var transferRepresentation: some TransferRepresentation {
        FileRepresentation(importedContentType: .image) { received in
            // Too large: refused before anything is copied.
            let size = try received.file.resourceValues(forKeys: [.fileSizeKey]).fileSize ?? 0
            guard size <= QRCodeReader.maxImageBytes else { throw QRCodeReader.ImageError.tooLarge }
            let copy = FileManager.default.temporaryDirectory
                .appending(path: "qr-import-\(UUID().uuidString)")
                .appendingPathExtension(received.file.pathExtension)
            try FileManager.default.copyItem(at: received.file, to: copy)
            return ImportedPicture(url: copy)
        }
    }
}
