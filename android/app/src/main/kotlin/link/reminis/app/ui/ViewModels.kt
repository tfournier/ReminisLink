package link.reminis.app.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Deferred
import kotlinx.coroutines.Job
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.async
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import link.reminis.app.DiscoveryEvent
import link.reminis.app.Generations
import link.reminis.app.container
import link.reminis.core.ApiException
import link.reminis.core.DeviceStatus
import link.reminis.core.GameDetail
import link.reminis.core.LibraryGame
import link.reminis.core.LibrarySearch
import link.reminis.core.Locator
import link.reminis.core.PairedConsole
import link.reminis.core.PairingAttempt
import link.reminis.core.PairingLink
import link.reminis.core.Pairing

/** A screen's content: loading, loaded or failed. */
sealed interface Load<out T> {
    data object Loading : Load<Nothing>
    data class Loaded<T>(val value: T) : Load<T>
    data class Failed(val error: Throwable) : Load<Nothing>
}

// MARK: consoles list

class ConsolesViewModel(app: Application) : AndroidViewModel(app) {
    private val container = app.container
    private val _consoles = MutableStateFlow(container.consoles.all())
    val consoles: StateFlow<List<PairedConsole>> = _consoles.asStateFlow()

    /** mDNS service name → device id, for the services whose `/hello` validated with the pin. */
    private val validated = MutableStateFlow<Map<String, String>>(emptyMap())

    /**
     * Generation of each announcement (service name), bumped by every Found
     * and Lost: a validation publishes its result only if its announcement is
     * still the current one (not lost, not superseded). Found and Lost arrive
     * in order: [link.reminis.app.Discovery] never emits a stale Found after
     * the Lost that superseded it.
     */
    private val generations = Generations<String>()
    private val _online = MutableStateFlow<Set<String>>(emptySet())
    val online: StateFlow<Set<String>> = _online.asStateFlow()
    private var browsing: Job? = null

    fun reload() {
        _consoles.value = container.consoles.all().sortedBy { it.deviceName.lowercase() }
    }

    /**
     * Watches mDNS while the list is visible. An announcement alone is not
     * trusted: a console is Online only after its `/hello` answered with the
     * pinned certificate, and goes Offline when its service disappears.
     */
    fun startBrowsing() {
        if (browsing?.isActive == true) return
        browsing = viewModelScope.launch {
            runCatching {
                container.discovery.events().collect { event ->
                    when (event) {
                        is DiscoveryEvent.Found -> {
                            val c = event.console
                            val generation = generations.next(c.serviceName)
                            // A new announcement replaces the previous one until validated.
                            setValidated { it - c.serviceName }
                            if (container.consoles.get(c.deviceId) != null) {
                                launch {
                                    val ok = container.validates(c.deviceId, c.endpoint)
                                    if (ok && generations.isCurrent(c.serviceName, generation)) {
                                        setValidated { it + (c.serviceName to c.deviceId) }
                                        reload()
                                    }
                                }
                            }
                        }
                        is DiscoveryEvent.Lost -> {
                            generations.invalidate(event.serviceName)
                            setValidated { it - event.serviceName }
                        }
                    }
                }
            }
        }
    }

    fun stopBrowsing() {
        browsing?.cancel()
        browsing = null
        generations.invalidateAll()
        setValidated { emptyMap() }
    }

    private fun setValidated(change: (Map<String, String>) -> Map<String, String>) {
        validated.update(change)
        _online.value = validated.value.values.toSet()
    }

    fun forget(deviceId: String) {
        container.forget(deviceId)
        setValidated { map -> map.filterValues { it != deviceId } }
        reload()
    }
}

// MARK: pairing

enum class PairingStep {
    /** A link from another app: nothing is sent before the user confirms. */
    CONFIRM_EXTERNAL,

    /** The console is already paired: confirm to replace its pairing. */
    CONFIRM,
    SEARCHING,
    PAIRING,
    DONE,
}

data class PairingState(
    val step: PairingStep,
    val deviceName: String? = null,
    val deviceId: String? = null,
    /** The address of the link, shown in the confirmation of an external link. */
    val host: String? = null,
    val alreadyPaired: Boolean = false,
    val error: Throwable? = null,
)

/** The pairing link of this screen is gone (the app was restarted): scan it again. */
class PairingLinkGoneException : Exception("pairing link gone")

/** The user left before `POST /pair` was sent: nothing was sent. */
class PairingCancelledException : java.io.IOException("pairing cancelled before POST /pair")

/**
 * Pairing v2 (docs/specs/03-protocole.md §3) for the link behind [token]
 * (kept in memory by [link.reminis.app.PendingLinks]).
 *
 * - A link from another app waits for the user's confirmation before any
 *   network access.
 * - mDNS with the link's `id` for 3 s, then the link's address; every
 *   candidate is tried with the pin `k`; `GET /hello` then `POST /pair`; the
 *   console's proof is checked before anything is stored.
 * - The commit point is explicit: the moment OkHttp is about to write
 *   `POST /pair` on the pinned connection ([link.reminis.core.ApiClient.pair]
 *   `beforeSend`), the attempt moves from WAITING to SENT ([PairingAttempts]).
 *   [cancel] (screen button, system back, or the ViewModel being cleared)
 *   closes the screen: a WAITING attempt is cancelled and the request is never
 *   written; a SENT one runs to its end in the application scope and the
 *   pairing is stored even if the screen is gone.
 * - A failed attempt (before or after `POST /pair`) can be retried with the
 *   same in-memory link. The link is dropped from
 *   [link.reminis.app.PendingLinks] on success and whenever the screen is
 *   left, as soon as no attempt is in flight.
 */
class PairingViewModel(app: Application, private val token: String) : AndroidViewModel(app) {
    private val container = app.container
    private val pending = container.pendingLinks.get(token)
    private val link: PairingLink? = pending?.link
    private val _state = MutableStateFlow(initialState())
    val state: StateFlow<PairingState> = _state.asStateFlow()
    private var job: Job? = null
    private val attempts = PairingAttempts()

    @Volatile
    private var exchange: Deferred<*>? = null

    private fun initialState(): PairingState {
        if (pending == null) return PairingState(PairingStep.SEARCHING, error = PairingLinkGoneException())
        val link = pending.link ?: return PairingState(PairingStep.SEARCHING, error = pending.error)
        val alreadyPaired = container.consoles.get(link.deviceId) != null
        val step = when {
            pending.external -> PairingStep.CONFIRM_EXTERNAL
            alreadyPaired -> PairingStep.CONFIRM
            else -> PairingStep.SEARCHING
        }
        return PairingState(step, link.deviceName, host = link.endpoint?.authority, alreadyPaired = alreadyPaired)
    }

    init {
        if (_state.value.step == PairingStep.SEARCHING && _state.value.error == null) start()
    }

    /** The user confirmed (external link, or replacing an existing pairing), or retries after an error. */
    fun start() {
        val link = link ?: return
        val id = attempts.begin() ?: return
        _state.value = _state.value.copy(step = PairingStep.SEARCHING, error = null)
        job = viewModelScope.launch {
            try {
                val candidates = Locator.candidates(link.endpoint) { container.discovery.find(link.deviceId) }
                val (endpoint, connected) = Locator.connectFirst(candidates) { ep ->
                    val client = container.newClient(ep, link.deviceId, link.pin)
                    client to client.hello()
                }
                val (client, hello) = connected
                if (!hello.pairingOpen) throw PairingClosedException()
                val identity = withContext(Dispatchers.IO) { container.identityStore.identity() }
                val attempt = PairingAttempt(
                    secret = link.secret,
                    deviceId = link.deviceId,
                    clientId = identity.clientId,
                    key = identity.key,
                    clientName = Pairing.clientName(container.clientName, container.defaultClientName),
                )
                ensureActive()
                _state.value = _state.value.copy(step = PairingStep.PAIRING, deviceName = hello.deviceName)
                val deferred = container.scope.async {
                    var success = false
                    try {
                        val response = client.pair(attempt) {
                            container.pairingGateForTests?.invoke()
                            // The commit point: only from here may the console pair.
                            if (!attempts.commit(id)) throw PairingCancelledException()
                        }
                        withContext(Dispatchers.IO) {
                            container.replacePairing(
                                PairedConsole(
                                    deviceId = response.deviceId,
                                    deviceName = response.deviceName,
                                    model = hello.model,
                                    pin = link.pin,
                                    lastEndpoint = endpoint,
                                    pairedAt = System.currentTimeMillis(),
                                ),
                            )
                        }
                        success = true
                        response
                    } finally {
                        // Runs even when the screen is gone: the link never outlives the exchange.
                        if (attempts.end(id, success)) container.pendingLinks.remove(token)
                    }
                }
                exchange = deferred
                // cancel() may have run between ensureActive() and here.
                if (attempts.current == PairingAttempts.State.CLOSED) deferred.cancel()
                val response = deferred.await()
                _state.value = PairingState(PairingStep.DONE, response.deviceName, response.deviceId)
            } catch (e: kotlinx.coroutines.CancellationException) {
                cancel()
                throw e
            } catch (e: Exception) {
                // Back to IDLE (no-op if the exchange already ended it): Retry begins a fresh attempt.
                if (attempts.end(id, success = false)) container.pendingLinks.remove(token)
                _state.value = _state.value.copy(error = e)
            }
        }
    }

    /**
     * The user declined or left (screen button, system back, ViewModel
     * cleared): cancels everything unless `POST /pair` is already written,
     * and drops the link once no attempt is in flight.
     */
    fun cancel() {
        val close = attempts.close()
        if (close.cancelAttempt) {
            job?.cancel()
            exchange?.cancel()
        }
        if (close.releaseLink) container.pendingLinks.remove(token)
    }

    override fun onCleared() {
        cancel()
    }
}

// MARK: dashboard

class DashboardViewModel(app: Application, private val deviceId: String) : AndroidViewModel(app) {
    private val container = app.container
    private val _status = MutableStateFlow<Load<DeviceStatus>>(Load.Loading)
    val status: StateFlow<Load<DeviceStatus>> = _status.asStateFlow()
    val console: PairedConsole? get() = container.consoles.get(deviceId)

    init {
        refresh()
    }

    fun refresh(reconnect: Boolean = false) {
        _status.value = Load.Loading
        viewModelScope.launch {
            _status.value = try {
                Load.Loaded(container.client(deviceId, refresh = reconnect).device())
            } catch (e: ApiException.Network) {
                // The address changed: look for the console again once.
                runCatching { Load.Loaded(container.client(deviceId, refresh = true).device()) }
                    .getOrElse { Load.Failed(it) }
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                Load.Failed(e)
            }
        }
    }

    fun forget() = container.forget(deviceId)
}

// MARK: library

data class LibraryContent(val games: List<LibraryGame>, val systems: Map<String, String>, val truncated: Boolean)

class LibraryViewModel(app: Application, private val deviceId: String) : AndroidViewModel(app) {
    private val container = app.container
    private val _content = MutableStateFlow<Load<LibraryContent>>(Load.Loading)
    val content: StateFlow<Load<LibraryContent>> = _content.asStateFlow()
    private val _query = MutableStateFlow("")
    val query: StateFlow<String> = _query.asStateFlow()

    init {
        refresh()
    }

    fun setQuery(text: String) {
        _query.value = text
    }

    fun filtered(content: LibraryContent, query: String): List<LibraryGame> = LibrarySearch.filter(content.games, query)

    fun refresh() {
        _content.value = Load.Loading
        viewModelScope.launch {
            _content.value = try {
                val client = container.client(deviceId)
                val systems = client.systems().associate { it.id to it.label }
                val all = client.allGames()
                Load.Loaded(LibraryContent(all.games, systems, all.truncated))
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                Load.Failed(e)
            }
        }
    }
}

// MARK: game

class GameViewModel(app: Application, private val deviceId: String, private val gameId: String) : AndroidViewModel(app) {
    private val container = app.container
    private val _game = MutableStateFlow<Load<GameDetail>>(Load.Loading)
    val game: StateFlow<Load<GameDetail>> = _game.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        _game.value = Load.Loading
        viewModelScope.launch {
            _game.value = try {
                Load.Loaded(container.client(deviceId).game(gameId))
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                Load.Failed(e)
            }
        }
    }
}
