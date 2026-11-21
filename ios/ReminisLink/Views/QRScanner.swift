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
    /// The scanner could not start, or stopped being available.
    let onUnavailable: () -> Void

    func makeUIViewController(context: Context) -> DataScannerViewController {
        let scanner = DataScannerViewController(
            recognizedDataTypes: [.barcode(symbologies: [.qr])],
            qualityLevel: .balanced,
            recognizesMultipleItems: false,
            isHighFrameRateTrackingEnabled: false,
            isHighlightingEnabled: true
        )
        scanner.delegate = context.coordinator
        do {
            try scanner.startScanning()
        } catch {
            // Not available any more (camera access withdrawn...): said
            // after this update, the camera path takes over.
            let onUnavailable = onUnavailable
            Task { @MainActor in onUnavailable() }
        }
        return scanner
    }

    func updateUIViewController(_ scanner: DataScannerViewController, context: Context) {
        context.coordinator.onLink = onLink
        context.coordinator.onUnavailable = onUnavailable
    }

    static func dismantleUIViewController(_ scanner: DataScannerViewController, coordinator: Coordinator) {
        scanner.stopScanning()
    }

    func makeCoordinator() -> Coordinator { Coordinator(onLink: onLink, onUnavailable: onUnavailable) }

    final class Coordinator: NSObject, DataScannerViewControllerDelegate {
        var onLink: (Result<PairingLink, PairingLinkError>) -> Void
        var onUnavailable: () -> Void
        private var filter = PairingScanFilter()

        init(onLink: @escaping (Result<PairingLink, PairingLinkError>) -> Void, onUnavailable: @escaping () -> Void) {
            self.onLink = onLink
            self.onUnavailable = onUnavailable
        }

        func dataScanner(_ dataScanner: DataScannerViewController, becameUnavailableWithError error: DataScannerViewController.ScanningUnavailable) {
            onUnavailable()
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

    /// The capture session, touched only on ``queue`` (configuration,
    /// start, stop), so a camera change, a restart and a stop never overlap.
    private let capture = Capture()
    private let queue = DispatchQueue(label: "link.reminis.qr-camera")
    private let reader = FrameReader()
    private var preview: AVCaptureVideoPreviewLayer?
    private var stopped = false
    private var observers: [NSObjectProtocol] = []
    /// Restarts after a runtime error, before giving up.
    private var restarts = 0

    private nonisolated final class Capture: @unchecked Sendable {
        let session = AVCaptureSession()
    }

    /// The camera to use: a Mac's camera (front, or Continuity Camera),
    /// else the back camera, else any.
    nonisolated static func preferredCamera() -> AVCaptureDevice? {
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
        for observer in observers { NotificationCenter.default.removeObserver(observer) }
        observers = []
        let capture = capture
        queue.async { if capture.session.isRunning { capture.session.stopRunning() } }
    }

    /// No camera could be used: stop and say so (on the main actor).
    private func unavailable() {
        guard !stopped else { return }
        stop()
        onUnavailable?(.noCamera)
    }

    /// A capture that fails (runtime error) is started again, twice at
    /// most; a camera that goes away (Continuity Camera, an external
    /// camera) is replaced by another, or the screen says there is none.
    /// An interruption (another app took the camera) ends by itself: the
    /// session resumes.
    private func observeCapture() {
        let center = NotificationCenter.default
        observers.append(center.addObserver(forName: AVCaptureSession.runtimeErrorNotification, object: capture.session, queue: .main) {
            [weak self] _ in MainActor.assumeIsolated { self?.captureFailed() }
        })
        observers.append(center.addObserver(forName: AVCaptureDevice.wasDisconnectedNotification, object: nil, queue: .main) {
            [weak self] _ in MainActor.assumeIsolated { self?.cameraChanged() }
        })
    }

    private func captureFailed() {
        guard !stopped else { return }
        restarts += 1
        guard restarts <= 2 else { return unavailable() }
        let capture = capture
        queue.async { if !capture.session.isRunning { capture.session.startRunning() } }
    }

    private func cameraChanged() {
        guard !stopped else { return }
        let capture = capture
        queue.async { [weak self] in
            let session = capture.session
            let inputs = session.inputs.compactMap { $0 as? AVCaptureDeviceInput }
            guard inputs.isEmpty || inputs.contains(where: { !$0.device.isConnected }) else { return }
            session.beginConfiguration()
            for input in inputs { session.removeInput(input) }
            let replaced: Bool
            if let camera = Self.preferredCamera(), let input = try? AVCaptureDeviceInput(device: camera),
               session.canAddInput(input) {
                session.addInput(input)
                replaced = true
            } else {
                replaced = false
            }
            session.commitConfiguration()
            if replaced, !session.isRunning { session.startRunning() }
            if !replaced { Task { @MainActor in self?.unavailable() } }
        }
    }

    private func configure() {
        guard !stopped else { return }
        let preview = AVCaptureVideoPreviewLayer(session: capture.session)
        preview.videoGravity = .resizeAspectFill
        preview.frame = view.bounds
        view.layer.addSublayer(preview)
        self.preview = preview
        observeCapture()
        let capture = capture
        let reader = reader
        let queue = queue
        queue.async { [weak self] in
            let session = capture.session
            guard let camera = Self.preferredCamera(), let input = try? AVCaptureDeviceInput(device: camera),
                  session.canAddInput(input)
            else {
                Task { @MainActor in self?.unavailable() }
                return
            }
            let output = AVCaptureVideoDataOutput()
            output.alwaysDiscardsLateVideoFrames = true
            output.setSampleBufferDelegate(reader, queue: queue)
            session.beginConfiguration()
            session.sessionPreset = .high
            session.addInput(input)
            guard session.canAddOutput(output) else {
                session.commitConfiguration()
                Task { @MainActor in self?.unavailable() }
                return
            }
            session.addOutput(output)
            session.commitConfiguration()
            session.startRunning()
        }
    }

    private func handle(_ payloads: [String]) {
        guard !stopped, let result = reader.filter(payloads) else { return }
        if case .success = result { stop() }
        onLink?(result)
    }

    /// Reads the frames on the capture queue, a few per second.
    private nonisolated final class FrameReader: NSObject, AVCaptureVideoDataOutputSampleBufferDelegate, @unchecked Sendable {
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
