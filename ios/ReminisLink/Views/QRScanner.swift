@preconcurrency import AVFoundation
import ReminisLinkKit
import SwiftUI
import VisionKit

/// How this device scans the console's QR code (docs/specs/06-app-ios.md
/// §4.2, M15).
enum ScannerKind: Equatable {
    /// VisionKit's scanner (iPhone, iPad): the main path.
    case visionKit
    /// A camera read with AVFoundation and Vision: an iPad app on a Mac
    /// (VisionKit's scanner is not supported there), or VisionKit not
    /// available (camera access refused: this path says so).
    case camera
    /// No camera: import a picture, or pair from another Apple device.
    case none

    /// The first that works: VisionKit, then any camera, else none.
    static func choose(dataScannerSupported: Bool, dataScannerAvailable: Bool, hasCamera: Bool) -> ScannerKind {
        if dataScannerSupported && dataScannerAvailable { return .visionKit }
        return hasCamera ? .camera : .none
    }

    @MainActor
    static func current(environment: [String: String] = ProcessInfo.processInfo.environment) -> ScannerKind {
        #if DEBUG
        // UI tests: the simulator has no camera; force a path.
        switch environment["RL_UITEST_SCANNER"] {
        case "camera": return .camera
        case "none": return .none
        default: break
        }
        #endif
        return choose(
            dataScannerSupported: DataScannerViewController.isSupported,
            dataScannerAvailable: DataScannerViewController.isAvailable,
            hasCamera: CameraQRScanner.preferredCamera() != nil
        )
    }
}

/// VisionKit scanner that recognizes `reminislink://pair` QR codes. A
/// pairing link this app cannot use (a pak older than M13...) is reported
/// once, and scanning goes on.
struct QRScannerView: UIViewControllerRepresentable {
    let onLink: (Result<PairingLink, PairingLinkError>) -> Void

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
        private var filter = PairingScanFilter()

        init(onLink: @escaping (Result<PairingLink, PairingLinkError>) -> Void) {
            self.onLink = onLink
        }

        func dataScanner(_ scanner: DataScannerViewController, didAdd items: [RecognizedItem], allItems: [RecognizedItem]) {
            let payloads = items.compactMap { item -> String? in
                guard case .barcode(let barcode) = item else { return nil }
                return barcode.payloadStringValue
            }
            guard let result = filter.evaluate(payloads) else { return }
            if case .success = result { scanner.stopScanning() }
            onLink(result)
        }
    }
}

/// The camera fallback (M15): an AVFoundation capture session whose frames
/// go to Vision (``QRCodeReader``), then through the same filter and link
/// check as VisionKit's scanner. The front camera of a Mac (FaceTime HD or
/// Continuity Camera), the back camera of an iPad or iPhone.
struct CameraQRScannerView: UIViewControllerRepresentable {
    let onLink: (Result<PairingLink, PairingLinkError>) -> Void
    /// Camera access refused or restricted, or no camera could start.
    let onUnavailable: (CameraQRScanner.Failure) -> Void

    func makeUIViewController(context: Context) -> CameraQRScanner {
        let scanner = CameraQRScanner()
        scanner.onLink = onLink
        scanner.onUnavailable = onUnavailable
        return scanner
    }

    func updateUIViewController(_ scanner: CameraQRScanner, context: Context) {
        scanner.onLink = onLink
        scanner.onUnavailable = onUnavailable
    }

    static func dismantleUIViewController(_ scanner: CameraQRScanner, coordinator: ()) {
        scanner.stop()
    }
}

final class CameraQRScanner: UIViewController {
    enum Failure: Equatable {
        case denied
        case noCamera
    }

    var onLink: ((Result<PairingLink, PairingLinkError>) -> Void)?
    var onUnavailable: ((Failure) -> Void)?

    private let session = AVCaptureSession()
    private let queue = DispatchQueue(label: "link.reminis.qr-camera")
    private let reader = FrameReader()
    private var preview: AVCaptureVideoPreviewLayer?
    private var stopped = false

    /// The camera to use: a Mac's camera (front, or Continuity Camera),
    /// else the back camera, else any.
    static func preferredCamera() -> AVCaptureDevice? {
        if ProcessInfo.processInfo.isiOSAppOnMac {
            return AVCaptureDevice.default(for: .video)
        }
        return AVCaptureDevice.default(.builtInWideAngleCamera, for: .video, position: .back)
            ?? AVCaptureDevice.default(for: .video)
    }

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = .black
        reader.onPayloads = { [weak self] payloads in
            Task { @MainActor in self?.handle(payloads) }
        }
        switch AVCaptureDevice.authorizationStatus(for: .video) {
        case .authorized:
            configure()
        case .notDetermined:
            AVCaptureDevice.requestAccess(for: .video) { [weak self] granted in
                Task { @MainActor in
                    if granted { self?.configure() } else { self?.onUnavailable?(.denied) }
                }
            }
        default:
            onUnavailable?(.denied)
        }
    }

    override func viewDidLayoutSubviews() {
        super.viewDidLayoutSubviews()
        preview?.frame = view.bounds
    }

    func stop() {
        stopped = true
        let session = session
        queue.async { if session.isRunning { session.stopRunning() } }
    }

    private func configure() {
        guard !stopped else { return }
        guard let camera = Self.preferredCamera(), let input = try? AVCaptureDeviceInput(device: camera),
              session.canAddInput(input)
        else { return onUnavailable?(.noCamera) ?? () }
        let output = AVCaptureVideoDataOutput()
        output.alwaysDiscardsLateVideoFrames = true
        output.setSampleBufferDelegate(reader, queue: queue)
        guard session.canAddOutput(output) else { return onUnavailable?(.noCamera) ?? () }
        session.beginConfiguration()
        session.sessionPreset = .high
        session.addInput(input)
        session.addOutput(output)
        session.commitConfiguration()

        let preview = AVCaptureVideoPreviewLayer(session: session)
        preview.videoGravity = .resizeAspectFill
        preview.frame = view.bounds
        view.layer.addSublayer(preview)
        self.preview = preview
        let session = session
        queue.async { session.startRunning() }
    }

    private func handle(_ payloads: [String]) {
        guard !stopped, let result = reader.filter(payloads) else { return }
        if case .success = result { stop() }
        onLink?(result)
    }

    /// Reads the frames on the capture queue, a few per second.
    private final class FrameReader: NSObject, AVCaptureVideoDataOutputSampleBufferDelegate, @unchecked Sendable {
        var onPayloads: (@Sendable ([String]) -> Void)?
        private var lastRead = Date.distantPast
        private let lock = NSLock()
        private var scanFilter = PairingScanFilter()

        func filter(_ payloads: [String]) -> Result<PairingLink, PairingLinkError>? {
            lock.withLock { scanFilter.evaluate(payloads) }
        }

        func captureOutput(_ output: AVCaptureOutput, didOutput sampleBuffer: CMSampleBuffer, from connection: AVCaptureConnection) {
            let now = Date()
            guard now.timeIntervalSince(lastRead) >= 0.25, let buffer = CMSampleBufferGetImageBuffer(sampleBuffer) else { return }
            lastRead = now
            // QR codes read in any orientation: no need to follow the
            // device's.
            guard let payloads = try? QRCodeReader.payloads(in: buffer), !payloads.isEmpty else { return }
            onPayloads?(payloads)
        }
    }
}
