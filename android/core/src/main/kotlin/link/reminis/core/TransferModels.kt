package link.reminis.core

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put

// Models of the transfer, save and state routes of protocol/openapi.yaml
// (`/uploads`, `/games/{gameId}/save...`, `/games/{gameId}/states...`).
// Mirror of TransferModels.swift and GameDataModels.swift.

/** What to do when the destination exists (`OnConflict`, `rom` and `staging` targets only). */
enum class OnConflict(val wire: String) {
    /** `409 conflict` (the default). */
    FAIL("fail"),

    /** Adds ` (2)`, ` (3)`... to the name. */
    RENAME("rename"),

    /** The old file goes to the trash of the console. */
    OVERWRITE("overwrite"),
}

/** Destination of an upload (`UploadTarget`): opaque ids only, never a path. */
sealed interface UploadTarget {
    val kind: String

    /** A game file sent to a system (and optionally one of its folders). */
    data class Rom(val systemId: String, val fileName: String, val folderId: String? = null) : UploadTarget {
        override val kind get() = "rom"
    }

    /** The save of a game; the pak copies the current one first (`before-replace`). */
    data class Save(val gameId: String) : UploadTarget {
        override val kind get() = "save"
    }

    /** A state slot of a game; the current state goes to the trash. */
    data class State(val gameId: String, val slot: Int) : UploadTarget {
        override val kind get() = "state"
    }

    /** A file for the staging area (a patch, a `.cht` to import): `complete` answers its `stagingId`. */
    data class Staging(val fileName: String) : UploadTarget {
        override val kind get() = "staging"
    }

    /**
     * M12: a pak archive (zip) to install; `complete` answers the `jobId` of
     * a `pak-install` job. `onConflict` accepts `fail` and `overwrite` only.
     */
    data class Pak(val fileName: String, val pakKind: PakInstallKind = PakInstallKind.AUTO) : UploadTarget {
        override val kind get() = "pak"
    }

    fun toJson(): JsonObject = buildJsonObject {
        put("kind", kind)
        when (this@UploadTarget) {
            is Rom -> {
                put("systemId", systemId)
                if (folderId != null) put("folderId", folderId)
                put("fileName", fileName)
            }
            is Save -> put("gameId", gameId)
            is State -> {
                put("gameId", gameId)
                put("slot", slot)
            }
            is Staging -> put("fileName", fileName)
            is Pak -> {
                put("pakKind", pakKind.wire)
                put("fileName", fileName)
            }
        }
    }
}

/** `POST /uploads`. `onConflict` only matters for a `rom` target. */
data class CreateUploadRequest(
    val target: UploadTarget,
    val sizeBytes: Long,
    val sha256: String,
    val onConflict: OnConflict = OnConflict.FAIL,
) {
    fun toJson(): String = JsonObject(
        mapOf(
            "target" to target.toJson(),
            "sizeBytes" to JsonPrimitive(sizeBytes),
            "sha256" to JsonPrimitive(sha256),
            "onConflict" to JsonPrimitive(onConflict.wire),
        ),
    ).toString()
}

/** `201` of `POST /uploads`. */
@Serializable
data class UploadCreated(val uploadId: String, val chunkSize: Long, val offset: Long, val expiresAt: String)

/** `GET /uploads/{uploadId}`. */
@Serializable
data class UploadStatus(val uploadId: String, val offset: Long, val sizeBytes: Long)

/**
 * `200` of `POST /uploads/{uploadId}/complete`, depending on the target:
 * `gameId` (rom), `backup` (save: the copy of the previous save, null if
 * there was none) or `slot` (state).
 */
@Serializable
data class UploadResult(
    val gameId: String? = null,
    val stagingId: String? = null,
    val backup: Backup? = null,
    val slot: Slot? = null,
    val jobId: String? = null,
)

/** Reasons of a backup copy (`Backup.reason`), kept as strings: an unknown one never fails a list. */
object BackupReasons {
    const val MANUAL = "manual"
    const val BEFORE_REPLACE = "before-replace"
    const val BEFORE_RESTORE = "before-restore"
    const val BEFORE_DELETE = "before-delete"
    const val BEFORE_PATCH = "before-patch"

    /** M19: the save before the import of a game archive. */
    const val BEFORE_IMPORT = "before-import"
}

/** A backup copy of a save, on the console. */
@Serializable
data class Backup(
    val id: String,
    val label: String? = null,
    val createdAt: String,
    val sizeBytes: Long,
    val reason: String,
) {
    val isAutomatic: Boolean get() = reason != BackupReasons.MANUAL
}

@Serializable
data class BackupList(val backups: List<Backup>)

/** `backup`: the copy of the previous save, null when there was none. */
@Serializable
data class BackupResult(val backup: Backup? = null)

@Serializable
data class CreateBackupRequest(val label: String? = null)

/** The label of a backup copy: at most 80 characters, counted in code points (the pak's runes). */
object BackupLabel {
    const val MAX_LENGTH = 80

    /** Trimmed and cut to [MAX_LENGTH] code points; null when empty (the label is optional). */
    fun normalized(text: String): String? {
        val trimmed = text.trim()
        if (trimmed.isEmpty()) return null
        return limited(trimmed)
    }

    /** [text] cut to [MAX_LENGTH] code points, never splitting a surrogate pair. */
    fun limited(text: String): String {
        if (text.codePointCount(0, text.length) <= MAX_LENGTH) return text
        return text.substring(0, text.offsetByCodePoints(0, MAX_LENGTH))
    }
}

/** Lock statuses of a state slot (`Slot.lockStatus`). */
object LockStatus {
    const val INTACT = "intact"
    const val RESTORED = "restored"
    const val MISSING = "missing"
    const val MODIFIED = "modified"
    const val CORE_CHANGED = "core-changed"
}

/** A state slot (`Slot`). */
@Serializable
data class Slot(
    val slot: Int,
    val sizeBytes: Long,
    val modifiedAt: String,
    val hasThumbnail: Boolean,
    val locked: Boolean,
    val lockStatus: String? = null,
) {
    val isMissing: Boolean get() = lockStatus == LockStatus.MISSING

    /** Overwritten in game and restored at the last start of the pak (reported once). */
    val wasRestored: Boolean get() = lockStatus == LockStatus.RESTORED

    /** There is a state file to download. */
    val canDownload: Boolean get() = !isMissing

    /** The pak refuses to delete a locked slot (`409 state_locked`). */
    val canDelete: Boolean get() = !locked && !isMissing

    /** The locked copy can be put back (not when it comes from another emulator core). */
    val canRestore: Boolean get() = locked && lockStatus != LockStatus.CORE_CHANGED

    /** The state holds the bytes of the locked copy: unlocking erases the copy. */
    val matchesLockedCopy: Boolean get() = lockStatus == LockStatus.INTACT || lockStatus == LockStatus.RESTORED

    /** The number NextUI shows: slots 0 to 7 are shown 1 to 8. */
    val displayNumber: Int get() = slot + 1
}

/** `GET /games/{gameId}/states`. */
@Serializable
data class SlotList(val slots: List<Slot>, val resumeSlot: Int? = null) {
    /** The slots the user saves to (0 to 7, shown 1 to 8), without the resume slot. */
    val userSlots: List<Slot> get() = slots.filter { it.slot in USER_SLOTS && it.slot != resumeSlot }

    /** The automatic resume state, if present. */
    val resume: Slot? get() = resumeSlot?.let { r -> slots.firstOrNull { it.slot == r } }

    /** The same list with one slot replaced (after lock, unlock, restore). */
    fun replacing(slot: Slot): SlotList {
        val others = slots.filter { it.slot != slot.slot }
        return copy(slots = (others + slot).sortedBy { it.slot })
    }

    companion object {
        val USER_SLOTS = 0..7
    }
}

@Serializable
data class TrashMoveResult(val trashEntryId: String, val moved: List<String> = emptyList(), val movedBytes: Long = 0)

/** The game as answered by `PUT` and `DELETE /games/{gameId}/art` (only what the app reads). */
@Serializable
data class ArtGame(val id: String, val name: String, val hasArt: Boolean = false)

/**
 * A file the app can download, named by opaque ids only (never a path):
 * the console resolves it again for the request.
 */
sealed interface DownloadSource {
    val segments: List<String>

    /** The query of the request (canonical form, signed as sent). */
    val query: List<Pair<String, String>> get() = emptyList()

    /** `GET /games/{gameId}/files/{fileId}` (M11). */
    data class GameFile(val gameId: String, val fileId: String) : DownloadSource {
        override val segments get() = listOf("games", gameId, "files", fileId)
    }

    /** `GET /games/{gameId}/save`. */
    data class Save(val gameId: String) : DownloadSource {
        override val segments get() = listOf("games", gameId, "save")
    }

    /** `GET /games/{gameId}/save/backups/{backupId}`. */
    data class SaveBackup(val gameId: String, val backupId: String) : DownloadSource {
        override val segments get() = listOf("games", gameId, "save", "backups", backupId)
    }

    /** `GET /games/{gameId}/states/{slot}`. */
    data class State(val gameId: String, val slot: Int) : DownloadSource {
        override val segments get() = listOf("games", gameId, "states", slot.toString())
    }

    /**
     * M19: `GET /games/{gameId}/archive`, the game's data as a zip built as
     * it is sent (no `Content-Length`); [rom]: the ROM too (single-file
     * games only, else `422 unsupported_format`).
     */
    data class GameArchive(val gameId: String, val rom: Boolean) : DownloadSource {
        override val segments get() = listOf("games", gameId, "archive")
        override val query get() = if (rom) listOf("rom" to "true") else emptyList()
    }

    /** M22: `GET /screenshots/{screenshotId}`, the whole image. */
    data class Screenshot(val screenshotId: String) : DownloadSource {
        override val segments get() = listOf("screenshots", screenshotId)
    }
}

/** A download stopped at its size limit (announced by `Content-Length`, or reached while reading). */
class DownloadLimitExceededException(val limit: Long) : java.io.IOException("download larger than $limit bytes")

/** What a file added from the app is (docs/specs/06-app-ios.md §4.6), for this increment. */
enum class AddKind {
    ROM,

    /** `.sav`, `.srm`: replaces the save of a game. */
    SAVE,
    ;

    companion object {
        val saveExtensions = setOf("sav", "srm")

        fun suggested(fileName: String): AddKind = if (fileExtension(fileName) in saveExtensions) SAVE else ROM

        fun fileExtension(name: String): String {
            val dot = name.lastIndexOf('.')
            return if (dot <= 0) "" else name.substring(dot + 1).lowercase()
        }
    }
}

/** Suggests the system of a game file from its extension; the user always confirms or changes it. */
object SystemSuggestion {
    /** Extension → NextUI tags of the systems that take it, best first (same table as the iOS app). */
    val tagsByExtension: Map<String, List<String>> = mapOf(
        "gba" to listOf("GBA", "MGBA"),
        "gb" to listOf("GB", "GBC"),
        "gbc" to listOf("GBC", "GB"),
        "nes" to listOf("FC"),
        "fds" to listOf("FDS"),
        "sfc" to listOf("SFC", "SUPA"),
        "smc" to listOf("SFC", "SUPA"),
        "md" to listOf("MD"),
        "gen" to listOf("MD"),
        "smd" to listOf("MD"),
        "sms" to listOf("SMS"),
        "gg" to listOf("GG"),
        "pce" to listOf("PCE"),
        "ngp" to listOf("NGP", "NGPC"),
        "ngc" to listOf("NGPC", "NGP"),
        "ws" to listOf("WS", "WSC"),
        "wsc" to listOf("WSC", "WS"),
        "lnx" to listOf("LYNX"),
        "vb" to listOf("VB"),
        "min" to listOf("PKM"),
        "a26" to listOf("A2600"),
        "pbp" to listOf("PS"),
        "chd" to listOf("PS"),
        "cue" to listOf("PS"),
        "m3u" to listOf("PS"),
        "p8" to listOf("P8", "PICO"),
    )

    /** The first system whose tag matches the extension of [fileName]. */
    fun system(fileName: String, systems: List<GameSystem>): GameSystem? {
        val tags = tagsByExtension[AddKind.fileExtension(fileName)].orEmpty()
        for (tag in tags) {
            systems.firstOrNull { it.tag.equals(tag, ignoreCase = true) }?.let { return it }
        }
        return null
    }
}

/** The code of a server error, if [error] is one. */
val Throwable.serverCode: String? get() = (this as? ApiException.Server)?.code

/** For `409 conflict` on an upload chunk: the offset the console expects. */
val ApiException.Server.expectedOffset: Long?
    get() = if (code == ErrorCodes.CONFLICT) (details["offset"] as? JsonPrimitive)?.content?.toLongOrNull() else null
