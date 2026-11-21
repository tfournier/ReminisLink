package link.reminis.app.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import link.reminis.app.ConsoleNotFoundException
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
    private val _online = MutableStateFlow<Set<String>>(emptySet())
    val online: StateFlow<Set<String>> = _online.asStateFlow()
    private var browsing: Job? = null

    fun reload() {
        _consoles.value = container.consoles.all().sortedBy { it.deviceName.lowercase() }
    }

    /** Watches mDNS while the list is visible: a console is online once seen. */
    fun startBrowsing() {
        if (browsing?.isActive == true) return
        browsing = viewModelScope.launch {
            runCatching { container.discovery.consoles().collect { c -> _online.update { it + c.deviceId } } }
        }
    }

    fun stopBrowsing() {
        browsing?.cancel()
        browsing = null
    }

    fun forget(deviceId: String) {
        container.forget(deviceId)
        reload()
    }
}

// MARK: pairing

enum class PairingStep { CONFIRM, SEARCHING, PAIRING, DONE }

data class PairingState(
    val step: PairingStep,
    val deviceName: String? = null,
    val deviceId: String? = null,
    val error: Throwable? = null,
)

/**
 * Pairing v2 (docs/specs/03-protocole.md §3): mDNS with the link's `id` for
 * 3 s, else the link's address; `GET /hello` then `POST /pair` over TLS
 * pinned on `k`; the console's proof is checked before anything is stored.
 * Once `POST /pair` is sent, leaving the screen does not cancel the exchange.
 */
class PairingViewModel(app: Application, private val link: PairingLink) : AndroidViewModel(app) {
    private val container = app.container
    private val _state = MutableStateFlow(
        PairingState(
            step = if (container.consoles.get(link.deviceId) != null) PairingStep.CONFIRM else PairingStep.SEARCHING,
            deviceName = link.deviceName,
        ),
    )
    val state: StateFlow<PairingState> = _state.asStateFlow()
    private var job: Job? = null

    init {
        if (_state.value.step == PairingStep.SEARCHING) start()
    }

    fun start() {
        if (job?.isActive == true) return
        _state.value = _state.value.copy(step = PairingStep.SEARCHING, error = null)
        // The application scope: leaving the screen never interrupts an
        // exchange whose POST /pair may already be sent.
        job = container.scope.launch {
            try {
                val endpoint = Locator.locate(link.endpoint) { container.discovery.find(link.deviceId) }
                    ?: throw ConsoleNotFoundException()
                val client = container.newClient(endpoint, link.deviceId, link.pin)
                val hello = client.hello()
                if (!hello.pairingOpen) throw PairingClosedException()
                _state.value = _state.value.copy(step = PairingStep.PAIRING, deviceName = hello.deviceName)
                val identity = withContext(Dispatchers.IO) { container.identityStore.identity() }
                val attempt = PairingAttempt(
                    secret = link.secret,
                    deviceId = link.deviceId,
                    clientId = identity.clientId,
                    key = identity.key,
                    clientName = Pairing.clientName(container.clientName, container.defaultClientName),
                )
                val response = client.pair(attempt)
                withContext(Dispatchers.IO) {
                    container.forget(link.deviceId)
                    container.consoles.put(
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
                _state.value = PairingState(PairingStep.DONE, response.deviceName, response.deviceId)
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                _state.value = _state.value.copy(error = e)
            }
        }
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
