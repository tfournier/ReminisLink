package link.reminis.app.ui

import android.app.Application
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.net.Uri
import androidx.lifecycle.viewModelScope
import java.util.UUID
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import link.reminis.app.LocalFiles
import link.reminis.app.R
import link.reminis.app.container
import link.reminis.core.AddKind
import link.reminis.core.ApiClient
import link.reminis.core.Backup
import link.reminis.core.DownloadSource
import link.reminis.core.GameDetail
import link.reminis.core.GameFile
import link.reminis.core.GameFileRoles
import link.reminis.core.GameSystem
import link.reminis.core.LibraryGame
import link.reminis.core.LibrarySearch
import link.reminis.core.Slot
import link.reminis.core.SlotList
import link.reminis.core.SystemSuggestion
import link.reminis.core.UploadTarget

// MARK: save

data class SaveContent(val game: GameDetail, val backups: List<Backup>) {
    val save: GameFile? get() = game.files.firstOrNull { it.role == GameFileRoles.SAVE }
}

/**
 * The save of a game (docs/specs/06-app-ios.md §4.5): download it, replace
 * it from a file, and its backup copies (create with a label, restore,
 * download, delete to the trash of the console). The console copies the
 * current save before every replacement or restoration.
 */
class SaveViewModel(app: Application, deviceId: String, private val gameId: String) : ActionViewModel(app, deviceId) {
    private val _content = MutableStateFlow<Load<SaveContent>>(Load.Loading)
    val content: StateFlow<Load<SaveContent>> = _content.asStateFlow()

    init {
        load()
        reloadAfterTransfers(gameId) { load(quiet = true) }
    }

    fun load(quiet: Boolean = false) {
        if (!quiet || _content.value !is Load.Loaded) _content.value = Load.Loading
        viewModelScope.launch {
            val result = try {
                val client = container.client(deviceId)
                val game = async { client.game(gameId) }
                val backups = async { client.saveBackups(gameId) }
                Load.Loaded(SaveContent(game.await(), backups.await()))
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                Load.Failed(e)
            }
            if (!quiet || result is Load.Loaded) _content.value = result
        }
    }

    private suspend fun reload() {
        val client = container.client(deviceId)
        _content.value = Load.Loaded(SaveContent(client.game(gameId), client.saveBackups(gameId)))
    }

    fun createBackup(label: String?) = perform(Notice(R.string.backup_created)) {
        container.client(deviceId).createSaveBackup(gameId, label)
        reload()
    }

    fun restore(backup: Backup) = perform(null) {
        val result = container.client(deviceId).restoreSaveBackup(gameId, backup.id)
        showNotice(Notice(if (result.backup == null) R.string.backup_restored else R.string.backup_restored_kept))
        reload()
    }

    fun delete(backup: Backup) = perform(Notice(R.string.backup_deleted)) {
        container.client(deviceId).deleteSaveBackup(gameId, backup.id)
        reload()
    }

    /**
     * Replaces the save with a file chosen by the user. Up to 64 MiB it is
     * sent at once (`PUT /games/{gameId}/save`); larger, it goes through the
     * transfer queue (`target.kind = "save"`). Either way the console first
     * copies the current save (`before-replace`).
     */
    fun replace(document: Uri, gameName: String) = perform(null) {
        val app = getApplication<Application>()
        val picked = LocalFiles.copyIn(app, document, container.transferDirectory)
        if (picked.sizeBytes > ApiClient.MAX_SAVE_BODY_BYTES) {
            container.transfers.enqueueUpload(
                deviceId, picked.file, picked.name, UploadTarget.Save(gameId), gameName, ownsFile = true,
            )
            showNotice(Notice(R.string.save_replace_queued))
            return@perform
        }
        try {
            val result = container.client(deviceId).replaceSave(gameId, picked.file)
            showNotice(Notice(if (result.backup == null) R.string.save_replaced else R.string.save_replaced_kept))
        } finally {
            withContext(Dispatchers.IO) { picked.remove() }
        }
        reload()
    }

    fun downloadSave(save: GameFile, gameName: String, destination: Uri) =
        enqueueDownload(DownloadSource.Save(gameId), save.name, gameName, destination, save.sizeBytes)

    fun downloadBackup(backup: Backup, fileName: String, gameName: String, destination: Uri) =
        enqueueDownload(DownloadSource.SaveBackup(gameId, backup.id), fileName, gameName, destination, backup.sizeBytes)
}

// MARK: states

/**
 * The state slots of a game: lock, unlock, restore the locked copy,
 * download, delete (to the trash of the console).
 */
class StatesViewModel(app: Application, deviceId: String, private val gameId: String) : ActionViewModel(app, deviceId) {
    private val _slots = MutableStateFlow<Load<SlotList>>(Load.Loading)
    val slots: StateFlow<Load<SlotList>> = _slots.asStateFlow()

    /** Slots reported `restored` while this screen is open (the console reports it once). */
    private val _restored = MutableStateFlow<Set<Int>>(emptySet())
    val restored: StateFlow<Set<Int>> = _restored.asStateFlow()

    private val _thumbnails = MutableStateFlow<Map<Int, Bitmap>>(emptyMap())
    val thumbnails: StateFlow<Map<Int, Bitmap>> = _thumbnails.asStateFlow()
    private val requested = mutableMapOf<Int, String>()

    /** Only the last load shows its answer. */
    private var generation = 0

    init {
        load()
        reloadAfterTransfers(gameId) { load(quiet = true) }
    }

    fun load(quiet: Boolean = false) {
        val current = ++generation
        if (!quiet || _slots.value !is Load.Loaded) _slots.value = Load.Loading
        viewModelScope.launch {
            val result = try {
                Load.Loaded(container.client(deviceId).states(gameId))
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                Load.Failed(e)
            }
            if (current != generation) return@launch
            if (result is Load.Loaded) {
                _restored.update { it + result.value.slots.filter(Slot::wasRestored).map(Slot::slot) }
                loadThumbnails(result.value)
            }
            if (!quiet || result is Load.Loaded) _slots.value = result
        }
    }

    private fun updated(slot: Slot) {
        // Newer than any load in flight: those are dropped.
        generation++
        _restored.update { it - slot.slot }
        (_slots.value as? Load.Loaded)?.let { _slots.value = Load.Loaded(it.value.replacing(slot)) }
    }

    fun lock(slot: Slot, title: String) = perform(Notice(R.string.state_locked_notice, listOf(title))) {
        updated(container.client(deviceId).lockState(gameId, slot.slot))
    }

    fun unlock(slot: Slot, title: String) = perform(Notice(R.string.state_unlocked_notice, listOf(title))) {
        val result = container.client(deviceId).unlockState(gameId, slot.slot)
        if (slot.isMissing) load(quiet = true) else updated(result)
    }

    fun restore(slot: Slot, title: String) = perform(Notice(R.string.state_restored_notice, listOf(title))) {
        updated(container.client(deviceId).restoreLockedState(gameId, slot.slot))
    }

    fun delete(slot: Slot, title: String) = perform(Notice(R.string.state_deleted_notice, listOf(title))) {
        container.client(deviceId).deleteState(gameId, slot.slot)
        load(quiet = true)
    }

    fun download(slot: Slot, fileName: String, gameName: String, destination: Uri) =
        enqueueDownload(DownloadSource.State(gameId, slot.slot), fileName, gameName, destination, slot.sizeBytes)

    /** Best effort: a thumbnail error never shows. Reloaded when the slot changes. */
    private fun loadThumbnails(list: SlotList) {
        for (slot in list.slots.filter { it.hasThumbnail }) {
            val version = "${slot.modifiedAt}-${slot.sizeBytes}"
            if (requested[slot.slot] == version) continue
            requested[slot.slot] = version
            viewModelScope.launch {
                val bitmap = runCatching {
                    val bytes = container.client(deviceId).stateThumbnail(gameId, slot.slot) ?: return@runCatching null
                    withContext(Dispatchers.Default) { decodeThumbnail(bytes) }
                }.getOrNull() ?: return@launch
                _thumbnails.update { it + (slot.slot to bitmap) }
            }
        }
    }

    /** NextUI saves a full-screen capture: decoded reduced to the size of the row. */
    private fun decodeThumbnail(bytes: ByteArray): Bitmap? {
        val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
        BitmapFactory.decodeByteArray(bytes, 0, bytes.size, bounds)
        if (bounds.outWidth <= 0) return null
        var sample = 1
        while (bounds.outWidth / (sample * 2) >= 160) sample *= 2
        return BitmapFactory.decodeByteArray(bytes, 0, bytes.size, BitmapFactory.Options().apply { inSampleSize = sample })
    }
}

// MARK: add files

/** A file chosen on the Add screen. */
data class AddFile(
    val id: String = UUID.randomUUID().toString(),
    val uri: Uri,
    val name: String,
    val kind: AddKind,
    val systemId: String? = null,
    /** For a save: the game whose save it replaces. */
    val game: LibraryGame? = null,
) {
    val isReady: Boolean get() = when (kind) {
        AddKind.ROM -> systemId != null
        AddKind.SAVE -> game != null
    }
}

data class AddState(
    val deviceId: String?,
    val files: List<AddFile> = emptyList(),
    val systems: Load<List<GameSystem>>? = null,
    val sending: Boolean = false,
    val error: Throwable? = null,
) {
    val canSend: Boolean get() = deviceId != null && !sending && files.isNotEmpty() && files.all { it.isReady }
}

/**
 * Add screen (docs/specs/06-app-ios.md §4.6): files chosen with the system
 * picker or shared by another app, each sent as a game (system suggested
 * from the extension) or as the save of a game. The app proposes no source
 * of games.
 */
class AddFilesViewModel(app: Application, deviceId: String?, private val shareToken: String?) :
    androidx.lifecycle.AndroidViewModel(app) {
    private val container = app.container
    private val _state = MutableStateFlow(AddState(deviceId ?: container.consoles.all().singleOrNull()?.deviceId))
    val state: StateFlow<AddState> = _state.asStateFlow()

    private val _games = MutableStateFlow<Load<List<LibraryGame>>?>(null)
    val games: StateFlow<Load<List<LibraryGame>>?> = _games.asStateFlow()

    init {
        shareToken?.let { token -> container.shareInbox.get(token)?.let(::add) }
        loadSystems()
    }

    fun chooseConsole(deviceId: String) {
        if (_state.value.deviceId == deviceId) return
        _state.update { s -> s.copy(deviceId = deviceId, systems = null, files = s.files.map { it.copy(systemId = null, game = null) }) }
        _games.value = null
        loadSystems()
    }

    fun add(uris: List<Uri>) {
        val resolver = getApplication<Application>().contentResolver
        val added = uris.map { uri ->
            val name = LocalFiles.displayName(resolver, uri)
            AddFile(uri = uri, name = name, kind = AddKind.suggested(name))
        }
        _state.update { it.copy(files = it.files + added, error = null) }
        suggestSystems()
    }

    fun remove(id: String) = _state.update { s -> s.copy(files = s.files.filter { it.id != id }) }

    fun setKind(id: String, kind: AddKind) = change(id) { it.copy(kind = kind) }

    fun setSystem(id: String, systemId: String) = change(id) { it.copy(systemId = systemId) }

    fun setGame(id: String, game: LibraryGame) = change(id) { it.copy(game = game) }

    private fun change(id: String, transform: (AddFile) -> AddFile) =
        _state.update { s -> s.copy(files = s.files.map { if (it.id == id) transform(it) else it }) }

    private fun loadSystems() {
        val deviceId = _state.value.deviceId ?: return
        _state.update { it.copy(systems = Load.Loading) }
        viewModelScope.launch {
            val result = try {
                Load.Loaded(container.client(deviceId).systems())
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                Load.Failed(e)
            }
            if (_state.value.deviceId != deviceId) return@launch
            _state.update { it.copy(systems = result) }
            suggestSystems()
        }
    }

    /** The games of the console, for the save picker. */
    fun loadGames() {
        val deviceId = _state.value.deviceId ?: return
        if (_games.value is Load.Loaded || _games.value == Load.Loading) return
        _games.value = Load.Loading
        viewModelScope.launch {
            _games.value = try {
                Load.Loaded(container.client(deviceId).allGames().games)
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                Load.Failed(e)
            }
        }
    }

    fun filteredGames(games: List<LibraryGame>, query: String) = LibrarySearch.filter(games, query)

    /** Fills the system of the files still without one. */
    private fun suggestSystems() {
        val systems = (_state.value.systems as? Load.Loaded)?.value ?: return
        _state.update { s ->
            s.copy(files = s.files.map { f -> if (f.systemId != null) f else f.copy(systemId = SystemSuggestion.system(f.name, systems)?.id) })
        }
    }

    /**
     * Copies each file into the app and queues its upload; each file leaves
     * the list once queued (after a failure, a new tap only sends the
     * others). [onDone] gets the console once every file is queued.
     */
    fun send(onDone: (String) -> Unit) {
        val s = _state.value
        val deviceId = s.deviceId ?: return
        if (!s.canSend) return
        val systems = (s.systems as? Load.Loaded)?.value.orEmpty()
        _state.update { it.copy(sending = true, error = null) }
        viewModelScope.launch {
            try {
                for (file in s.files) {
                    val picked = LocalFiles.copyIn(getApplication(), file.uri, container.transferDirectory)
                    val (target, destination) = when (file.kind) {
                        AddKind.ROM -> UploadTarget.Rom(file.systemId!!, picked.name) to
                            (systems.firstOrNull { it.id == file.systemId }?.label.orEmpty())
                        AddKind.SAVE -> UploadTarget.Save(file.game!!.id) to file.game.name
                    }
                    container.transfers.enqueueUpload(deviceId, picked.file, picked.name, target, destination, ownsFile = true)
                    _state.update { st -> st.copy(files = st.files.filter { it.id != file.id }) }
                }
                shareToken?.let(container.shareInbox::remove)
                _state.update { it.copy(sending = false) }
                onDone(deviceId)
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                _state.update { it.copy(sending = false, error = e) }
            }
        }
    }

    override fun onCleared() {
        shareToken?.let(container.shareInbox::remove)
    }
}
