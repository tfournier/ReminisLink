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
    case transfers(String)
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

/// What the Add screen opens with.
struct AddContext: Identifiable, Equatable {
    let id = UUID()
    /// The console to add to; nil lets the user choose.
    var deviceId: String?
    /// Files and links from the share extension.
    var request: ShareRequest?
}

/// The sheet shown above the navigation.
enum Sheet: Identifiable {
    case addConsole
    case pairing(PairingFlow)
    case add(AddContext)

    var id: String {
        switch self {
        case .addConsole: "add"
        case .pairing(let flow): "pairing-\(flow.id)"
        case .add(let context): "add-\(context.id)"
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
    /// Requests of the share extension (app group container).
    let shareInbox: ShareInbox
    /// Copies of the files being uploaded, and scratch chunks.
    let transferDirectory: URL
    @ObservationIgnored let background: BackgroundActivity
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
    /// A share request to open once the current sheet is dismissed.
    @ObservationIgnored private var queuedShare: ShareRequest?

    @ObservationIgnored private let makeSession: () -> URLSession
    @ObservationIgnored private var sessions: [String: ConsoleSession] = [:]

    init(
        store: DeviceStore, discovery: Discovery, artCache: ArtCache,
        shareInbox: ShareInbox? = nil, transferDirectory: URL? = nil,
        background: BackgroundActivity = NoBackgroundActivity(),
        lookupTimeout: Duration = Discovery.lookupTimeout,
        makeSession: @escaping () -> URLSession
    ) {
        self.store = store
        self.discovery = discovery
        self.artCache = artCache
        let temporary = FileManager.default.temporaryDirectory.appending(path: "ReminisLink-\(UUID().uuidString)")
        self.shareInbox = shareInbox ?? ShareInbox(root: temporary.appending(path: "ShareInbox"))
        self.transferDirectory = transferDirectory ?? temporary.appending(path: "Transfers")
        self.background = background
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
        if url.host()?.lowercased() == ShareInbox.urlHost {
            if let id = ShareInbox.requestId(from: url), let request = shareInbox.request(id: id) {
                openShare(request)
            } else {
                checkShareInbox()
            }
            return
        }
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
        } else if let request = queuedShare {
            queuedShare = nil
            openShare(request)
        } else {
            // A request shared while another sheet was up (e.g. pairing).
            Task { checkShareInbox() }
        }
    }

    // MARK: - Add and share

    /// Opens the Add screen for a console.
    func showAdd(deviceId: String) {
        sheet = .add(AddContext(deviceId: deviceId))
    }

    /// Opens the oldest share request, if any (the app became active).
    func checkShareInbox() {
        shareInbox.purge()
        guard let request = shareInbox.pending().first else { return }
        if case .add(let context) = sheet, context.request?.id == request.id { return }
        openShare(request)
    }

    /// Opens the Add screen prefilled with a share request, once the
    /// current sheet (pairing...) is gone.
    func openShare(_ request: ShareRequest) {
        guard !store.devices.isEmpty else { return }
        if case .add(let context) = sheet, context.request?.id == request.id { return }
        if sheet != nil {
            queuedShare = request
            return
        }
        let deviceId = store.devices.count == 1 ? store.devices.first?.id : nil
        sheet = .add(AddContext(deviceId: deviceId, request: request))
    }

    /// The share request was used or dismissed.
    func finishShare(_ request: ShareRequest) {
        shareInbox.remove(request.id)
    }

    /// The app goes to the background: running uploads stop after their
    /// current chunk (with a background task to finish it).
    func enterBackground() {
        for session in sessions.values { session.transfers.pauseAfterCurrentChunk() }
    }

    /// The app is active again: paused uploads resume.
    func becomeActive() {
        for session in sessions.values { session.transfers.resumePaused() }
        checkShareInbox()
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
            shareInbox: group.flatMap { ShareInbox.appGroup($0) } ?? ShareInbox(root: localInbox()),
            transferDirectory: cleanTransferDirectory(), background: UIKitBackgroundActivity(),
            makeSession: { APIClient.makeSession() }
        )
        #if DEBUG
        app.injectTestShare(environment)
        #endif
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
        let group = Bundle.main.object(forInfoDictionaryKey: "RLAppGroup") as? String
        let app = AppModel(
            store: DeviceStore(secureStore: InMemorySecureStore(), defaults: UserDefaults(suiteName: suite)!),
            discovery: Discovery(fixedDevices: [pak.discoveredDevice]),
            artCache: ArtCache(directory: artDirectory),
            shareInbox: group.flatMap { ShareInbox.appGroup($0) } ?? ShareInbox(root: localInbox()),
            transferDirectory: cleanTransferDirectory(), background: UIKitBackgroundActivity(),
            makeSession: { APIClient.makeSession(protocolClasses: [FakeURLProtocol.self]) }
        )
        app.demoPairingLink = pak.pairingLink
        app.injectTestShare(ProcessInfo.processInfo.environment)
        return app
    }

    /// UI tests: `RL_UITEST_SHARE_FILE=<name>:<bytes>` stores a share
    /// request holding a generated file (pseudo-random bytes, no game
    /// content), as the share extension would. The document picker cannot
    /// be driven by XCUITest; this path covers the rest of "add a file".
    func injectTestShare(_ environment: [String: String]) {
        guard let spec = environment["RL_UITEST_SHARE_FILE"], let colon = spec.lastIndex(of: ":"),
              let size = Int(spec[spec.index(after: colon)...]), size >= 0
        else { return }
        let name = String(spec[..<colon])
        for request in shareInbox.pending() { shareInbox.remove(request.id) }
        let directory = FileManager.default.temporaryDirectory.appending(path: "rl-uitest-\(UUID().uuidString)")
        try? FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let file = directory.appending(path: name)
        var bytes = [UInt8](repeating: 0, count: size)
        var x: UInt32 = 0x1234_5678
        for i in 0..<size {
            x = x &* 1_103_515_245 &+ 12345
            bytes[i] = UInt8(truncatingIfNeeded: x >> 16)
        }
        try? Data(bytes).write(to: file)
        _ = try? shareInbox.write(files: [file], urls: [])
        try? FileManager.default.removeItem(at: directory)
    }
    #endif

    /// The inbox when there is no app group container (never shared with
    /// the extension).
    static func localInbox() -> URL {
        URL.applicationSupportDirectory.appending(path: "ShareInbox", directoryHint: .isDirectory)
    }

    /// Copies left by uploads of a previous run are deleted: the queue is
    /// not kept across launches.
    static func cleanTransferDirectory() -> URL {
        let directory = URL.applicationSupportDirectory.appending(path: "Transfers", directoryHint: .isDirectory)
        try? FileManager.default.removeItem(at: directory)
        try? FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        return directory
    }
}

/// `UIApplication.beginBackgroundTask`, to finish the current chunk when the
/// app goes to the background (docs/specs/06-app-ios.md §7).
@MainActor
final class UIKitBackgroundActivity: BackgroundActivity {
    private var tasks: [Int: UIBackgroundTaskIdentifier] = [:]
    private var next = 0

    func begin(name: String, onExpiration: @escaping @MainActor () -> Void) -> Int {
        next += 1
        let token = next
        let identifier = UIApplication.shared.beginBackgroundTask(withName: name) { [weak self] in
            MainActor.assumeIsolated {
                onExpiration()
                self?.end(token)
            }
        }
        if identifier != .invalid { tasks[token] = identifier }
        return token
    }

    func end(_ token: Int) {
        guard let identifier = tasks.removeValue(forKey: token) else { return }
        UIApplication.shared.endBackgroundTask(identifier)
    }
}
