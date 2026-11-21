import Foundation
import Observation
import ReminisLinkDemo
import ReminisLinkKit
import UIKit

/// Navigation destinations.
enum Route: Hashable {
    /// The console a screen belongs to (nil for the app settings).
    var deviceId: String? {
        switch self {
        case .console(let id), .library(let id), .systems(let id), .trash(let id), .transfers(let id), .syncedSaves(let id): id
        case .paks(let id): id
        case .sdFolder(let folder): folder.deviceId
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
    /// The tool and emulator paks of a console (milestone M12), pushed
    /// in its Console tab.
    case paks(String)
    /// A folder of the SD card, read only (milestone M12), pushed in the
    /// Console tab from its Storage section.
    case sdFolder(SdFolderRoute)
}

/// A folder of the SD browser (milestone M12).
struct SdFolderRoute: Hashable {
    let deviceId: String
    /// Nil for the root of the card.
    let folderId: String?
    let title: String
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

/// "This console was forgotten on another of your Apple devices" (M15),
/// shown on the list of consoles.
struct ForgottenConsoleNotice: Equatable, Identifiable {
    let id = UUID()
    let name: String?
}

/// "Patch applied", shown for a few seconds on the sheet of the game the
/// Patch screen leaves the user on once it closes by itself.
struct PatchNotice: Equatable, Identifiable {
    let id = UUID()
    let deviceId: String
    let gameId: String
    let text: String
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
    /// The QR code scanner. `repairing`: the console to pair again (after
    /// `401 unknown_client`, or paired before M13): only the title changes.
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
    /// The demo mode opened from the list of consoles (milestone M16):
    /// banner with Quit, no Add a Console, saves in a temporary folder.
    /// False for the Debug launch switch `RL_DEMO`, which behaves as the
    /// live app against the fake pak.
    let isDemo: Bool
    /// Extension → system tags added to `SystemSuggestion.tagsByExtension`
    /// when a file is added: the made-up systems of the demo console
    /// (empty for a real one).
    @ObservationIgnored var extraSystemTagsByExtension: [String: [String]] = [:]
    /// File copies and downloads in progress (M16: the end of a demo waits
    /// for them before erasing its folder).
    @ObservationIgnored let work = WorkTracker()
    /// The consoles whose save sync was stopped while the demo mode runs,
    /// started again when it ends (M16).
    @ObservationIgnored var syncsPausedForDemo: [String] = []
    /// Ended (the demo mode that owned it was quit): no session is created
    /// or kept, no sync, copy or download starts.
    @ObservationIgnored private(set) var isShutDown = false
    /// The real app while the demo mode runs (M16): no save sync, trash
    /// read or upload starts; sync requests wait for the end of the demo.
    @ObservationIgnored var isPausedForDemo = false

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
    /// A console forgotten on another device of the Apple account while
    /// this one used it (M15), said once on the list.
    var forgottenNotice: ForgottenConsoleNotice?
    /// A patch was applied and its screen closed by itself: said on the
    /// sheet of the game, then cleared.
    var patchNotice: PatchNotice?
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
    /// The app is active.
    @ObservationIgnored private(set) var isForeground = false
    /// The app went to the background (and has not become active since):
    /// screens still shown do not connect (M15).
    @ObservationIgnored private(set) var isInBackground = false
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
    /// M15: the iCloud key-value store marker of pairing changes (live app
    /// only; never in tests, the demo or with the in-memory store).
    @ObservationIgnored private var pairingSignal: PairingChangeSignal?
    /// The reads that follow a marker from another device.
    @ObservationIgnored private var laterPairingReads: Task<Void, Never>?

    init(
        store: DeviceStore, discovery: Discovery, artCache: ArtCache,
        shareInbox: ShareInbox? = nil, transferDirectory: URL? = nil, exportDirectory: URL? = nil,
        background: BackgroundActivity = NoBackgroundActivity(),
        settings: UserDefaults? = nil,
        vaultProvider: SaveVaultProvider? = nil,
        lookupTimeout: Duration = Discovery.lookupTimeout,
        syncScratchDirectory: URL? = nil,
        isDemo: Bool = false,
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
        self.syncScratchDirectory = syncScratchDirectory ?? temporary.appending(path: "SyncScratch")
        self.lookupTimeout = lookupTimeout
        self.isDemo = isDemo
        self.makeSession = makeSession
        // M11: the pak withdraws its Bonjour service as it quits.
        discovery.onDevicesRemoved = { [weak self] ids in self?.servicesRemoved(ids) }
    }

    static let syncOnConnectKey = "syncSavesOnConnect"

    static var appVersion: String {
        Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "1.0.0"
    }

    /// The name of this app's identity on the consoles (`clientName` of
    /// `POST /pair`, 1 to 64 Unicode scalars): the name typed on the Add a
    /// Console screen, shared by the Apple devices of the account; the
    /// model of this device ("iPhone", "iPad", "Mac") until one is typed.
    /// iOS gives neither the iCloud account nor the real name of the device.
    var clientName: String {
        Pairing.clientName(from: store.pairingName() ?? "", fallback: Self.defaultClientName)
    }

    /// M15: an iPad app on a Mac reports the model "iPad"; the Mac's own
    /// name is not available to iOS apps.
    static var defaultClientName: String {
        defaultClientName(isiOSAppOnMac: ProcessInfo.processInfo.isiOSAppOnMac, model: UIDevice.current.model)
    }

    static func defaultClientName(isiOSAppOnMac: Bool, model: String) -> String {
        isiOSAppOnMac ? "Mac" : model
    }

    /// `X-RL-Platform` (informative): the kind of this device.
    static var requestPlatform: RequestPlatform {
        if ProcessInfo.processInfo.isiOSAppOnMac || ProcessInfo.processInfo.isMacCatalystApp { return .mac }
        return UIDevice.current.userInterfaceIdiom == .pad ? .ipad : .iphone
    }

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
        do {
            requestPairing(try PairingLink.parse(url))
        } catch {
            linkError = AppError(pairingLink: error)
        }
    }

    func requestPairing(_ link: PairingLink) {
        requestPairing(.link(link))
    }

    /// Pairs, after a confirmation if the console is already paired (its
    /// key would be replaced). The confirmation waits for the current sheet
    /// to be dismissed.
    func requestPairing(_ source: PairingSource) {
        // A console paired before M13 has nothing left to replace.
        guard let device = store.device(id: source.deviceId), !device.needsRepair else {
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
        isInBackground = true
        for session in sessions.values {
            session.transfers.suspend()
            // Released unless a transfer is still running (M10).
            session.setForeground(false)
        }
    }

    /// The app is active again: uploads go on or resume.
    func becomeActive() {
        isForeground = true
        isInBackground = false
        // M13: another device of the Apple account may have paired or
        // forgotten a console (iCloud Keychain).
        reloadPairings()
        for session in sessions.values {
            // Paused because the pairing changed: after the next connection.
            if !session.pausedForCredentials { session.transfers.resumeAfterSuspension() }
            session.setForeground(true)
        }
        // M15: the console shown, dropped while in the background (its
        // pairing changed), connects again: its screen will not ask.
        if let deviceId = openConsoleId, let session = sessions[deviceId], session.state == .idle {
            Task { await session.connect() }
        }
        checkShareInbox()
    }

    /// Reads the shared Keychain again (M15: on activation, on a marker
    /// from another device, and every minute while active): the list of
    /// consoles, and the sessions that work with credentials another
    /// device replaced since, or that failed before the identity of their
    /// pairing arrived. Those connect again with what the Keychain holds
    /// now; nothing in memory keeps an older identity.
    ///
    /// Per session, from what the Keychain says (a read error changes
    /// nothing): the pairing was forgotten (on another device): the session
    /// is revoked, the console closed, and the list says why; the record is
    /// here but not its identity: nothing is signed until it arrives; other
    /// credentials: the client is dropped, and the session connects again
    /// at once only if its console is shown and the app is active.
    func reloadPairings() {
        let names = Dictionary(store.devices.map { ($0.id, $0.name) }, uniquingKeysWith: { first, _ in first })
        store.reload()
        for (deviceId, session) in sessions {
            guard let state = try? store.pairingState(for: deviceId) else { continue }
            switch state {
            case .notPaired:
                // Gone from the list (not a console to pair again), and in
                // use here.
                guard store.device(id: deviceId) == nil, session.holdsPairing || openConsoleId == deviceId else { continue }
                session.revoke()
                dropSession(deviceId)
                if openConsoleId == deviceId { closeConsole() }
                forgottenNotice = ForgottenConsoleNotice(name: names[deviceId])
            case .awaitingIdentity:
                session.awaitIdentity()
            case .paired:
                if session.usesStaleCredentials(try? store.credentials(for: deviceId)) {
                    session.invalidateCredentials(reconnect: isForeground && openConsoleId == deviceId)
                }
            }
        }
    }

    /// Starts the marker of pairing changes (M15, live app).
    func startPairingSignal(_ signal: PairingChangeSignal) {
        pairingSignal = signal
        store.onSharedItemsChange = { [weak signal] in signal?.noteLocalChange() }
        signal.start()
    }

    /// Another device changed the pairings: the Keychain is read now, and
    /// again a little later (the marker may arrive before the Keychain
    /// items it announces).
    func pairingsChangedElsewhere() {
        reloadPairings()
        laterPairingReads?.cancel()
        laterPairingReads = Task { [weak self] in
            for delay in [5, 20] {
                try? await Task.sleep(for: .seconds(delay))
                guard !Task.isCancelled else { return }
                self?.reloadPairings()
            }
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

    /// "Pair Again": the Add a Console sheet (scan the console's QR code)
    /// for the console of the current screen. Pairing it asks the
    /// replacement confirmation first.
    func showRepair(deviceId: String? = nil) {
        sheet = .addConsole(repairing: deviceId ?? path.last.flatMap(\.deviceId))
    }

    /// A new pairing was stored: connections made before are dropped.
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

    /// A client of one console, over TLS pinned on `pin` (or on the pin of
    /// `credentials`).
    func makeClient(
        endpoint: Endpoint, expectedDeviceId: String, credentials: DeviceCredentials? = nil, pin: String? = nil,
        errorObserver: (@Sendable (ServerError, ContinuousClock.Instant) -> Void)? = nil
    ) -> APIClient {
        APIClient(
            endpoint: endpoint, expectedDeviceId: expectedDeviceId, credentials: credentials, pin: pin,
            platform: Self.requestPlatform, session: makeSession(),
            errorObserver: errorObserver
        )
    }

    /// Drops a session; its event stream closes (M14).
    private func dropSession(_ deviceId: String) {
        sessions.removeValue(forKey: deviceId)?.detach()
    }

    func session(for deviceId: String) -> ConsoleSession {
        if let session = sessions[deviceId] { return session }
        let session = ConsoleSession(deviceId: deviceId, app: self)
        // M16: once shut down, a late caller gets a session that is never
        // kept (and never connects), so the app can be freed.
        guard !isShutDown else { return session }
        sessions[deviceId] = session
        return session
    }

    /// The sessions kept (tests).
    var sessionCount: Int { sessions.count }

    /// Finds a paired console with Bonjour by its id; when Bonjour does not
    /// find it quickly, the last address where it presented its pinned
    /// certificate (the address of its QR code right after pairing). M13:
    /// the pin authenticates the console at that address.
    func endpoint(for deviceId: String) async throws -> Endpoint {
        let discovery = discovery
        let fallback = store.lastEndpoint(for: deviceId)
        return try await DeviceLocator.locate(
            timeout: fallback == nil ? lookupTimeout : min(lookupTimeout, Self.bonjourTimeoutWithFallback),
            find: { await discovery.waitForDevice(id: deviceId, timeout: $0) },
            resolve: { try await EndpointResolver.resolve($0) },
            isLocalNetworkDenied: { discovery.status == .localNetworkDenied },
            fallback: fallback
        )
    }

    /// How long Bonjour is waited for before trying a known address (the
    /// QR code's, or the last validated one).
    static let bonjourTimeoutWithFallback: Duration = .seconds(3)

    /// Finds the console of a pairing link: Bonjour by its id first, then,
    /// if Bonjour does not find it quickly, the address of the QR code
    /// (`h`:`p`). M13: the certificate pin of the link authenticates the
    /// console, so a fake pak at that address cannot be paired
    /// (docs/specs/03-protocole.md §3.1).
    func pairingEndpoint(for link: PairingLink) async throws -> Endpoint {
        let discovery = discovery
        let deviceId = link.deviceId
        return try await DeviceLocator.locate(
            timeout: link.endpoint == nil ? lookupTimeout : min(lookupTimeout, Self.bonjourTimeoutWithFallback),
            find: { await discovery.waitForDevice(id: deviceId, timeout: $0) },
            resolve: { try await EndpointResolver.resolve($0) },
            isLocalNetworkDenied: { discovery.status == .localNetworkDenied },
            fallback: link.endpoint
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

    /// Forgets the entry of a console paired before M13 (this device's
    /// list only; an M13 pairing synced meanwhile stays).
    func forgetRepairMarker(deviceId: String) {
        store.forgetRepairMarker(deviceId)
    }

    /// Stops everything this model runs and waits for it (the end of the
    /// demo mode, M16): connections, event streams, uploads, pak tasks, save
    /// syncs, file copies and downloads, and the discovery. The model is
    /// not used afterwards.
    func shutDown() async {
        guard !isShutDown else { return }
        isShutDown = true
        sheet = nil
        let ended = Array(sessions.values)
        sessions.removeAll()
        openConsoleId = nil
        discovery.stop()
        let syncs = cancelSaveSyncs()
        for session in ended { await session.shutDown() }
        for task in syncs { await task.value }
        await work.close()
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
        // M13: the identity and the paired consoles are synchronized
        // through the iCloud Keychain; the RL1 keys (this device only) are
        // deleted at the first launch.
        let store = liveStore(defaults: defaults, environment: environment)
        #if DEBUG
        if environment["RL_RESET_STORE"] == "1" {
            for device in store.devices { try? store.remove(device.id) }
            try? store.setPairingName("")
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
        // M15: never with the in-memory store of the UI tests.
        if environment["RL_UITEST_MEMORY_STORE"] != "1" {
            app.startPairingSignal(PairingChangeSignal { [weak app] in app?.pairingsChangedElsewhere() })
        }
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
    /// The Debug launch switch `RL_DEMO=1` / `-RLDemo YES` (UI tests): the
    /// whole app runs against the fake pak through M16's ``DemoMode`` (same
    /// isolation: no network, no Keychain, no iCloud), without its banner
    /// and not paired yet, as the live app would; the test hooks below
    /// apply.
    static func makeDemo() -> AppModel {
        let environment = ProcessInfo.processInfo.environment
        DemoMode.removeLeftovers(name: DemoMode.debugName)
        let demo = DemoMode(name: DemoMode.debugName, host: FakePak.demoHost, deviceName: "Demo Brick", model: .brick, isDemo: false) { pak, defaults in
            // UI tests: the first save of a cheat list meets a change made
            // on the console (412).
            pak.conflictOnNextCheatsSave = environment["RL_UITEST_CHEATS_CONFLICT"] == "1"
            // UI tests (M15): file downloads answered this many seconds late.
            if let delay = environment["RL_UITEST_DOWNLOAD_DELAY"].flatMap(TimeInterval.init) {
                pak.fileDownloadDelay = delay
            }
            // UI tests (M13): the next writes find the console busy.
            if let n = environment["RL_UITEST_BUSY_WRITES"].flatMap(Int.init) {
                pak.busyNextWrites(n, retryAfter: 1)
            }
            // UI tests (M13): the demo console was paired before M13 (RL1).
            if environment["RL_UITEST_RL1_CONSOLE"] == "1" {
                let legacy = [PairedDevice(id: pak.deviceId, name: pak.deviceName, model: pak.model, pairedAt: Date(), needsRepair: true)]
                defaults.set(try? JSONEncoder().encode(legacy), forKey: "pairedDevices")
            }
            // UI tests (M9): the second listing of the saves finds one changed.
            if let n = environment["RL_UITEST_SAVE_CHANGES_ON_LISTING"].flatMap(Int.init) {
                pak.changeSaveOnListing = n
            }
            // UI tests (M17): an older pak (`RL_UITEST_PAK_VERSION=0.9.0`),
            // or a pak that needs a newer app (`RL_UITEST_MIN_CLIENT_VERSION`).
            if let version = environment["RL_UITEST_PAK_VERSION"] { pak.pakVersion = version }
            if let version = environment["RL_UITEST_MIN_CLIENT_VERSION"] { pak.minClientVersion = version }
            // UI tests (M11): `RL_UITEST_PAK_CLOSES_ON="GET /games"` makes the
            // fake pak quit on that request, and open again 5 seconds later.
            if let spec = environment["RL_UITEST_PAK_CLOSES_ON"] {
                let parts = spec.split(separator: " ", maxSplits: 1).map(String.init)
                if parts.count == 2 { pak.closeOnRequest(method: parts[0], path: parts[1], reopenAfter: 5) }
            }
        }
        let app = demo.app
        app.demoPairingLink = demo.pak.pairingLink
        app.injectTestShare(environment)
        return app
    }

    /// UI tests: `RL_UITEST_SHARE_FILE=<name>:<bytes>` stores a share
    /// request holding a generated file (pseudo-random bytes, no game
    /// content), as the share extension would. The document picker cannot
    /// be driven by XCUITest; this path covers the rest of "add a file".
    func injectTestShare(_ environment: [String: String]) {
        if injectTestPatch(environment) { return }
        if injectTestPak(environment) { return }
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
        _ = try? shareInbox.write(files: [file])
        try? FileManager.default.removeItem(at: directory)
    }

    /// UI tests (milestone M12): `RL_UITEST_SHARE_PAK=<name>` stores a share
    /// request holding a tiny pak, `<name>.pak.zip` (a `<name>.pak` folder
    /// with a `launch.sh` and a `pak.json`, zipped by ``PakArchiver`` as a
    /// `.pak` folder chosen in Files is), as the share extension would.
    func injectTestPak(_ environment: [String: String]) -> Bool {
        guard let name = environment["RL_UITEST_SHARE_PAK"], !name.isEmpty, !name.contains("/") else { return false }
        for request in shareInbox.pending() { shareInbox.remove(request.id) }
        let directory = FileManager.default.temporaryDirectory.appending(path: "rl-uitest-\(UUID().uuidString)")
        let folder = directory.appending(path: "\(name).pak", directoryHint: .isDirectory)
        defer { try? FileManager.default.removeItem(at: directory) }
        do {
            try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
            try Data("#!/bin/sh\necho \"\(name)\"\n".utf8).write(to: folder.appending(path: "launch.sh"))
            try Data("{\"name\":\"\(name)\",\"type\":\"TOOL\",\"version\":\"0.0.1\"}".utf8).write(to: folder.appending(path: "pak.json"))
            let zipped = try PakArchiver.zip(folder: folder, into: directory.appending(path: "out"))
            _ = try shareInbox.write(files: [zipped.url])
        } catch {
            return false
        }
        return true
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
        _ = try? shareInbox.write(files: [file])
        try? FileManager.default.removeItem(at: directory)
        return true
    }
    #endif

    /// The paired consoles: the iCloud Keychain, or (Debug, UI tests,
    /// `RL_UITEST_MEMORY_STORE=1`) a fresh store in memory that never
    /// touches the Keychain.
    private static func liveStore(defaults: UserDefaults, environment: [String: String]) -> DeviceStore {
        #if DEBUG
        if environment["RL_UITEST_MEMORY_STORE"] == "1" {
            let suite = "ReminisLink.uitest-memory"
            UserDefaults.standard.removePersistentDomain(forName: suite)
            return DeviceStore(secureStore: InMemorySecureStore(), defaults: UserDefaults(suiteName: suite)!)
        }
        #endif
        return DeviceStore(
            secureStore: KeychainStore(service: "ReminisLink.pairing", synchronizable: true),
            defaults: defaults,
            legacySecureStore: KeychainStore(service: "ReminisLink.devices")
        )
    }

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
