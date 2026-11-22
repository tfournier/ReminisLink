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
import link.reminis.app.ArtImage
import link.reminis.app.DiscoveryEvent
import link.reminis.app.DocumentSink
import link.reminis.app.Generations
import link.reminis.app.R
import link.reminis.app.container
import link.reminis.core.ApiException
import link.reminis.core.DeviceStatus
import link.reminis.core.DownloadSource
import link.reminis.core.GameDetail
import link.reminis.core.GameFile
import link.reminis.core.TransferDirection
import link.reminis.core.UploadTarget
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
            // The console answers again: the transfers paused by a lost connection go on.
            if (_status.value is Load.Loaded) container.transfers.resumePaused(deviceId)
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
        // A game sent from this phone: the list reloads.
        viewModelScope.launch {
            container.transfers.finished.collect { item ->
                if (item.deviceId == deviceId && item.target is UploadTarget.Rom) refresh()
            }
        }
        // A game deleted, restored or patched from this phone.
        viewModelScope.launch {
            container.libraryChanged.collect { if (it == deviceId) refresh() }
        }
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

class GameViewModel(app: Application, deviceId: String, private val gameId: String) :
    ActionViewModel(app, deviceId) {
    private val _game = MutableStateFlow<Load<GameDetail>>(Load.Loading)
    val game: StateFlow<Load<GameDetail>> = _game.asStateFlow()

    init {
        refresh()
        reloadAfterTransfers(gameId) { refresh(quiet = true) }
    }

    fun refresh(quiet: Boolean = false) {
        if (!quiet || _game.value !is Load.Loaded) _game.value = Load.Loading
        viewModelScope.launch {
            val result = try {
                Load.Loaded(container.client(deviceId).game(gameId))
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                Load.Failed(e)
            }
            // A quiet reload keeps the screen when it fails.
            if (!quiet || result is Load.Loaded) _game.value = result
        }
    }

    /** Downloads a file of the game (M11) into the document chosen by the user. */
    fun download(file: GameFile, gameName: String, destination: android.net.Uri) {
        val fileId = file.fileId ?: return
        enqueueDownload(DownloadSource.GameFile(gameId, fileId), file.name, gameName, destination, file.sizeBytes)
    }

    /**
     * Sets the box art from a picture chosen by the user: scaled down to
     * 1024 px at most and sent as PNG (at most 4 MiB); the old art goes to
     * the trash of the console.
     */
    fun setArt(picture: android.net.Uri) = perform(Notice(R.string.art_set)) {
        val png = withContext(Dispatchers.IO) {
            try {
                ArtImage.png(getApplication<Application>().contentResolver, picture)
            } catch (e: java.io.IOException) {
                throw link.reminis.app.LocalFileException(e)
            } catch (e: SecurityException) {
                throw link.reminis.app.LocalFileException(e)
            }
        }
        container.client(deviceId).setGameArt(gameId, png)
        refresh(quiet = true)
    }

    fun removeArt() = perform(Notice(R.string.art_removed)) {
        container.client(deviceId).deleteGameArt(gameId)
        refresh(quiet = true)
    }

    /** A notice left by another screen (a patch applied): shown once, and the game read again. */
    fun takeNotice(notice: link.reminis.app.GameNotice?) {
        if (notice == null || notice.deviceId != deviceId || notice.gameId != gameId) return
        container.gameNotice.compareAndSet(notice, null)
        showText(notice.text)
        refresh(quiet = true)
    }

    private val _deletion = MutableStateFlow<DeletionState?>(null)

    /**
     * The deletion being confirmed: the option shown and the preview of
     * `GET /games/{gameId}/deletion` for that option only.
     */
    val deletion: StateFlow<DeletionState?> = _deletion.asStateFlow()
    private var deletionGeneration = 0

    /**
     * Asks what a deletion with [keepSave] would move now. Only the answer
     * of the latest request for the option shown is applied: an answer for
     * the other position of the option never enables the confirmation.
     */
    fun previewDeletion(keepSave: Boolean) {
        val generation = ++deletionGeneration
        _deletion.value = DeletionState(keepSave, Load.Loading)
        viewModelScope.launch {
            val result = try {
                Load.Loaded(container.client(deviceId).deletionPreview(gameId, keepSave))
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                Load.Failed(e)
            }
            val current = _deletion.value
            if (current != null && generation == deletionGeneration && current.keepSave == keepSave) _deletion.value = current.copy(preview = result)
            container.deletionAnswersForTests.incrementAndGet()
        }
    }

    fun closeDeletion() {
        deletionGeneration++
        _deletion.value = null
    }

    /** `DELETE /games/{gameId}`: the game and its data go to the trash of the console. */
    fun delete(onDeleted: () -> Unit) {
        val state = _deletion.value ?: return
        // Only the preview of the option shown can be confirmed.
        if (state.preview !is Load.Loaded) return
        delete(state.keepSave, onDeleted)
    }

    private fun delete(keepSave: Boolean, onDeleted: () -> Unit) = perform(null) {
        closeDeletion()
        container.client(deviceId).deleteGame(gameId, keepSave)
        container.libraryChanged.tryEmit(deviceId)
        onDeleted()
    }
}

/** A deletion being confirmed: the option shown and its preview. */
data class DeletionState(val keepSave: Boolean, val preview: Load<link.reminis.core.DeletionPreview>)

/** A short message shown after an action (a string resource and its arguments). */
data class Notice(@param:androidx.annotation.StringRes val id: Int, val args: List<Any> = emptyList())

/**
 * A screen of one console whose actions show a working state, then a notice
 * or an error; downloads go to the transfer queue.
 */
open class ActionViewModel(app: Application, protected val deviceId: String) : AndroidViewModel(app) {
    protected val container = app.container
    private val _working = MutableStateFlow(false)
    val working: StateFlow<Boolean> = _working.asStateFlow()
    private val _notice = MutableStateFlow<Notice?>(null)
    val notice: StateFlow<Notice?> = _notice.asStateFlow()
    private val _error = MutableStateFlow<Throwable?>(null)
    val error: StateFlow<Throwable?> = _error.asStateFlow()

    fun dismissMessages() {
        _notice.value = null
        _error.value = null
    }

    protected fun showNotice(notice: Notice?) {
        _notice.value = notice
    }

    /** A text already translated (a notice from another screen). */
    protected fun showText(text: String) {
        _notice.value = Notice(R.string.plain_text, listOf(text))
    }

    /** Runs a user action: one at a time, a notice on success, the error otherwise. */
    protected fun perform(success: Notice?, action: suspend () -> Unit) {
        if (_working.value) return
        _working.value = true
        _error.value = null
        _notice.value = null
        viewModelScope.launch {
            try {
                action()
                if (_notice.value == null) _notice.value = success
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                _error.value = e
            } finally {
                _working.value = false
            }
        }
    }

    protected fun enqueueDownload(
        source: DownloadSource,
        fileName: String,
        gameName: String,
        destination: android.net.Uri,
        sizeBytes: Long?,
    ) {
        val app = getApplication<Application>()
        container.lastDownloadDocumentForTests = destination
        container.transfers.enqueueDownload(deviceId, source, fileName, gameName, DocumentSink(app, destination), sizeBytes)
        _notice.value = Notice(R.string.download_queued, listOf(fileName))
    }

    /** Calls [reload] after an upload to [gameId] of this console finishes (a save or a state sent). */
    protected fun reloadAfterTransfers(gameId: String?, reload: () -> Unit) {
        viewModelScope.launch {
            container.transfers.finished.collect { item ->
                if (item.deviceId != deviceId || item.direction != TransferDirection.UPLOAD) return@collect
                val target = item.target
                val concerned = gameId == null || when (target) {
                    is UploadTarget.Save -> target.gameId == gameId
                    is UploadTarget.State -> target.gameId == gameId
                    else -> false
                }
                if (concerned) reload()
            }
        }
    }
}
