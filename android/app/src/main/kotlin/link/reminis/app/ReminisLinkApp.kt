package link.reminis.app

import android.app.Application
import android.content.Context
import android.os.Build
import java.io.File
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import link.reminis.core.ApiClient
import link.reminis.core.ApiException
import link.reminis.core.ConsoleStore
import link.reminis.core.DeviceCredentials
import link.reminis.core.Endpoint
import link.reminis.core.Locator
import link.reminis.core.PairedConsole
import link.reminis.core.PairingLink
import link.reminis.core.PairingLinkException
import link.reminis.core.JobTracker
import link.reminis.core.PakInstaller
import link.reminis.core.TransferQueue

class ReminisLinkApp : Application() {
    lateinit var container: AppContainer
        private set

    override fun onCreate() {
        super.onCreate()
        container = AppContainer(this)
    }
}

val Context.container: AppContainer get() = (applicationContext as ReminisLinkApp).container

/**
 * A pairing link waiting for its screen. [external]: opened from another
 * app, so the user must confirm before any network access.
 */
class PendingLink(val link: PairingLink?, val error: PairingLinkException?, val external: Boolean)

/**
 * Pairing links kept in memory only, behind an opaque token: the QR secret
 * never goes into navigation arguments or saved instance state. After a
 * process restart the token is unknown and the link must be scanned again.
 */
class PendingLinks {
    private val links = ConcurrentHashMap<String, PendingLink>()

    fun put(text: String, external: Boolean): String {
        val pending = try {
            PendingLink(PairingLink.parse(text), null, external)
        } catch (e: PairingLinkException) {
            PendingLink(null, e, external)
        }
        val token = UUID.randomUUID().toString()
        links[token] = pending
        return token
    }

    fun get(token: String): PendingLink? = links[token]

    fun remove(token: String) {
        links.remove(token)
    }

    /** Tests only: how many links are held. */
    @androidx.annotation.VisibleForTesting
    val size: Int get() = links.size
}

/** A notice shown once by the screen of a game ([gameId] of [deviceId]). */
data class GameNotice(val deviceId: String, val gameId: String, val text: String)

/** Long-lived objects of the app. */
class AppContainer(context: Context) {
    val identityStore = IdentityStore(context)
    val consoles = ConsoleStore(File(context.filesDir, "consoles.json"))
    val discovery = Discovery(context)
    val pendingLinks = PendingLinks()

    /** The box art of the games shown (the covers of the library and of the game screen). */
    val artCache = GameArtCache()

    /** Work that must outlive a screen: a pairing exchange once `POST /pair` is sent. */
    val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private val prefs = context.getSharedPreferences("settings", Context.MODE_PRIVATE)

    /** Shared files waiting for the Add screen. */
    val shareInbox = ShareInbox()

    /**
     * Local copies of the files to send (a directory per file, deleted once
     * its upload ends). The queue lives in memory: copies left by a previous
     * process are deleted at start.
     */
    val transferDirectory = File(context.filesDir, "transfers")

    private val transferScope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private val appContext: Context = context.applicationContext

    /** Tests only: the document of the last download asked from a screen (to read it back and delete it). */
    @androidx.annotation.VisibleForTesting
    @Volatile
    var lastDownloadDocumentForTests: android.net.Uri? = null

    /** Uploads and downloads of every console; a foreground service holds the process while it is busy. */
    val transfers = TransferQueue(
        transferScope,
        // Every time work is added or resumed (idempotent): no start depends on a busy flag's transitions.
        onActive = { TransferService.start(appContext) },
    ) { deviceId, refresh -> client(deviceId, refresh) }

    /** Jobs of the consoles (patches, pak installations), polled by the screens that show them. */
    val jobs = JobTracker({ deviceId -> pairingIdentity(deviceId) }) { deviceId -> client(deviceId) }

    /** The identity of the current pairing of a console (pin and pairing date), null when forgotten. */
    fun pairingIdentity(deviceId: String): Any? {
        demo.value?.let { if (it.deviceId == deviceId) return it.identity }
        return consoles.get(deviceId)?.let { it.pin to it.pairedAt }
    }

    // MARK: demo mode (M16)

    private val _demo = kotlinx.coroutines.flow.MutableStateFlow<link.reminis.core.demo.DemoConsole?>(null)

    /**
     * The demo console while the demo mode runs ("Try Without a Console"):
     * a fake pak in this process, with made-up games, reached without any
     * network and never written to the list of consoles.
     */
    val demo: kotlinx.coroutines.flow.StateFlow<link.reminis.core.demo.DemoConsole?> get() = _demo

    fun isDemo(deviceId: String): Boolean = _demo.value?.deviceId == deviceId

    /** Starts a new demo session (a previous one ends first) and returns its console. */
    fun startDemo(): link.reminis.core.demo.DemoConsole {
        quitDemo()
        // The real consoles are left alone while the demo runs (as on iOS): their transfers pause
        // and resume when the demo ends; discovery, probes and requests to them are off.
        transfers.holdForDemo { !isDemo(it) }
        var started: link.reminis.core.demo.DemoConsole? = null
        val console = link.reminis.core.demo.DemoConsole(
            appContext.getString(R.string.demo_console_name),
            // The demo's answers close its connection as a real console's do.
            errorObserver = { error -> started?.let { serverError(it.deviceId, error) } },
        ) { n -> appContext.getString(R.string.demo_cheat_name, n) }
        started = console
        // Under the pairing lock: no address of a real console is written once the demo has started.
        synchronized(pairingLock) { _demo.value = console }
        // Searches and probes of real consoles in flight stop now.
        resolutions.children.forEach { it.cancel() }
        return console
    }

    /** Ends the demo: its transfers and jobs are dropped, its fake pak stops, nothing is kept. */
    fun quitDemo() {
        val console = _demo.value ?: return
        _demo.value = null
        events.dropConsole(console.deviceId)
        transfers.dropConsole(console.deviceId)
        jobs.dropConsole(console.deviceId)
        pakInstaller.dropConsole(console.deviceId)
        screenshotManagers.remove(console.deviceId)
        console.close()
        // Exactly the transfers the demo paused, whatever their phase now; never one paused before it.
        transfers.releaseDemoHold()
    }

    /** The name shown for a console: the demo's, or the stored one. */
    fun consoleName(deviceId: String): String =
        _demo.value?.takeIf { it.deviceId == deviceId }?.pak?.deviceName ?: consoles.get(deviceId)?.deviceName.orEmpty()

    /**
     * The event stream of the console shown (M14). Started and stopped by
     * the activity (foreground) and the navigation (the console shown).
     */
    val events: ConsoleEvents by lazy {
        ConsoleEvents(
            scope = scope,
            pairingIdentity = { pairingIdentity(it) },
            client = { id, refresh -> client(id, refresh) },
            consoleName = { consoleName(it) },
            onClosed = { transfers.pauseConsole(it) },
            onJob = { id, job, identity -> jobs.apply(id, job, identity) },
            onLive = { id -> backgroundScope.launch { runCatching { jobs.refresh(id) } } },
        )
    }

    /**
     * Runs [block] only if [identity] is still the pairing of [deviceId],
     * under the lock that forgetting and replacing a pairing hold.
     */
    fun ifPairing(deviceId: String, identity: Any?, block: () -> Unit): Boolean = synchronized(pairingLock) {
        if (identity == null || pairingIdentity(deviceId) != identity) return@synchronized false
        block()
        true
    }

    /** Pak installations from a zip of the phone (M12); a local copy is deleted once its installation ends. */
    val pakInstaller = PakInstaller(transferScope, transfers, jobs, client = { client(it) }) { file -> file.parentFile?.deleteRecursively() }

    /** The last game deleted from this phone: the Library offers Empty Trash Now (iOS). */
    val lastDeletedGame = kotlinx.coroutines.flow.MutableStateFlow<GameNotice?>(null)

    /** A notice for the game screen of a console (a patch applied, the screen closed by itself). */
    val gameNotice = kotlinx.coroutines.flow.MutableStateFlow<GameNotice?>(null)

    /** The library of a console changed from this phone (a game deleted, restored or patched): its screens reload. */
    val libraryChanged = kotlinx.coroutines.flow.MutableSharedFlow<String>(extraBufferCapacity = 16)

    /** The background scope of the app (cleanups that outlive a screen). */
    val backgroundScope: CoroutineScope get() = transferScope

    /** The trash of a console changed from another screen (screenshots moved): its screen reloads. */
    val trashChanged = kotlinx.coroutines.flow.MutableSharedFlow<String>(extraBufferCapacity = 16)

    private val screenshotManagers = ConcurrentHashMap<String, ScreenshotManager>()

    /** M22: the screenshots of a console (one manager per console, shared by its screens). */
    fun screenshots(deviceId: String): ScreenshotManager = screenshotManagers.getOrPut(deviceId) {
        ScreenshotManager(appContext, deviceId, scope, client = { client(deviceId) }, onTrashChanged = { trashChanged.tryEmit(deviceId) })
    }

    init {
        // Listed now, before any new copy can be made; deleted in the background.
        val leftovers = transferDirectory.listFiles().orEmpty()
        transferScope.launch(Dispatchers.IO) { leftovers.forEach { it.deleteRecursively() } }
        // Copies made for the share sheet more than an hour ago, and the temporary files of a
        // screenshot being shown when a previous process died (M22).
        // Listed now, before any new folder can be made (Codex review round 2); deleted in the background.
        val stale = ScreenshotManager.staleShares(appContext) + ScreenshotManager.leftoverPreviews(appContext)
        transferScope.launch(Dispatchers.IO) { stale.forEach { it.deleteRecursively() } }
        // M22: a `screenshots` event (or a new stream) reloads the screenshots of that console.
        scope.launch {
            events.changes.collect { change ->
                if (change is ConsoleChange.Screenshots || change is ConsoleChange.CatchUp) screenshotManagers[change.deviceId]?.changed()
                // An art may have been set or removed by any client.
                if (change is ConsoleChange.Library || change is ConsoleChange.CatchUp) artCache.invalidate(change.deviceId)
            }
        }
        scope.launch { libraryChanged.collect { artCache.invalidate(it) } }
    }

    /** `clientName` of `POST /pair`: typed by the user, the device model by default. */
    var clientName: String
        get() = prefs.getString("clientName", null) ?: defaultClientName
        set(value) {
            prefs.edit().putString("clientName", value).apply()
        }

    val defaultClientName: String get() = Build.MODEL?.takeIf { it.isNotBlank() } ?: "Android"

    private val clients = ConcurrentHashMap<String, ApiClient>()
    private val lock = Mutex()

    /**
     * Guards the cache and the pairing entries against a connection that was
     * started with a pairing replaced or forgotten while it waited on the
     * network: such a connection never saves its address nor caches its client.
     */
    private val pairingLock = Any()

    /**
     * Tests only: runs on the network interceptor thread right before the
     * commit point of `POST /pair` (blocking it holds the request unwritten).
     */
    @androidx.annotation.VisibleForTesting
    @Volatile
    var pairingGateForTests: (() -> Unit)? = null

    /**
     * Probe generation of each console (device id): a `/hello` probe saves its
     * address only if no newer probe of the same console started meanwhile,
     * checked and saved under this object's lock.
     */
    private val probes = Generations<String>()

    fun newClient(endpoint: Endpoint, deviceId: String, pin: String, credentials: DeviceCredentials? = null) = ApiClient(
        endpoint = endpoint,
        expectedDeviceId = deviceId,
        credentials = credentials,
        pin = pin,
        // Only a paired client's answers (not those of a pairing in progress) close the connection.
        errorObserver = if (credentials != null) ({ error -> serverError(deviceId, error) }) else null,
    )

    /**
     * Every error answer of a console's client (a screen, a transfer, a job;
     * M23 on Android, as iOS observes them): `503 shutting_down` means the
     * console closed ReminisLink, `401 disconnected` that it disconnected
     * this phone; either closes the connection (its transfers pause).
     * Called on the thread that read the answer.
     */
    private fun serverError(deviceId: String, error: link.reminis.core.ApiException.Server) {
        when (error.code) {
            link.reminis.core.ErrorCodes.DISCONNECTED -> {
                val seen = events.reconnection(deviceId)
                scope.launch {
                    if (events.disconnectedByRequest(deviceId, error, seen)) transfers.pauseConsole(deviceId)
                }
            }
            link.reminis.core.ErrorCodes.SHUTTING_DOWN -> scope.launch {
                if (pairingIdentity(deviceId) != null) events.closedSeenByRequest(deviceId)
            }
        }
    }

    /**
     * A signed client for a paired console, connected (`/hello` answered with
     * the pinned certificate). mDNS for 3 s, then the last validated address;
     * each candidate is tried with the same pin.
     */
    suspend fun client(deviceId: String, refresh: Boolean = false): ApiClient = lock.withLock {
        _demo.value?.let { if (it.deviceId == deviceId) return it.client }
        noDemo()
        if (!refresh) clients[deviceId]?.let { return it }
        val console = consoles.get(deviceId) ?: throw ApiException.NotPaired()
        // The demo may start during any of the suspensions below: checked again after each one,
        // and the whole resolution is cancelled when it starts ([resolving]).
        val (endpoint, client, hello) = resolving {
            val identity = withContext(Dispatchers.IO) { identityStore.identity() }
            noDemo()
            val credentials = DeviceCredentials(deviceId, identity.clientId, identity.key, console.pin)
            val candidates = Locator.candidates(console.lastEndpoint) { discovery.find(deviceId).also { noDemo() } }
            val (endpoint, connected) = Locator.connectFirst(candidates) { ep ->
                noDemo()
                val client = newClient(ep, deviceId, console.pin, credentials)
                client to client.hello()
            }
            noDemo()
            Triple(endpoint, connected.first, connected.second)
        }
        withContext(Dispatchers.IO) {
            synchronized(pairingLock) {
                // The pairing may have been replaced or forgotten during the network calls,
                // or the demo started: nothing is written then.
                noDemo()
                if (!isCurrent(console)) throw ApiException.NotPaired()
                remember(console, endpoint, hello.deviceName, hello.model)
                clients[deviceId] = client
            }
        }
        client
    }

    /** The demo mode never reaches a real console (its paused transfers wait for the end of the demo). */
    private fun noDemo() {
        if (_demo.value != null) throw ApiException.Network(java.io.IOException("demo mode: real consoles are not contacted"))
    }

    /** Parent of the network resolutions of real consoles: cancelled all at once when the demo starts. */
    private val resolutions = kotlinx.coroutines.SupervisorJob()

    /**
     * Runs [block] (a resolution: mDNS, `/hello`) so that starting the demo
     * cancels it, while the caller's own cancellation still applies. A
     * resolution cut by the demo ends as a network failure.
     */
    private suspend fun <T> resolving(block: suspend () -> T): T = kotlinx.coroutines.coroutineScope {
        val child = kotlinx.coroutines.Job(resolutions)
        val watcher = launch {
            try {
                kotlinx.coroutines.awaitCancellation()
            } finally {
                child.cancel()
            }
        }
        try {
            withContext(child) { block() }
        } catch (e: kotlinx.coroutines.CancellationException) {
            if (child.isCancelled && coroutineContext[kotlinx.coroutines.Job]?.isActive == true) throw ApiException.Network(java.io.IOException("demo mode started"))
            throw e
        } finally {
            watcher.cancel()
            child.complete()
        }
    }

    /** Whether [console] (as read before a network call) is still the stored pairing. */
    private fun isCurrent(console: PairedConsole): Boolean {
        val now = consoles.get(console.deviceId) ?: return false
        return now.pin == console.pin && now.pairedAt == console.pairedAt
    }

    /**
     * Whether the console answers `/hello` at [endpoint] with its pinned
     * certificate (for the Online state). A probe superseded by a newer probe
     * of the same console answers false and saves nothing, so an older
     * address finishing late never overwrites the newer one.
     */
    suspend fun validates(deviceId: String, endpoint: Endpoint): Boolean {
        // No probe of a real console during the demo, and no address written.
        if (_demo.value != null) return false
        val console = consoles.get(deviceId) ?: return false
        val probe = probes.next(deviceId)
        return try {
            val hello = resolving { newClient(endpoint, deviceId, console.pin).hello() }
            withContext(Dispatchers.IO) {
                // Still the newest probe and the same pairing, checked and saved atomically.
                probes.ifCurrent(deviceId, probe) {
                    synchronized(pairingLock) {
                        if (_demo.value != null || !isCurrent(console)) return@synchronized false
                        remember(console, endpoint, hello.deviceName, hello.model)
                        true
                    }
                } ?: false
            }
        } catch (e: kotlinx.coroutines.CancellationException) {
            throw e
        } catch (_: Exception) {
            false
        }
    }

    /** Stores a new pairing, replacing any entry of the same console in one atomic write. */
    fun replacePairing(console: PairedConsole) = synchronized(pairingLock) {
        consoles.put(console)
        clients.remove(console.deviceId)
        // The jobs of the previous pairing are never asked through the new one.
        jobs.dropConsole(console.deviceId)
        pakInstaller.dropConsole(console.deviceId)
        screenshotManagers.remove(console.deviceId)
        // The event stream of the previous pairing stops (it also checks the identity itself).
        scope.launch { events.dropConsole(console.deviceId) }
    }

    /** Renames a console on this phone only (the console keeps its own name). */
    fun renameConsole(deviceId: String, name: String) {
        val clean = PairedConsole.cleanName(name) ?: return
        synchronized(pairingLock) { consoles.update(deviceId) { it.copy(deviceName = clean) } }
    }

    /** Tests only: deletion previews answered (applied or not), a client-side signal. */
    @androidx.annotation.VisibleForTesting
    val deletionAnswersForTests = java.util.concurrent.atomic.AtomicInteger()

    /**
     * Forgets a console on this phone. Its transfers stop and never start
     * again (a running one may hold a client already obtained); an upload
     * whose `complete` is in flight keeps its real outcome.
     */
    fun forget(deviceId: String) {
        if (isDemo(deviceId)) {
            quitDemo()
            return
        }
        synchronized(pairingLock) {
            consoles.remove(deviceId)
            clients.remove(deviceId)
            jobs.dropConsole(deviceId)
        }
        // Only now: the stream is re-evaluated without a pairing, so none can start again.
        scope.launch { events.dropConsole(deviceId) }
        transfers.dropConsole(deviceId)
        pakInstaller.dropConsole(deviceId)
        screenshotManagers.remove(deviceId)
    }

    /**
     * Keeps the console's own name (matched by id, never by name) and the
     * address that answered, only on the same pairing (held under [pairingLock]).
     */
    private fun remember(console: PairedConsole, endpoint: Endpoint, deviceName: String, model: String) {
        consoles.update(console.deviceId) {
            if (it.pin == console.pin && it.pairedAt == console.pairedAt) {
                it.reporting(deviceName).copy(model = model, lastEndpoint = endpoint)
            } else {
                it
            }
        }
    }
}
