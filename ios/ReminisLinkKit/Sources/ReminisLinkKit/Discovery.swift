import Foundation
import Network
import Observation

/// A Bonjour service instance.
public struct BonjourService: Hashable, Sendable {
    public let name: String
    public let type: String
    public let domain: String

    public init(name: String, type: String = Discovery.serviceType, domain: String = "local.") {
        self.name = name
        self.type = type
        self.domain = domain
    }
}

/// A pak seen on the network (docs/specs/03-protocole.md §2).
public struct DiscoveredDevice: Identifiable, Hashable, Sendable {
    /// The stable device id from the TXT record: the only way to recognize
    /// a paired device (never its name nor its address).
    public let id: String
    /// The Bonjour instance name (the device name).
    public let name: String
    public let model: DeviceModel
    public let platform: String?
    public let os: String?
    public let osVersion: String?
    public let pakVersion: String?
    /// Major API version (`api`), nil if absent or unreadable.
    public let apiVersion: Int?
    public let service: BonjourService
    /// Set by the demo mode, which has no Bonjour service to resolve.
    public let fixedEndpoint: Endpoint?

    public init(
        id: String, name: String, model: DeviceModel, platform: String? = nil, os: String? = nil,
        osVersion: String? = nil, pakVersion: String? = nil, apiVersion: Int? = 1,
        service: BonjourService, fixedEndpoint: Endpoint? = nil
    ) {
        self.id = id
        self.name = name
        self.model = model
        self.platform = platform
        self.os = os
        self.osVersion = osVersion
        self.pakVersion = pakVersion
        self.apiVersion = apiVersion
        self.service = service
        self.fixedEndpoint = fixedEndpoint
    }

    /// Whether this app speaks the device's API version.
    public var isCompatible: Bool { apiVersion == 1 }
}

/// What the browser is doing, as the screens need it.
public enum DiscoveryStatus: Equatable, Sendable {
    case idle
    case searching
    /// The user refused the local network access (`kDNSServiceErr_PolicyDenied`).
    case localNetworkDenied
    case failed(String)
}

/// Pure logic of the discovery, kept apart from `NWBrowser` to be tested.
public enum DiscoveryLogic {
    /// `kDNSServiceErr_PolicyDenied`: the local network permission is off.
    public static let policyDeniedError: DNSServiceErrorType = -65570

    /// Reads a TXT record. Nil when `id` is missing or is not a lowercase
    /// UUID: such a service cannot be recognized later.
    public static func device(service: BonjourService, txt: [String: String]) -> DiscoveredDevice? {
        guard let id = txt["id"], PairingLink.isLowercaseUUID(id) else { return nil }
        func value(_ key: String) -> String? {
            guard let v = txt[key]?.trimmingCharacters(in: .whitespaces), !v.isEmpty else { return nil }
            return v
        }
        return DiscoveredDevice(
            id: id,
            name: service.name,
            model: DeviceModel(rawValue: value("model") ?? ""),
            platform: value("platform"),
            os: value("os"),
            osVersion: value("osv"),
            pakVersion: value("cv"),
            apiVersion: value("api").flatMap { Int($0) },
            service: service
        )
    }

    /// One entry per device id (a device seen on several interfaces, or
    /// announced twice, appears once; the first one wins), sorted by name.
    public static func merge(_ devices: [DiscoveredDevice]) -> [DiscoveredDevice] {
        var seen = Set<String>()
        return devices
            .filter { seen.insert($0.id).inserted }
            .sorted { a, b in
                let order = a.name.localizedStandardCompare(b.name)
                return order == .orderedSame ? a.id < b.id : order == .orderedAscending
            }
    }

    /// The device with this id, if seen.
    public static func find(id: String, in devices: [DiscoveredDevice]) -> DiscoveredDevice? {
        devices.first { $0.id == id }
    }

    /// Devices that are not paired yet (for the PIN fallback).
    public static func unpaired(_ devices: [DiscoveredDevice], pairedIds: Set<String>) -> [DiscoveredDevice] {
        devices.filter { !pairedIds.contains($0.id) }
    }

    /// Delay before restarting a failed browser: 1 s, 2 s, 4 s... at most 30 s.
    public static func restartDelay(afterFailures failures: Int) -> Duration {
        .seconds(min(30, 1 << min(failures, 5)))
    }

    public static func status(for state: NWBrowser.State) -> DiscoveryStatus {
        switch state {
        case .setup: .idle
        case .ready: .searching
        case .cancelled: .idle
        case .waiting(let error), .failed(let error):
            if case .dns(let code) = error, code == policyDeniedError {
                .localNetworkDenied
            } else if case .waiting = state {
                .searching
            } else {
                .failed(String(describing: error))
            }
        @unknown default: .searching
        }
    }
}

/// Bonjour browser of `_reminislink._tcp` with TXT records.
@MainActor
@Observable
public final class Discovery {
    public nonisolated static let serviceType = "_reminislink._tcp"
    /// How long to look for a device before saying it cannot be found.
    public nonisolated static let lookupTimeout: Duration = .seconds(8)

    public private(set) var devices: [DiscoveredDevice] = []
    public private(set) var status: DiscoveryStatus = .idle

    @ObservationIgnored private var browser: NWBrowser?
    @ObservationIgnored private let fixedDevices: [DiscoveredDevice]?
    @ObservationIgnored private var failures = 0
    @ObservationIgnored private var restartTask: Task<Void, Never>?

    /// A real Bonjour browser.
    public init() {
        fixedDevices = nil
    }

    /// A browser that "sees" these devices, for previews and the demo mode.
    public init(fixedDevices: [DiscoveredDevice]) {
        self.fixedDevices = fixedDevices
    }

    public func start() {
        if let fixedDevices {
            devices = DiscoveryLogic.merge(fixedDevices)
            status = .searching
            return
        }
        guard browser == nil else { return }
        let parameters = NWParameters()
        parameters.includePeerToPeer = false
        let browser = NWBrowser(for: .bonjourWithTXTRecord(type: Self.serviceType, domain: nil), using: parameters)
        browser.stateUpdateHandler = { [weak self] state in
            let status = DiscoveryLogic.status(for: state)
            MainActor.assumeIsolated {
                self?.apply(status)
            }
        }
        browser.browseResultsChangedHandler = { [weak self] results, _ in
            let found = results.compactMap(Self.device(from:))
            MainActor.assumeIsolated {
                self?.devices = DiscoveryLogic.merge(found)
            }
        }
        self.browser = browser
        status = .searching
        browser.start(queue: .main)
    }

    /// After a failure, restarts the browser with a growing delay.
    private func apply(_ newStatus: DiscoveryStatus) {
        status = newStatus
        switch newStatus {
        case .failed:
            let delay = DiscoveryLogic.restartDelay(afterFailures: failures)
            failures += 1
            browser?.cancel()
            browser = nil
            restartTask?.cancel()
            restartTask = Task { [weak self] in
                try? await Task.sleep(for: delay)
                guard !Task.isCancelled else { return }
                self?.start()
            }
        case .searching:
            failures = 0
        default:
            break
        }
    }

    public func stop() {
        restartTask?.cancel()
        restartTask = nil
        browser?.cancel()
        browser = nil
        if fixedDevices == nil { devices = [] }
        status = .idle
    }

    /// Restarts the browser (after a failure, or on "Retry").
    public func restart() {
        stop()
        start()
    }

    /// Waits until the device with this id is seen, or the timeout expires.
    public func waitForDevice(id: String, timeout: Duration = Discovery.lookupTimeout) async -> DiscoveredDevice? {
        start()
        let deadline = ContinuousClock.now + timeout
        while true {
            if let device = DiscoveryLogic.find(id: id, in: devices) { return device }
            if status == .localNetworkDenied || ContinuousClock.now >= deadline || Task.isCancelled { return nil }
            try? await Task.sleep(for: .milliseconds(200))
        }
    }

    private nonisolated static func device(from result: NWBrowser.Result) -> DiscoveredDevice? {
        guard case .service(let name, let type, let domain, _) = result.endpoint,
              case .bonjour(let record) = result.metadata
        else { return nil }
        return DiscoveryLogic.device(service: BonjourService(name: name, type: type, domain: domain), txt: record.dictionary)
    }
}

/// Why a device could not be found.
public enum DeviceLookupError: Error, Equatable, Sendable {
    case notFound
    case localNetworkDenied
}

/// Finds the endpoint of a device: Bonjour by id first, then, only when one
/// is allowed, a fallback endpoint (the `h`/`p` of a QR code). A cancelled
/// lookup throws `CancellationError` and never falls back.
@MainActor
public enum DeviceLocator {
    public static func locate(
        fallback: Endpoint?,
        timeout: Duration,
        fallbackDelay: Duration,
        find: (Duration) async -> DiscoveredDevice?,
        resolve: (DiscoveredDevice) async throws -> Endpoint,
        isLocalNetworkDenied: () -> Bool
    ) async throws -> Endpoint {
        let device = await find(fallback == nil ? timeout : fallbackDelay)
        try Task.checkCancellation()
        if let device {
            do {
                return try await resolve(device)
            } catch is CancellationError {
                throw CancellationError()
            } catch {
                // Resolution failed: try the fallback below.
            }
        }
        try Task.checkCancellation()
        if let fallback { return fallback }
        throw isLocalNetworkDenied() ? DeviceLookupError.localNetworkDenied : DeviceLookupError.notFound
    }

    /// The fallback endpoint a pairing link may use: none for a device that
    /// is already paired, which must be found with Bonjour under its id (a
    /// link must not replace a key by talking to another address).
    public static func pairingFallback(for link: PairingLink, pairedIds: Set<String>) -> Endpoint? {
        pairedIds.contains(link.deviceId) ? nil : link.fallbackEndpoint
    }
}

public enum ResolveError: Error, Sendable, Equatable {
    case timeout
    case failed(String)
    case noAddress
}

/// Resolves a Bonjour service to an IPv4 address and a port, by opening
/// (then closing) a TCP connection to it.
public enum EndpointResolver {
    public static func resolve(_ device: DiscoveredDevice, timeout: Duration = .seconds(8)) async throws -> Endpoint {
        if let fixed = device.fixedEndpoint { return fixed }
        let service = device.service
        let parameters = NWParameters.tcp
        if let ip = parameters.defaultProtocolStack.internetProtocol as? NWProtocolIP.Options {
            ip.version = .v4
        }
        let connection = NWConnection(
            to: .service(name: service.name, type: service.type, domain: service.domain, interface: nil),
            using: parameters
        )
        let queue = DispatchQueue(label: "ReminisLink.resolve")
        let once = Once()
        return try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Endpoint, Error>) in
                let finish: @Sendable (Result<Endpoint, Error>) -> Void = { result in
                    guard once.claim() else { return }
                    connection.cancel()
                    continuation.resume(with: result)
                }
                connection.stateUpdateHandler = { state in
                    switch state {
                    case .ready:
                        if let endpoint = connection.currentPath?.remoteEndpoint.flatMap(endpoint(from:)) {
                            finish(.success(endpoint))
                        } else {
                            finish(.failure(ResolveError.noAddress))
                        }
                    case .failed(let error):
                        finish(.failure(ResolveError.failed(String(describing: error))))
                    case .cancelled:
                        finish(.failure(CancellationError()))
                    default:
                        break
                    }
                }
                connection.start(queue: queue)
                let seconds = Double(timeout.components.seconds) + Double(timeout.components.attoseconds) / 1e18
                queue.asyncAfter(deadline: .now() + seconds) { finish(.failure(ResolveError.timeout)) }
            }
        } onCancel: {
            connection.cancel()
        }
    }

    static func endpoint(from nw: NWEndpoint) -> Endpoint? {
        guard case .hostPort(let host, let port) = nw else { return nil }
        switch host {
        case .ipv4(let address):
            let b = [UInt8](address.rawValue)
            guard b.count == 4 else { return nil }
            return Endpoint(host: "\(b[0]).\(b[1]).\(b[2]).\(b[3])", port: port.rawValue)
        case .ipv6(let address):
            var text = "\(address)"
            if let percent = text.firstIndex(of: "%") { text = String(text[..<percent]) }
            if let interface = address.interface { text += "%" + interface.name }
            return Endpoint(host: text, port: port.rawValue)
        case .name(let name, _):
            return Endpoint(host: name, port: port.rawValue)
        @unknown default:
            return nil
        }
    }
}

/// A flag that can be claimed once, from any thread.
final class Once: @unchecked Sendable {
    private let lock = NSLock()
    private var claimed = false

    func claim() -> Bool {
        lock.lock()
        defer { lock.unlock() }
        if claimed { return false }
        claimed = true
        return true
    }
}
