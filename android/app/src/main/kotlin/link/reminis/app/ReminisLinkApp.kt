package link.reminis.app

import android.app.Application
import android.content.Context
import android.os.Build
import java.io.File
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

class ReminisLinkApp : Application() {
    lateinit var container: AppContainer
        private set

    override fun onCreate() {
        super.onCreate()
        container = AppContainer(this)
    }
}

val Context.container: AppContainer get() = (applicationContext as ReminisLinkApp).container

/** The console could not be found by mDNS nor at its fallback address. */
class ConsoleNotFoundException : Exception("console not found")

/** Long-lived objects of the app. */
class AppContainer(context: Context) {
    val identityStore = IdentityStore(context)
    val consoles = ConsoleStore(File(context.filesDir, "consoles.json"))
    val discovery = Discovery(context)
    /** Work that must outlive a screen (a pairing exchange). */
    val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private val prefs = context.getSharedPreferences("settings", Context.MODE_PRIVATE)

    /** `clientName` of `POST /pair`: typed by the user, the device model by default. */
    var clientName: String
        get() = prefs.getString("clientName", null) ?: defaultClientName
        set(value) {
            prefs.edit().putString("clientName", value).apply()
        }

    val defaultClientName: String get() = Build.MODEL?.takeIf { it.isNotBlank() } ?: "Android"

    private val clients = HashMap<String, ApiClient>()
    private val lock = Mutex()

    fun newClient(endpoint: Endpoint, deviceId: String, pin: String, credentials: DeviceCredentials? = null) = ApiClient(
        endpoint = endpoint,
        expectedDeviceId = deviceId,
        credentials = credentials,
        pin = pin,
        appVersion = BuildConfig.CLIENT_PROTOCOL_VERSION,
    )

    /**
     * A signed client for a paired console, connected (`/hello` answered with
     * the pinned certificate). mDNS for 3 s, then the last validated address.
     */
    suspend fun client(deviceId: String, refresh: Boolean = false): ApiClient = lock.withLock {
        if (!refresh) clients[deviceId]?.let { return it }
        val console = consoles.get(deviceId) ?: throw ApiException.NotPaired()
        val identity = withContext(Dispatchers.IO) { identityStore.identity() }
        val credentials = DeviceCredentials(deviceId, identity.clientId, identity.key, console.pin)
        val found = Locator.locate(fallback = null) { discovery.find(deviceId) }
        val candidates = listOfNotNull(found, console.lastEndpoint).distinct()
        if (candidates.isEmpty()) throw ConsoleNotFoundException()
        var failure: Exception = ConsoleNotFoundException()
        for (endpoint in candidates) {
            val client = newClient(endpoint, deviceId, console.pin, credentials)
            try {
                val hello = client.hello()
                remember(console, endpoint, hello.deviceName, hello.model)
                clients[deviceId] = client
                return client
            } catch (e: ApiException.Network) {
                failure = e
            }
        }
        throw failure
    }

    fun forget(deviceId: String) {
        consoles.remove(deviceId)
        clients.remove(deviceId)
    }

    /** Keeps the console's own name (matched by id, never by name) and the address that answered. */
    private suspend fun remember(console: PairedConsole, endpoint: Endpoint, deviceName: String, model: String) =
        withContext(Dispatchers.IO) {
            consoles.update(console.deviceId) { it.copy(deviceName = deviceName, model = model, lastEndpoint = endpoint) }
        }
}
