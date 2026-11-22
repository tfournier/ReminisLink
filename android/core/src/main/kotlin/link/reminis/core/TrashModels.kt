package link.reminis.core

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull

// The trash of the console (`/trash`, M18 restore `/trash/{entryId}/restore`)
// and the deletion of a game (`/games/{gameId}/deletion`, `DELETE /games/{gameId}`).
// Mirror of TrashModels.swift.

/** One entry of the trash: what a single operation moved there. */
@Serializable
data class TrashEntry(
    val id: String,
    val trashedAt: String,
    val reason: String,
    val label: String,
    val files: List<String> = emptyList(),
    val sizeBytes: Long,
    val expiresAt: String,
    /**
     * M22: holds something moved by mistake that could not go back; never
     * purged automatically (`expiresAt` is then an estimate only).
     */
    val held: Boolean = false,
) {
    /**
     * How many screenshots the entry holds (null for another reason): the
     * pak labels several in English (`3 screenshots`), the app says it in
     * the user's language instead.
     */
    val screenshotCount: Int? get() = if (reason == SCREENSHOT_REASON) files.size else null

    companion object {
        /** The reason of screenshots moved to the trash (M22). */
        const val SCREENSHOT_REASON = "delete-screenshot"
    }
}

@Serializable
data class TrashList(val entries: List<TrashEntry>, val totalBytes: Long)

@Serializable
data class FreedBytes(val freedBytes: Long)

/** `onConflict` of a restore. */
enum class TrashRestoreConflict(val wire: String) {
    /** `409 conflict` when a place is taken (default). */
    FAIL("fail"),

    /** Every item goes back under the same free name. */
    RENAME("rename"),

    /** The save of an existing game (or its backup copy) replaces the current save, kept first. */
    REPLACE("replace"),
}

/** `freeNameUnavailable` values. */
object TrashNoFreeName {
    const val ALL_TAKEN = "all-taken"
    const val PATH_BLOCKED = "path-blocked"
}

/** `replaceKeptAs` / `replacedKeptAs` values. */
object TrashKeptAs {
    const val TRASH = "trash"
    const val BACKUP = "backup"
}

/** A user file of a restore preview. */
@Serializable
data class TrashRestoreItem(
    /** Original place, relative to the SD card (informative). */
    val name: String,
    val size: Long,
    /** The place is taken (same name ignoring case). */
    val conflict: Boolean,
    /** Where it goes with `rename`; `name` without conflict; null when no free name exists. */
    val freeName: String? = null,
    /** Where it goes with `replace`. */
    val replaceName: String? = null,
) {
    val fileName: String get() = name.substringAfterLast('/')
}

/** `GET /trash/{entryId}/restore`; lenient: a first M18 pak has no rename and replace fields. */
@Serializable
data class TrashRestorePreview(
    val entryId: String,
    val reason: String,
    val label: String,
    val conflict: Boolean,
    val items: List<TrashRestoreItem>,
    val restoredBytes: Long,
    val freeNameUnavailable: String? = null,
    val replaceable: Boolean = false,
    val replaceDisplaced: List<String> = emptyList(),
    val replaceKeptAs: String? = null,
    val replaceUnavailable: String? = null,
) {
    /** A conflict that a free name can settle. */
    val canRename: Boolean get() = conflict && freeNameUnavailable == null

    /** Something in the way of `replace` the app can explain (not offered then). */
    val replaceBlocked: Boolean
        get() = !replaceable && (replaceUnavailable == "place-blocked" || replaceUnavailable == "backup-taken")

    /** The copy comes with a clock file: a pair goes in as one journaled replacement. */
    val withClock: Boolean get() = items.any { it.name.lowercase().endsWith(".rtc") }
}

@Serializable
data class TrashRestoreRequest(val onConflict: String)

/** `POST /trash/{entryId}/restore`. */
@Serializable
data class TrashRestoreResult(
    val restored: List<String>,
    val restoredBytes: Long,
    val renamed: Boolean,
    val replacedEntryId: String? = null,
    val replaced: List<String> = emptyList(),
    val replacedKeptAs: String? = null,
)

/** `409 conflict` of a restore: the taken places (`details.conflicts`), null for another error. */
val ApiException.Server.trashRestoreConflicts: List<String>?
    get() {
        if (code != ErrorCodes.CONFLICT) return null
        val values = details["conflicts"] as? JsonArray ?: return null
        return values.mapNotNull { (it as? JsonPrimitive)?.contentOrNull }
    }

/** `409` of a restore under a free name when none exists. */
val ApiException.Server.trashRestoreNoFreeName: String?
    get() = if (code == ErrorCodes.CONFLICT) (details["freeNameUnavailable"] as? JsonPrimitive)?.contentOrNull else null

/** `422` of `replace`: `details.reason`, null for another error. */
val ApiException.Server.trashReplaceRefusal: String?
    get() = if (code == ErrorCodes.UNSUPPORTED_FORMAT) (details["reason"] as? JsonPrimitive)?.contentOrNull else null

/** A file of a deletion preview. */
@Serializable
data class DeletionItem(val name: String, val role: String, val size: Long)

/** `GET /games/{gameId}/deletion`: what `DELETE /games/{gameId}` would move and keep now. */
@Serializable
data class DeletionPreview(val moved: List<DeletionItem>, val kept: List<DeletionItem>, val movedBytes: Long)
