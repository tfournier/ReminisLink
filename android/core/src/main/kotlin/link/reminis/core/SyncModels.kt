package link.reminis.core

import kotlinx.serialization.Serializable

// Models of `GET /saves` (M9) and `GET /states` (M19), protocol/openapi.yaml.
// Mirror of SyncModels.swift and the state part of ArchiveModels.swift.

/** `SaveClockFile`: the `.rtc` that belongs to a save. */
@Serializable
data class SaveClockFile(
    /** Informative, relative to the SD card root. Never sent back. */
    val path: String,
    val sizeBytes: Long,
    val modifiedAt: String,
    val sha256: String,
)

/** `SaveSummary`: the save of one game. */
@Serializable
data class SaveSummary(
    val gameId: String,
    val systemId: String,
    val gameName: String,
    val fileName: String,
    /** Informative (`Saves/<TAG>/<file>.sav`). Never sent back. */
    val path: String,
    val sizeBytes: Long,
    /** From the console clock (informative): compare [sha256]. */
    val modifiedAt: String,
    val sha256: String,
    val rtc: SaveClockFile? = null,
)

/** `GET /saves`. */
@Serializable
data class SaveList(
    val saves: List<SaveSummary>,
    /** More than 10,000 saves: nothing is inferred from a save missing from the list. */
    val truncated: Boolean = false,
    val generatedAt: String? = null,
)

/** `StateThumbnailFile`. */
@Serializable
data class StateThumbnailFile(val sizeBytes: Long, val sha256: String)

/** `StateSummary`: a state of one game, with its SHA-256. */
@Serializable
data class StateSummary(
    val gameId: String,
    val systemId: String,
    val gameName: String,
    val fileName: String,
    val slot: Int,
    /** Informative (`.userdata/shared/<TAG>-<core>/<file>.st<slot>`). Never sent back. */
    val path: String,
    val sizeBytes: Long,
    val modifiedAt: String,
    val sha256: String,
    val thumbnail: StateThumbnailFile? = null,
) {
    val id: String get() = "$gameId/$slot"
}

/** `GET /states`. */
@Serializable
data class StateList(
    val states: List<StateSummary>,
    /** More than 20,000 states. */
    val truncated: Boolean = false,
    val generatedAt: String? = null,
)
