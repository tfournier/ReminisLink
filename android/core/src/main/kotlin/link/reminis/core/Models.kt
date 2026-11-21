package link.reminis.core

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement

// Models of protocol/openapi.yaml for the routes used by the app. Decoding
// ignores unknown fields (docs/specs/03-protocole.md §9), and the
// enumerations of the contract are kept as strings: an unknown value never
// fails a whole response. Dates stay RFC 3339 strings (parsed for display).

val ProtocolJson = Json {
    ignoreUnknownKeys = true
    explicitNulls = false
    encodeDefaults = true
}

/** Host and port of a pak on the local network. */
@Serializable
data class Endpoint(val host: String, val port: Int) {
    /** `host:port`, with brackets for IPv6. */
    val authority: String get() = if (':' in host) "[$host]:$port" else "$host:$port"

    override fun toString(): String = authority
}

/** Handheld models (`Model`); product names are not translated. */
object DeviceModels {
    fun displayName(model: String): String = when (model) {
        "brick" -> "TrimUI Brick"
        "brickpro" -> "TrimUI Brick Pro"
        "smartpro" -> "TrimUI Smart Pro"
        "smartpros" -> "TrimUI Smart Pro S"
        "h700" -> "H700"
        "my355" -> "MY355"
        "desktop" -> "Desktop"
        else -> model
    }
}

/** `GET /hello` (unsigned). */
@Serializable
data class Hello(
    val deviceId: String,
    val deviceName: String,
    val model: String,
    val platform: String,
    val os: String,
    val osVersion: String? = null,
    val pakVersion: String,
    val apiVersion: Int,
    val minClientVersion: String,
    val serverTime: String,
    val pairingOpen: Boolean,
)

/** `GET /device`. */
@Serializable
data class DeviceStatus(
    val deviceId: String,
    val deviceName: String,
    val model: String,
    val platform: String,
    val os: String,
    val osVersion: String? = null,
    val pakVersion: String,
    val network: Network,
    val battery: Battery? = null,
    val storage: List<Storage>,
    val uptimeSeconds: Long,
    /** Required by the contract, read leniently: missing means none. */
    val connectedClients: List<ConnectedClient> = emptyList(),
) {
    @Serializable
    data class Network(val ipv4: String, val ssid: String? = null)

    @Serializable
    data class Battery(val percent: Int, val charging: Boolean)

    @Serializable
    data class Storage(val id: String, val label: String, val totalBytes: Long, val freeBytes: Long) {
        val usedBytes: Long get() = (totalBytes - freeBytes).coerceAtLeast(0)
    }
}

@Serializable
data class ConnectedClient(val clientName: String, val lastSeenAt: String)

/** One system of `GET /systems`. */
@Serializable
data class GameSystem(
    val id: String,
    val label: String,
    val folder: String,
    val tag: String,
    val gameCount: Int,
    /** A pak that does not send the field is taken as having the emulator. */
    val emulatorInstalled: Boolean = true,
)

@Serializable
data class SystemList(val systems: List<GameSystem>)

/** A game of `GET /games` (M11): a game of a folder listing, with its system and folder. */
@Serializable
data class LibraryGame(
    val id: String,
    val name: String,
    val fileName: String,
    val kind: String,
    val sizeBytes: Long,
    val modifiedAt: String,
    val hasSave: Boolean,
    val stateCount: Int,
    val lockedStateCount: Int = 0,
    val cheatCount: Int,
    val hasArt: Boolean = false,
    val systemId: String,
    val folderId: String? = null,
    val folderPath: String? = null,
)

/** `GET /games` (M11): at most 20 000 games; beyond, `truncated` is true. */
@Serializable
data class AllGames(val games: List<LibraryGame>, val truncated: Boolean, val generatedAt: String)

/** Roles of the files of a game. */
object GameFileRoles {
    const val ROM = "rom"
    const val SAVE = "save"
    const val RTC = "rtc"
    const val STATE = "state"
    const val CHEATS = "cheats"
    const val ART = "art"
}

/** A file of a game; `path` is informative only and never sent back. */
@Serializable
data class GameFile(
    val fileId: String? = null,
    val role: String,
    val slot: Int? = null,
    val path: String,
    val sizeBytes: Long,
    val modifiedAt: String,
) {
    val name: String get() = path.substringAfterLast('/')
}

/** `GET /games/{gameId}`. */
@Serializable
data class GameDetail(
    val id: String,
    val systemId: String,
    val name: String,
    val fileName: String,
    val kind: String,
    val files: List<GameFile>,
    val totalBytes: Long,
) {
    val hasSave: Boolean get() = files.any { it.role == GameFileRoles.SAVE }
    val stateCount: Int get() = files.count { it.role == GameFileRoles.STATE }
    val cheatFileCount: Int get() = files.count { it.role == GameFileRoles.CHEATS }
}

/** `{ "error": { "code", "message", "details" } }`. */
@Serializable
data class ErrorEnvelope(val error: Body) {
    @Serializable
    data class Body(val code: String, val message: String, val details: Map<String, JsonElement>? = null)
}

/** Compares `pakVersion`-style semantic versions ("1.2.3", optional pre-release ignored). */
object AppVersion {
    fun isSupported(app: String, minimum: String): Boolean = compare(app, minimum) >= 0

    fun compare(a: String, b: String): Int {
        fun parts(v: String) = v.removePrefix("v").substringBefore('-').substringBefore('+')
            .split('.').map { it.toIntOrNull() ?: 0 }
        val x = parts(a)
        val y = parts(b)
        for (i in 0 until maxOf(x.size, y.size)) {
            val d = x.getOrElse(i) { 0 }.compareTo(y.getOrElse(i) { 0 })
            if (d != 0) return d
        }
        return 0
    }
}
