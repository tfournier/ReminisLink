import Foundation
import Observation
#if DEBUG
import ReminisLinkDemo
#endif
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
    /// Pairing link of the fake pak in the demo mode (Debug builds only).
    let demoPairingLink: PairingLink?
    /// How long to look for a console with Bonjour.
    let lookupTimeout: Duration
    /// How long to wait for Bonjour before using the QR code endpoint.
    let fallbackDelay: Duration

    var path: [Route] = []
    var sheet: Sheet?
    var linkError: AppError?
    /// A link for a console that is already paired, waiting for the user
    /// to confirm that it should be paired again.
    var pendingRepair: PairingLink?

    @ObservationIgnored private let makeSession: () -> URLSession
    @ObservationIgnored private var sessions: [String: ConsoleSession] = [:]
    /// Endpoints learned from a QR code during this run, used when Bonjour
    /// fails. Never persisted (docs/specs/03-protocole.md §3.1).
    @ObservationIgnored private var sessionEndpoints: [String: Endpoint] = [:]

    init(
        store: DeviceStore, discovery: Discovery, artCache: ArtCache, demoPairingLink: PairingLink? = nil,
        lookupTimeout: Duration = Discovery.lookupTimeout, fallbackDelay: Duration = .seconds(3),
        makeSession: @escaping () -> URLSession
    ) {
        self.store = store
        self.discovery = discovery
        self.artCache = artCache
        self.demoPairingLink = demoPairingLink
        self.lookupTimeout = lookupTimeout
        self.fallbackDelay = fallbackDelay
        self.makeSession = makeSession
    }

    static var appVersion: String {
        Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "1.0.0"
    }

    /// The name sent at pairing: 1 to 64 Unicode scalars.
    var clientName: String { Pairing.clientName(from: UIDevice.current.name) }

    // MARK: - Pairing

    func handle(url: URL) {
        guard url.scheme?.lowercased() == PairingLink.scheme else { return }
        guard let link = PairingLink(url: url) else {
            linkError = .invalidPairingLink
            return
        }
        requestPairing(link)
    }

    /// Pairs with a link, after a confirmation if the console is already
    /// paired (its key would be replaced).
    func requestPairing(_ link: PairingLink) {
        if store.device(id: link.deviceId) != nil {
            sheet = nil
            pendingRepair = link
        } else {
            startPairing(.link(link))
        }
    }

    func confirmRepair() {
        guard let link = pendingRepair else { return }
        pendingRepair = nil
        startPairing(.link(link))
    }

    @discardableResult
    func startPairing(_ source: PairingSource) -> PairingFlow {
        let flow = PairingFlow(app: self, source: source)
        sheet = .pairing(flow)
        flow.start()
        return flow
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
        try await locate(deviceId: deviceId, fallback: sessionEndpoints[deviceId])
    }

    /// Endpoint for a pairing link: Bonjour by id, else `h`/`p` of the link,
    /// but never for a console that is already paired.
    func endpoint(for link: PairingLink) async throws -> Endpoint {
        let fallback = DeviceLocator.pairingFallback(for: link, pairedIds: Set(store.devices.map(\.id)))
        return try await locate(deviceId: link.deviceId, fallback: fallback)
    }

    private func locate(deviceId: String, fallback: Endpoint?) async throws -> Endpoint {
        let discovery = discovery
        return try await DeviceLocator.locate(
            fallback: fallback, timeout: lookupTimeout, fallbackDelay: fallbackDelay,
            find: { await discovery.waitForDevice(id: deviceId, timeout: $0) },
            resolve: { try await EndpointResolver.resolve($0) },
            isLocalNetworkDenied: { discovery.status == .localNetworkDenied }
        )
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
    /// The live app, or (Debug builds only) the demo mode with `RL_DEMO=1`
    /// (environment) or `-RLDemo YES` (launch argument).
    static func make(processInfo: ProcessInfo = .processInfo) -> AppModel {
        let environment = processInfo.environment
        #if DEBUG
        if environment["RL_DEMO"] == "1" || UserDefaults.standard.bool(forKey: "RLDemo") {
            return makeDemo()
        }
        #endif
        let group = Bundle.main.object(forInfoDictionaryKey: "RLAppGroup") as? String
        let defaults = group.flatMap { UserDefaults(suiteName: $0) } ?? .standard
        let store = DeviceStore(secureStore: KeychainStore(service: "ReminisLink.devices"), defaults: defaults)
        if environment["RL_RESET_STORE"] == "1" {
            for device in store.devices { try? store.remove(device.id) }
        }
        return AppModel(
            store: store, discovery: Discovery(), artCache: ArtCache(directory: ArtCache.defaultDirectory()),
            makeSession: { APIClient.makeSession() }
        )
    }

    #if DEBUG
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
            demoPairingLink: pak.pairingLink,
            makeSession: { APIClient.makeSession(protocolClasses: [FakeURLProtocol.self]) }
        )
    }
    #endif
}
