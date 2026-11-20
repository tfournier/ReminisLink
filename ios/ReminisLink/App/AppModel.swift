import Foundation
import Observation
#if DEBUG
import ReminisLinkDemo
#endif
import ReminisLinkKit
import UIKit

/// Navigation destinations.
enum Route: Hashable {
    /// The console a screen belongs to (nil for the app settings).
    var deviceId: String? {
        switch self {
        case .console(let id), .library(let id), .systems(let id), .trash(let id), .transfers(let id), .syncedSaves(let id): id
        case .folder(let folder): folder.deviceId
        case .game(let game), .save(let game), .states(let game), .cheats(let game): game.deviceId
        case .syncedGame(let game): game.deviceId
        case .settings, .syncedConsoles: nil
        }
    }

    case console(String)
    /// The Library tab of a console (M11: every game in one list).
    case library(String)
    /// The systems of a console, to browse its library per system.
    case systems(String)
    case folder(FolderRoute)
    case game(GameRoute)
    /// The save of a game and its backup copies.
    case save(GameRoute)
    /// The state slots of a game, their locks and the resume history.
    case states(GameRoute)
    case cheats(GameRoute)
    case trash(String)
    case transfers(String)
    /// The app settings (docs/specs/06-app-ios.md §4.9).
    case settings
    /// The consoles with synced saves on this iPhone (milestone M9).
    case syncedConsoles
    /// The synced saves of a console, by system and game.
    case syncedSaves(String)
    /// The versions of one synced save.
    case syncedGame(SyncedGameRoute)
}

/// The tabs shown while a console is open (milestone M11).
enum ConsoleTab: Hashable, CaseIterable {
    case console
    case library
    case transfers
    case trash

    /// The route that stands for this tab in ``AppModel/path``.
    func route(_ deviceId: String) -> Route? {
        switch self {
        case .console: nil
        case .library: .library(deviceId)
        case .transfers: .transfers(deviceId)
        case .trash: .trash(deviceId)
        }
    }

    /// The tab a route opens when it follows `.console` in a path.
    static func opened(by route: Route, deviceId: String) -> ConsoleTab? {
        allCases.first { $0.route(deviceId) == route }
    }
}

/// "The console closed ReminisLink" (milestone M11), shown on the list of
/// consoles.
struct ClosedConsoleNotice: Equatable, Identifiable {
    let id = UUID()
    let deviceId: String
}

struct SyncedGameRoute: Hashable {
    let deviceId: String
    let gameId: String
    let name: String
    let systemName: String
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
    /// The ROM file name, when known (to find its `.cht` on the SD card).
    var fileName: String?
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
    /// Scan or PIN. `repairing`: the paired console to pair again (after
    /// `401 unknown_client`), offered for the PIN like the new ones.
    case addConsole(repairing: String? = nil)
    case pairing(PairingFlow)
    case add(AddContext)
    /// A shared patch file: the Patch screen (milestone M6).
    case patch(PatchContext)

    var id: String {
        switch self {
        case .addConsole(let id): "add-\(id ?? "")"
        case .pairing(let flow): "pairing-\(flow.id)"
        case .add(let context): "add-\(context.id)"
        case .patch(let context): "patch-\(context.id)"
        }
    }

    /// The share request shown by this sheet, if any.
    var shareRequestId: UUID? {
        switch self {
        case .add(let context): context.request?.id
        case .patch(let context): context.request?.id
        case .addConsole, .pairing: nil
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
    /// Files downloaded from a console to hand to the share sheet (a
    /// directory each, deleted once the sheet closes, and at launch).
    let exportDirectory: URL
    @ObservationIgnored let background: BackgroundActivity
    /// Preferences of the app (Settings screen).
    @ObservationIgnored let settings: UserDefaults
    /// Where synced saves go (milestone M9).
    @ObservationIgnored let vaultProvider: SaveVaultProvider
    /// Downloads of a sync waiting to go into the vault.
    let syncScratchDirectory: URL
    /// Sync each console when connecting to it (default on).
    var syncOnConnect: Bool {
        didSet { settings.set(syncOnConnect, forKey: Self.syncOnConnectKey) }
    }
    /// Where the saves went at the last sync or browse (nil before).
    var vaultLocation: SaveVaultLocation?
    /// The sync of each console, kept across connections.
    @ObservationIgnored var syncStates: [String: SaveSyncState] = [:]
    #if DEBUG
    /// Pairing link of the fake pak in the demo mode.
    var demoPairingLink: PairingLink?
    #endif
    /// How long to look for a console with Bonjour.
    let lookupTimeout: Duration

    /// The screens pushed above the list of consoles (settings, synced
    /// saves), while no console is open.
    var listPath: [Route] = []
    /// The console shown with its tabs (milestone M11), nil for the list.
    private(set) var openConsoleId: String? {
        didSet {
            guard openConsoleId != oldValue else { return }
            // M14: only the console shown keeps an event stream open.
            for id in [oldValue, openConsoleId].compactMap({ $0 }) { sessions[id]?.updateEventStream() }
        }
    }
    var consoleTab: ConsoleTab = .console
    /// The screens pushed in each tab of the open console.
    var tabPaths: [ConsoleTab: [Route]] = [:]
    /// The console closed ReminisLink (M11): said once on the list.
    var closedNotice: ClosedConsoleNotice?
    /// How long Bonjour must have been browsing before the absence of a
    /// console means it closed ReminisLink (tests shorten it).
    @ObservationIgnored var closureMinimumBrowsing = PakClosure.minimumBrowsing
    /// The delays of the event streams (M14; tests shorten them).
    @ObservationIgnored var eventTiming = EventStreamTiming.live
    /// `library` events closer than this are merged into one refresh.
    @ObservationIgnored var libraryEventDebounce: Duration = .milliseconds(300)

    /// The navigation as one path: the list's screens, or `.console(id)`,
    /// the route of the selected tab (`.library`, `.transfers`, `.trash`;
    /// none for the Console tab) and the screens pushed in it. Setting it
    /// opens that console, tab and screens (deep links, end of an upload or
    /// of a patch), or goes back to the list.
    var path: [Route] {
        get {
            guard let id = openConsoleId else { return listPath }
            return [.console(id)] + (consoleTab.route(id).map { [$0] } ?? []) + (tabPaths[consoleTab] ?? [])
        }
        set {
            guard case .console(let id)? = newValue.first else {
                openConsoleId = nil
                tabPaths = [:]
                consoleTab = .console
                listPath = newValue
                return
            }
            let wasClosed = openConsoleId == nil
            if openConsoleId != id {
                tabPaths = [:]
                openConsoleId = id
            }
            listPath = []
            var rest = Array(newValue.dropFirst())
            if let first = rest.first, let tab = ConsoleTab.opened(by: first, deviceId: id) {
                consoleTab = tab
                rest.removeFirst()
            } else {
                consoleTab = .console
            }
            tabPaths[consoleTab] = rest
            if wasClosed {
                closedNotice = nil
                // Opening a console from the list is an explicit action of
                // the user: the connection it starts may lift a
                // disconnection set from the console (M10).
                if newValue.count == 1 { session(for: id).userOpened() }
            }
        }
    }

    /// Opens a console from the list, on its Console tab.
    func openConsole(_ deviceId: String) {
        path = [.console(deviceId)]
    }

    /// The end of an Add: the Transfers tab of its console. Not when that
    /// console closed ReminisLink meanwhile (M11): the app stays on the
    /// list with its notice, the uploads wait paused.
    func showTransfersAfterAdd(deviceId: String) {
        guard !session(for: deviceId).closedByConsole else { return }
        if path.last != .transfers(deviceId) {
            path = [.console(deviceId), .transfers(deviceId)]
        }
    }

    /// Back to the list of consoles.
    func closeConsole() {
        path = []
    }

    var sheet: Sheet?
    /// The app is active (M10: the write lease is renewed while it is).
    @ObservationIgnored private(set) var isForeground = false
    var linkError: AppError?
    /// A pairing with a console that is already paired, waiting for the
    /// user to confirm that it should be paired again.
    var pendingRepair: PairingSource?
    /// A confirmation to ask once the current sheet is dismissed.
    @ObservationIgnored private var queuedRepair: PairingSource?
    /// A share request to open once the current sheet is dismissed.
    @ObservationIgnored private var queuedShare: ShareRequest?
    /// The share request shown by the Add or Patch sheet: consumed when the sheet
    /// closes, however it closes (sent, cancelled, swiped down).
    @ObservationIgnored private var presentedShare: ShareRequest?

    /// The last `/pair` exchange of each console, until its key is stored.
    @ObservationIgnored private var pairExchanges: [String: (id: UUID, task: Task<PairedDevice, Error>)] = [:]

    @ObservationIgnored private let makeSession: () -> URLSession
    @ObservationIgnored private var sessions: [String: ConsoleSession] = [:]

    init(
        store: DeviceStore, discovery: Discovery, artCache: ArtCache,
        shareInbox: ShareInbox? = nil, transferDirectory: URL? = nil, exportDirectory: URL? = nil,
        background: BackgroundActivity = NoBackgroundActivity(),
        settings: UserDefaults? = nil,
        vaultProvider: SaveVaultProvider? = nil,
        lookupTimeout: Duration = Discovery.lookupTimeout,
        makeSession: @escaping () -> URLSession
    ) {
        self.store = store
        self.discovery = discovery
        self.artCache = artCache
        let temporary = FileManager.default.temporaryDirectory.appending(path: "ReminisLink-\(UUID().uuidString)")
        self.shareInbox = shareInbox ?? ShareInbox(root: temporary.appending(path: "ShareInbox"))
        self.transferDirectory = transferDirectory ?? temporary.appending(path: "Transfers")
        self.exportDirectory = exportDirectory ?? temporary.appending(path: "Exports")
        self.background = background
        let settings = settings ?? UserDefaults(suiteName: "ReminisLink.test-\(UUID().uuidString)")!
        self.settings = settings
        self.syncOnConnect = settings.object(forKey: Self.syncOnConnectKey) as? Bool ?? true
        self.vaultProvider = vaultProvider ?? .folder(temporary.appending(path: "Saves"), location: .local)
        self.syncScratchDirectory = temporary.appending(path: "SyncScratch")
        self.lookupTimeout = lookupTimeout
        self.makeSession = makeSession
        // M11: the pak withdraws its Bonjour service as it quits.
        discovery.onDevicesRemoved = { [weak self] ids in self?.servicesRemoved(ids) }
    }

    static let syncOnConnectKey = "syncSavesOnConnect"

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
        if let request = presentedShare {
            presentedShare = nil
            finishShare(request)
        }
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
        if sheet?.shareRequestId == request.id { return }
        openShare(request)
    }

    /// Opens the Add screen prefilled with a share request, once the
    /// current sheet (pairing...) is gone. A single patch file opens the
    /// Patch screen instead (docs/specs/06-app-ios.md §4.8): the console
    /// if there are several, then the game, are chosen there.
    func openShare(_ request: ShareRequest) {
        guard !store.devices.isEmpty else { return }
        if sheet?.shareRequestId == request.id { return }
        if sheet != nil {
            queuedShare = request
            return
        }
        let deviceId = store.devices.count == 1 ? store.devices.first?.id : nil
        presentedShare = request
        if request.isSinglePatch {
            sheet = .patch(PatchContext(deviceId: deviceId, request: request))
        } else {
            sheet = .add(AddContext(deviceId: deviceId, request: request))
        }
    }

    /// The share request was used or dismissed.
    func finishShare(_ request: ShareRequest) {
        shareInbox.remove(request.id)
    }

    /// The app goes to the background: running uploads stop after their
    /// current chunk (with a background task to finish it) and nothing else
    /// starts.
    func enterBackground() {
        isForeground = false
        for session in sessions.values {
            session.transfers.suspend()
            // Released unless a transfer is still running (M10).
            session.setForeground(false)
        }
    }

    /// The app is active again: uploads go on or resume.
    func becomeActive() {
        isForeground = true
        for session in sessions.values {
            session.transfers.resumeAfterSuspension()
            session.setForeground(true)
        }
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

    /// Runs a `/pair` exchange once the previous exchange with the same
    /// console has ended (key stored or failed), so that the last key the
    /// pak registered is the last one stored. The exchange is never
    /// cancelled with its screen.
    func pairExchange(
        deviceId: String, _ body: @escaping @MainActor () async throws -> PairedDevice
    ) -> Task<PairedDevice, Error> {
        let previous = pairExchanges[deviceId]?.task
        let id = UUID()
        let task = Task { @MainActor in
            defer { if self.pairExchanges[deviceId]?.id == id { self.pairExchanges[deviceId] = nil } }
            _ = await previous?.result
            return try await body()
        }
        pairExchanges[deviceId] = (id, task)
        return task
    }

    /// "Pair Again": the Add a Console sheet, with the console of the
    /// current screen offered for the PIN too (the camera may be
    /// unavailable). Pairing it asks the replacement confirmation first.
    func showRepair(deviceId: String? = nil) {
        sheet = .addConsole(repairing: deviceId ?? path.last.flatMap(\.deviceId))
    }

    /// A new key was stored: connections signed with the old one are dropped.
    func didReplaceKey(for deviceId: String) {
        dropSession(deviceId)
    }

    func finishPairing(deviceId: String?) {
        sheet = nil
        if let deviceId {
            dropSession(deviceId)
            path = [.console(deviceId)]
        }
    }

    // MARK: - Connections

    func makeClient(
        endpoint: Endpoint, expectedDeviceId: String, credentials: DeviceCredentials? = nil,
        errorObserver: (@Sendable (ServerError, ContinuousClock.Instant) -> Void)? = nil
    ) -> APIClient {
        APIClient(
            endpoint: endpoint, expectedDeviceId: expectedDeviceId, credentials: credentials,
            appVersion: Self.appVersion, session: makeSession(), errorObserver: errorObserver
        )
    }

    /// Drops a session; its event stream closes (M14).
    private func dropSession(_ deviceId: String) {
        sessions.removeValue(forKey: deviceId)?.stopEventStream()
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

    /// Whether a failed request of a live connection means the console
    /// closed ReminisLink (M11): `503 shutting_down`, or a connection
    /// failure while its Bonjour service is gone.
    func meansConsoleClosed(_ error: APIError, deviceId: String) -> Bool {
        PakClosure.isClosed(
            error,
            serviceVisible: DiscoveryLogic.find(id: deviceId, in: discovery.devices) != nil,
            browsingFor: discovery.browsingDuration,
            minimumBrowsing: closureMinimumBrowsing
        )
    }

    /// Whether Bonjour currently sees this console.
    func isServiceVisible(_ deviceId: String) -> Bool {
        DiscoveryLogic.find(id: deviceId, in: discovery.devices) != nil
    }

    /// Whether Bonjour has been browsing long enough for the absence of a
    /// console to mean something (M11).
    var hasBrowsedLongEnough: Bool {
        discovery.browsingDuration.map { $0 >= closureMinimumBrowsing } ?? false
    }

    /// Bonjour saw these consoles withdraw their service (M11): each
    /// connected one checks at once whether it closed ReminisLink, even if
    /// the user does nothing.
    func servicesRemoved(_ deviceIds: [String]) {
        for id in deviceIds { sessions[id]?.checkStillOpen(waitForBrowsing: false) }
    }

    /// The console closed ReminisLink (M11): back to the list of consoles,
    /// with a short message, if it was open.
    func consoleClosed(deviceId: String) {
        guard openConsoleId == deviceId else { return }
        closeConsole()
        closedNotice = ClosedConsoleNotice(deviceId: deviceId)
    }

    /// Forgets a device on this iPhone (after unpairing).
    func forget(deviceId: String) throws {
        dropSession(deviceId)
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
            transferDirectory: cleanTransferDirectory(), exportDirectory: cleanExportDirectory(),
            background: UIKitBackgroundActivity(),
            settings: defaults,
            vaultProvider: .live(containerIdentifier: Bundle.main.object(forInfoDictionaryKey: "RLICloudContainer") as? String),
            makeSession: { APIClient.makeSession() }
        )
        #if DEBUG
        // UI tests: a share request left by a previous run (the app was
        // stopped while it was open) must not open in this one.
        if environment["RL_RESET_STORE"] == "1" {
            try? FileManager.default.removeItem(at: app.shareInbox.root)
        }
        app.injectTestShare(environment)
        #endif
        return app
    }

    #if DEBUG
    /// Everything in memory, against the fake pak: no network, no Keychain.
    static func makeDemo() -> AppModel {
        let pak = FakePak()
        pak.register()
        // UI tests: the first save of a cheat list meets a change made on
        // the console (412).
        pak.conflictOnNextCheatsSave = ProcessInfo.processInfo.environment["RL_UITEST_CHEATS_CONFLICT"] == "1"
        let suite = "ReminisLink.demo"
        UserDefaults.standard.removePersistentDomain(forName: suite)
        let artDirectory = FileManager.default.temporaryDirectory.appending(path: "ReminisLinkDemoArt")
        try? FileManager.default.removeItem(at: artDirectory)
        let demoSaves = FileManager.default.temporaryDirectory.appending(path: "ReminisLinkDemoSaves")
        try? FileManager.default.removeItem(at: demoSaves)
        // UI tests (M10): another paired iPhone holds the write lease.
        if let name = ProcessInfo.processInfo.environment["RL_UITEST_OTHER_WRITER"], !name.isEmpty {
            pak.simulateOtherWriter(name: name)
        }
        // UI tests (M9): the second listing of the saves finds one changed.
        if let n = ProcessInfo.processInfo.environment["RL_UITEST_SAVE_CHANGES_ON_LISTING"].flatMap(Int.init) {
            pak.changeSaveOnListing = n
        }
        let app = AppModel(
            store: DeviceStore(secureStore: InMemorySecureStore(), defaults: UserDefaults(suiteName: suite)!),
            discovery: Discovery(fixedDevices: [pak.discoveredDevice]),
            artCache: ArtCache(directory: artDirectory),
            // Its own inbox: the demo never consumes real shared files.
            shareInbox: ShareInbox(root: FileManager.default.temporaryDirectory.appending(path: "ReminisLinkDemoInbox")),
            transferDirectory: cleanTransferDirectory(), exportDirectory: cleanExportDirectory(),
            background: UIKitBackgroundActivity(),
            settings: UserDefaults(suiteName: suite)!,
            // Its own folder, emptied at each launch: the demo never mixes
            // with real synced saves. As without iCloud.
            vaultProvider: .folder(demoSaves, location: .local),
            makeSession: { APIClient.makeSession(protocolClasses: [FakeURLProtocol.self]) }
        )
        app.demoPairingLink = pak.pairingLink
        // Milestone M11: the fake pak withdraws its Bonjour service when it
        // quits, and announces it again when it opens.
        let discovery = app.discovery
        let device = pak.discoveredDevice
        pak.onAvailabilityChange = { availability in
            Task { @MainActor in discovery.setFixedDevice(device, visible: availability == .open) }
        }
        // UI tests (M11): `RL_UITEST_PAK_CLOSES_ON="GET /games"` makes the
        // fake pak quit on that request, and open again 5 seconds later.
        if let spec = ProcessInfo.processInfo.environment["RL_UITEST_PAK_CLOSES_ON"] {
            let parts = spec.split(separator: " ", maxSplits: 1).map(String.init)
            if parts.count == 2 { pak.closeOnRequest(method: parts[0], path: parts[1], reopenAfter: 5) }
        }
        // A request left by a previous demo run is not opened again.
        for request in app.shareInbox.pending() { app.shareInbox.remove(request.id) }
        app.injectTestShare(ProcessInfo.processInfo.environment)
        return app
    }

    /// UI tests: `RL_UITEST_SHARE_FILE=<name>:<bytes>` stores a share
    /// request holding a generated file (pseudo-random bytes, no game
    /// content), as the share extension would. The document picker cannot
    /// be driven by XCUITest; this path covers the rest of "add a file".
    func injectTestShare(_ environment: [String: String]) {
        if injectTestPatch(environment) { return }
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

    /// UI tests (milestone M6): `RL_UITEST_SHARE_PATCH=<name>:<source
    /// size>:<source CRC32, hex>:<target text>` stores a share request
    /// holding a BPS made by the app (``DemoPatch``: it writes the target
    /// text and declares that source), as the share extension would.
    func injectTestPatch(_ environment: [String: String]) -> Bool {
        guard let spec = environment["RL_UITEST_SHARE_PATCH"] else { return false }
        let parts = spec.split(separator: ":", maxSplits: 3, omittingEmptySubsequences: false).map(String.init)
        guard parts.count == 4, let size = Int(parts[1]), let crc = UInt32(parts[2], radix: 16) else { return false }
        for request in shareInbox.pending() { shareInbox.remove(request.id) }
        let directory = FileManager.default.temporaryDirectory.appending(path: "rl-uitest-\(UUID().uuidString)")
        try? FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let file = directory.appending(path: parts[0])
        try? DemoPatch.bps(sourceSize: size, sourceCRC32: crc, target: Data(parts[3].utf8)).write(to: file)
        _ = try? shareInbox.write(files: [file], urls: [])
        try? FileManager.default.removeItem(at: directory)
        return true
    }
    #endif

    /// The inbox when there is no app group container (never shared with
    /// the extension).
    static func localInbox() -> URL {
        URL.applicationSupportDirectory.appending(path: "ShareInbox", directoryHint: .isDirectory)
    }

    /// Downloads left by a previous run (the share sheet was still open
    /// when the app ended) are deleted.
    static func cleanExportDirectory() -> URL {
        let directory = FileManager.default.temporaryDirectory.appending(path: "Exports", directoryHint: .isDirectory)
        try? FileManager.default.removeItem(at: directory)
        try? FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        return directory
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
