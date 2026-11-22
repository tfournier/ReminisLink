package link.reminis.app

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import link.reminis.core.ApiClient
import link.reminis.core.ApiException
import link.reminis.core.ErrorCodes
import link.reminis.core.EventDevice
import link.reminis.core.EventStream
import link.reminis.core.EventStreamTiming
import link.reminis.core.PakClosure
import link.reminis.core.PakEvent

/** What a console's event stream changed, for the screens of that console. */
sealed interface ConsoleChange {
    val deviceId: String

    /** The SD card changed (`library`, merged for [ConsoleEvents.libraryDebounceMillis]); [systems] null: everything. */
    data class Library(override val deviceId: String, val systems: Set<String>?) : ConsoleChange {
        /** Whether the change may concern [systemId] (an unknown system or change: yes). */
        fun affects(systemId: String?): Boolean = systemId == null || systems == null || systemId in systems
    }

    /** The installed paks changed (`paks`). */
    data class Paks(override val deviceId: String) : ConsoleChange

    /**
     * A `hello`: the stream (re)opened. The pak replays nothing: whatever
     * happened before (background, another screen, a slow opening) is
     * unknown, so every screen of the console reloads.
     */
    data class CatchUp(override val deviceId: String) : ConsoleChange

    /** `device`: battery, connected clients or storage. */
    data class Device(override val deviceId: String, val device: EventDevice) : ConsoleChange

    /** The connection is gone (35 s without a byte, the console forgot or disconnected this phone, another certificate). */
    data class Lost(override val deviceId: String, val error: Throwable) : ConsoleChange
}

/** The console closed ReminisLink (M11): the app went back to the list of consoles. */
data class ClosedNotice(val deviceId: String, val name: String)

/**
 * The event stream of the console shown (milestone M14, docs/specs/04
 * `GET /events`), mirror of `ConsoleSession+Events.swift`.
 *
 * It runs only while the app is in the foreground ([setForeground], from the
 * activity's start and stop) and a screen of that console is shown
 * ([setOpenConsole], from the navigation): stopped in the background, on the
 * list of consoles, and when the console is forgotten or paired again
 * ([dropConsole], called under the pairing lock). Each stream keeps the
 * identity of the pairing it started with (pin and pairing date): a stream
 * of a replaced pairing publishes nothing and stops.
 *
 * What it brings:
 * - `hello`: [ConsoleChange.CatchUp] (screens reload, the dashboard reads
 *   `GET /device` once), and the jobs are read once;
 * - `library`: the screens reload once the events stop coming for
 *   [libraryDebounceMillis] (merged, by system when known);
 * - `job`: the jobs follow the events, the screens stop polling;
 * - `device`: the dashboard (battery, connected devices, storage);
 * - `paks`: the Apps screen reloads;
 * - `shutdown`, or the stream ending and a `/hello` probe failing: the
 *   console closed ReminisLink. The app goes back to the list with a
 *   notice, its transfers pause, and nothing connects to it again until the
 *   user opens it from the list;
 * - `401 disconnected`: the console disconnected this phone. The dashboard
 *   shows it with Reconnect (`POST /clients/me/connect`); the stream never
 *   reopens by itself;
 * - 35 s without a byte, `401 unknown_client`, another certificate: the
 *   connection is lost (dashboard error, Retry), never reopened by itself;
 * - `404`: a pak older than M14, the screens keep polling for the life of
 *   the process;
 * - anything else (no network, `429`, an unexpected answer): again after
 *   the backoff of [EventStream].
 *
 * Everything runs on the main thread ([scope]).
 */
class ConsoleEvents(
    private val scope: CoroutineScope,
    private val pairingIdentity: (String) -> Any?,
    private val client: suspend (deviceId: String, refresh: Boolean) -> ApiClient,
    private val consoleName: (String) -> String,
    private val onClosed: (String) -> Unit,
    private val onJob: (String, link.reminis.core.Job, Any?) -> Unit,
    private val onLive: (String) -> Unit,
) {
    /** Delays of the streams (tests shorten them). */
    var timing: () -> EventStreamTiming = { EventStreamTiming() }

    /** How long `library` events are merged before the screens reload. */
    var libraryDebounceMillis = 300L

    private val _changes = MutableSharedFlow<ConsoleChange>(extraBufferCapacity = 64)
    val changes: SharedFlow<ConsoleChange> = _changes.asSharedFlow()

    private val _live = MutableStateFlow<Set<String>>(emptySet())

    /** Consoles whose stream is open (`hello` received): their jobs follow the events. */
    val live: StateFlow<Set<String>> = _live.asStateFlow()

    private val _closed = MutableStateFlow<ClosedNotice?>(null)

    /** The last console that closed ReminisLink, until dismissed or opened again. */
    val closed: StateFlow<ClosedNotice?> = _closed.asStateFlow()

    private val _disconnected = MutableStateFlow<Set<String>>(emptySet())

    /** Consoles that disconnected this phone: no stream until the user reconnects. */
    val disconnected: StateFlow<Set<String>> = _disconnected.asStateFlow()

    /** Closed by the console (M11): nothing connects automatically until the user opens it. */
    private val closedConsoles = mutableSetOf<String>()

    /** The connection was lost: the stream waits for an action of the user (Retry, opening the console). */
    private val lostConsoles = mutableSetOf<String>()

    /** Consoles opened by the user from the list, until their dashboard reads it ([takeUserOpened]). */
    private val opened = mutableSetOf<String>()

    /** Paks older than M14 (`404`): polling only. */
    private val unsupported = mutableSetOf<String>()

    private var foreground = false
    private var openConsole: String? = null
    private var session: Session? = null

    private class Session(val deviceId: String, val identity: Any) {
        lateinit var stream: EventStream
        var refreshClient = false
        var pendingLibrary: Set<String>? = null
        var hasPendingLibrary = false
        var debounce: Job? = null
    }

    fun isLive(deviceId: String): Boolean = deviceId in _live.value

    /** Bumped when a console closes ReminisLink or disconnects this phone (main thread). */
    private val epochs = mutableMapOf<String, Int>()

    /**
     * The connection epoch of [deviceId]: a read started in one epoch must
     * not publish nor resume transfers in another (the console closed
     * ReminisLink or disconnected this phone meanwhile), like the catch-up
     * generation of `ConsoleSession+Events.swift`.
     */
    fun epoch(deviceId: String): Int = epochs[deviceId] ?: 0

    private fun nextEpoch(deviceId: String) {
        epochs[deviceId] = epoch(deviceId) + 1
    }

    /**
     * A request of a screen got `503 shutting_down`: the console closed
     * ReminisLink, as `shutdown` on the stream would say.
     */
    fun closedSeenByRequest(deviceId: String) {
        if (deviceId in closedConsoles) return
        session?.takeIf { it.deviceId == deviceId }?.let { stopSession() }
        markClosed(deviceId)
    }

    /** Tests only: the stream of the console shown, if any. */
    @androidx.annotation.VisibleForTesting
    val streamForTests: EventStream? get() = session?.stream

    fun setForeground(value: Boolean) {
        if (foreground == value) return
        foreground = value
        // Back from the background: a lost connection gets a new chance.
        if (value) lostConsoles.clear()
        update()
    }

    fun setOpenConsole(deviceId: String?) {
        if (openConsole == deviceId) return
        openConsole = deviceId
        update()
    }

    /**
     * The user opened [deviceId] from the list of consoles (or tapped
     * Retry): an action of the user, so a console that closed ReminisLink or
     * a lost connection may connect again.
     */
    fun userOpened(deviceId: String) {
        opened += deviceId
        closedConsoles -= deviceId
        lostConsoles -= deviceId
        if (_closed.value?.deviceId == deviceId) _closed.value = null
        update()
    }

    /**
     * Whether the dashboard of [deviceId] opens on an action of the user
     * ([userOpened]); read once. Only that connection may send
     * `POST /clients/me/connect` (M10).
     */
    fun takeUserOpened(deviceId: String): Boolean = opened.remove(deviceId)

    /** The console accepted `POST /clients/me/connect` (Reconnect): the stream may open again. */
    fun reconnected(deviceId: String) {
        _disconnected.update { it - deviceId }
        lostConsoles -= deviceId
        update()
    }

    /** A request of a screen got `401 disconnected`: the console disconnected this phone. */
    fun disconnectedByConsole(deviceId: String) {
        if (deviceId !in _disconnected.value) nextEpoch(deviceId)
        _disconnected.update { it + deviceId }
        update()
    }

    fun dismissClosed() {
        _closed.value = null
    }

    /** Forgotten or paired again: its stream stops and its flags are dropped. */
    fun dropConsole(deviceId: String) {
        closedConsoles -= deviceId
        lostConsoles -= deviceId
        unsupported -= deviceId
        _disconnected.update { it - deviceId }
        if (_closed.value?.deviceId == deviceId) _closed.value = null
        if (session?.deviceId == deviceId) stopSession()
        update()
    }

    private fun wanted(): String? {
        val id = openConsole ?: return null
        if (!foreground) return null
        if (id in closedConsoles || id in lostConsoles || id in unsupported || id in _disconnected.value) return null
        return id
    }

    private fun update() {
        val id = wanted()
        val current = session
        if (id == null) {
            stopSession()
            return
        }
        if (current != null && current.deviceId == id && current.stream.isRunning && pairingIdentity(id) == current.identity) return
        stopSession()
        startSession(id)
    }

    private fun stopSession() {
        val current = session ?: return
        session = null
        current.debounce?.cancel()
        current.stream.stop()
        _live.update { it - current.deviceId }
    }

    private fun isCurrent(s: Session) = session === s && pairingIdentity(s.deviceId) == s.identity

    private fun startSession(deviceId: String) {
        val identity = pairingIdentity(deviceId) ?: return
        val s = Session(deviceId, identity)
        s.stream = EventStream(
            scope,
            timing(),
            connect = {
                val c = client(deviceId, s.refreshClient)
                s.refreshClient = false
                // Forgotten or paired again while the console was looked for.
                if (!isCurrent(s)) throw ApiException.NotPaired()
                c.openEvents()
            },
            onEvent = { event -> if (isCurrent(s)) handle(s, event) },
            onEnd = { end -> ended(s, end) },
        )
        session = s
        s.stream.start()
    }

    private fun handle(s: Session, event: PakEvent) {
        val id = s.deviceId
        when (event) {
            is PakEvent.Hello -> {
                _live.update { it + id }
                onLive(id)
                _changes.tryEmit(ConsoleChange.CatchUp(id))
            }
            is PakEvent.Device -> _changes.tryEmit(ConsoleChange.Device(id, event.device))
            is PakEvent.Library -> libraryChanged(s, event.library.affectedSystems)
            is PakEvent.JobChanged -> onJob(id, event.job, s.identity)
            PakEvent.Paks -> _changes.tryEmit(ConsoleChange.Paks(id))
            is PakEvent.Shutdown, is PakEvent.Unknown -> Unit
        }
    }

    private fun libraryChanged(s: Session, systems: Set<String>?) {
        if (s.hasPendingLibrary) {
            val current = s.pendingLibrary
            s.pendingLibrary = if (current == null || systems == null) null else current + systems
            return
        }
        s.hasPendingLibrary = true
        s.pendingLibrary = systems
        s.debounce = scope.launch {
            delay(libraryDebounceMillis)
            s.hasPendingLibrary = false
            if (isCurrent(s)) _changes.tryEmit(ConsoleChange.Library(s.deviceId, s.pendingLibrary))
        }
    }

    private suspend fun ended(s: Session, end: EventStream.End): EventStream.Decision {
        if (!isCurrent(s)) return EventStream.Decision.STOP
        _live.update { it - s.deviceId }
        return when (end) {
            is EventStream.End.Shutdown -> {
                consoleClosed(s)
                EventStream.Decision.STOP
            }
            EventStream.End.LivenessLost -> {
                lost(s, ApiException.Network(java.io.IOException("no event for 35 s")))
                EventStream.Decision.STOP
            }
            EventStream.End.EndOfStream -> {
                // Not announced: the `/hello` probe tells a closing pak from a stream cut for another reason.
                val closed = try {
                    client(s.deviceId, false).hello(PakClosure.PROBE_TIMEOUT_MILLIS)
                    false
                } catch (e: kotlinx.coroutines.CancellationException) {
                    throw e
                } catch (e: Exception) {
                    PakClosure.probeMeansClosed(e)
                }
                when {
                    !isCurrent(s) -> EventStream.Decision.STOP
                    closed -> {
                        consoleClosed(s)
                        EventStream.Decision.STOP
                    }
                    else -> EventStream.Decision.RECONNECT
                }
            }
            is EventStream.End.Failed -> failed(s, end.error)
        }
    }

    private fun failed(s: Session, error: Throwable): EventStream.Decision {
        val server = error as? ApiException.Server
        val status = server?.status ?: (error as? ApiException.UnexpectedStatus)?.status
        return when {
            status == 404 -> {
                // A pak older than M14: the screens keep polling.
                unsupported += s.deviceId
                stopLater(s)
                EventStream.Decision.STOP
            }
            server?.code == ErrorCodes.SHUTTING_DOWN -> {
                consoleClosed(s)
                EventStream.Decision.STOP
            }
            server?.code == ErrorCodes.DISCONNECTED -> {
                nextEpoch(s.deviceId)
                _disconnected.update { it + s.deviceId }
                _changes.tryEmit(ConsoleChange.Lost(s.deviceId, error))
                stopLater(s)
                EventStream.Decision.STOP
            }
            server?.code == ErrorCodes.UNKNOWN_CLIENT || error is ApiException.IdentityChanged ||
                error is ApiException.DeviceMismatch || error is ApiException.Incompatible -> {
                lost(s, error)
                EventStream.Decision.STOP
            }
            error is ApiException.NotPaired -> EventStream.Decision.STOP
            else -> {
                // No network, `429`, an unexpected answer: again after the backoff; the address may have changed.
                if (error is ApiException.Network) s.refreshClient = true
                if (wanted() == s.deviceId) EventStream.Decision.RECONNECT else EventStream.Decision.STOP
            }
        }
    }

    private fun lost(s: Session, error: Throwable) {
        lostConsoles += s.deviceId
        _changes.tryEmit(ConsoleChange.Lost(s.deviceId, error))
        stopLater(s)
    }

    /** The session ends once its stream returns from this decision. */
    private fun stopLater(s: Session) {
        if (session === s) {
            session = null
            s.debounce?.cancel()
        }
    }

    private fun consoleClosed(s: Session) {
        stopLater(s)
        markClosed(s.deviceId)
    }

    private fun markClosed(id: String) {
        closedConsoles += id
        // Reads in flight (the catch-up after `hello`) publish nothing and resume nothing.
        nextEpoch(id)
        onClosed(id)
        _closed.value = ClosedNotice(id, consoleName(id))
    }
}
