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
    /// The screen went away (or a new run has not started yet).
    @ObservationIgnored private(set) var isStopped = false

    init(app: AppModel, source: PairingSource) {
        self.app = app
        self.source = source
    }

    /// Runs the pairing in a task that ``cancel()`` stops. Does nothing if
    /// a run is already in progress.
    func start() {
        guard task == nil else { return }
        isStopped = false
        isRunning = true
        error = nil
        step = .searching
        task = Task {
            await execute()
            task = nil
        }
    }

    /// Runs the pairing and waits for its end.
    func run() async {
        start()
        await wait()
    }

    private func execute() async {
        defer { isRunning = false }
        do {
            let endpoint: Endpoint
            let secret: PairingSecret
            var suggestedName: String?
            switch source {
            case .link(let link):
                endpoint = try await app.endpoint(for: link.deviceId)
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
            let clientId = try app.store.installationClientId()
            let attempt = try PairingAttempt(secret: secret, deviceId: deviceId, clientId: clientId, clientName: app.clientName)
            // Last point where cancelling changes anything.
            try Task.checkCancellation()
            // Once /pair is sent, the pak may already hold the new key (and
            // have dropped the previous one): the exchange runs to its end in
            // its own task, not cancelled with the screen, and the key is
            // stored as soon as the device proof is valid.
            //
            // Exchanges with the same console run one after the other, each
            // up to the Keychain write: a slow first exchange must never
            // store its key after a newer one (the pak keeps the newer).
            let app = app
            let name = suggestedName ?? hello.deviceName
            let exchange = app.pairExchange(deviceId: deviceId) { [weak self] in
                // Closed while an earlier exchange was finishing: /pair was
                // not sent yet, so nothing changes.
                if self?.isStopped ?? true { throw CancellationError() }
                let key = try await client.pair(attempt)
                let device = try app.store.save(
                    deviceId: deviceId, name: name, model: hello.model, clientId: clientId, key: key,
                    consoleName: hello.deviceName
                )
                app.didReplaceKey(for: deviceId)
                return device
            }
            let device: PairedDevice
            do {
                device = try await exchange.value
            } catch APIError.server(let server) where server.code == .pairingFailed {
                // Refused because the pairing screen was closed on the
                // console in the meantime (it only opens on demand)?
                if let again = try? await client.hello(), !again.pairingOpen { throw AppError.pairingClosed }
                throw APIError.server(server)
            }
            paired = device
            self.name = device.name
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
        isStopped = true
        task?.cancel()
    }

    /// Waits for the current run, including a `/pair` exchange that goes
    /// on after a cancellation.
    func wait() async {
        await task?.value
    }
}
