package link.reminis.core.vault

import java.io.File
import java.io.OutputStream
import java.util.UUID
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.withContext
import link.reminis.core.ApiClient
import link.reminis.core.ApiException
import link.reminis.core.DownloadSource
import link.reminis.core.ErrorCodes
import link.reminis.core.GameSystem
import link.reminis.core.SaveList
import link.reminis.core.SaveSummary
import link.reminis.core.StateList
import link.reminis.core.StateSummary
import link.reminis.core.UploadFailure

/** What the sync needs from a console ([ApiSyncSource] in the app, the fake pak in tests). */
interface SaveSyncSource {
    suspend fun saves(): SaveList
    suspend fun systems(): List<GameSystem>

    /** `GET /states` (M19): `404`/`405` from a pak older than M19. */
    suspend fun allStates(): StateList
    suspend fun download(source: DownloadSource, out: OutputStream)
}

/** A [SaveSyncSource] over the API; [client] is asked again for every request (the app checks the pairing there). */
class ApiSyncSource(private val client: suspend () -> ApiClient) : SaveSyncSource {
    override suspend fun saves() = client().saves()
    override suspend fun systems() = client().systems()
    override suspend fun allStates() = client().allStates()
    override suspend fun download(source: DownloadSource, out: OutputStream) {
        client().download(source, out)
    }
}

/** Where a sync stands. */
data class SaveSyncProgress(
    /** Saves listed by the console. */
    val total: Int = 0,
    val checked: Int = 0,
    /** New versions stored so far. */
    val stored: Int = 0,
    val currentGame: String? = null,
    /** States listed (0 for a pak without `GET /states`). */
    val statesTotal: Int = 0,
    val statesChecked: Int = 0,
    val statesStored: Int = 0,
) {
    val fraction: Float get() = (total + statesTotal).let { if (it == 0) 0f else (checked + statesChecked).toFloat() / it }
}

/** The file downloaded is not the one listed: it changed on the console during the sync, or arrived damaged. */
class SyncChecksumException(val file: String) : java.io.IOException("checksum mismatch: $file")

/** A save (or a state, [slot]) that could not be copied; the others go on. */
data class SaveSyncFailure(val gameId: String, val gameName: String, val slot: Int? = null, val error: Throwable) {
    val id: String get() = slot?.let { "$gameId/$it" } ?: gameId
}

/** The outcome of a sync that went to its end. */
data class SaveSyncReport(
    val location: VaultLocation,
    val total: Int,
    /** Games with a new version. */
    val stored: List<String>,
    val unchanged: Int,
    val failures: List<SaveSyncFailure>,
    /** The console listed only its first 10,000 saves. */
    val truncated: Boolean,
    val finishedAtMillis: Long,
    /** The states; null when the pak has no `GET /states`. */
    val states: StateSyncReport?,
)

/** The state part of a sync (M19). */
data class StateSyncReport(
    val total: Int,
    /** `<gameId>/<slot>` of the states with a new version. */
    val stored: List<String>,
    val unchanged: Int,
    val failures: List<SaveSyncFailure>,
    val truncated: Boolean,
    /** The listing failed, or the console was lost during the states: they wait for the next sync. */
    val listingError: Throwable? = null,
)

/**
 * Copies the saves of a console that changed since their newest version,
 * then its states (port of `SaveSync.swift`): `GET /saves`, then for each
 * save whose SHA-256 (or the SHA-256 of its `.rtc`) differs from the newest
 * version of that game, `GET /games/{gameId}/save` (and `/save/rtc`), each
 * checked against the listed SHA-256 and stored as a new version (the vault
 * rotates); then `GET /states`, compared per slot (state and thumbnail).
 *
 * A failure on one game is recorded and the sync goes on; losing the
 * console (network error, `401 unknown_client`, `503`), the vault folder
 * becoming unavailable, or a cancellation stops it (versions already
 * stored stay). After the saves, whatever happens to the states, the saves
 * already copied are reported.
 */
class SaveSync(
    val console: VaultConsoleInfo,
    private val source: SaveSyncSource,
    private val vault: SaveVault,
    /** Where downloads wait before going into the vault (a folder per game, deleted after). */
    private val scratchDirectory: File,
    private val nowMillis: () -> Long = System::currentTimeMillis,
) {
    suspend fun run(progress: (SaveSyncProgress) -> Unit = {}): SaveSyncReport {
        // The vault first, even when the console has nothing to copy: a folder that can no longer be
        // reached is said at once (with the recovery actions), never a sync that looks fine.
        withContext(Dispatchers.IO) { vault.checkAccess() }
        val list = source.saves()
        coroutineContextActive()
        // Folder names of the systems: label and tag (GBA and MGBA share a label). Without the list
        // (a busy pak), the tag of the saves folder (`Saves/<TAG>/...`).
        val labels = try {
            source.systems().associate { it.id to systemName(it) }
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            if (stopsSync(e)) throw e
            emptyMap()
        }

        var state = SaveSyncProgress(total = list.saves.size)
        progress(state)
        val stored = mutableListOf<String>()
        var unchanged = 0
        val failures = mutableListOf<SaveSyncFailure>()
        for (save in list.saves) {
            coroutineContextActive()
            state = state.copy(currentGame = save.gameName)
            progress(state)
            try {
                if (sync(save, labels[save.systemId] ?: systemNameFromPath(save.path))) {
                    stored += save.gameId
                    state = state.copy(stored = state.stored + 1)
                } else {
                    unchanged++
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                if (stopsSync(e)) throw e
                failures += SaveSyncFailure(save.gameId, save.gameName, error = e)
            }
            state = state.copy(checked = state.checked + 1, currentGame = null)
            progress(state)
        }

        val statesReport: StateSyncReport? = when (val listing = listStates()) {
            is StateListing.Unsupported -> null
            is StateListing.Failed -> StateSyncReport(0, emptyList(), 0, emptyList(), false, listing.error)
            is StateListing.Listed -> {
                state = state.copy(statesTotal = listing.list.states.size)
                progress(state)
                var stopped: Throwable? = null
                val statesStored = mutableListOf<String>()
                var statesUnchanged = 0
                val stateFailures = mutableListOf<SaveSyncFailure>()
                val latestByGame = mutableMapOf<String, Map<Int, StateVersion>>()
                // The slots of each game the console listed: only those are read in the vault.
                val listedSlots = listing.list.states.groupBy { it.gameId }.mapValues { (_, states) -> states.map { it.slot }.toSet() }
                for (summary in listing.list.states) {
                    if (stopped != null) break
                    coroutineContextActive()
                    state = state.copy(currentGame = summary.gameName)
                    progress(state)
                    try {
                        val latest = latestByGame.getOrPut(summary.gameId) {
                            withContext(Dispatchers.IO) { vault.latestPresentStateVersions(console.deviceId, summary.gameId, listedSlots[summary.gameId].orEmpty()) }
                        }[summary.slot]
                        if (needsNewVersion(summary, latest)) {
                            sync(summary, labels[summary.systemId] ?: systemNameFromStatePath(summary.path))
                            statesStored += summary.id
                            state = state.copy(statesStored = state.statesStored + 1)
                        } else {
                            statesUnchanged++
                        }
                    } catch (e: CancellationException) {
                        throw e
                    } catch (e: Exception) {
                        // Losing the console stops the states, not the report of the saves.
                        // The vault folder became unavailable (a permission removed): the whole sync fails,
                        // so that the app offers another folder; never a mere "states incomplete".
                        if (e is VaultUnavailableException) throw e
                        if (stopsSync(e)) stopped = e
                        stateFailures += SaveSyncFailure(summary.gameId, summary.gameName, summary.slot, e)
                    }
                    state = state.copy(statesChecked = state.statesChecked + 1, currentGame = null)
                    progress(state)
                }
                StateSyncReport(listing.list.states.size, statesStored, statesUnchanged, stateFailures, listing.list.truncated, stopped)
            }
        }
        // Checked again before the report: a folder lost meanwhile (during `GET /states`, with nothing
        // left to write) is said as unavailable, never a sync that looks fine.
        withContext(Dispatchers.IO) { vault.checkAccess() }
        return SaveSyncReport(vault.location, list.saves.size, stored, unchanged, failures, list.truncated, nowMillis(), statesReport)
    }

    private sealed interface StateListing {
        data object Unsupported : StateListing
        class Listed(val list: StateList) : StateListing
        class Failed(val error: Throwable) : StateListing
    }

    /** `GET /states`: unsupported for a pak without the route (`404`, `405`); a failure is reported with the saves. */
    private suspend fun listStates(): StateListing = try {
        StateListing.Listed(source.allStates())
    } catch (e: CancellationException) {
        throw e
    } catch (e: ApiException.Server) {
        if (e.status == 404 || e.status == 405) StateListing.Unsupported else StateListing.Failed(e)
    } catch (e: ApiException.UnexpectedStatus) {
        if (e.status == 404 || e.status == 405) StateListing.Unsupported else StateListing.Failed(e)
    } catch (e: Exception) {
        StateListing.Failed(e)
    }

    /** Stores a new version if the save changed; returns whether it did. */
    private suspend fun sync(save: SaveSummary, systemName: String): Boolean {
        val latest = withContext(Dispatchers.IO) { vault.latestPresentVersion(console.deviceId, save.gameId) }
        if (!needsNewVersion(save, latest)) return false
        withScratch { folder ->
            val saveFile = download(DownloadSource.Save(save.gameId), File(folder, "save"), save.sha256)
            val rtc = save.rtc?.let { clock ->
                try {
                    NewVersionFile(download(DownloadSource.SaveClock(save.gameId), File(folder, "rtc"), clock.sha256), fileName(clock.path), clock.sha256.lowercase())
                } catch (e: ApiException.Server) {
                    // Removed since the listing: the save alone.
                    if (e.status != 404) throw e
                    null
                }
            }
            withContext(Dispatchers.IO) {
                vault.addVersion(
                    console, VaultGameInfo(save.gameId, save.systemId, systemName, save.gameName),
                    NewVersionFile(saveFile, fileName(save.path), save.sha256.lowercase()), rtc, save.modifiedAt,
                )
            }
        }
        return true
    }

    /** Stores a new version of a state (and its thumbnail), each checked against the listed SHA-256. */
    private suspend fun sync(summary: StateSummary, systemName: String) = withScratch { folder ->
        val stateName = fileName(summary.path)
        val stateFile = download(DownloadSource.State(summary.gameId, summary.slot), File(folder, "state"), summary.sha256)
        val thumbnail = summary.thumbnail?.let { thumb ->
            try {
                // Named after the state (a ".png"), never like the state itself.
                NewVersionFile(download(DownloadSource.StateThumbnail(summary.gameId, summary.slot), File(folder, "thumbnail"), thumb.sha256), thumbnailName(stateName), thumb.sha256.lowercase())
            } catch (e: ApiException.Server) {
                if (e.status != 404) throw e
                null
            }
        }
        withContext(Dispatchers.IO) {
            vault.addStateVersion(
                console, VaultGameInfo(summary.gameId, summary.systemId, systemName, summary.gameName), summary.slot,
                NewVersionFile(stateFile, stateName, summary.sha256.lowercase()), thumbnail, summary.modifiedAt,
            )
        }
    }

    private suspend fun <T> withScratch(block: suspend (File) -> T): T {
        val folder = File(scratchDirectory, UUID.randomUUID().toString())
        withContext(Dispatchers.IO) { if (!folder.mkdirs()) throw java.io.IOException("scratch folder not created") }
        try {
            return block(folder)
        } finally {
            withContext(Dispatchers.IO + kotlinx.coroutines.NonCancellable) { folder.deleteRecursively() }
        }
    }

    /** Downloads [source] into [file]; the SHA-256 of what arrived must be [expected]. */
    private suspend fun download(source: DownloadSource, file: File, expected: String): File {
        withContext(Dispatchers.IO) { file.outputStream() }.use { out -> this.source.download(source, out) }
        val sha = withContext(Dispatchers.IO) { file.inputStream().use { SaveVault.sha256(it) } }
        if (sha != expected.lowercase()) throw SyncChecksumException(file.name)
        return file
    }

    private suspend fun coroutineContextActive() = kotlin.coroutines.coroutineContext.ensureActive()

    companion object {
        /** Whether a save needs a new version: its SHA-256 or its clock file's differ from the newest version (or none). */
        fun needsNewVersion(save: SaveSummary, latest: SaveVersion?): Boolean {
            if (latest == null) return true
            return latest.info.save.sha256 != save.sha256.lowercase() || latest.info.rtc?.sha256 != save.rtc?.sha256?.lowercase()
        }

        /** Whether a state needs a new version: its SHA-256 or its thumbnail's differ from the newest version of its slot. */
        fun needsNewVersion(state: StateSummary, latest: StateVersion?): Boolean {
            if (latest == null) return true
            return latest.info.state.sha256 != state.sha256.lowercase() || latest.info.thumbnail?.sha256 != state.thumbnail?.sha256?.lowercase()
        }

        /** Errors that end the whole sync rather than one game. */
        fun stopsSync(error: Throwable): Boolean = when (error) {
            is CancellationException, is VaultUnavailableException -> true
            // A request refused by its guard (the pairing changed, the demo started): never sent.
            is UploadFailure -> error.reason == UploadFailure.Reason.PAUSED
            is ApiException.Network, is ApiException.NotPaired, is ApiException.IdentityChanged, is ApiException.DeviceMismatch -> true
            is ApiException.Server -> error.code == ErrorCodes.UNKNOWN_CLIENT || error.code == ErrorCodes.SHUTTING_DOWN || error.code == ErrorCodes.DISCONNECTED
            else -> false
        }

        /** `Game Boy Advance (GBA)`. */
        fun systemName(system: GameSystem): String = if (system.tag.isEmpty()) system.label else "${system.label} (${system.tag})"

        /** `GBA` for `Saves/GBA/Game.gba.sav`. */
        fun systemNameFromPath(path: String): String = path.split('/').let { if (it.size >= 3) it[it.size - 2] else "_" }

        /** `GBA` for `.userdata/shared/GBA-gpsp/Game.gba.st0`. */
        fun systemNameFromStatePath(path: String): String {
            val parts = path.split('/').filter { it.isNotEmpty() }
            if (parts.size < 2) return "_"
            return parts[parts.size - 2].substringBefore('-')
        }

        /** `Game.gba.st0.png` for `Game.gba.st0`. */
        fun thumbnailName(stateName: String) = "$stateName.png"

        /** The file name of an informative path of the console (its last component). */
        fun fileName(path: String) = path.substringAfterLast('/')
    }
}
