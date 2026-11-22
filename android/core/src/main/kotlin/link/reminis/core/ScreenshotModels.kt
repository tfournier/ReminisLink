package link.reminis.core

import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonArray

// Models and pure logic of milestone M22 (protocol/openapi.yaml, tag
// Screenshots): the screenshots taken on the console. Identifiers are opaque
// and sent back as received. Mirror of ScreenshotModels.swift.

/** `png`, `jpeg` or `bmp` (from the extension); an unknown format from a newer pak is kept (a generic image). */
object ScreenshotFormats {
    const val PNG = "png"
    const val JPEG = "jpeg"
    const val BMP = "bmp"
}

/** A screenshot of `GET /screenshots`. Lenient: `sizeBytes`, `format`, `gameName` and `hasThumbnail` may be missing. */
@Serializable
data class Screenshot(
    val screenshotId: String,
    /** The file name on the card (informative, never sent back). */
    val name: String,
    /** From the file name when the OS writes a date in it, else the date of the file (RFC 3339). */
    val takenAt: String,
    val sizeBytes: Long = 0,
    val format: String = ScreenshotFormats.PNG,
    /** The game the file name gives (informative, never resolved to a game). */
    val gameName: String? = null,
    /** `GET /screenshots/{id}/thumbnail` may make a preview. */
    val hasThumbnail: Boolean = false,
) {
    /** What the app shows as the title: the game, else the file name without its extension. */
    val title: String
        get() {
            gameName?.takeIf { it.isNotEmpty() }?.let { return it }
            val dot = name.lastIndexOf('.')
            val stem = if (dot > 0) name.substring(0, dot) else name
            return stem.ifEmpty { name }
        }

    /** Seconds since the epoch of [takenAt] (0 when unreadable). */
    val takenAtSeconds: Long get() = ApiClient.parseRfc3339Seconds(takenAt) ?: 0

    /** The key of a cached preview: a screenshot replaced under the same name (another size or date) gets a new one. */
    val thumbnailKey: String get() = "$screenshotId|$sizeBytes|$takenAtSeconds"
}

/** `GET /screenshots`. */
@Serializable
data class ScreenshotList(
    val screenshots: List<Screenshot>,
    val totalBytes: Long = screenshots.sumOf { it.sizeBytes },
    /** More than 5000 screenshots: the newest are listed. */
    val truncated: Boolean = false,
) {
    /** One day of the grid: its date in the phone's time zone and its screenshots, newest first. */
    data class Day(val date: LocalDate, val screenshots: List<Screenshot>)

    companion object {
        /**
         * The screenshots grouped by day of `takenAt` in [zone] (the device's:
         * the contract does not give the console's), newest day first, each
         * newest first, then by name.
         */
        fun byDay(screenshots: List<Screenshot>, zone: ZoneId = ZoneId.systemDefault()): List<Day> {
            val sorted = screenshots.sortedWith(compareByDescending<Screenshot> { it.takenAtSeconds }.thenBy { it.name })
            val days = mutableListOf<Day>()
            for (shot in sorted) {
                val date = Instant.ofEpochSecond(shot.takenAtSeconds).atZone(zone).toLocalDate()
                val last = days.lastOrNull()
                if (last != null && last.date == date) {
                    days[days.lastIndex] = last.copy(screenshots = last.screenshots + shot)
                } else {
                    days += Day(date, listOf(shot))
                }
            }
            return days
        }
    }
}

/** `POST /screenshots/trash`. */
@Serializable
data class ScreenshotTrashRequest(val screenshotIds: List<String>) {
    companion object {
        /** The most identifiers of one request. */
        const val MAX_IDS = 500

        /** The requests that move [ids] (duplicates once, order kept), at most [MAX_IDS] each. */
        fun batches(ids: List<String>): List<List<String>> = ids.distinct().chunked(MAX_IDS)
    }
}

/** Only some screenshots went to the trash ([moved]); the list and the trash must be read again. */
class ScreenshotsPartlyMovedException(val moved: Int, cause: Throwable) : Exception("$moved screenshots moved", cause)

/**
 * Moves screenshots to the trash of the console, in batches of
 * [ScreenshotTrashRequest.MAX_IDS] (one trash entry each). A failure after
 * earlier batches moved (or inside a batch: the `500` lists in
 * `details.kept` the screenshots that stayed in the trash) is a
 * [ScreenshotsPartlyMovedException] with the number moved; a failure before
 * anything moved is thrown as is. Returns how many moved.
 */
suspend fun trashScreenshotsInBatches(ids: List<String>, send: suspend (List<String>) -> TrashMoveResult): Int {
    var moved = 0
    for (batch in ScreenshotTrashRequest.batches(ids)) {
        try {
            send(batch)
        } catch (e: kotlinx.coroutines.CancellationException) {
            throw e
        } catch (e: Exception) {
            val server = e as? ApiException.Server
            if (server != null && server.status == 500) moved += (server.details["kept"] as? JsonArray)?.size ?: 0
            if (moved > 0) throw ScreenshotsPartlyMovedException(moved, e)
            throw e
        }
        moved += batch.size
    }
    return moved
}

/** The columns of the grid of screenshots for a width (dp): by the width of the window, never by the device. */
object ScreenshotGridLayout {
    /** The narrowest a cell gets, and the spacing between cells (dp). */
    const val MINIMUM_CELL = 104f
    const val SPACING = 4f

    /** How many columns fit in [width] (at least 2, at most 8). */
    fun columns(width: Float): Int {
        if (!width.isFinite() || width <= 0f) return 2
        val fit = ((width + SPACING) / (MINIMUM_CELL + SPACING)).toInt()
        return fit.coerceIn(2, 8)
    }
}

/**
 * What a selection of screenshots holds (multiple selection). Immutable.
 * It holds listed **versions** ([Screenshot.thumbnailKey]: id, size and
 * date), never bare ids (Codex review round 1 of the Android increment 5):
 * a screenshot replaced on the console under the same name is another
 * version, which leaves the selection at the next reload instead of being
 * moved to the trash in place of the one shown.
 */
data class ScreenshotSelection(val versions: Set<String> = emptySet()) {
    val isEmpty: Boolean get() = versions.isEmpty()
    val count: Int get() = versions.size

    operator fun contains(shot: Screenshot): Boolean = shot.thumbnailKey in versions

    fun toggled(shot: Screenshot) = ScreenshotSelection(if (shot.thumbnailKey in versions) versions - shot.thumbnailKey else versions + shot.thumbnailKey)

    /** Every screenshot, or none when all are selected already. */
    fun toggledAll(all: List<Screenshot>): ScreenshotSelection {
        val every = all.map { it.thumbnailKey }.toSet()
        return ScreenshotSelection(if (every.isNotEmpty() && versions.containsAll(every)) emptySet() else every)
    }

    /** Keeps only the versions still listed (after a reload): a replaced screenshot leaves. */
    fun kept(listed: List<Screenshot>) = ScreenshotSelection(versions intersect listed.map { it.thumbnailKey }.toSet())

    /** The selected screenshots, in the order of [all]. */
    fun selected(all: List<Screenshot>): List<Screenshot> = all.filter { it.thumbnailKey in versions }
}

/** A screenshot shown (or selected) is no longer the one listed by the console: nothing was moved. */
class ScreenshotChangedException : Exception("screenshot changed on the console")

/**
 * The screenshots of [versions] in [listed] (a list just read from the
 * console), or [ScreenshotChangedException] when one of them is gone or
 * was replaced under the same name: the trash always refers to the exact
 * version shown.
 */
fun exactScreenshots(versions: Set<String>, listed: List<Screenshot>): List<Screenshot> {
    val found = listed.filter { it.thumbnailKey in versions }
    if (found.size != versions.size) throw ScreenshotChangedException()
    return found
}

/** A name not in [taken] (ignoring case, like most phone file systems): ` (n)` before the extension. */
fun freeFileName(name: String, taken: MutableSet<String>): String {
    var candidate = name
    var n = 2
    val dot = name.lastIndexOf('.')
    val stem = if (dot > 0) name.substring(0, dot) else name
    val ext = if (dot > 0) name.substring(dot) else ""
    while (candidate.lowercase() in taken) {
        candidate = "$stem ($n)$ext"
        n++
    }
    taken += candidate.lowercase()
    return candidate
}
