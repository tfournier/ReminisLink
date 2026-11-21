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
    /// A QR code that is not a pairing link this app can use.
    @State private var scanError: AppError?
    @State private var scanner = ScannerKind.current()
    /// The camera fallback could not start.
    @State private var cameraFailure: CameraQRScanner.Failure?
    @State private var photo: PhotosPickerItem?
    @State private var importsFile = false

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
                    .accessibilityIdentifier("pairing.importPhoto")
                    Button {
                        importsFile = true
                    } label: {
                        Label("Choose a Picture File", systemImage: "folder")
                    }
                    .accessibilityIdentifier("pairing.importFile")
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
            .onAppear { pairingName = app.store.pairingName() ?? "" }
            .onDisappear { savePairingName() }
            .onChange(of: photo) { _, item in
                guard let item else { return }
                photo = nil
                Task { await importPicture { try await item.loadTransferable(type: Data.self) } }
            }
            .fileImporter(isPresented: $importsFile, allowedContentTypes: [.image]) { result in
                guard case .success(let url) = result else { return }
                Task {
                    await importPicture {
                        let scoped = url.startAccessingSecurityScopedResource()
                        defer { if scoped { url.stopAccessingSecurityScopedResource() } }
                        return try Data(contentsOf: url)
                    }
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
            QRScannerView { scanned($0) }
                .frame(height: 280)
                .listRowInsets(EdgeInsets())
                .accessibilityLabel(Text("QR code scanner"))
        case .camera where cameraFailure == nil:
            CameraQRScannerView(onLink: { scanned($0) }, onUnavailable: { cameraFailure = $0 })
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

    private func scanned(_ result: Result<PairingLink, PairingLinkError>) {
        switch result {
        case .success(let link):
            savePairingName()
            app.requestPairing(link)
        case .failure(let error):
            scanError = AppError(pairingLink: error)
        }
    }

    /// Reads the QR code of an imported picture. Nothing else than a
    /// pairing link the scanner would accept leads to a pairing.
    private func importPicture(_ load: @escaping @Sendable () async throws -> Data?) async {
        scanError = nil
        let search: PairingCodeSearch? = await Task.detached {
            guard let data = try? await load(), let payloads = try? QRCodeReader.payloads(inImageData: data) else { return nil }
            return PairingCodeSearch(payloads: payloads)
        }.value
        switch search {
        case .link(let link)?:
            scanned(.success(link))
        case .refused(let error)?:
            scanError = AppError(pairingLink: error)
        case .notAPairingCode?, .noCode?:
            scanError = .noPairingCodeInPicture
        case nil:
            scanError = .file
        }
    }

    /// Keeps the typed name (sent at the next pairing); an empty field
    /// falls back on the model of this device.
    private func savePairingName() {
        guard pairingName != (app.store.pairingName() ?? "") else { return }
        try? app.store.setPairingName(pairingName)
    }
}
