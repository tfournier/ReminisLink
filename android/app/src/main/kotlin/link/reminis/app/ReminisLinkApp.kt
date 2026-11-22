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
    val jobs = JobTracker { deviceId -> client(deviceId) }

    /** Pak installations from a zip of the phone (M12); a local copy is deleted once its installation ends. */
    val pakInstaller = PakInstaller(transferScope, transfers, jobs) { file -> file.parentFile?.deleteRecursively() }

    /** A notice for the game screen of a console (a patch applied, the screen closed by itself). */
    val gameNotice = kotlinx.coroutines.flow.MutableStateFlow<GameNotice?>(null)

    /** The library of a console changed from this phone (a game deleted, restored or patched): its screens reload. */
    val libraryChanged = kotlinx.coroutines.flow.MutableSharedFlow<String>(extraBufferCapacity = 16)

    /** The background scope of the app (cleanups that outlive a screen). */
    val backgroundScope: CoroutineScope get() = transferScope

    init {
        // Listed now, before any new copy can be made; deleted in the background.
        val leftovers = transferDirectory.listFiles().orEmpty()
        transferScope.launch(Dispatchers.IO) { leftovers.forEach { it.deleteRecursively() } }
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
    )

    /**
     * A signed client for a paired console, connected (`/hello` answered with
     * the pinned certificate). mDNS for 3 s, then the last validated address;
     * each candidate is tried with the same pin.
     */
    suspend fun client(deviceId: String, refresh: Boolean = false): ApiClient = lock.withLock {
        if (!refresh) clients[deviceId]?.let { return it }
        val console = consoles.get(deviceId) ?: throw ApiException.NotPaired()
        val identity = withContext(Dispatchers.IO) { identityStore.identity() }
        val credentials = DeviceCredentials(deviceId, identity.clientId, identity.key, console.pin)
        val candidates = Locator.candidates(console.lastEndpoint) { discovery.find(deviceId) }
        val (endpoint, connected) = Locator.connectFirst(candidates) { ep ->
            val client = newClient(ep, deviceId, console.pin, credentials)
            client to client.hello()
        }
        val (client, hello) = connected
        withContext(Dispatchers.IO) {
            synchronized(pairingLock) {
                // The pairing may have been replaced or forgotten during the network calls.
                if (!isCurrent(console)) throw ApiException.NotPaired()
                remember(console, endpoint, hello.deviceName, hello.model)
                clients[deviceId] = client
            }
        }
        client
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
        val console = consoles.get(deviceId) ?: return false
        val probe = probes.next(deviceId)
        return try {
            val hello = newClient(endpoint, deviceId, console.pin).hello()
            withContext(Dispatchers.IO) {
                // Still the newest probe and the same pairing, checked and saved atomically.
                probes.ifCurrent(deviceId, probe) {
                    synchronized(pairingLock) {
                        if (!isCurrent(console)) return@synchronized false
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
    }

    /**
     * Forgets a console on this phone. Its transfers stop and never start
     * again (a running one may hold a client already obtained); an upload
     * whose `complete` is in flight keeps its real outcome.
     */
    fun forget(deviceId: String) {
        synchronized(pairingLock) {
            consoles.remove(deviceId)
            clients.remove(deviceId)
        }
        transfers.dropConsole(deviceId)
        jobs.dropConsole(deviceId)
        pakInstaller.dropConsole(deviceId)
    }

    /**
     * Keeps the console's own name (matched by id, never by name) and the
     * address that answered, only on the same pairing (held under [pairingLock]).
     */
    private fun remember(console: PairedConsole, endpoint: Endpoint, deviceName: String, model: String) {
        consoles.update(console.deviceId) {
            if (it.pin == console.pin && it.pairedAt == console.pairedAt) {
                it.copy(deviceName = deviceName, model = model, lastEndpoint = endpoint)
            } else {
                it
            }
        }
    }
}
