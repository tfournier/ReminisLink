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
}

/** Long-lived objects of the app. */
class AppContainer(context: Context) {
    val identityStore = IdentityStore(context)
    val consoles = ConsoleStore(File(context.filesDir, "consoles.json"))
    val discovery = Discovery(context)
    val pendingLinks = PendingLinks()

    /** Work that must outlive a screen: a pairing exchange once `POST /pair` is sent. */
    val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private val prefs = context.getSharedPreferences("settings", Context.MODE_PRIVATE)

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

    /** Tests only: suspends a pairing after the hand-off, before `POST /pair` may be written. */
    @androidx.annotation.VisibleForTesting
    @Volatile
    var pairingGateForTests: (suspend () -> Unit)? = null

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

    /** Whether the console answers `/hello` at [endpoint] with its pinned certificate (for the Online state). */
    suspend fun validates(deviceId: String, endpoint: Endpoint): Boolean {
        val console = consoles.get(deviceId) ?: return false
        return try {
            val hello = newClient(endpoint, deviceId, console.pin).hello()
            withContext(Dispatchers.IO) {
                synchronized(pairingLock) {
                    if (!isCurrent(console)) return@withContext false
                    remember(console, endpoint, hello.deviceName, hello.model)
                    true
                }
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

    fun forget(deviceId: String) = synchronized(pairingLock) {
        consoles.remove(deviceId)
        clients.remove(deviceId)
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
