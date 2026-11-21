package link.reminis.app

import android.content.Context
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import android.util.Log
import java.net.Inet4Address
import kotlin.coroutines.resume
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.channels.awaitClose
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.callbackFlow
import kotlinx.coroutines.flow.firstOrNull
import kotlinx.coroutines.launch
import kotlinx.coroutines.suspendCancellableCoroutine
import link.reminis.core.Endpoint

/** A pak seen on the network (docs/specs/03-protocole.md §2). Only `id` identifies it, never the name or the address. */
data class DiscoveredConsole(val deviceId: String, val serviceName: String, val endpoint: Endpoint)

/** What mDNS reports. An announcement is not authenticated: the app validates it with `/hello` and the pin. */
sealed interface DiscoveryEvent {
    data class Found(val console: DiscoveredConsole) : DiscoveryEvent
    data class Lost(val serviceName: String) : DiscoveryEvent
}

/**
 * mDNS / DNS-SD discovery of `_reminislink._tcp` with the platform's NSD
 * (no Google Play Services). Each found service is resolved, one at a time
 * (NsdManager refuses parallel resolutions before API 34), to read its TXT
 * record and address.
 */
class Discovery(context: Context) {
    private val nsd = context.getSystemService(Context.NSD_SERVICE) as NsdManager

    /** Consoles as they are resolved, and services as they disappear, while collected. */
    fun events(): Flow<DiscoveryEvent> = callbackFlow {
        val pending = Channel<Pair<NsdServiceInfo, Long>>(Channel.UNLIMITED)
        // Generation of each service name, bumped by Found and Lost: a
        // resolution that finishes after its service was lost or announced
        // again is dropped (NSD callbacks and the resolver run on other threads).
        val generations = java.util.concurrent.ConcurrentHashMap<String, Long>()
        fun bump(name: String): Long = generations.merge(name, 1L, Long::plus)!!
        val listener = object : NsdManager.DiscoveryListener {
            override fun onDiscoveryStarted(serviceType: String) = Unit
            override fun onDiscoveryStopped(serviceType: String) = Unit
            override fun onServiceFound(info: NsdServiceInfo) {
                pending.trySend(info to bump(info.serviceName))
            }
            override fun onServiceLost(info: NsdServiceInfo) {
                bump(info.serviceName)
                trySend(DiscoveryEvent.Lost(info.serviceName))
            }
            override fun onStartDiscoveryFailed(serviceType: String, errorCode: Int) {
                Log.w(TAG, "mDNS discovery failed: $errorCode")
                close()
            }
            override fun onStopDiscoveryFailed(serviceType: String, errorCode: Int) = Unit
        }
        launch {
            for ((info, generation) in pending) {
                if (generations[info.serviceName] != generation) continue
                val resolved = resolve(info) ?: continue
                val console = toConsole(resolved) ?: continue
                if (generations[info.serviceName] == generation) trySend(DiscoveryEvent.Found(console))
            }
        }
        nsd.discoverServices(SERVICE_TYPE, NsdManager.PROTOCOL_DNS_SD, listener)
        awaitClose {
            pending.close()
            runCatching { nsd.stopServiceDiscovery(listener) }
        }
    }

    /** The address of the console with this id, as soon as mDNS resolves it; never returns while not found. */
    suspend fun find(deviceId: String): Endpoint? = events()
        .firstOrNull { it is DiscoveryEvent.Found && it.console.deviceId == deviceId }
        ?.let { (it as DiscoveryEvent.Found).console.endpoint }

    @Suppress("DEPRECATION") // resolveService: still the only API before 34, works on 34+.
    private suspend fun resolve(info: NsdServiceInfo): NsdServiceInfo? = suspendCancellableCoroutine { cont ->
        nsd.resolveService(
            info,
            object : NsdManager.ResolveListener {
                override fun onResolveFailed(serviceInfo: NsdServiceInfo, errorCode: Int) {
                    if (cont.isActive) cont.resume(null)
                }

                override fun onServiceResolved(serviceInfo: NsdServiceInfo) {
                    if (cont.isActive) cont.resume(serviceInfo)
                }
            },
        )
    }

    @Suppress("DEPRECATION") // host: hostAddresses needs API 34.
    private fun toConsole(info: NsdServiceInfo): DiscoveredConsole? {
        val id = info.attributes["id"]?.let { String(it, Charsets.US_ASCII) } ?: return null
        val host = info.host as? Inet4Address ?: return null
        return DiscoveredConsole(id, info.serviceName, Endpoint(host.hostAddress ?: return null, info.port))
    }

    companion object {
        private const val TAG = "ReminisLink"
        const val SERVICE_TYPE = "_reminislink._tcp"
    }
}
