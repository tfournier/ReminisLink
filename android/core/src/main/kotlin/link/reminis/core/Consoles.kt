package link.reminis.core

import java.io.File
import java.io.FileOutputStream
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer

/**
 * A paired console, as the app keeps it. Nothing secret: the pin is the hash
 * of a public key, and the signing key lives in the Android Keystore.
 */
@Serializable
data class PairedConsole(
    val deviceId: String,
    /** The console's own name (`deviceName` of `/hello`, `/device` or `/pair`). */
    val deviceName: String,
    val model: String? = null,
    /** b64u(SHA-256(SPKI)) of the console certificate, from the QR code. */
    val pin: String,
    /** The last address that answered with the pinned certificate. */
    val lastEndpoint: Endpoint? = null,
    val pairedAt: Long,
)

/**
 * The list of paired consoles in one JSON file, written atomically (write to
 * `<file>.tmp`, sync, rename), like every write of the project.
 */
class ConsoleStore(private val file: File) {
    private val lock = Any()
    private val serializer = ListSerializer(PairedConsole.serializer())

    fun all(): List<PairedConsole> = synchronized(lock) {
        if (!file.exists()) return emptyList()
        runCatching { ProtocolJson.decodeFromString(serializer, file.readText()) }.getOrDefault(emptyList())
    }

    fun get(deviceId: String): PairedConsole? = all().firstOrNull { it.deviceId == deviceId }

    /** Adds or replaces the console with the same `deviceId`. */
    fun put(console: PairedConsole) = synchronized(lock) {
        write(all().filter { it.deviceId != console.deviceId } + console)
    }

    fun update(deviceId: String, change: (PairedConsole) -> PairedConsole) = synchronized(lock) {
        val list = all()
        if (list.none { it.deviceId == deviceId }) return
        write(list.map { if (it.deviceId == deviceId) change(it) else it })
    }

    fun remove(deviceId: String) = synchronized(lock) {
        write(all().filter { it.deviceId != deviceId })
    }

    private fun write(list: List<PairedConsole>) {
        file.parentFile?.mkdirs()
        val tmp = File(file.path + ".tmp")
        FileOutputStream(tmp).use { out ->
            out.write(ProtocolJson.encodeToString(serializer, list).toByteArray(Charsets.UTF_8))
            out.fd.sync()
        }
        if (!tmp.renameTo(file)) {
            tmp.delete()
            throw java.io.IOException("cannot replace ${file.name}")
        }
    }
}

/**
 * Finds the address of a console (docs/specs/06-app-ios.md §4.2, same rule on
 * Android): mDNS (NSD) with its `id` for 3 s, then the fallback address (the
 * QR code's `h`:`p` when pairing, the last validated address afterwards).
 * Either way the connection is pinned, so a fake pak cannot answer.
 */
object Locator {
    const val DISCOVERY_TIMEOUT_MS = 3_000L

    suspend fun locate(
        fallback: Endpoint?,
        timeoutMs: Long = DISCOVERY_TIMEOUT_MS,
        find: suspend () -> Endpoint?,
    ): Endpoint? = withTimeoutOrNull(timeoutMs) { find() } ?: fallback
}
