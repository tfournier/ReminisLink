package link.reminis.core

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.longOrNull

// Models of the game archives of milestone M19 (protocol/openapi.yaml, tag
// Archives): `GET /games/{gameId}/archive`, `GET /archives/{stagingId}`,
// `POST /archives/{stagingId}/import` and `DELETE /archives/{stagingId}`.
// Mirror of ArchiveModels.swift. Roles, actions and reasons are kept as
// strings: an unknown value from a newer pak is shown, never a failure.

/** What a file of a game archive is (`ArchiveRole`). An importer maps a file by its role, never by its path. */
object ArchiveRoles {
    const val SAVE = "save"
    const val RTC = "rtc"
    const val STATE = "state"
    const val STATE_THUMBNAIL = "state-thumbnail"
    const val CHEATS = "cheats"
    const val ART = "art"
    const val ROM = "rom"
}

/** What the import does with one file (`ArchivePlanEntry.action`). */
object ArchiveActions {
    const val CREATE = "create"
    const val REPLACE = "replace"
    const val SKIP = "skip"
}

/** Why a file is skipped (`ArchivePlanEntry.reason`); an unknown reason is shown as skipped. */
object ArchiveSkipReasons {
    const val GAME_EXISTS = "game-exists"
    const val LOCKED = "locked"
    const val UNSUPPORTED_FORMAT = "unsupported-format"
    const val NO_STATE_FOLDER = "no-state-folder"
    const val NO_STATE = "no-state"
    const val NO_TARGET = "no-target"
}

/** `GameArchiveSource`: where an archive comes from (informative). */
@Serializable
data class GameArchiveSource(
    val deviceName: String? = null,
    val os: String,
    val platform: String? = null,
    val pakVersion: String? = null,
    val systemTag: String,
    val systemLabel: String,
    val gameName: String,
    val fileName: String,
    val romName: String,
    val kind: String,
)

/** `GameArchiveFile`: a file of the manifest. */
@Serializable
data class GameArchiveFile(
    val path: String,
    val role: String,
    val slot: Int? = null,
    val sizeBytes: Long,
    val sha256: String,
)

/** `GameArchiveManifest` (`manifest.json`, format version 1). */
@Serializable
data class GameArchiveManifest(
    val format: String = FORMAT,
    val version: Int = VERSION,
    val createdAt: String,
    val source: GameArchiveSource,
    val files: List<GameArchiveFile>,
) {
    /** The archive holds a ROM: it can make a new game. */
    val hasRom: Boolean get() = files.any { it.role == ArchiveRoles.ROM }

    companion object {
        const val FORMAT = "reminislink-game-archive"
        const val VERSION = 1

        /** The end of the name of an exported archive. */
        const val FILE_SUFFIX = ".reminislink.zip"

        /** Whether a file name looks like an exported game archive (ignoring case). */
        fun isArchiveName(name: String): Boolean = name.lowercase().endsWith(FILE_SUFFIX)

        /** The name suggested for the export of [gameName] (what the pak names it in `Content-Disposition`). */
        fun exportName(gameName: String): String = gameName.ifBlank { "game" } + FILE_SUFFIX
    }
}

/** The wire form of `ArchiveTarget`: exactly one of `gameId` and `systemId`. */
@Serializable
data class ArchiveTargetJson(val gameId: String? = null, val systemId: String? = null, val folderId: String? = null)

/** `ArchiveTarget`: an existing game, or a folder of a system for a new game made from the archive's ROM. */
sealed interface ArchiveTarget {
    data class Game(override val gameId: String) : ArchiveTarget
    data class NewGame(val systemId: String, val folderId: String? = null) : ArchiveTarget

    val gameId: String? get() = null

    /** The query of `GET /archives/{stagingId}`. */
    val query: List<Pair<String, String>>
        get() = when (this) {
            is Game -> listOf("gameId" to gameId)
            is NewGame -> listOf("systemId" to systemId) + listOfNotNull(folderId?.let { "folderId" to it })
        }

    fun toJson(): ArchiveTargetJson = when (this) {
        is Game -> ArchiveTargetJson(gameId = gameId)
        is NewGame -> ArchiveTargetJson(systemId = systemId, folderId = folderId)
    }

    companion object {
        fun from(json: ArchiveTargetJson?): ArchiveTarget? = when {
            json == null -> null
            json.gameId != null -> Game(json.gameId)
            json.systemId != null -> NewGame(json.systemId, json.folderId)
            else -> null
        }
    }
}

/** `ArchivePlanEntry`. */
@Serializable
data class ArchivePlanEntry(
    val role: String,
    val slot: Int? = null,
    val sizeBytes: Long,
    val action: String,
    val reason: String? = null,
)

/** `ArchivePlan`: the answer of `GET /archives/{stagingId}`. */
@Serializable
data class ArchivePlan(
    val manifest: GameArchiveManifest,
    /** The target used (given or suggested), null when none fits. */
    @kotlinx.serialization.SerialName("target") val targetJson: ArchiveTargetJson? = null,
    /** The target game's name, or the system label for a new game. */
    val targetName: String? = null,
    /** The pak chose the target. */
    val suggested: Boolean = false,
    val entries: List<ArchivePlanEntry> = emptyList(),
    val importBytes: Long = 0,
) {
    val target: ArchiveTarget? get() = ArchiveTarget.from(targetJson)

    /** Files the import replaces (their current version goes to the trash first). */
    val replaced: List<ArchivePlanEntry> get() = entries.filter { it.action == ArchiveActions.REPLACE }

    /** Files the import adds. */
    val created: List<ArchivePlanEntry> get() = entries.filter { it.action == ArchiveActions.CREATE }

    /** Files left out (skipped, or an unknown action). */
    val skipped: List<ArchivePlanEntry>
        get() = entries.filter { it.action != ArchiveActions.CREATE && it.action != ArchiveActions.REPLACE }

    /** Whether the import would write anything. */
    val importsAnything: Boolean
        get() = target != null && entries.any { it.action == ArchiveActions.CREATE || it.action == ArchiveActions.REPLACE }

    /**
     * What the confirmation promises about the save (M19 reviews): a copy
     * of the save is sure only when the save is replaced; when only its
     * clock file is, the save may be absent.
     */
    val savePromise: SavePromise
        get() = when {
            replaced.any { it.role == ArchiveRoles.SAVE } -> SavePromise.COPY
            replaced.any { it.role == ArchiveRoles.RTC } -> SavePromise.COPY_IF_ANY
            else -> SavePromise.NONE
        }

    enum class SavePromise { COPY, COPY_IF_ANY, NONE }
}

/** `ArchiveImportRequest`. */
@Serializable
data class ArchiveImportRequest(val target: ArchiveTargetJson)

/** The result of a succeeded `archive-import` job. */
data class ArchiveImportResult(
    val gameId: String,
    val imported: Int,
    val skipped: List<Skipped>,
    val trashEntryId: String?,
    val saveBackupId: String?,
) {
    data class Skipped(val role: String, val slot: Int?, val reason: String)

    companion object {
        /** Reads the `result` of a job; null when it is not an import result. */
        fun from(result: JsonObject?): ArchiveImportResult? {
            result ?: return null
            val gameId = (result["gameId"] as? JsonPrimitive)?.takeIf { it.isString }?.content ?: return null
            val skipped = (result["skipped"] as? JsonArray).orEmpty().mapNotNull { item ->
                val o = item as? JsonObject ?: return@mapNotNull null
                val role = (o["role"] as? JsonPrimitive)?.contentOrNull ?: return@mapNotNull null
                val reason = (o["reason"] as? JsonPrimitive)?.contentOrNull ?: return@mapNotNull null
                Skipped(role, (o["slot"] as? JsonPrimitive)?.intOrNull, reason)
            }
            fun string(key: String) = (result[key] as? JsonPrimitive)?.takeIf { it.isString }?.content
            return ArchiveImportResult(
                gameId = gameId,
                imported = ((result["imported"] as? JsonPrimitive)?.longOrNull ?: 0).toInt(),
                skipped = skipped,
                trashEntryId = string("trashEntryId"),
                saveBackupId = string("saveBackupId"),
            )
        }
    }
}

/** A pak older than M19 has no archive routes. */
class ArchivesNeedNewerPakException : Exception("game archives need a newer pak")
