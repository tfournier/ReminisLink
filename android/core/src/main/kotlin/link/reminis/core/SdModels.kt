package link.reminis.core

import java.text.Normalizer
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.contentOrNull

// Models of the SD card browser (M12, M23), the renaming of a game (M23)
// and the resume history of a game (protocol/openapi.yaml: `/sd/browse`,
// `POST /sd/folders`, `POST /sd/trash`, `POST /games/{gameId}/rename`,
// `/games/{gameId}/resume-history`). Mirror of SdWriteModels.swift and of
// the SD models of Models.swift.

/** Why a place of the SD card cannot be changed (`SdProtection`, open: kept as a string). */
object SdProtection {
    /** A folder the OS needs (read only with everything in it). */
    const val SYSTEM = "system"

    /** ReminisLink itself (its data, its own pak). */
    const val DATA = "data"

    /** A main folder of the OS, the root of the card, or a folder holding a protected place: never to the trash. */
    const val ROOT = "root"

    /** A folder holding a symbolic link or a special file. */
    const val LINK = "link"
}

/** An entry of `GET /sd/browse`: opaque id, never a path. */
@Serializable
data class SdEntry(
    val sdEntryId: String,
    val name: String,
    val kind: String,
    /** Null for a folder. */
    val sizeBytes: Long? = null,
    val modifiedAt: String,
    /** M23: the pak accepts to move it to the trash (absent from an older pak: false). */
    val deletable: Boolean = false,
    /** M23: why it cannot go to the trash ([SdProtection]). */
    val protection: String? = null,
    /** M23: the opaque version, sent back by `POST /sd/trash` (absent from an older pak). */
    val version: String? = null,
) {
    val isFolder: Boolean get() = kind == "folder"
    val isFile: Boolean get() = kind == "file"

    /** A folder whose name ends with `.pak` (ignoring case). */
    val isPakFolder: Boolean get() = isFolder && name.lowercase().endsWith(".pak")
    val isZip: Boolean get() = isFile && name.lowercase().endsWith(".zip")

    /** What "Install from the SD card" can install: a zip or a `.pak` folder. */
    val isInstallableAsPak: Boolean get() = isPakFolder || isZip
}

/** `GET /sd/browse`: one folder, folders first. */
@Serializable
data class SdFolder(
    val folderId: String? = null,
    val path: String = "",
    val parentId: String? = null,
    val entries: List<SdEntry>,
    val truncated: Boolean = false,
    /** M23: files and folders may be added here (absent from an older pak: read only). */
    val writable: Boolean = false,
) {
    val isRoot: Boolean get() = folderId == null
}

/** `POST /sd/folders`: a new folder in [parentId] (null: the root, sent as `null`). */
data class SdFolderRequest(val parentId: String?, val name: String) {
    fun toJson(): String = JsonObject(
        mapOf("parentId" to (parentId?.let(::JsonPrimitive) ?: JsonNull), "name" to JsonPrimitive(name)),
    ).toString()
}

/**
 * `POST /sd/trash`: 1 to [MAX_ITEMS] entries in one trash entry, all or
 * nothing. Each item carries the version the app showed (size, date,
 * opaque version): the pak refuses an entry changed since (`409
 * details.changed`).
 */
class SdTrashRequest(entries: List<SdEntry>) {
    /** The entries, without duplicates (by id). */
    val items: List<SdEntry> = entries.distinctBy { it.sdEntryId }

    /** Every item carries a version (an older pak's listing cannot be moved). */
    val isComplete: Boolean get() = items.all { !it.version.isNullOrEmpty() }

    fun toJson(): String = JsonObject(
        mapOf(
            "items" to JsonArray(
                items.map {
                    JsonObject(
                        mapOf(
                            "sdEntryId" to JsonPrimitive(it.sdEntryId),
                            // Required, null for a folder.
                            "sizeBytes" to (it.sizeBytes?.let(::JsonPrimitive) ?: JsonNull),
                            "modifiedAt" to JsonPrimitive(it.modifiedAt),
                            "version" to JsonPrimitive(it.version.orEmpty()),
                        ),
                    )
                },
            ),
        ),
    ).toString()

    companion object {
        /** The most entries of one request: the app never splits a selection (it stays all or nothing). */
        const val MAX_ITEMS = 100
    }
}

/**
 * The selection of the Files tab (M23): each entry is kept as it was when
 * selected. A refresh drops the entries that changed or left, and the move
 * to the trash sends the remembered versions, never those of the current
 * listing (Codex review of M23, round 3).
 */
data class SdSelection(val entries: List<SdEntry> = emptyList()) {
    val count: Int get() = entries.size
    fun contains(id: String): Boolean = entries.any { it.sdEntryId == id }

    /** Selects or deselects [entry]; null when it would go beyond [SdTrashRequest.MAX_ITEMS]. */
    fun toggle(entry: SdEntry): SdSelection? = when {
        contains(entry.sdEntryId) -> copy(entries = entries.filterNot { it.sdEntryId == entry.sdEntryId })
        entries.size >= SdTrashRequest.MAX_ITEMS -> null
        else -> copy(entries = entries + entry)
    }

    /** Keeps only the entries still listed with the same version (size, date and version). */
    fun prune(listing: List<SdEntry>): SdSelection = copy(
        entries = entries.filter { selected ->
            val now = listing.firstOrNull { it.sdEntryId == selected.sdEntryId } ?: return@filter false
            now.version == selected.version && now.sizeBytes == selected.sizeBytes && now.modifiedAt == selected.modifiedAt
        },
    )
}

/** `POST /paks/install`: a zip or a `.pak` folder of the SD card (M12). */
data class PakInstallRequest(val sdEntryId: String, val kind: PakInstallKind, val replace: Boolean) {
    fun toJson(): String = JsonObject(
        mapOf(
            "sdEntryId" to JsonPrimitive(sdEntryId),
            "kind" to JsonPrimitive(kind.wire),
            "onConflict" to JsonPrimitive(if (replace) "replace" else "fail"),
        ),
    ).toString()
}

/** A file renamed (informative paths, never sent back). */
@Serializable
data class RenamedFile(val from: String, val to: String)

/** `200` of `POST /games/{gameId}/rename`: the game under its new identifier, and what was renamed. */
@Serializable
data class GameRenameResult(val game: LibraryGame, val renamed: List<RenamedFile> = emptyList())

/** One archived version of the resume state (`GET /games/{gameId}/resume-history`). */
@Serializable
data class ResumeHistoryEntry(val id: String, val capturedAt: String, val sizeBytes: Long)

@Serializable
data class ResumeHistory(val entries: List<ResumeHistoryEntry> = emptyList())

// MARK: errors of M23

/** `409 conflict` for a protected place of the SD card (`details.protected`), null otherwise. */
val ApiException.Server.sdProtection: String?
    get() = if (code == ErrorCodes.CONFLICT) (details["protected"] as? JsonPrimitive)?.contentOrNull else null

/** `409 conflict` for a taken name of the SD browser: the name `onConflict: rename` would give. */
val ApiException.Server.sdFreeName: String?
    get() = if (code == ErrorCodes.CONFLICT) (details["freeName"] as? JsonPrimitive)?.takeIf { it.isString }?.content else null

/** The entries of `POST /sd/trash` that changed since they were listed (`details.changed`). */
val ApiException.Server.sdChanged: List<String>
    get() = if (code == ErrorCodes.CONFLICT) stringList("changed") else emptyList()

/** The taken places of a game rename (`details.conflicts`). */
val ApiException.Server.renameConflicts: List<String>
    get() = if (code == ErrorCodes.CONFLICT) stringList("conflicts") else emptyList()

/** A game whose save, states or discs another game shares (`details.shared`). */
val ApiException.Server.renameSharesDiscs: Boolean
    get() = code == ErrorCodes.CONFLICT && (details["shared"] as? JsonPrimitive)?.booleanOrNull == true

/** The `404` of a route the pak does not have (an older pak): its catch-all answer, message `No route`. */
val ApiException.Server.isMissingRoute: Boolean
    get() = status == 404 && code == ErrorCodes.NOT_FOUND && serverMessage == "No route"

private fun ApiException.Server.stringList(key: String): List<String> =
    (details[key] as? JsonArray)?.mapNotNull { (it as? JsonPrimitive)?.takeIf { p -> p.isString }?.content }.orEmpty()

/**
 * The rules of a new game name, as the pak checks them (M23, mirror of
 * `GameNameRule` of ReminisLinkKit): the app says what is wrong before
 * sending. The pak stays the judge.
 */
object GameNameRule {
    sealed interface Problem {
        data object Empty : Problem

        /** One of `/ \ : * ? " < > |`. */
        data class ForbiddenCharacter(val character: Char) : Problem

        /** A control or invisible format character. */
        data object InvisibleCharacter : Problem
        data object StartsWithDot : Problem

        /** Ends with a space or a dot (exFAT drops them). */
        data object TrailingSpaceOrDot : Problem

        /** A name Windows reserves (CON, NUL, COM1…). */
        data object Reserved : Problem

        /** Longer than 255 bytes with the extension (or the data file names built on it). */
        data object TooLong : Problem
    }

    /** The characters exFAT forbids, and the path separators. */
    val forbidden: Set<Char> = setOf('/', '\\', ':', '*', '?', '"', '<', '>', '|')

    /** The name as sent (NFC, as the pak stores it). */
    fun normalized(name: String): String = Normalizer.normalize(name, Normalizer.Form.NFC)

    /**
     * The first problem of [name] for a game whose ROM has [fileExtension]
     * (with its dot, "" for a folder game), null when the pak should accept
     * it. [longestSuffix]: the longest suffix the pak adds to build a data
     * file name (`.gba.cht.bak` on NextUI).
     */
    fun problem(name: String, fileExtension: String = "", longestSuffix: Int = 16): Problem? {
        val n = normalized(name)
        if (n.isBlank()) return Problem.Empty
        n.firstOrNull { it in forbidden }?.let { return Problem.ForbiddenCharacter(it) }
        var i = 0
        while (i < n.length) {
            val cp = n.codePointAt(i)
            if (cp < 0x20 || cp == 0x7f || cp in 0x80 until 0xa0) return Problem.InvisibleCharacter
            if (Character.getType(cp) == Character.FORMAT.toInt() && cp != 0x200c && cp != 0x200d && cp !in 0xe0020..0xe007f) {
                return Problem.InvisibleCharacter
            }
            i += Character.charCount(cp)
        }
        if (n.startsWith(".")) return Problem.StartsWithDot
        if (n.endsWith(" ") || n.endsWith(".")) return Problem.TrailingSpaceOrDot
        if (isReserved(n)) return Problem.Reserved
        if (n.toByteArray(Charsets.UTF_8).size + fileExtension.toByteArray(Charsets.UTF_8).size + longestSuffix > 255) return Problem.TooLong
        return null
    }

    private fun isReserved(name: String): Boolean {
        val base = name.substringBefore('.').uppercase().trim()
        if (base in setOf("CON", "PRN", "AUX", "NUL")) return true
        return base.length == 4 && (base.startsWith("COM") || base.startsWith("LPT")) && base.last() in '1'..'9'
    }

    /** The name of a game without the extension of its ROM file: the folder name of a folder game. */
    fun editableName(fileName: String, isFolder: Boolean): String {
        val dot = fileName.lastIndexOf('.')
        return if (isFolder || dot <= 0) fileName else fileName.substring(0, dot)
    }

    /** The extension kept by a rename ("" for a folder game). */
    fun keptExtension(fileName: String, isFolder: Boolean): String {
        val dot = fileName.lastIndexOf('.')
        return if (isFolder || dot <= 0) "" else fileName.substring(dot)
    }
}
