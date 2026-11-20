import ReminisLinkKit
import SwiftUI
import VisionKit

/// Add a console: scan the QR code (docs/specs/06-app-ios.md §4.2). M13:
/// no PIN; the name the consoles show for this app is typed here.
struct AddConsoleView: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    /// The console to pair again ("Pair Again"): only the title changes.
    var repairing: String?
    /// The name of this app's identity on the consoles.
    @State private var pairingName = ""
    /// A QR code that is not a pairing link this app can use.
    @State private var scanError: AppError?

    var body: some View {
        NavigationStack {
            List {
                Section {
                    if QRScannerView.isAvailable {
                        QRScannerView { result in
                            switch result {
                            case .success(let link):
                                savePairingName()
                                app.requestPairing(link)
                            case .failure(let error):
                                scanError = AppError(pairingLink: error)
                            }
                        }
                        .frame(height: 280)
                        .listRowInsets(EdgeInsets())
                        .accessibilityLabel(Text("QR code scanner"))
                    } else {
                        Text("The scanner is not available on this device. Scan the QR code with the Camera app.")
                            .foregroundStyle(.secondary)
                    }
                    if let scanError {
                        ErrorView(error: scanError)
                    }
                } header: {
                    Text("Scan the QR Code")
                } footer: {
                    Text("Point the camera at the QR code shown by ReminisLink on the console. If it is not shown, open Settings > Pair a New Device on the console.")
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
            .navigationTitle(repairing == nil ? "Add a Console" : "Pair Again")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
            }
        }
    }

    /// Keeps the typed name (sent at the next pairing); an empty field
    /// falls back on the model of this device.
    private func savePairingName() {
        guard pairingName != (app.store.pairingName() ?? "") else { return }
        try? app.store.setPairingName(pairingName)
    }
}

/// VisionKit scanner that recognizes `reminislink://pair` QR codes. A
/// pairing link this app cannot use (a pak older than M13...) is reported
/// once, and scanning goes on.
struct QRScannerView: UIViewControllerRepresentable {
    let onLink: (Result<PairingLink, PairingLinkError>) -> Void

    static var isAvailable: Bool {
        DataScannerViewController.isSupported && DataScannerViewController.isAvailable
    }

    func makeUIViewController(context: Context) -> DataScannerViewController {
        let scanner = DataScannerViewController(
            recognizedDataTypes: [.barcode(symbologies: [.qr])],
            qualityLevel: .balanced,
            recognizesMultipleItems: false,
            isHighFrameRateTrackingEnabled: false,
            isHighlightingEnabled: true
        )
        scanner.delegate = context.coordinator
        try? scanner.startScanning()
        return scanner
    }

    func updateUIViewController(_ scanner: DataScannerViewController, context: Context) {
        context.coordinator.onLink = onLink
    }

    static func dismantleUIViewController(_ scanner: DataScannerViewController, coordinator: Coordinator) {
        scanner.stopScanning()
    }

    func makeCoordinator() -> Coordinator { Coordinator(onLink: onLink) }

    final class Coordinator: NSObject, DataScannerViewControllerDelegate {
        var onLink: (Result<PairingLink, PairingLinkError>) -> Void
        private var found = false
        /// Refused payloads already reported.
        private var refused: Set<String> = []

        init(onLink: @escaping (Result<PairingLink, PairingLinkError>) -> Void) {
            self.onLink = onLink
        }

        func dataScanner(_ scanner: DataScannerViewController, didAdd items: [RecognizedItem], allItems: [RecognizedItem]) {
            guard !found else { return }
            for item in items {
                guard case .barcode(let barcode) = item, let payload = barcode.payloadStringValue else { continue }
                do {
                    let link = try PairingLink.parse(payload)
                    found = true
                    scanner.stopScanning()
                    onLink(.success(link))
                    return
                } catch {
                    // Another QR code: ignored. A ReminisLink one that cannot
                    // be used (older pak): said once.
                    guard error != .invalid, refused.insert(payload).inserted else { continue }
                    onLink(.failure(error))
                }
            }
        }
    }
}
