package link.reminis.core

import kotlinx.serialization.SerializationException
import kotlinx.serialization.Serializable

// Events of `GET /events` (M14, docs/specs/04-api-v1.md "GET /events",
// protocol/openapi.yaml). Mirror of `PakEvent.swift`.

/** `hello`: the first event of a stream. */
@Serializable
data class EventHello(val serverTime: String, val keepAliveSeconds: Int = 15)

/** `shutdown`: the pak starts closing; the stream ends right after. */
@Serializable
data class EventShutdown(
    /** `quit` (left from the console) or `stopped` (power off, signal); another value from a newer pak is kept. */
    val reason: String,
)

/** `device`: the battery, the connected clients or the free space changed. */
@Serializable
data class EventDevice(
    val battery: DeviceStatus.Battery? = null,
    val connectedClients: List<ConnectedClient>,
    val storage: List<DeviceStatus.Storage>,
)

/** `library`: the SD card changed through the API (any client). */
@Serializable
data class EventLibrary(
    /** The systems whose games changed; empty when unknown (everything). */
    val systemIds: List<String> = emptyList(),
) {
    /** The systems to refresh, null for all of them. */
    val affectedSystems: Set<String>? get() = if (systemIds.isEmpty()) null else systemIds.toSet()
}

/** A typed event of `GET /events`. */
sealed interface PakEvent {
    data class Hello(val hello: EventHello) : PakEvent
    data class Shutdown(val shutdown: EventShutdown) : PakEvent
    data class Device(val device: EventDevice) : PakEvent
    data class Library(val library: EventLibrary) : PakEvent
    data class JobChanged(val job: Job) : PakEvent

    /** M12: the installed paks changed (installed, deleted or restored by any client): reload `GET /paks`. */
    data object Paks : PakEvent

    /** M22: screenshots went to the trash or came back (any client): reload `GET /screenshots`. */
    data object Screenshots : PakEvent

    /** A type this app does not know (a newer pak): ignored. */
    data class Unknown(val type: String) : PakEvent

    companion object {
        /** The event, or null when a known type carries unreadable data (then ignored, as an unknown one). */
        fun parse(event: SseEvent): PakEvent? = try {
            when (event.type) {
                "hello" -> Hello(ProtocolJson.decodeFromString(event.data))
                "shutdown" -> Shutdown(ProtocolJson.decodeFromString(event.data))
                "device" -> Device(ProtocolJson.decodeFromString(event.data))
                "library" -> Library(ProtocolJson.decodeFromString(event.data))
                "job" -> JobChanged(ProtocolJson.decodeFromString(event.data))
                "paks" -> {
                    // `{}` today; anything that is not a JSON object is unreadable.
                    ProtocolJson.decodeFromString<kotlinx.serialization.json.JsonObject>(event.data)
                    Paks
                }
                "screenshots" -> {
                    ProtocolJson.decodeFromString<kotlinx.serialization.json.JsonObject>(event.data)
                    Screenshots
                }
                else -> Unknown(event.type)
            }
        } catch (_: SerializationException) {
            null
        } catch (_: IllegalArgumentException) {
            null
        }
    }
}

/** This status with the fields of a `device` event. */
fun DeviceStatus.updated(event: EventDevice): DeviceStatus =
    copy(battery = event.battery, connectedClients = event.connectedClients, storage = event.storage)

/**
 * Whether a failure means the pak closed (M11, mirror of `PakClosure.swift`).
 */
object PakClosure {
    /** How long the `/hello` probe may take (sent once, never retried). */
    const val PROBE_TIMEOUT_MILLIS = 3_000L

    /**
     * The failure of the `/hello` probe sent after the event stream ended
     * without `shutdown`: `503 shutting_down`, or no connection (refused,
     * lost, timed out). Any answer, even an error, means the pak is still there.
     */
    fun probeMeansClosed(error: Throwable): Boolean = when (error) {
        is ApiException.Server -> error.code == ErrorCodes.SHUTTING_DOWN
        is ApiException.Network -> true
        else -> false
    }
}
