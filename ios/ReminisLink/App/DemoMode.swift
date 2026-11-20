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
/// - **No network**: its requests go through ``FakeURLProtocol`` to its fake
///   pak, and ``OfflineURLProtocol`` fails any other one (a request left
///   after the demo was quit) without going out. Its discovery is fixed (no
///   Bonjour browser) and its console is reached at the fake pak's address
///   (no name resolution).
/// - **No Keychain**: its identity and pairing are in an
///   ``InMemorySecureStore``.
/// - **No iCloud Drive**: synced saves go to a temporary folder
///   (``SaveVaultLocation/demo``); downloads never reach the share sheet
///   (``ExportSheetModifier``).
/// - **No trace**: each session has its own folder, `UserDefaults` suite
///   (the Library sort included, through `defaultAppStorage`) and fake pak
///   host, all named with a new UUID. ``end()`` stops the session's work,
///   waits for it, then erases its own folder and suite (again a little
///   later); a session left by a crash is erased at the next launch. It
///   never touches the real list of consoles, the share inbox of the share
///   extension or the real transfers.
@MainActor
final class DemoMode {
    /// The start of the names of the app's demo sessions (folders, suites).
    static let prefix = "ReminisLinkDemo-"
    /// The Debug launch switch `RL_DEMO` (UI tests).
    static let debugName = "ReminisLinkDebugDemo"

    let app: AppModel
    let pak: FakePak
    /// Every file the session writes (art cache, copies of the files sent,
    /// downloads, synced saves).
    let root: URL
    /// The `UserDefaults` suite of the session (settings, Library sort).
    let suiteName: String
    let defaults: UserDefaults
    /// How long after ``end()`` the session's files are erased once more.
    var eraseAgainAfter: Duration = .seconds(2)
    /// How long ``end()`` waits for the session's work (a file provider
    /// that never answers); past it the session is released anyway and a
    /// late write is left to the cleanup at the next launch.
    var shutdownTimeout: Duration = .seconds(10)
    /// The last ``end()`` gave up waiting (tests).
    private(set) var endTimedOut = false

    /// - Parameters:
    ///   - name: the folder and suite name; a new one per session by default.
    ///   - host: the fake pak's host; a new one per session by default.
    ///   - isDemo: false for the Debug launch switch (no banner, saves
    ///     reported as kept on this iPhone, as the live app without iCloud).
    ///   - hooks: changes to the fake pak and the defaults before the app
    ///     starts (UI tests).
    init(
        name: String? = nil, host: String? = nil,
        deviceName: String = String(localized: "Demo Brick"), model: DeviceModel = .demo, isDemo: Bool = true,
        background: BackgroundActivity? = nil,
        hooks: (FakePak, UserDefaults) -> Void = { _, _ in }
    ) {
        let session = UUID().uuidString.lowercased()
        let name = name ?? Self.prefix + session
        let root = Self.root(name: name)
        let suiteName = Self.suiteName(name: name)
        Self.erase(root: root, suiteName: suiteName)
        let defaults = UserDefaults(suiteName: suiteName)!
        self.root = root
        self.suiteName = suiteName
        self.defaults = defaults
        let pak = FakePak(deviceName: deviceName, model: model, host: host ?? "\(session).try.demo.reminislink.invalid")
        // Original drawn box arts and state thumbnails; a short request log
        // (a long session does not grow in memory).
        pak.drawsArtwork = true
        if isDemo { pak.logLimit = 100 }
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
        // Its systems have invented tags and extensions: Add suggests them
        // from the demo library.
        app.extraSystemTagsByExtension = pak.library.tagsByExtension
        // Milestone M11: the fake pak withdraws its Bonjour service when it
        // quits, and announces it again when it opens.
        let discovery = app.discovery
        let device = pak.discoveredDevice
        pak.onAvailabilityChange = { [weak discovery] availability in
            Task { @MainActor in discovery?.setFixedDevice(device, visible: availability == .open) }
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

    /// Ends the session: stops its work (connections, uploads, pak tasks,
    /// syncs, file copies, downloads) and waits for it, forgets its fake
    /// pak, then erases its own folder and suite, and once more a little
    /// later. Never another session's.
    func end() async {
        pak.unregister()
        let app = app
        endTimedOut = !(await Self.wait(upTo: shutdownTimeout) { await app.shutDown() })
        pak.onAvailabilityChange = nil
        erase()
        try? await Task.sleep(for: eraseAgainAfter)
        erase()
    }

    /// Runs `operation` and waits for it at most `limit`: true if it ended.
    static func wait(upTo limit: Duration, _ operation: @escaping @MainActor () async -> Void) async -> Bool {
        let gate = OnceGate()
        return await withCheckedContinuation { continuation in
            Task { @MainActor in
                await operation()
                if gate.claim() { continuation.resume(returning: true) }
            }
            Task { @MainActor in
                try? await Task.sleep(for: limit)
                if gate.claim() { continuation.resume(returning: false) }
            }
        }
    }

    func erase() {
        Self.erase(root: root, suiteName: suiteName)
    }

    /// Erases the demo sessions left by a previous run (the app was stopped
    /// during a demo): their folders and suites, found by name.
    static func removeLeftovers() {
        let fileManager = FileManager.default
        let temporary = fileManager.temporaryDirectory
        for name in (try? fileManager.contentsOfDirectory(atPath: temporary.path)) ?? [] where name.hasPrefix(prefix) {
            erase(root: root(name: name), suiteName: suiteName(name: name))
        }
        let preferences = URL.libraryDirectory.appending(path: "Preferences", directoryHint: .isDirectory)
        let suitePrefix = suiteName(name: prefix)
        for file in (try? fileManager.contentsOfDirectory(atPath: preferences.path)) ?? []
        where file.hasPrefix(suitePrefix) && file.hasSuffix(".plist") {
            let suite = String(file.dropLast(".plist".count))
            UserDefaults.standard.removePersistentDomain(forName: suite)
            try? fileManager.removeItem(at: preferences.appending(path: file))
        }
    }

    /// Erases one named session (the Debug launch switch's).
    static func removeLeftovers(name: String) {
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

/// True for the first caller only.
@MainActor
private final class OnceGate {
    private var claimed = false

    func claim() -> Bool {
        defer { claimed = true }
        return !claimed
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
    /// Sessions being ended (waiting for their work, then erased); each is
    /// released once ended.
    @ObservationIgnored private var endings: [UUID: Task<Void, Never>] = [:]

    var current: AppModel { demo?.app ?? live }

    init(live: AppModel) {
        self.live = live
        DemoMode.removeLeftovers()
    }

    /// "Try Without a Console": opens the demo console with its tabs. The
    /// real app pauses (Bonjour, uploads, save syncs) until the demo ends.
    func startDemo(makeDemo: () -> DemoMode = { DemoMode() }) async {
        guard demo == nil, !isStartingDemo else { return }
        isStartingDemo = true
        defer { isStartingDemo = false }
        let mode = makeDemo()
        do {
            try await mode.pair()
        } catch {
            end(mode)
            demoError = AppError(error)
            return
        }
        live.pauseForDemo()
        demo = mode
        mode.app.openConsole(mode.pak.deviceId)
    }

    /// Quit: back to the list of real consoles at once; the session ends
    /// (its work stopped and awaited, its files erased) in the background.
    func quitDemo() {
        guard let mode = demo else { return }
        demo = nil
        live.resumeAfterDemo()
        end(mode)
    }

    /// A link opened during the demo (pairing QR code, share extension) is
    /// for the real app: the demo ends first.
    func handle(url: URL) {
        quitDemo()
        live.handle(url: url)
    }

    /// Waits for the sessions being ended (tests).
    func waitForEndedDemos() async {
        while let task = endings.values.first { await task.value }
    }

    private func end(_ mode: DemoMode) {
        let id = UUID()
        endings[id] = Task { [weak self] in
            await mode.end()
            self?.endings[id] = nil
        }
    }
}
