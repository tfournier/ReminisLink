package link.reminis.core.vault

import java.io.File
import java.io.IOException
import java.io.InputStream
import java.security.MessageDigest
import java.text.Normalizer
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.UUID
import kotlinx.serialization.KSerializer
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import link.reminis.core.NaturalOrder

// Saves (with their clock file) and states copied from the consoles to the
// phone (milestones M9 and M19 of the iOS app, ported by the Android
// increment 7, docs/specs/10-app-android.md §6 septies). Same layout and
// manifests as `FileSaveVault.swift`, so that a vault copied from a device
// to another stays readable:
//
//     <root>/<console>/<system>/<game>/2026-11-19 15.42.10/Game.gba.sav
//                                                         /Game.gba.rtc
//                                                         /.version.json
//     <root>/<console>/<system>/<game>/2026-11-19 15.42.10 state 3/Game.gba.st3
//                                                                  /Game.gba.st3.png
//                                                                  /.state-version.json
//
// with `.console.json` (`deviceId`, `name`) in a console folder and
// `.game.json` (`gameId`, `systemId`, `systemName`, `name`) in a game folder.

/** Versions kept per save and per state slot. */
object SaveVaultLimits {
    const val MAX_VERSIONS = 10
}

/** Where a vault is. */
enum class VaultLocation {
    /** The app's internal storage (the default): on this device only, inside the app. */
    INTERNAL,

    /** A folder chosen with the Android picker: on the device, or at a cloud provider of the picker. */
    FOLDER,

    /** A temporary folder of the demo mode, erased when the demo ends. */
    DEMO,
}

/** Who a console folder belongs to (`.console.json`). */
@Serializable
data class VaultConsoleInfo(val deviceId: String, val name: String)

/** Which game a game folder belongs to (`.game.json`). */
@Serializable
data class VaultGameInfo(val gameId: String, val systemId: String, val systemName: String, val name: String)

/** One file of a version. */
@Serializable
data class SaveVersionFile(val name: String, val sha256: String, val sizeBytes: Long)

/** The manifest of a version: `.version.json` (a save) or `.state-version.json` (a state). */
sealed interface VaultVersionRecord {
    val gameId: String

    /** Publication order in its series (1 for the first; 0 for a version of the first iOS builds). */
    val sequence: Int

    /** RFC 3339, when the phone copied it. */
    val syncedAt: String

    /** RFC 3339, from the console clock (informative). */
    val modifiedAt: String?

    /** The files, the main one first. */
    val files: List<SaveVersionFile>

    /** `save`, or `state/<slot>`. */
    val series: String

    /** The main file and the optional one (null when absent), as the iOS manifest key lists them. */
    val fileSlots: List<SaveVersionFile?>

    /**
     * What tells two versions apart when everything else is equal, written
     * as iOS writes it (`sha/size/name` or `-` per file, then `modifiedAt` in
     * seconds as a Swift `Double`): the same order on every device.
     */
    val manifestKey: String
        get() = fileSlots.joinToString("|") { f -> f?.let { "${it.sha256}/${it.sizeBytes}/${it.name}" } ?: "-" } +
            "|" + swiftSeconds(modifiedAt?.let(::parseInstant))

    /** The sync date at its full precision (ordering, as iOS compares its `Date`); the epoch if unreadable. */
    val syncedAtInstant: Instant get() = parseInstant(syncedAt) ?: Instant.EPOCH
}

/** What a save version holds (`.version.json`). */
@Serializable
data class SaveVersionInfo(
    override val gameId: String,
    override val sequence: Int = 0,
    override val syncedAt: String,
    override val modifiedAt: String? = null,
    val save: SaveVersionFile,
    /** The clock file of the game, when the console has one. */
    val rtc: SaveVersionFile? = null,
) : VaultVersionRecord {
    override val files: List<SaveVersionFile> get() = listOfNotNull(save, rtc)
    override val fileSlots: List<SaveVersionFile?> get() = listOf(save, rtc)
    override val series: String get() = "save"
}

/** What a state version holds (`.state-version.json`, M19). */
@Serializable
data class StateVersionInfo(
    override val gameId: String,
    val slot: Int,
    override val sequence: Int,
    override val syncedAt: String,
    override val modifiedAt: String? = null,
    val state: SaveVersionFile,
    /** The thumbnail of the state (a PNG), when the console has one. */
    val thumbnail: SaveVersionFile? = null,
) : VaultVersionRecord {
    override val files: List<SaveVersionFile> get() = listOfNotNull(state, thumbnail)
    override val fileSlots: List<SaveVersionFile?> get() = listOf(state, thumbnail)
    override val series: String get() = "state/$slot"
}

/** One file of an assembly list (`.rl-partial.json`). */
@Serializable
internal data class AssemblyFile(val name: String, val sha256: String)

/** The assembly list of a folder being written: the files it will hold, written before any of them. */
@Serializable
internal data class Assembly(val files: List<AssemblyFile>)

/** A stored save version; [id]: its folder name (or its path in the vault, in another folder of the game). */
data class SaveVersion(val id: String, val info: SaveVersionInfo)

/** A stored state version. */
data class StateVersion(val id: String, val info: StateVersionInfo)

data class VaultConsole(val info: VaultConsoleInfo, val folderName: String) {
    val id: String get() = info.deviceId
}

data class VaultGame(
    val info: VaultGameInfo,
    /** `<system>/<game>`: the folder names in the console folder. */
    val folderPath: String,
    val latest: SaveVersion?,
    val versionCount: Int,
    /** The slots with at least one state version, in order. */
    val stateSlots: List<Int> = emptyList(),
    val latestState: StateVersion? = null,
) {
    val id: String get() = info.gameId

    /** The folder name: unique, even for two games with the same name ("Name (2)"). */
    val title: String get() = folderPath.substringAfterLast('/')
}

/** A downloaded file to store in a new version (copied in; the caller deletes its own copy). */
data class NewVersionFile(val file: File, val name: String, val sha256: String)

/** Copies of a version's files, outside the vault: delete [directory] once used. */
data class ExportedVersion(val files: List<File>) {
    val main: File get() = files[0]
    val directory: File get() = files[0].parentFile
}

sealed class SaveVaultException(message: String) : IOException(message) {
    class VersionNotFound : SaveVaultException("version not found")

    /** The copy read back from the vault is not the version chosen (changed by hand, or damaged). */
    class VersionMismatch : SaveVaultException("version changed")

    /** A file name the vault cannot use (empty, a path...). */
    class InvalidName(name: String) : SaveVaultException("invalid name: $name")
}

/**
 * The store of synced saves and states (port of `FileSaveVault.swift`).
 *
 * - A version is assembled in a hidden folder of its game folder
 *   (`.rl-partial-…`, never read as a version), its manifest written last,
 *   then renamed to its final name in one step. A store that cannot rename
 *   (a provider without rename support) gets the version written again
 *   under its final name, manifest last, then the hidden copy deleted:
 *   never the other way round. A folder without a readable manifest is not
 *   a version: never counted nor deleted.
 * - Nothing is ever deleted but the oldest versions of a series beyond
 *   [maxVersions] when a new one is published, each only if its folder
 *   holds exactly its listed files with their size and SHA-256; never the
 *   new one, never the newest restorable one. Versions are ordered by their
 *   publication `sequence`, never by date (the phone clock can go back).
 * - Console and game folders are found by the identifier of their
 *   `.console.json` / `.game.json`, never by name; everything reads the
 *   union of the folders of one console (same `deviceId`) or one game
 *   (same `gameId`), canonical first (a vault copied from devices that
 *   each made a folder). Unlike iOS, folders are never tidied (moved).
 */
class SaveVault(
    val store: VaultStore,
    val location: VaultLocation,
    maxVersions: Int = SaveVaultLimits.MAX_VERSIONS,
    private val zone: ZoneId = ZoneId.systemDefault(),
    private val now: () -> Instant = Instant::now,
    /** Says what the vault kept or could not clean (the type of a failure, never a name nor a path). */
    private val log: (String) -> Unit = {},
) {
    private val maxVersions = maxOf(1, maxVersions)
    private val lock = Any()

    /** Console folders by device id, game folders by device then game id; canonical first. */
    private var consoleIndex: Map<String, List<Node>>? = null
    private val gameIndex = mutableMapOf<String, MutableMap<String, List<Node>>>()

    /** A folder of the vault and its path from the root (names). */
    private class Node(val entry: VaultEntry, val path: List<String>) {
        val name: String get() = entry.name
        fun child(entry: VaultEntry) = Node(entry, path + entry.name)
    }

    private class Stored<I : VaultVersionRecord>(val id: String, val info: I, val folder: Node)

    // MARK: reading

    /** The consoles with a folder, by name. */
    fun consoles(): List<VaultConsole> = synchronized(lock) {
        reset()
        loadConsoleIndex().mapNotNull { (deviceId, folders) ->
            val folder = folders.first()
            val info = readJson<VaultConsoleInfo>(folder, CONSOLE_FILE) ?: return@mapNotNull null
            VaultConsole(VaultConsoleInfo(deviceId, info.name), folder.name)
        }.sortedWith(compareBy(NaturalOrder) { it.folderName })
    }

    /** The games of a console with at least one version, by system then name. */
    fun games(deviceId: String): List<VaultGame> = synchronized(lock) {
        reset()
        loadGameIndex(deviceId).mapNotNull { (gameId, folders) ->
            val folder = folders.first()
            val info = readJson<VaultGameInfo>(folder, GAME_FILE) ?: return@mapNotNull null
            val versions = shown(stored(SaveVersionInfo.serializer(), SAVE_MANIFEST, folders, gameId))
            val states = shownBySeries(stored(StateVersionInfo.serializer(), STATE_MANIFEST, folders, gameId))
            if (versions.isEmpty() && states.isEmpty()) return@mapNotNull null
            val path = folder.path.takeLast(2).joinToString("/")
            VaultGame(
                info, path, versions.firstOrNull()?.let { SaveVersion(it.id, it.info) }, versions.size,
                states.map { it.info.slot }.distinct().sorted(),
                states.maxByOrNull { it.info.syncedAtInstant }?.let { StateVersion(it.id, it.info) },
            )
        }.sortedWith(
            compareBy<VaultGame, String>(NaturalOrder) { it.info.systemName }
                .thenBy(NaturalOrder) { it.info.name }
                .thenBy(NaturalOrder) { it.folderPath },
        )
    }

    /** The save versions of a game, last published first (identical successive versions shown once). */
    fun versions(deviceId: String, gameId: String): List<SaveVersion> = synchronized(lock) {
        shown(stored(SaveVersionInfo.serializer(), SAVE_MANIFEST, gameFolders(deviceId, gameId), gameId)).map { SaveVersion(it.id, it.info) }
    }

    fun latestVersion(deviceId: String, gameId: String): SaveVersion? = versions(deviceId, gameId).firstOrNull()

    /**
     * Reaches the vault folder and lists it: [VaultUnavailableException] when
     * it cannot be used (a folder of the picker whose permission was removed,
     * a provider gone). The sync calls it first, even with nothing to copy.
     */
    fun checkAccess() = synchronized(lock) {
        store.children(store.root())
        Unit
    }

    /**
     * The newest save version of a game, only if its files (of it or of a
     * version identical to it) are there with their size; null otherwise,
     * so that the sync copies the save again instead of saying "up to date".
     */
    fun latestPresentVersion(deviceId: String, gameId: String): SaveVersion? = synchronized(lock) {
        val group = groups(stored(SaveVersionInfo.serializer(), SAVE_MANIFEST, gameFolders(deviceId, gameId), gameId)).firstOrNull()
            ?: return@synchronized null
        if (group.any(::present)) SaveVersion(group[0].id, group[0].info) else null
    }

    /** [latestStateVersions], each slot only if the files of its newest version are there with their size. */
    fun latestPresentStateVersions(deviceId: String, gameId: String, slots: Set<Int>): Map<Int, StateVersion> = synchronized(lock) {
        // Only the slots the console listed: a slot kept in the vault but gone from the console is never read nor hashed.
        stored(StateVersionInfo.serializer(), STATE_MANIFEST, gameFolders(deviceId, gameId), gameId)
            .filter { it.info.slot in slots }
            .groupBy { it.info.slot }
            .mapNotNull { (slot, versions) ->
                val group = groups(versions).firstOrNull() ?: return@mapNotNull null
                if (group.any(::present)) slot to StateVersion(group[0].id, group[0].info) else null
            }.toMap()
    }

    /** The state versions of a game, by slot, then last published first. */
    fun stateVersions(deviceId: String, gameId: String): List<StateVersion> = synchronized(lock) {
        shownBySeries(stored(StateVersionInfo.serializer(), STATE_MANIFEST, gameFolders(deviceId, gameId), gameId))
            .sortedWith { a, b -> if (a.info.slot != b.info.slot) a.info.slot.compareTo(b.info.slot) else newerFirst(a, b) }
            .map { StateVersion(it.id, it.info) }
    }

    /** The newest version of every state slot of a game. */
    fun latestStateVersions(deviceId: String, gameId: String): Map<Int, StateVersion> {
        val latest = mutableMapOf<Int, StateVersion>()
        for (version in stateVersions(deviceId, gameId)) latest.putIfAbsent(version.info.slot, version)
        return latest
    }

    /**
     * Copies the files of [version] into a new folder under [directory],
     * checked against its SHA-256: what is sent to a console (or shared) is
     * always the version the user chose. The version is found by what it
     * is (its manifest), wherever it is now; when its copy does not match
     * (a file changed by hand), an identical version is tried, then every
     * candidate again after reloading the folders.
     */
    fun export(deviceId: String, gameId: String, version: SaveVersion, directory: File): ExportedVersion =
        synchronized(lock) { exportRecord(deviceId, gameId, version.info, version.id, SaveVersionInfo.serializer(), SAVE_MANIFEST, directory) }

    fun exportState(deviceId: String, gameId: String, version: StateVersion, directory: File): ExportedVersion =
        synchronized(lock) { exportRecord(deviceId, gameId, version.info, version.id, StateVersionInfo.serializer(), STATE_MANIFEST, directory) }

    private fun <I : VaultVersionRecord> exportRecord(
        deviceId: String, gameId: String, info: I, id: String, serializer: KSerializer<I>, manifest: String, directory: File,
    ): ExportedVersion {
        fun attempt(folders: List<Node>): Pair<ExportedVersion?, Boolean> {
            val stored = stored(serializer, manifest, folders, gameId)
            val candidates = stored.filter { it.info == info } + stored.filter { it.info != info && sameContent(it.info, info) }
            for (candidate in candidates) {
                val copy = try {
                    copyOut(candidate, directory)
                } catch (e: VaultUnavailableException) {
                    throw e
                } catch (e: Exception) {
                    continue
                }
                if (copy.second == info.files.map { it.sha256 }) return ExportedVersion(copy.first) to true
                copy.first.firstOrNull()?.parentFile?.deleteRecursively()
            }
            return null to (candidates.isNotEmpty() || stored.any { it.id == id })
        }
        val first = attempt(gameFolders(deviceId, gameId))
        first.first?.let { return it }
        reset()
        val second = attempt(gameFolders(deviceId, gameId))
        second.first?.let { return it }
        throw if (first.second || second.second) SaveVaultException.VersionMismatch() else SaveVaultException.VersionNotFound()
    }

    /** Copies the files of a version out, with the SHA-256 of what was copied. */
    private fun <I : VaultVersionRecord> copyOut(stored: Stored<I>, directory: File): Pair<List<File>, List<String>> {
        val destination = File(directory, UUID.randomUUID().toString())
        if (!destination.mkdirs()) throw IOException("export folder not created")
        try {
            val children = store.children(stored.folder.entry)
            val copies = stored.info.files.map { file ->
                val entry = children.firstOrNull { it.name == file.name && !it.isDirectory } ?: throw SaveVaultException.VersionNotFound()
                val target = File(destination, file.name)
                val sha = target.outputStream().use { out -> store.openRead(entry).use { copyHashing(it, out) } }
                target to sha
            }
            if (copies.isEmpty()) throw SaveVaultException.VersionNotFound()
            return copies.map { it.first } to copies.map { it.second }
        } catch (e: Exception) {
            destination.deleteRecursively()
            throw e
        }
    }

    // MARK: writing

    /**
     * Stores a new save version (the files are copied in) with the next
     * sequence number, then deletes the oldest versions beyond the limit
     * (never the new one).
     */
    fun addVersion(console: VaultConsoleInfo, game: VaultGameInfo, save: NewVersionFile, rtc: NewVersionFile?, modifiedAt: String?): SaveVersion =
        synchronized(lock) {
            val (id, info) = publish(console, game, "save", listOfNotNull(save, rtc), SaveVersionInfo.serializer(), SAVE_MANIFEST, { versionName(it) }) { sequence, syncedAt, files ->
                SaveVersionInfo(game.gameId, sequence, syncedAt, modifiedAt, files[0], files.getOrNull(1))
            }
            SaveVersion(id, info)
        }

    /** Stores a new version of a state slot, then deletes the oldest versions of that slot beyond the limit. */
    fun addStateVersion(
        console: VaultConsoleInfo, game: VaultGameInfo, slot: Int, state: NewVersionFile, thumbnail: NewVersionFile?, modifiedAt: String?,
    ): StateVersion = synchronized(lock) {
        val (id, info) = publish(console, game, "state/$slot", listOfNotNull(state, thumbnail), StateVersionInfo.serializer(), STATE_MANIFEST, { "${versionName(it)} state $slot" }) { sequence, syncedAt, files ->
            StateVersionInfo(game.gameId, slot, sequence, syncedAt, modifiedAt, files[0], files.getOrNull(1))
        }
        StateVersion(id, info)
    }

    private fun <I : VaultVersionRecord> publish(
        console: VaultConsoleInfo,
        game: VaultGameInfo,
        series: String,
        files: List<NewVersionFile>,
        serializer: KSerializer<I>,
        manifest: String,
        folderName: (Instant) -> String,
        makeInfo: (sequence: Int, syncedAt: String, files: List<SaveVersionFile>) -> I,
    ): Pair<String, I> {
        val names = files.map { checkedFileName(it.name) }
        if (names.map { it.lowercase() }.toSet().size != names.size) throw SaveVaultException.InvalidName(names.last())
        var folders = gameFolders(console.deviceId, game.gameId)
        if (folders.isEmpty()) folders = listOf(ensureGameFolder(console, game))
        val gameFolder = folders[0]
        tidy(gameFolder)
        val sequence = (stored(serializer, manifest, folders, game.gameId).filter { it.info.series == series }.maxOfOrNull { it.info.sequence } ?: 0) + 1
        val syncedAt = now().let { Instant.ofEpochSecond(it.epochSecond) }
        val versionFiles = files.zip(names).map { (file, name) -> SaveVersionFile(name, file.sha256.lowercase(), file.file.length()) }
        val info = makeInfo(sequence, syncedAt.toString(), versionFiles)
        val name = uniqueName(folderName(syncedAt), gameFolder)
        val manifestBytes = VAULT_JSON.encodeToString(serializer, info).toByteArray()
        val planned = files.zip(names).map { (file, fileName) ->
            // Hashed as copied: a file that changed since it was checked is never published.
            Planned(fileName, file.sha256.lowercase()) { out -> file.file.inputStream().use { copyHashing(it, out) } }
        } + Planned(manifest, sha256(manifestBytes.inputStream())) { out -> copyHashing(manifestBytes.inputStream(), out) }
        val published = publishFolder(gameFolder, name, planned)
        rotate(serializer, manifest, series, folders, published, game.gameId)
        return published.name to info
    }

    /** A file a publication writes: its name, its SHA-256 known beforehand, and how to write it (returns the SHA-256 written). */
    private class Planned(val name: String, val sha256: String, val write: (java.io.OutputStream) -> String)

    /** The identity file of a console or game folder, planned. */
    private fun identityFile(name: String, json: String): Planned {
        val bytes = json.toByteArray()
        return Planned(name, sha256(bytes.inputStream())) { out -> copyHashing(bytes.inputStream(), out) }
    }

    /**
     * A new folder [name] in [parent] holding the [planned] files (the
     * manifest last): assembled under a hidden temporary name, then renamed
     * in one step; a store that cannot rename gets the folder written again
     * under its final name, then the hidden copy cleaned (a new item first,
     * the old one after).
     *
     * Every folder being written first receives its assembly list
     * (`.rl-partial.json`: the names and SHA-256 of the files it will hold),
     * so that a cleanup, later or on a failure, deletes only those files,
     * each read again and matching its SHA-256: a file another device put in
     * the folder is never touched. The list is removed once the folder is
     * complete (a leftover one in a version folder is allowed and purged with
     * it).
     */
    private fun publishFolder(parent: Node, name: String, planned: List<Planned>): Node {
        val partial = store.createFolder(parent.entry, "$PARTIAL_PREFIX${now().toEpochMilli()}-${UUID.randomUUID()}")
        try {
            writeAll(partial, planned)
            val renamed = try {
                store.rename(partial, name)
            } catch (e: VaultUnavailableException) {
                throw e
            } catch (e: IOException) {
                null
            }
            if (renamed != null) {
                dropAssemblyList(renamed)
                return parent.child(renamed)
            }
            val folder = store.createFolder(parent.entry, name)
            try {
                writeAll(folder, planned)
            } catch (e: Exception) {
                runCatching { removeListed(folder, now = true) }
                    .onFailure { log("vault: an unfinished version folder without manifest was left (${e.javaClass.simpleName})") }
                throw e
            }
            dropAssemblyList(folder)
            runCatching { removeListed(partial, now = true) }.onFailure { log("vault: an assembly folder could not be cleaned") }
            return parent.child(folder)
        } catch (e: Exception) {
            runCatching { removeListed(partial, now = true) }
            throw e
        }
    }

    /** The assembly list first, then each planned file (its SHA-256 as written must be the planned one). */
    private fun writeAll(folder: VaultEntry, planned: List<Planned>) {
        val list = VAULT_JSON.encodeToString(Assembly.serializer(), Assembly(planned.map { AssemblyFile(it.name, it.sha256) })).toByteArray()
        store.createFile(folder, ASSEMBLY_FILE) { out -> out.write(list) }
        for (file in planned) {
            var written = ""
            store.createFile(folder, file.name) { out -> written = file.write(out) }
            if (written != file.sha256) throw IOException("file changed while stored")
        }
    }

    /** Removes the assembly list of a folder now complete (best effort: a leftover list is allowed in a version folder). */
    private fun dropAssemblyList(folder: VaultEntry) {
        try {
            store.children(folder).firstOrNull { it.name == ASSEMBLY_FILE && !it.isDirectory }?.let { store.delete(it) }
        } catch (e: VaultUnavailableException) {
            throw e
        } catch (e: IOException) {
            log("vault: an assembly list was left in a version folder")
        }
    }

    /**
     * Cleans a folder written by this vault from its assembly list: each
     * listed file still there is read again and deleted only if it matches
     * its SHA-256 (anything else, not listed or changed, stays); the list
     * goes once none of its files is left; then the folder, if it is empty
     * ([removeIfEmpty]). Without a list, only an empty folder is removed.
     * [now]: a folder this publication itself just wrote (no waiting).
     */
    private fun removeListed(folder: VaultEntry, now: Boolean) {
        val children = store.children(folder)
        val listEntry = children.firstOrNull { it.name == ASSEMBLY_FILE && !it.isDirectory }
        if (listEntry != null) {
            val list = try {
                store.openRead(listEntry).use { VAULT_JSON.decodeFromString(Assembly.serializer(), readAtMost(it, MAX_MANIFEST_BYTES.toInt()).decodeToString()) }
            } catch (e: VaultUnavailableException) {
                throw e
            } catch (e: Exception) {
                log("vault: an assembly list could not be read: its folder is kept")
                return
            }
            var left = 0
            for (file in list.files) {
                val entry = children.firstOrNull { it.name == file.name && !it.isDirectory } ?: continue
                val sha = try {
                    store.openRead(entry).use { sha256(it) }
                } catch (e: VaultUnavailableException) {
                    throw e
                } catch (e: IOException) {
                    null
                }
                if (sha == file.sha256 && store.delete(entry)) continue
                left++
                log("vault: a file of an assembly folder was kept (changed, or not deleted)")
            }
            if (left == 0) store.delete(listEntry)
        }
        removeIfEmpty(folder, justWritten = now)
    }

    /**
     * Deletes one of the vault's own folders (a version folder by its name,
     * or an assembly folder `.rl-partial-…`) once it reads empty, right
     * before the deletion. A storage provider (SAF) deletes a folder with
     * whatever is in it at that moment: there the folder must also be
     * untouched for more than an hour (its last modification), so that only
     * a file put in an empty folder that stayed so for an hour, within the
     * milliseconds between the reading and the deletion, could be lost.
     */
    private fun removeIfEmpty(folder: VaultEntry, justWritten: Boolean = false): Boolean {
        if (!isOwnFolder(folder.name)) return false
        if (store.foldersNeedAge) {
            if (justWritten) return false
            val modified = folder.modifiedMillis ?: return false
            if (now().toEpochMilli() - modified < FOLDER_AGE_MILLIS) return false
        }
        if (store.children(folder).isNotEmpty()) return false
        return store.delete(folder)
    }

    /**
     * Housekeeping of a folder at a publication (a game folder; the vault
     * root and system folders for the console and game folders): assembly
     * folders older than an hour, and version folders left without a
     * manifest (a purge, a failed copy), are cleaned from their assembly
     * list and removed once empty ([removeListed], [removeIfEmpty]).
     */
    private fun tidy(folder: Node) {
        val limit = now().toEpochMilli() - FOLDER_AGE_MILLIS
        for (entry in store.children(folder.entry)) {
            if (!entry.isDirectory) continue
            try {
                if (entry.name.startsWith(PARTIAL_PREFIX)) {
                    val started = entry.name.removePrefix(PARTIAL_PREFIX).substringBefore('-').toLongOrNull() ?: continue
                    if (started < limit) removeListed(entry, now = false)
                } else if (VERSION_FOLDER.matches(entry.name)) {
                    val names = store.children(entry).map { it.name }
                    if (SAVE_MANIFEST in names || STATE_MANIFEST in names) continue
                    removeListed(entry, now = false)
                }
            } catch (e: VaultUnavailableException) {
                throw e
            } catch (e: IOException) {
                log("vault: a leftover folder could not be cleaned (${e.javaClass.simpleName})")
            }
        }
    }

    /**
     * Deletes the versions of a series beyond the limit, counted as shown,
     * oldest published first, in every folder of the game; never [kept],
     * never the newest restorable version, and only a version whose folder
     * holds exactly its listed files with their size and SHA-256.
     */
    private fun <I : VaultVersionRecord> rotate(serializer: KSerializer<I>, manifest: String, series: String, folders: List<Node>, kept: Node, gameId: String) {
        val stored = stored(serializer, manifest, folders, gameId).filter { it.info.series == series }
        val groups = groups(stored)
        if (groups.size <= maxVersions) return
        val restorable = stored.firstOrNull { check(it, manifest).first }
        for (group in groups.drop(maxVersions)) {
            for (version in group) {
                if (version.folder.entry.ref == kept.entry.ref || version.folder.entry.ref == restorable?.folder?.entry?.ref) continue
                deleteVersion(version, serializer, manifest)
            }
        }
    }

    /**
     * Deletes a version beyond the limit, never a folder recursively (a
     * storage provider has no conditional atomic deletion, so the window is
     * kept as short as possible):
     * 1. its folder must hold nothing but regular files: its listed files,
     *    its manifest, a leftover assembly list and known system files (a
     *    sub-folder, even one named `.DS_Store`, keeps it);
     * 2. every listed file is read again (size and SHA-256); one that no
     *    longer matches keeps the whole version;
     * 3. the manifest, read again and unchanged, is deleted first: from then
     *    on the version is neither listed nor counted (an interruption
     *    leaves orphan files without manifest, ignored by every read, never
     *    a listed version that cannot be restored);
     * 4. then each verified file, read again right before its own deletion
     *    (one replaced meanwhile stays), then the system files;
     * 5. then the folder once empty ([removeIfEmpty]; with SAF, at a later
     *    publication, an hour after).
     */
    private fun <I : VaultVersionRecord> purge(version: Stored<I>, serializer: KSerializer<I>, manifest: String) {
        val folder = version.folder.entry
        val children = store.children(folder)
        val expected = setOf(manifest, ASSEMBLY_FILE) + SYSTEM_FILES + version.info.files.map { it.name }
        if (children.any { it.isDirectory || it.name !in expected }) {
            log("vault: a version beyond the limit was kept: it holds an unexpected item")
            return
        }
        val verified = version.info.files.map { file ->
            val entry = children.firstOrNull { it.name == file.name }
            if (entry == null || store.openRead(entry).use { measure(it) } != (file.sizeBytes to file.sha256)) {
                log("vault: a version beyond the limit was kept: a file changed meanwhile")
                return
            }
            entry to file
        }
        val manifestEntry = children.firstOrNull { it.name == manifest } ?: return
        if (readJson(version.folder, manifest, serializer) != version.info || !store.delete(manifestEntry)) {
            log("vault: a version beyond the limit was kept: its manifest changed or could not be deleted")
            return
        }
        for ((entry, file) in verified) {
            // Read again right before its own deletion: a file replaced since the check stays.
            val again = try {
                store.openRead(entry).use { measure(it) }
            } catch (e: VaultUnavailableException) {
                throw e
            } catch (e: IOException) {
                null
            }
            if (again != (file.sizeBytes to file.sha256) || !store.delete(entry)) {
                log("vault: a file of a purged version was kept (changed, or not deleted)")
            }
        }
        children.filter { it.name in SYSTEM_FILES || it.name == ASSEMBLY_FILE }.forEach { store.delete(it) }
        removeIfEmpty(folder)
    }

    private fun <I : VaultVersionRecord> deleteVersion(version: Stored<I>, serializer: KSerializer<I>, manifest: String) {
        val info = readJson(version.folder, manifest, serializer) ?: return
        if (info != version.info || !check(version, manifest).second) return
        try {
            purge(version, serializer, manifest)
        } catch (e: VaultUnavailableException) {
            throw e
        } catch (e: IOException) {
            log("vault: a version beyond the limit was kept (${e.javaClass.simpleName})")
        }
    }

    /**
     * `restorable`: the listed files are there with their size and SHA-256.
     * `exact`: also nothing else in the folder but the manifest (and known
     * system files), all regular files. A folder that cannot be read is
     * neither.
     */
    private fun <I : VaultVersionRecord> check(version: Stored<I>, manifest: String): Pair<Boolean, Boolean> = try {
        val children = store.children(version.folder.entry)
        val ok = version.info.files.all { file ->
            val entry = children.firstOrNull { it.name == file.name }
            entry != null && !entry.isDirectory && entry.sizeBytes == file.sizeBytes && store.openRead(entry).use { sha256(it) } == file.sha256
        }
        val expected = setOf(manifest, ASSEMBLY_FILE) + SYSTEM_FILES + version.info.files.map { it.name }
        ok to (ok && children.all { !it.isDirectory && it.name in expected })
    } catch (e: VaultUnavailableException) {
        throw e
    } catch (e: IOException) {
        false to false
    }

    /**
     * Whether the files of a version are all there with their size and
     * SHA-256 (read again): a version whose file disappeared or changed
     * (from a folder of the picker, by hand) is not "up to date", the sync
     * copies it again from the console.
     */
    private fun <I : VaultVersionRecord> present(version: Stored<I>): Boolean {
        val children = store.children(version.folder.entry)
        return version.info.files.all { file ->
            val entry = children.firstOrNull { it.name == file.name && !it.isDirectory } ?: return@all false
            try {
                store.openRead(entry).use { measure(it) } == (file.sizeBytes to file.sha256)
            } catch (e: VaultUnavailableException) {
                throw e
            } catch (e: IOException) {
                false
            }
        }
    }

    // MARK: folders

    private fun reset() {
        consoleIndex = null
        gameIndex.clear()
    }

    private fun rootNode() = Node(store.root(), emptyList())

    private fun loadConsoleIndex(): Map<String, List<Node>> {
        consoleIndex?.let { return it }
        val index = mutableMapOf<String, MutableList<Node>>()
        for (folder in ranked(subfolders(rootNode()))) {
            val info = readJson<VaultConsoleInfo>(folder, CONSOLE_FILE) ?: continue
            index.getOrPut(info.deviceId) { mutableListOf() } += folder
        }
        consoleIndex = index
        return index
    }

    private fun loadGameIndex(deviceId: String): MutableMap<String, List<Node>> {
        gameIndex[deviceId]?.let { return it }
        val index = mutableMapOf<String, List<Node>>()
        for (console in loadConsoleIndex()[deviceId].orEmpty()) {
            for (system in ranked(subfolders(console))) {
                for (folder in ranked(subfolders(system))) {
                    val info = readJson<VaultGameInfo>(folder, GAME_FILE) ?: continue
                    index[info.gameId] = index[info.gameId].orEmpty() + folder
                }
            }
        }
        gameIndex[deviceId] = index
        return index
    }

    private fun gameFolders(deviceId: String, gameId: String): List<Node> = loadGameIndex(deviceId)[gameId].orEmpty()

    private fun ensureConsoleFolder(console: VaultConsoleInfo): Node {
        loadConsoleIndex()[console.deviceId]?.firstOrNull()?.let { return it }
        val root = rootNode()
        tidy(root)
        val folder = publishFolder(root, uniqueName(folderName(console.name), root), listOf(identityFile(CONSOLE_FILE, VAULT_JSON.encodeToString(VaultConsoleInfo.serializer(), console))))
        consoleIndex = loadConsoleIndex() + (console.deviceId to listOf(folder))
        gameIndex.remove(console.deviceId)
        return folder
    }

    private fun ensureGameFolder(console: VaultConsoleInfo, game: VaultGameInfo): Node {
        gameFolders(console.deviceId, game.gameId).firstOrNull()?.let { return it }
        val consoleFolder = ensureConsoleFolder(console)
        val system = systemFolder(folderName(game.systemName), consoleFolder)
        tidy(system)
        val folder = publishFolder(system, uniqueName(folderName(game.name), system), listOf(identityFile(GAME_FILE, VAULT_JSON.encodeToString(VaultGameInfo.serializer(), game))))
        loadGameIndex(console.deviceId)[game.gameId] = listOf(folder)
        return folder
    }

    /**
     * The system folder for a new game ([name]: the folder name of its
     * system): the folder with that name, else the folder holding that
     * system under another name (a duplicate suffix), else a new one.
     */
    private fun systemFolder(name: String, console: Node): Node {
        val systems = ranked(subfolders(console))
        systems.firstOrNull { it.name.equals(name, ignoreCase = true) }?.let { return it }
        systems.firstOrNull { systemIdentity(it) == name }?.let { return it }
        return console.child(store.createFolder(console.entry, name))
    }

    /** The system a system folder holds: the folder name of the `systemName` of its games, when they agree. */
    private fun systemIdentity(system: Node): String? {
        val names = mutableSetOf<String>()
        for (game in subfolders(system)) names += folderName(readJson<VaultGameInfo>(game, GAME_FILE)?.systemName ?: return null)
        return names.singleOrNull()
    }

    private fun subfolders(folder: Node): List<Node> =
        store.children(folder.entry).filter { it.isDirectory && !it.name.startsWith(".") }.map(folder::child)

    /**
     * The versions of one kind of the folders of game [gameId], last
     * published first. A version whose manifest names another game (a
     * folder copied into the wrong game by hand) is ignored, as iOS does:
     * it is never shown, restored to this game, counted nor deleted.
     */
    private fun <I : VaultVersionRecord> stored(serializer: KSerializer<I>, manifest: String, games: List<Node>, gameId: String): List<Stored<I>> {
        val result = mutableListOf<Stored<I>>()
        games.forEachIndexed { index, game ->
            for (folder in subfolders(game)) {
                val info = readJson(folder, manifest, serializer) ?: continue
                if (info.gameId != gameId) continue
                result += Stored(if (index == 0) folder.name else folder.path.joinToString("/"), info, folder)
            }
        }
        return result.sortedWith(::newerFirst)
    }

    private fun uniqueName(base: String, folder: Node): String {
        val taken = store.children(folder.entry).map { (placeholderName(it.name) ?: it.name).lowercase() }.toSet()
        if (base.lowercase() !in taken) return base
        var n = 2
        while ("$base ($n)".lowercase() in taken) n++
        return "$base ($n)"
    }

    private inline fun <reified T> readJson(folder: Node, name: String): T? = readJson(folder, name, kotlinx.serialization.serializer<T>())

    private fun <T> readJson(folder: Node, name: String, serializer: KSerializer<T>): T? = try {
        val entry = store.children(folder.entry).firstOrNull { it.name == name && !it.isDirectory }
        if (entry == null || entry.sizeBytes > MAX_MANIFEST_BYTES) {
            null
        } else {
            // Bounded read with the API of Android 8 (`readNBytes` is API 33).
            val bytes = store.openRead(entry).use { readAtMost(it, MAX_MANIFEST_BYTES.toInt() + 1) }
            if (bytes.size > MAX_MANIFEST_BYTES) null else VAULT_JSON.decodeFromString(serializer, bytes.decodeToString())
        }
    } catch (e: VaultUnavailableException) {
        throw e
    } catch (e: Exception) {
        null
    }

    private fun versionName(instant: Instant): String = versionName(instant, zone)

    companion object {
        const val CONSOLE_FILE = ".console.json"
        const val GAME_FILE = ".game.json"
        const val SAVE_MANIFEST = ".version.json"
        const val STATE_MANIFEST = ".state-version.json"
        /** Files a desktop system adds to a folder it shows (macOS, Windows): ignored in a version folder. */
        private val SYSTEM_FILES = setOf(".DS_Store", "Thumbs.db", "desktop.ini")
        private const val PARTIAL_PREFIX = ".rl-partial-"

        /** The assembly list of a folder being written: the names and SHA-256 of the files it will hold. */
        const val ASSEMBLY_FILE = ".rl-partial.json"

        /** How long a leftover folder must stay untouched before a storage provider (SAF) has it deleted. */
        private const val FOLDER_AGE_MILLIS = 3_600_000L

        /** The name of a version folder: `2026-11-19 15.42.10`, `… state 3`, with a ` (2)` the vault may add. */
        private val VERSION_FOLDER = Regex("""\d{4}-\d{2}-\d{2} \d{2}\.\d{2}\.\d{2}( state \d+)?( \(\d+\))?""")

        /** A folder the vault itself makes and may remove once empty: a version folder or an assembly folder. */
        private fun isOwnFolder(name: String) = name.startsWith(PARTIAL_PREFIX) || VERSION_FOLDER.matches(name)
        private const val MAX_MANIFEST_BYTES = 1L shl 20

        private val VAULT_JSON = Json {
            ignoreUnknownKeys = true
            explicitNulls = false
            prettyPrint = true
        }

        private val VERSION_FORMAT: DateTimeFormatter = DateTimeFormatter.ofPattern("yyyy-MM-dd HH.mm.ss")

        /** `2026-11-19 15.42.10`: sorts by date, valid on every file system (no colon). */
        fun versionName(instant: Instant, zone: ZoneId): String = VERSION_FORMAT.format(instant.atZone(zone))

        /**
         * A name made safe for a folder (as `FileSaveVault.folderName`): no
         * path separator or reserved character, no leading or trailing dot,
         * not empty, at most 100 characters.
         */
        fun folderName(name: String): String {
            val reserved = "/\\:*?\"<>|"
            val normalized = Normalizer.normalize(name, Normalizer.Form.NFC)
            val builder = StringBuilder()
            var i = 0
            while (i < normalized.length) {
                val cp = normalized.codePointAt(i)
                if (Character.getType(cp) == Character.CONTROL.toInt() || (cp < 128 && cp.toChar() in reserved)) builder.append('_') else builder.appendCodePoint(cp)
                i += Character.charCount(cp)
            }
            var result = builder.toString().trim().trimStart('.').trimEnd('.')
            if (result.codePointCount(0, result.length) > 100) result = result.substring(0, result.offsetByCodePoints(0, 100)).trim()
            return result.ifEmpty { "_" }
        }

        /** A file name taken from the console: a single, visible path component. */
        fun checkedFileName(name: String): String {
            val bad = name.isBlank() || name != name.trim() || name.startsWith(".") || name.any { it == '/' || it == '\\' || it < ' ' } ||
                name == SAVE_MANIFEST || name == STATE_MANIFEST
            if (bad) throw SaveVaultException.InvalidName(name)
            return name
        }

        /** `("X", 2)` for `X 2` and `X (2)`, `("X", 1)` for `X` (duplicate folders made by a cloud provider or the vault). */
        fun duplicateRank(raw: String): Pair<String, Int> {
            val name = Normalizer.normalize(raw, Normalizer.Form.NFC)
            fun number(digits: String): Int? = digits.takeIf { it.isNotEmpty() && it.all { c -> c in '0'..'9' } }?.toIntOrNull()?.takeIf { it >= 2 }
            if (name.endsWith(")")) {
                val open = name.lastIndexOf(" (")
                if (open > 0) number(name.substring(open + 2, name.length - 1))?.let { return name.substring(0, open) to it }
            }
            val space = name.lastIndexOf(' ')
            if (space > 0) number(name.substring(space + 1))?.let { return name.substring(0, space) to it }
            return name to 1
        }

        /** Whether the folder named [a] is canonical before [b] among folders of one item (the same on every device). */
        fun precedes(a: String, b: String): Boolean {
            val na = duplicateRank(a).second
            val nb = duplicateRank(b).second
            if (na != nb) return na < nb
            return compareCodePoints(Normalizer.normalize(a, Normalizer.Form.NFC), Normalizer.normalize(b, Normalizer.Form.NFC)) < 0
        }

        private fun compareCodePoints(a: String, b: String): Int {
            val x = a.codePoints().toArray()
            val y = b.codePoints().toArray()
            for (i in 0 until minOf(x.size, y.size)) if (x[i] != y[i]) return x[i].compareTo(y[i])
            return x.size.compareTo(y.size)
        }

        private fun ranked(folders: List<Node>): List<Node> = folders.sortedWith { a, b ->
            when {
                precedes(a.name, b.name) -> -1
                precedes(b.name, a.name) -> 1
                else -> 0
            }
        }

        /** `name` for the iCloud placeholder `.name.icloud` of an item not downloaded yet (a vault copied from iOS). */
        private fun placeholderName(raw: String): String? =
            if (raw.startsWith(".") && raw.endsWith(".icloud") && raw.length > ".icloud".length + 1) raw.drop(1).dropLast(".icloud".length) else null

        /**
         * Publication order, newest first, as `FileSaveVault.publishedAfter`:
         * sequence, then the sync date at its full precision, then the
         * version folder name, then the manifest key (code point order).
         */
        private fun <I : VaultVersionRecord> newerFirst(a: Stored<I>, b: Stored<I>): Int {
            val x = a.info
            val y = b.info
            if (x.sequence != y.sequence) return y.sequence.compareTo(x.sequence)
            if (x.syncedAtInstant != y.syncedAtInstant) return y.syncedAtInstant.compareTo(x.syncedAtInstant)
            // Names equal as Swift sees them (canonically equivalent, NFC and NFD alike) go on to the manifest key.
            val names = compareNames(b.folder.name, a.folder.name)
            if (names != 0) return names
            return compareNames(y.manifestKey, x.manifestKey)
        }

        /** Versions in publication order, each identical to the one just before it grouped with it. */
        private fun <I : VaultVersionRecord> groups(stored: List<Stored<I>>): List<List<Stored<I>>> {
            val groups = mutableListOf<MutableList<Stored<I>>>()
            for (version in stored) {
                val last = groups.lastOrNull()
                if (last != null && sameContent(last[0].info, version.info)) last += version else groups += mutableListOf(version)
            }
            return groups
        }

        private fun <I : VaultVersionRecord> shown(stored: List<Stored<I>>): List<Stored<I>> = groups(stored).map { it[0] }

        private fun <I : VaultVersionRecord> shownBySeries(stored: List<Stored<I>>): List<Stored<I>> =
            stored.groupBy { it.info.series }.values.flatMap { shown(it) }.sortedWith(::newerFirst)

        /** Whether two versions hold the same files (names, sizes, SHA-256) of the same series of the same game. */
        private fun sameContent(a: VaultVersionRecord, b: VaultVersionRecord): Boolean =
            a.gameId == b.gameId && a.series == b.series && a.files.size == b.files.size &&
                a.files.zip(b.files).all { (x, y) -> sameFile(x, y) }

        /** Same SHA-256 and size, and names canonically equivalent (NFC and NFD alike), as Swift compares them. */
        private fun sameFile(a: SaveVersionFile, b: SaveVersionFile): Boolean =
            a.sha256 == b.sha256 && a.sizeBytes == b.sizeBytes && compareNames(a.name, b.name) == 0

        /** At most [limit] bytes of [input]. */
        internal fun readAtMost(input: InputStream, limit: Int): ByteArray {
            val out = java.io.ByteArrayOutputStream()
            val buffer = ByteArray(16 * 1024)
            while (out.size() < limit) {
                val n = input.read(buffer, 0, minOf(buffer.size, limit - out.size()))
                if (n < 0) break
                out.write(buffer, 0, n)
            }
            return out.toByteArray()
        }

        /** The size and the SHA-256 of what [input] holds. */
        internal fun measure(input: InputStream): Pair<Long, String> {
            val digest = MessageDigest.getInstance("SHA-256")
            val buffer = ByteArray(64 * 1024)
            var size = 0L
            while (true) {
                val n = input.read(buffer)
                if (n < 0) break
                digest.update(buffer, 0, n)
                size += n
            }
            return size to digest.digest().joinToString("") { "%02x".format(it) }
        }

        internal fun sha256(input: InputStream): String {
            val digest = MessageDigest.getInstance("SHA-256")
            val buffer = ByteArray(64 * 1024)
            while (true) {
                val n = input.read(buffer)
                if (n < 0) break
                digest.update(buffer, 0, n)
            }
            return digest.digest().joinToString("") { "%02x".format(it) }
        }

        /** Copies [input] to [out] and returns the SHA-256 of what was copied. */
        internal fun copyHashing(input: InputStream, out: java.io.OutputStream): String {
            val digest = MessageDigest.getInstance("SHA-256")
            val buffer = ByteArray(64 * 1024)
            while (true) {
                val n = input.read(buffer)
                if (n < 0) break
                digest.update(buffer, 0, n)
                out.write(buffer, 0, n)
            }
            out.flush()
            return digest.digest().joinToString("") { "%02x".format(it) }
        }
    }
}

/** An RFC 3339 date at its full precision (with or without fractional seconds), null if unreadable. */
internal fun parseInstant(text: String): Instant? = try {
    java.time.OffsetDateTime.parse(text).toInstant()
} catch (e: java.time.format.DateTimeParseException) {
    null
}

/**
 * Seconds since the epoch as Swift prints a `Double` (`timeIntervalSince1970`,
 * `0` when absent): `1763481300.0`, `1763481300.5`.
 */
internal fun swiftSeconds(instant: Instant?): String {
    if (instant == null) return "0.0"
    val seconds = instant.epochSecond + instant.nano / 1_000_000_000.0
    val plain = java.math.BigDecimal(seconds.toString()).toPlainString()
    return if ('.' in plain) plain else "$plain.0"
}

/** Code point order of two names (NFC), as Swift compares strings (not UTF-16 units). */
internal fun compareNames(a: String, b: String): Int {
    val x = Normalizer.normalize(a, Normalizer.Form.NFC).codePoints().toArray()
    val y = Normalizer.normalize(b, Normalizer.Form.NFC).codePoints().toArray()
    for (i in 0 until minOf(x.size, y.size)) if (x[i] != y[i]) return x[i].compareTo(y[i])
    return x.size.compareTo(y.size)
}
