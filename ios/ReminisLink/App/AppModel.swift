import Foundation
import Observation
import ReminisLinkDemo
import ReminisLinkKit
import UIKit

/// Navigation destinations.
enum Route: Hashable {
    case console(String)
    case library(String)
    case folder(FolderRoute)
    case game(GameRoute)
    case trash(String)
}

struct FolderRoute: Hashable {
    let deviceId: String
    let systemId: String
    let systemLabel: String
    let folderId: String?
    let title: String
}

struct GameRoute: Hashable {
    let deviceId: String
    let gameId: String
    let systemLabel: String
    let name: String
    let hasArt: Bool
}

/// The sheet shown above the navigation.
enum Sheet: Identifiable {
    case addConsole
    case pairing(PairingFlow)

    var id: String {
        switch self {
        case .addConsole: "add"
        case .pairing(let flow): "pairing-\(flow.id)"
        }
    }
}

/// State shared by every screen.
@MainActor
@Observable
final class AppModel {
    let store: DeviceStore
    let discovery: Discovery
    let artCache: ArtCache
    /// The fake pak of the demo mode, nil otherwise.
    let demoPak: FakePak?

    var path: [Route] = []
    var sheet: Sheet?
    var linkError: AppError?

    @ObservationIgnored private let makeSession: () -> URLSession
    @ObservationIgnored private var sessions: [String: ConsoleSession] = [:]
    /// Endpoints learned from a QR code during this run, used when Bonjour
    /// fails. Never persisted (docs/specs/03-protocole.md §3.1).
    @ObservationIgnored private var sessionEndpoints: [String: Endpoint] = [:]

    init(store: DeviceStore, discovery: Discovery, artCache: ArtCache, demoPak: FakePak?, makeSession: @escaping () -> URLSession) {
        self.store = store
        self.discovery = discovery
        self.artCache = artCache
        self.demoPak = demoPak
        self.makeSession = makeSession
    }

    var isDemo: Bool { demoPak != nil }

    /// Wait for Bonjour before using the QR code endpoint.
    static let fallbackDelay: Duration = .seconds(3)

    static var appVersion: String {
        Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "1.0.0"
    }

    /// The name sent at pairing: 1 to 64 characters.
    var clientName: String {
        let name = UIDevice.current.name.trimmingCharacters(in: .whitespacesAndNewlines)
        return name.isEmpty ? "iPhone" : String(name.prefix(DeviceStore.maxNameLength))
    }

    // MARK: - Pairing

    func handle(url: URL) {
        guard url.scheme?.lowercased() == PairingLink.scheme else { return }
        guard let link = PairingLink(url: url) else {
            linkError = .invalidPairingLink
            return
        }
        startPairing(.link(link))
    }

    func startPairing(_ source: PairingSource) {
        let flow = PairingFlow(app: self, source: source)
        sheet = .pairing(flow)
        Task { await flow.run() }
    }

    func finishPairing(deviceId: String?) {
        sheet = nil
        if let deviceId {
            sessions[deviceId] = nil
            path = [.console(deviceId)]
        }
    }

    // MARK: - Connections

    func makeClient(endpoint: Endpoint, expectedDeviceId: String, credentials: DeviceCredentials? = nil) -> APIClient {
        APIClient(
            endpoint: endpoint, expectedDeviceId: expectedDeviceId, credentials: credentials,
            appVersion: Self.appVersion, session: makeSession()
        )
    }

    func session(for deviceId: String) -> ConsoleSession {
        if let session = sessions[deviceId] { return session }
        let session = ConsoleSession(deviceId: deviceId, app: self)
        sessions[deviceId] = session
        return session
    }

    func rememberEndpoint(_ endpoint: Endpoint, for deviceId: String) {
        sessionEndpoints[deviceId] = endpoint
    }

    /// Finds a paired device: Bonjour by id first, then the endpoint of a QR
    /// code scanned during this run.
    func endpoint(for deviceId: String) async throws -> Endpoint {
        let wait = sessionEndpoints[deviceId] == nil ? Discovery.lookupTimeout : Self.fallbackDelay
        if let device = await discovery.waitForDevice(id: deviceId, timeout: wait),
           let endpoint = try? await EndpointResolver.resolve(device) {
            return endpoint
        }
        if let endpoint = sessionEndpoints[deviceId] { return endpoint }
        throw discovery.status == .localNetworkDenied ? AppError.localNetworkDenied : AppError.consoleNotFound
    }

    /// Endpoint for a pairing link: Bonjour by id, else `h`/`p` of the link.
    func endpoint(for link: PairingLink) async throws -> Endpoint {
        let wait = link.fallbackEndpoint == nil ? Discovery.lookupTimeout : Self.fallbackDelay
        if let device = await discovery.waitForDevice(id: link.deviceId, timeout: wait),
           let endpoint = try? await EndpointResolver.resolve(device) {
            return endpoint
        }
        if let endpoint = link.fallbackEndpoint { return endpoint }
        throw discovery.status == .localNetworkDenied ? AppError.localNetworkDenied : AppError.consoleNotFound
    }

    /// Forgets a device on this iPhone (after unpairing).
    func forget(deviceId: String) throws {
        sessions[deviceId] = nil
        sessionEndpoints[deviceId] = nil
        try store.remove(deviceId)
        path.removeAll()
    }
}

// MARK: - Construction

extension AppModel {
    /// The live app, or the demo mode with `RL_DEMO=1` (environment) or
    /// `-RLDemo YES` (launch argument).
    static func make(processInfo: ProcessInfo = .processInfo) -> AppModel {
        let environment = processInfo.environment
        if environment["RL_DEMO"] == "1" || UserDefaults.standard.bool(forKey: "RLDemo") {
            return makeDemo()
        }
        let group = Bundle.main.object(forInfoDictionaryKey: "RLAppGroup") as? String
        let defaults = group.flatMap { UserDefaults(suiteName: $0) } ?? .standard
        let store = DeviceStore(secureStore: KeychainStore(service: "ReminisLink.devices"), defaults: defaults)
        if environment["RL_RESET_STORE"] == "1" {
            for device in store.devices { try? store.remove(device.id) }
        }
        return AppModel(
            store: store, discovery: Discovery(), artCache: ArtCache(directory: ArtCache.defaultDirectory()),
            demoPak: nil, makeSession: { APIClient.makeSession() }
        )
    }

    /// Everything in memory, against the fake pak: no network, no Keychain.
    static func makeDemo() -> AppModel {
        let pak = FakePak()
        pak.register()
        let suite = "ReminisLink.demo"
        UserDefaults.standard.removePersistentDomain(forName: suite)
        let artDirectory = FileManager.default.temporaryDirectory.appending(path: "ReminisLinkDemoArt")
        try? FileManager.default.removeItem(at: artDirectory)
        return AppModel(
            store: DeviceStore(secureStore: InMemorySecureStore(), defaults: UserDefaults(suiteName: suite)!),
            discovery: Discovery(fixedDevices: [pak.discoveredDevice]),
            artCache: ArtCache(directory: artDirectory),
            demoPak: pak,
            makeSession: { APIClient.makeSession(protocolClasses: [FakeURLProtocol.self]) }
        )
    }
}
