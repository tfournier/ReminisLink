import Foundation
import Observation
import ReminisLinkDemo
import ReminisLinkKit

/// The demo mode (milestone M16, docs/specs/09-feuille-de-route.md): the
/// whole app against the fake pak of `ReminisLinkDemo`, a "Demo Brick" with
/// made-up games, to try every feature without a console (App Review, the
/// curious, the App Store screenshots).
///
/// It is isolated from the real app:
/// - **No network**: its requests go through ``FakeURLProtocol`` to the fake
///   pak, and ``OfflineURLProtocol`` fails any other one (a request left
///   after the demo was quit) without going out. Its discovery is fixed (no
///   Bonjour browser) and its consoles are reached at the fake pak's
///   address (no name resolution).
/// - **No Keychain**: its identity and pairing are in an
///   ``InMemorySecureStore``.
/// - **No iCloud Drive**: synced saves go to a temporary folder
///   (``SaveVaultLocation/demo``).
/// - **No trace**: everything it writes is under ``root`` (temporary
///   directory) and in its own `UserDefaults` suite (the Library sort
///   order included, through `defaultAppStorage`); ``tearDown()`` erases
///   both, and a demo left by a crash is erased at the next launch. It
///   never touches the real list of consoles, the share inbox of the share
///   extension or the real transfers.
@MainActor
final class DemoMode {
    /// The demo mode of the app.
    static let name = "ReminisLinkDemo"
    /// The Debug launch switch `RL_DEMO` (UI tests).
    static let debugName = "ReminisLinkDebugDemo"
    /// The host of the demo's fake pak: never resolved (`.invalid`), and
    /// apart from the one of the Debug launch switch.
    static let host = "try.demo.reminislink.invalid"

    let app: AppModel
    let pak: FakePak
    /// Every file the demo writes (art cache, copies of the files sent,
    /// downloads, synced saves).
    let root: URL
    /// The `UserDefaults` suite of the demo (settings, Library sort).
    let suiteName: String
    let defaults: UserDefaults

    /// - Parameters:
    ///   - isDemo: false for the Debug launch switch (no banner, saves
    ///     reported as kept on this iPhone, as the live app without iCloud).
    ///   - hooks: changes to the fake pak and the defaults before the app
    ///     starts (UI tests).
    init(
        name: String = DemoMode.name, host: String = DemoMode.host,
        deviceName: String = String(localized: "Demo Brick"), model: DeviceModel = .demo, isDemo: Bool = true,
        background: BackgroundActivity? = nil,
        hooks: (FakePak, UserDefaults) -> Void = { _, _ in }
    ) {
        let root = Self.root(name: name)
        let suiteName = Self.suiteName(name: name)
        Self.erase(root: root, suiteName: suiteName)
        let defaults = UserDefaults(suiteName: suiteName)!
        self.root = root
        self.suiteName = suiteName
        self.defaults = defaults
        let pak = FakePak(deviceName: deviceName, model: model, host: host)
        pak.register()
        hooks(pak, defaults)
        self.pak = pak
        let transfers = root.appending(path: "Transfers", directoryHint: .isDirectory)
        let exports = root.appending(path: "Exports", directoryHint: .isDirectory)
        for directory in [transfers, exports] {
            try? FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        }
        app = AppModel(
            store: DeviceStore(secureStore: InMemorySecureStore(), defaults: defaults),
            discovery: Discovery(fixedDevices: [pak.discoveredDevice]),
            artCache: ArtCache(directory: root.appending(path: "Art", directoryHint: .isDirectory)),
            // Its own inbox: the demo never consumes files shared with the
            // real app.
            shareInbox: ShareInbox(root: root.appending(path: "ShareInbox", directoryHint: .isDirectory)),
            transferDirectory: transfers, exportDirectory: exports,
            background: background ?? UIKitBackgroundActivity(),
            settings: defaults,
            vaultProvider: .folder(root.appending(path: "Saves", directoryHint: .isDirectory), location: isDemo ? .demo : .local),
            syncScratchDirectory: root.appending(path: "SyncScratch", directoryHint: .isDirectory),
            isDemo: isDemo,
            makeSession: { FakePak.makeOfflineSession() }
        )
        // Milestone M11: the fake pak withdraws its Bonjour service when it
        // quits, and announces it again when it opens.
        let discovery = app.discovery
        let device = pak.discoveredDevice
        pak.onAvailabilityChange = { availability in
            Task { @MainActor in discovery.setFixedDevice(device, visible: availability == .open) }
        }
    }

    /// Pairs the demo app with its fake pak, as scanning its QR code would
    /// (pairing v2, signed requests from then on), without a screen.
    func pair() async throws {
        let client = app.makeClient(endpoint: pak.endpoint, expectedDeviceId: pak.deviceId, pin: pak.pin)
        let hello = try await client.hello()
        let identity = try app.store.identity()
        let attempt = try PairingAttempt(
            secret: pak.secret, deviceId: pak.deviceId, identity: identity, clientName: app.clientName, platform: .apple
        )
        try await client.pair(attempt)
        try app.store.save(
            deviceId: pak.deviceId, name: pak.deviceName, model: hello.model, clientId: identity.clientId, pin: pak.pin,
            endpoint: pak.endpoint, consoleName: hello.deviceName
        )
    }

    /// Ends the demo: stops what runs, forgets the fake pak and erases
    /// everything the demo wrote. A task that was still running may write
    /// once more: the erasure is repeated a little later (and at the next
    /// launch).
    func tearDown() {
        app.shutDown()
        pak.onAvailabilityChange = nil
        pak.unregister()
        let root = root
        let suiteName = suiteName
        Self.erase(root: root, suiteName: suiteName)
        Task { @MainActor in
            try? await Task.sleep(for: .seconds(2))
            Self.erase(root: root, suiteName: suiteName)
        }
    }

    /// Erases a demo left by a previous run (the app was stopped during
    /// the demo).
    static func removeLeftovers(name: String = DemoMode.name) {
        erase(root: root(name: name), suiteName: suiteName(name: name))
    }

    static func root(name: String) -> URL {
        FileManager.default.temporaryDirectory.appending(path: name, directoryHint: .isDirectory)
    }

    static func suiteName(name: String) -> String { "ReminisLink.\(name)" }

    /// The file `UserDefaults` keeps a suite in.
    static func preferencesFile(suiteName: String) -> URL {
        URL.libraryDirectory.appending(path: "Preferences/\(suiteName).plist")
    }

    private static func erase(root: URL, suiteName: String) {
        try? FileManager.default.removeItem(at: root)
        UserDefaults.standard.removePersistentDomain(forName: suiteName)
        // The preferences daemon may leave an empty file behind.
        try? FileManager.default.removeItem(at: preferencesFile(suiteName: suiteName))
    }
}

/// The app: the real consoles, and the demo mode above them (M16). Every
/// screen gets ``current`` as its ``AppModel``: the demo's while it runs.
@MainActor
@Observable
final class AppRoot {
    let live: AppModel
    private(set) var demo: DemoMode?
    /// The demo is being opened (its pairing with the fake pak).
    private(set) var isStartingDemo = false
    /// The demo could not open.
    var demoError: AppError?
    /// Demos that ended, kept alive: their sessions refer to their model
    /// (`unowned`) and a task of theirs may still be finishing (it fails
    /// offline). A few objects in memory per demo, nothing on disk.
    @ObservationIgnored private var endedDemos: [DemoMode] = []

    var current: AppModel { demo?.app ?? live }

    init(live: AppModel) {
        self.live = live
        DemoMode.removeLeftovers()
    }

    /// "Try Without a Console": opens the demo console with its tabs. The
    /// real app stops as if it went to the background (Bonjour, uploads),
    /// and goes on when the demo ends.
    func startDemo(makeDemo: () -> DemoMode = { DemoMode() }) async {
        guard demo == nil, !isStartingDemo else { return }
        isStartingDemo = true
        defer { isStartingDemo = false }
        let mode = makeDemo()
        do {
            try await mode.pair()
        } catch {
            mode.tearDown()
            endedDemos.append(mode)
            demoError = AppError(error)
            return
        }
        live.discovery.stop()
        live.enterBackground()
        demo = mode
        mode.app.openConsole(mode.pak.deviceId)
    }

    /// Quit: back to the list of real consoles, the demo erased. The root
    /// view of the real app starts its discovery again and resumes its
    /// uploads as it appears.
    func quitDemo() {
        guard let mode = demo else { return }
        demo = nil
        mode.tearDown()
        endedDemos.append(mode)
    }

    /// A link opened during the demo (pairing QR code, share extension) is
    /// for the real app: the demo ends first.
    func handle(url: URL) {
        quitDemo()
        live.handle(url: url)
    }
}
