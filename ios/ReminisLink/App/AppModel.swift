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
    #if DEBUG
    /// Pairing link of the fake pak in the demo mode.
    var demoPairingLink: PairingLink?
    #endif
    /// How long to look for a console with Bonjour.
    let lookupTimeout: Duration

    var path: [Route] = []
    var sheet: Sheet?
    var linkError: AppError?
    /// A pairing with a console that is already paired, waiting for the
    /// user to confirm that it should be paired again.
    var pendingRepair: PairingSource?
    /// A confirmation to ask once the current sheet is dismissed.
    @ObservationIgnored private var queuedRepair: PairingSource?

    @ObservationIgnored private let makeSession: () -> URLSession
    @ObservationIgnored private var sessions: [String: ConsoleSession] = [:]

    init(
        store: DeviceStore, discovery: Discovery, artCache: ArtCache,
        lookupTimeout: Duration = Discovery.lookupTimeout,
        makeSession: @escaping () -> URLSession
    ) {
        self.store = store
        self.discovery = discovery
        self.artCache = artCache
        self.lookupTimeout = lookupTimeout
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

    func requestPairing(_ link: PairingLink) {
        requestPairing(.link(link))
    }

    /// Pairs, after a confirmation if the console is already paired (its
    /// key would be replaced). The confirmation waits for the current sheet
    /// to be dismissed.
    func requestPairing(_ source: PairingSource) {
        guard store.device(id: source.deviceId) != nil else {
            startPairing(source)
            return
        }
        if sheet != nil {
            queuedRepair = source
            sheet = nil
        } else {
            pendingRepair = source
        }
    }

    /// Called when a sheet is dismissed.
    func sheetDismissed() {
        if let source = queuedRepair {
            queuedRepair = nil
            pendingRepair = source
        }
    }

    func confirmRepair() {
        guard let source = pendingRepair else { return }
        pendingRepair = nil
        startPairing(source)
    }

    /// Starts a pairing now, stopping any previous one: no pairing runs
    /// without its screen.
    @discardableResult
    func startPairing(_ source: PairingSource) -> PairingFlow {
        if case .pairing(let previous) = sheet { previous.stop() }
        pendingRepair = nil
        queuedRepair = nil
        let flow = PairingFlow(app: self, source: source)
        sheet = .pairing(flow)
        flow.start()
        return flow
    }

    /// A new key was stored: connections signed with the old one are dropped.
    func didReplaceKey(for deviceId: String) {
        sessions[deviceId] = nil
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

    /// Finds a console with Bonjour by its id, for a paired console as for a
    /// pairing link: the `h`/`p` of a QR code are never used
    /// (docs/specs/03-protocole.md §3.1).
    func endpoint(for deviceId: String) async throws -> Endpoint {
        let discovery = discovery
        return try await DeviceLocator.locate(
            timeout: lookupTimeout,
            find: { await discovery.waitForDevice(id: deviceId, timeout: $0) },
            resolve: { try await EndpointResolver.resolve($0) },
            isLocalNetworkDenied: { discovery.status == .localNetworkDenied }
        )
    }

    /// Forgets a device on this iPhone (after unpairing).
    func forget(deviceId: String) throws {
        sessions[deviceId] = nil
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
        #if DEBUG
        if environment["RL_RESET_STORE"] == "1" {
            for device in store.devices { try? store.remove(device.id) }
        }
        #endif
        let app = AppModel(
            store: store, discovery: Discovery(), artCache: ArtCache(directory: ArtCache.defaultDirectory()),
            makeSession: { APIClient.makeSession() }
        )
        return app
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
        let app = AppModel(
            store: DeviceStore(secureStore: InMemorySecureStore(), defaults: UserDefaults(suiteName: suite)!),
            discovery: Discovery(fixedDevices: [pak.discoveredDevice]),
            artCache: ArtCache(directory: artDirectory),
            makeSession: { APIClient.makeSession(protocolClasses: [FakeURLProtocol.self]) }
        )
        app.demoPairingLink = pak.pairingLink
        return app
    }
    #endif
}
