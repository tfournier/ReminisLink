import ReminisLinkKit
import SwiftUI
import VisionKit

/// Add a console: scan the QR code, or type the PIN (docs/specs/06-app-ios.md §4.2).
struct AddConsoleView: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    /// The paired console to pair again ("Pair Again"): offered for the PIN.
    var repairing: String?
    /// The search for consoles gave nothing within the lookup time.
    @State private var searchTimedOut = false
    /// Bumped by "Try Again": restarts the search and its timer.
    @State private var searchAttempt = 0

    var body: some View {
        NavigationStack {
            List {
                Section {
                    if QRScannerView.isAvailable {
                        QRScannerView { link in app.requestPairing(link) }
                            .frame(height: 280)
                            .listRowInsets(EdgeInsets())
                            .accessibilityLabel(Text("QR code scanner"))
                    } else {
                        Text("The scanner is not available on this device. Scan the QR code with the Camera app, or use the PIN.")
                            .foregroundStyle(.secondary)
                    }
                } header: {
                    Text("Scan the QR Code")
                } footer: {
                    Text("Point the camera at the QR code shown by ReminisLink on the console.")
                }

                Section {
                    let devices = DiscoveryLogic.pinCandidates(
                        app.discovery.devices, pairedIds: Set(app.store.devices.map(\.id)), repairing: repairing
                    )
                    switch PINSearch.state(status: app.discovery.status, found: !devices.isEmpty, timedOut: searchTimedOut) {
                    case .denied:
                        ErrorView(error: .localNetworkDenied)
                    case .searching:
                        HStack(spacing: 12) {
                            ProgressView()
                            Text("Looking for consoles…")
                                .foregroundStyle(.secondary)
                        }
                    case .notFound:
                        ErrorView(error: .consoleNotFound, retry: {
                            app.discovery.restart()
                            searchAttempt += 1
                        })
                    case .found:
                        EmptyView()
                    }
                    ForEach(devices) { device in
                        NavigationLink {
                            PINEntryView(device: device)
                        } label: {
                            Label(device.name, systemImage: device.model.symbolName)
                        }
                        .accessibilityIdentifier("nearby.\(device.id)")
                    }
                } header: {
                    Text("Use the PIN Instead")
                } footer: {
                    Label("Less secure than the QR code. Avoid on public Wi-Fi.", systemImage: "exclamationmark.shield")
                }

                #if DEBUG
                if let link = app.demoPairingLink {
                    Section {
                        Button("Pair with the Demo Console") { app.requestPairing(link) }
                            .accessibilityIdentifier("demo.pair")
                    } footer: {
                        Text("Demo mode: no real console is used.")
                    }
                }
                #endif
            }
            .task(id: searchAttempt) {
                // docs/specs/06-app-ios.md §5: "Console not found" after 8 s.
                searchTimedOut = false
                try? await Task.sleep(for: app.lookupTimeout)
                if !Task.isCancelled { searchTimedOut = true }
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
}

/// What the PIN section shows while consoles are looked for.
enum PINSearch: Equatable {
    case denied
    case searching
    /// Nothing seen within the lookup time: "Console not found" and Try Again.
    case notFound
    case found

    static func state(status: DiscoveryStatus, found: Bool, timedOut: Bool) -> PINSearch {
        if status == .localNetworkDenied { return .denied }
        if found { return .found }
        return timedOut ? .notFound : .searching
    }
}

/// The fallback PIN, with its warning.
struct PINEntryView: View {
    @Environment(AppModel.self) private var app
    let device: DiscoveredDevice
    @State private var pin = ""
    @FocusState private var focused: Bool

    private var isValid: Bool { PairingSecret.pin(pin).isWellFormed }

    var body: some View {
        Form {
            Section {
                TextField("6-digit PIN", text: $pin)
                    .keyboardType(.numberPad)
                    .textContentType(.oneTimeCode)
                    .font(.title2.monospacedDigit())
                    .focused($focused)
                    .onChange(of: pin) { _, value in
                        let digits = String(value.filter(\.isASCII).filter(\.isNumber).prefix(PairingSecret.pinLength))
                        if digits != value { pin = digits }
                    }
                    .accessibilityIdentifier("pin.field")
            } header: {
                Text("PIN shown on \(device.name)")
            } footer: {
                Label("Less secure than the QR code. Avoid on public Wi-Fi.", systemImage: "exclamationmark.shield")
                    .foregroundStyle(.orange)
            }
            Section {
                Button("Pair") { app.requestPairing(.pin(pin, device)) }
                    .disabled(!isValid)
                    .accessibilityIdentifier("pin.pair")
            }
        }
        .navigationTitle("PIN")
        .onAppear { focused = true }
    }
}

/// VisionKit scanner that recognizes `reminislink://pair` QR codes.
struct QRScannerView: UIViewControllerRepresentable {
    let onLink: (PairingLink) -> Void

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
        var onLink: (PairingLink) -> Void
        private var found = false

        init(onLink: @escaping (PairingLink) -> Void) {
            self.onLink = onLink
        }

        func dataScanner(_ scanner: DataScannerViewController, didAdd items: [RecognizedItem], allItems: [RecognizedItem]) {
            guard !found else { return }
            for item in items {
                if case .barcode(let barcode) = item, let payload = barcode.payloadStringValue,
                   let link = PairingLink(string: payload) {
                    found = true
                    scanner.stopScanning()
                    onLink(link)
                    return
                }
            }
        }
    }
}
