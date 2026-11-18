import Foundation
import Observation
import ReminisLinkKit

/// Where the pairing secret comes from.
enum PairingSource {
    /// The QR code link (scanned in the app, or opened from the Camera app).
    case link(PairingLink)
    /// The fallback PIN typed for a console found with Bonjour.
    case pin(String, DiscoveredDevice)

    var deviceId: String {
        switch self {
        case .link(let link): link.deviceId
        case .pin(_, let device): device.id
        }
    }
}

/// One pairing, step by step: searching for the console, pairing, done.
@MainActor
@Observable
final class PairingFlow: Identifiable {
    enum Step: Int, Comparable {
        case searching
        case pairing
        case done

        static func < (a: Step, b: Step) -> Bool { a.rawValue < b.rawValue }
    }

    let id = UUID()
    let source: PairingSource
    private(set) var step: Step = .searching
    private(set) var error: AppError?
    private(set) var isRunning = false
    private(set) var paired: PairedDevice?
    /// The display name, editable once paired.
    var name = ""

    @ObservationIgnored private unowned let app: AppModel
    @ObservationIgnored private var task: Task<Void, Never>?

    init(app: AppModel, source: PairingSource) {
        self.app = app
        self.source = source
    }

    /// Runs the pairing in a task that ``cancel()`` stops.
    func start() {
        guard !isRunning else { return }
        task = Task { await run() }
    }

    /// Prefer ``start()``: a pairing run directly cannot be cancelled.
    func run() async {
        guard !isRunning else { return }
        isRunning = true
        error = nil
        step = .searching
        defer { isRunning = false }
        do {
            let endpoint: Endpoint
            let secret: PairingSecret
            var suggestedName: String?
            switch source {
            case .link(let link):
                endpoint = try await app.endpoint(for: link)
                secret = .qr(link.secret)
                suggestedName = link.deviceName
            case .pin(let pin, let device):
                endpoint = try await EndpointResolver.resolve(device)
                secret = .pin(pin)
                suggestedName = device.name
            }
            step = .pairing
            let deviceId = source.deviceId
            let client = app.makeClient(endpoint: endpoint, expectedDeviceId: deviceId)
            let hello = try await client.hello()
            guard hello.pairingOpen else { throw AppError.pairingClosed }
            try Task.checkCancellation()
            let clientId = try app.store.installationClientId()
            let attempt = try PairingAttempt(secret: secret, deviceId: deviceId, clientId: clientId, clientName: app.clientName)
            let key = try await client.pair(attempt)
            // Cancelled while the pak answered: the key is dropped.
            try Task.checkCancellation()
            let device = try app.store.save(
                deviceId: deviceId, name: suggestedName ?? hello.deviceName, model: hello.model, clientId: clientId, key: key
            )
            app.rememberEndpoint(endpoint, for: deviceId)
            paired = device
            name = device.name
            step = .done
        } catch {
            let error = AppError(error)
            self.error = error.isCancellation ? nil : error
        }
    }

    /// Saves the name and opens the console.
    func finish() {
        guard let paired else {
            app.finishPairing(deviceId: nil)
            return
        }
        if name != paired.name { try? app.store.rename(paired.id, to: name) }
        app.finishPairing(deviceId: paired.id)
    }

    /// Stops a pairing in progress and closes the sheet.
    func cancel() {
        stop()
        app.finishPairing(deviceId: paired?.id)
    }

    /// Stops a pairing in progress (the sheet went away).
    func stop() {
        task?.cancel()
        task = nil
    }

    /// Waits for the current run (tests).
    func wait() async {
        await task?.value
    }
}
