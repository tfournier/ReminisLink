package link.reminis.app

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.util.Log
import java.io.File
import java.util.UUID
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import link.reminis.core.ApiClient
import link.reminis.core.vault.ApiSyncSource
import link.reminis.core.vault.FileVaultStore
import link.reminis.core.vault.SaveSync
import link.reminis.core.vault.SaveSyncProgress
import link.reminis.core.vault.SaveSyncReport
import link.reminis.core.vault.SaveVault
import link.reminis.core.vault.VaultConsoleInfo
import link.reminis.core.vault.VaultLocation
import link.reminis.core.vault.VaultUnavailableException

/** Where a sync of one console stands (06 §8, Android increment 7). */
sealed interface SyncPhase {
    data object Idle : SyncPhase
    data class Running(val progress: SaveSyncProgress) : SyncPhase
    data class Finished(val report: SaveSyncReport) : SyncPhase
    data class Failed(val error: Throwable) : SyncPhase
}

/** The sync of one console: its phase, and the last sync that went to its end (kept while a new one runs). */
data class SyncState(val phase: SyncPhase = SyncPhase.Idle, val lastReport: SaveSyncReport? = null) {
    val isRunning: Boolean get() = phase is SyncPhase.Running
}

/** Where the copies go now: [name] is the folder's name for [VaultLocation.FOLDER]. */
data class VaultPlace(val location: VaultLocation, val name: String? = null)

/**
 * The sync of the saves (with their clock files) and the states of the
 * consoles to this device (port of `AppModel+SaveSync.swift`). The vault
 * is the app's internal storage by default, or a folder the user chose with
 * the Android picker (SAF, persisted permission), or, while the demo runs, a
 * temporary folder erased when it ends.
 *
 * - Never two syncs at once for a console; a connection made during a sync
 *   starts a new one when it ends (with the client current then).
 * - Every request asks the client again and checks that the pairing is the
 *   one the sync started with (and, for a real console, that the demo is
 *   not running): a forgotten or replaced pairing never gets a request of
 *   an older sync.
 * - A folder that became unavailable (permission revoked, provider gone)
 *   is never replaced silently: the sync fails with
 *   [VaultUnavailableException], nothing is lost, and the screens offer
 *   another folder or the internal storage.
 */
class SyncManager(
    context: Context,
    private val scope: CoroutineScope,
    private val client: suspend (String) -> ApiClient,
    private val pairingIdentity: (String) -> Any?,
    private val consoleName: (String) -> String,
    private val isDemo: (String) -> Boolean,
    private val demoRunning: () -> Boolean,
) {
    private val appContext = context.applicationContext
    private val prefs = appContext.getSharedPreferences("sync", Context.MODE_PRIVATE)

    /** The internal vault: in the app's private files (removed with the app or its data). */
    private val internalRoot = File(appContext.filesDir, "SyncedSaves")

    /** Downloads waiting to go into the vault (a folder per game, deleted after). */
    private val scratch = File(appContext.cacheDir, "sync-scratch")

    /** The demo's vault, a new folder per demo session, erased when it ends. */
    private val demoParent = File(appContext.cacheDir, "demo-vault")
    @Volatile
    private var demoRoot: File? = null

    private val _states = MutableStateFlow<Map<String, SyncState>>(emptyMap())
    val states: StateFlow<Map<String, SyncState>> = _states.asStateFlow()

    private val _place = MutableStateFlow(currentPlace())

    /** Where the copies go now (the demo's folder while the demo runs). */
    val place: StateFlow<VaultPlace> = _place.asStateFlow()

    private val _syncOnConnect = MutableStateFlow(prefs.getBoolean(KEY_ON_CONNECT, true))
    val syncOnConnect: StateFlow<Boolean> = _syncOnConnect.asStateFlow()

    private val jobs = mutableMapOf<String, Job>()
    private val pending = mutableSetOf<String>()

    /** The syncs stopped by the demo, started again when it ends (iOS `syncsPausedForDemo`). */
    private val pausedForDemo = mutableSetOf<String>()
    private val lock = Any()

    init {
        // Downloads and demo vaults left by a previous process.
        val leftovers = listOf(scratch, demoParent)
        scope.launch(Dispatchers.IO) { leftovers.forEach { it.deleteRecursively() } }
    }

    fun state(deviceId: String): SyncState = _states.value[deviceId] ?: SyncState()

    fun setSyncOnConnect(on: Boolean) {
        prefs.edit().putBoolean(KEY_ON_CONNECT, on).apply()
        _syncOnConnect.value = on
    }

    // MARK: the vault

    /** The tree chosen with the picker, if any. */
    val folderUri: Uri? get() = prefs.getString(KEY_FOLDER, null)?.let(Uri::parse)

    private fun currentPlace(): VaultPlace = when {
        demoRoot != null -> VaultPlace(VaultLocation.DEMO)
        folderUri != null -> VaultPlace(VaultLocation.FOLDER, prefs.getString(KEY_FOLDER_NAME, null))
        else -> VaultPlace(VaultLocation.INTERNAL)
    }

    /**
     * The vault to use now ([deviceId]: the console it is for). Read again
     * at every use: a folder changed in Settings is followed; a screen
     * keeps the vault it listed from (restore and share read that one).
     */
    fun vault(deviceId: String? = null): SaveVault {
        demoRoot?.let { root -> if (deviceId == null || isDemo(deviceId)) return SaveVault(FileVaultStore(root), VaultLocation.DEMO, log = vaultLog) }
        val folder = folderUri ?: return SaveVault(FileVaultStore(internalRoot), VaultLocation.INTERNAL, log = vaultLog)
        return SaveVault(SafVaultStore(appContext, folder), VaultLocation.FOLDER, log = vaultLog)
    }

    /** What the vault kept or could not clean: a fixed text with the type of a failure, no name nor path. */
    private val vaultLog: (String) -> Unit = { message -> Log.w(TAG, message) }

    /**
     * Takes the folder chosen with the picker (`ACTION_OPEN_DOCUMENT_TREE`):
     * its permission is persisted, then checked by listing it. The previous
     * folder's permission is released; the copies already made stay where
     * they are (never moved, never deleted).
     */
    suspend fun chooseFolder(tree: Uri) = withContext(Dispatchers.IO) {
        val flags = Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION
        appContext.contentResolver.takePersistableUriPermission(tree, flags)
        val store = SafVaultStore(appContext, tree)
        try {
            store.children(store.root())
        } catch (e: Exception) {
            runCatching { appContext.contentResolver.releasePersistableUriPermission(tree, flags) }
            throw e
        }
        val previous = folderUri
        prefs.edit().putString(KEY_FOLDER, tree.toString()).putString(KEY_FOLDER_NAME, store.displayName).apply()
        if (previous != null && previous != tree) releaseFolder(previous)
        _place.value = currentPlace()
    }

    /** Back to the app's internal storage; the folder's copies stay in it. */
    fun useInternalStorage() {
        val previous = folderUri
        prefs.edit().remove(KEY_FOLDER).remove(KEY_FOLDER_NAME).apply()
        previous?.let(::releaseFolder)
        _place.value = currentPlace()
    }

    private fun releaseFolder(tree: Uri) {
        runCatching {
            appContext.contentResolver.releasePersistableUriPermission(tree, Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION)
        }
    }

    // MARK: syncs

    /** A connection to [deviceId] succeeded: its sync starts if the setting is on (after a running one). */
    fun onConnected(deviceId: String) {
        if (_syncOnConnect.value) start(deviceId, afterCurrent = true)
    }

    /**
     * Starts the sync of a console, unless one is running for it (never two
     * at once). With [afterCurrent] (a new connection), a running sync is
     * followed by a new one once it ends.
     */
    fun start(deviceId: String, afterCurrent: Boolean = false) {
        synchronized(lock) {
            // Asked while the demo runs, for a real console: run when the demo ends (iOS).
            if (demoRunning() && !isDemo(deviceId)) {
                pausedForDemo += deviceId
                return
            }
            if (jobs[deviceId] != null) {
                if (afterCurrent) pending += deviceId
                return
            }
            update(deviceId) { it.copy(phase = SyncPhase.Running(SaveSyncProgress())) }
            val identity = pairingIdentity(deviceId)
            // Registered before it runs: a sync that ends at once still clears its own entry.
            val job = scope.launch(start = kotlinx.coroutines.CoroutineStart.LAZY) {
                try {
                    val report = run(deviceId, identity)
                    update(deviceId) { SyncState(SyncPhase.Finished(report), report) }
                } catch (e: CancellationException) {
                    update(deviceId) { s -> s.copy(phase = s.lastReport?.let { SyncPhase.Finished(it) } ?: SyncPhase.Idle) }
                } catch (e: Exception) {
                    Log.w(TAG, "sync failed: ${e.javaClass.simpleName}")
                    update(deviceId) { it.copy(phase = SyncPhase.Failed(e)) }
                } finally {
                    val again = synchronized(lock) {
                        jobs.remove(deviceId)
                        pending.remove(deviceId)
                    }
                    if (again) start(deviceId)
                }
            }
            jobs[deviceId] = job
            job.start()
        }
    }

    /** Stops the running sync of a console, and the one a connection asked for (versions already copied stay). */
    fun cancel(deviceId: String) {
        val job = synchronized(lock) {
            pending.remove(deviceId)
            jobs[deviceId]
        }
        job?.cancel()
    }

    /** The console was forgotten or paired again: its sync stops (no request of the old pairing goes out). */
    fun dropConsole(deviceId: String) {
        cancel(deviceId)
        synchronized(lock) { pausedForDemo.remove(deviceId) }
    }

    private suspend fun run(deviceId: String, identity: Any?): SaveSyncReport {
        if (identity == null) throw link.reminis.core.ApiException.NotPaired()
        val vault = vault(deviceId)
        val source = ApiSyncSource {
            if (pairingIdentity(deviceId) != identity) throw CancellationException("pairing changed")
            client(deviceId).guarded { pairingIdentity(deviceId) == identity && (isDemo(deviceId) || !demoRunning()) }
        }
        withContext(Dispatchers.IO) { scratch.mkdirs() }
        val sync = SaveSync(VaultConsoleInfo(deviceId, consoleName(deviceId).ifEmpty { deviceId }), source, vault, scratch)
        return sync.run { progress -> update(deviceId) { s -> if (s.phase is SyncPhase.Running) s.copy(phase = SyncPhase.Running(progress)) else s } }
    }

    private fun update(deviceId: String, change: (SyncState) -> SyncState) {
        _states.update { it + (deviceId to change(it[deviceId] ?: SyncState())) }
    }

    // MARK: demo mode

    /** The demo starts: the real consoles' syncs stop (started again when it ends); the demo gets a new temporary vault. */
    fun startDemo() {
        val running = synchronized(lock) { jobs.keys.toList() }
        synchronized(lock) { pausedForDemo += running }
        running.forEach(::cancel)
        demoRoot = File(demoParent, UUID.randomUUID().toString())
        _place.value = currentPlace()
    }

    /** The demo ended: its vault is erased (now and again shortly after a late write), the stopped syncs go on. */
    fun endDemo(demoDeviceId: String?) {
        demoDeviceId?.let { id -> cancel(id); _states.update { it - id } }
        val root = demoRoot
        demoRoot = null
        _place.value = currentPlace()
        if (root != null) {
            scope.launch(Dispatchers.IO) {
                root.deleteRecursively()
                kotlinx.coroutines.delay(2_000)
                root.deleteRecursively()
            }
        }
        val resumed = synchronized(lock) { pausedForDemo.toList().also { pausedForDemo.clear() } }
        resumed.forEach { start(it, afterCurrent = true) }
    }

    /** Where the folder picker opens: null (the system's choice) except in tests. */
    @Volatile
    var initialFolderForTests: Uri? = null

    /** Tests only: the demo's vault folder (null outside the demo). */
    @androidx.annotation.VisibleForTesting
    val demoVaultForTests: File? get() = demoRoot

    private companion object {
        const val TAG = "ReminisLinkSync"
        const val KEY_ON_CONNECT = "syncOnConnect"
        const val KEY_FOLDER = "folderUri"
        const val KEY_FOLDER_NAME = "folderName"
    }
}
