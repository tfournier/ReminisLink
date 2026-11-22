package link.reminis.core

import java.util.UUID
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive

// Models of the cheat routes of protocol/openapi.yaml (`/games/{gameId}/cheats`,
// `/games/{gameId}/cheats/import`, `/sd/files`), and the local editor of a
// cheat list. Mirror of GameDataModels.swift (cheats) and CheatEditor.swift.

/**
 * One cheat of a `.cht` file (`Cheat`). [extra] holds the keys of this
 * cheat the pak does not interpret, without their `cheat<index>_` prefix:
 * sent back untouched, the pak prefixes them with the cheat's new position.
 */
@Serializable
data class Cheat(
    /** Position in the file when read (renumbered by the pak on write). */
    val index: Int? = null,
    val description: String,
    val code: String,
    val enabled: Boolean,
    val extra: Map<String, String> = emptyMap(),
)

/** `GET` and `PUT /games/{gameId}/cheats`; [etag] goes back in `If-Match`. */
@Serializable
data class CheatList(val cheats: List<Cheat>, val etag: String)

/** Body of `PUT /games/{gameId}/cheats`: the whole list, in order. */
@Serializable
data class CheatsUpdate(val cheats: List<Cheat>)

/** Where imported cheats come from (no `url`: removed from v1). */
sealed interface CheatImportSource {
    fun toJson(): JsonObject

    /** A `.cht` of the SD card (`id` of `GET /sd/files?type=cht`). */
    data class Sd(val sdFileId: String) : CheatImportSource {
        override fun toJson() = JsonObject(mapOf("type" to JsonPrimitive("sd"), "sdFileId" to JsonPrimitive(sdFileId)))
    }

    /** A staging file, by the `stagingId` its upload returned (valid 24 h). */
    data class Staging(val stagingId: String) : CheatImportSource {
        override fun toJson() = JsonObject(mapOf("type" to JsonPrimitive("staging"), "stagingId" to JsonPrimitive(stagingId)))
    }
}

/** `merge` adds the imported cheats after the current ones; `replace` replaces the list. */
enum class CheatImportMode(val wire: String) { MERGE("merge"), REPLACE("replace") }

/** `POST /games/{gameId}/cheats/import`. */
data class CheatImportRequest(val source: CheatImportSource, val mode: CheatImportMode) {
    fun toJson(): String = JsonObject(mapOf("source" to source.toJson(), "mode" to JsonPrimitive(mode.wire))).toString()
}

/** `200` of `POST /games/{gameId}/cheats/import`. */
@Serializable
data class CheatImportResult(val imported: Int, val total: Int)

/** `type` of `GET /sd/files`. */
enum class SdFileType(val wire: String) { CHT("cht"), PATCH("patch") }

/** A usable file of the SD card; `path` is informative only, never sent back. */
@Serializable
data class SdFile(val id: String, val path: String, val sizeBytes: Long) {
    val fileName: String get() = path.substringAfterLast('/')
}

@Serializable
data class SdFileList(val files: List<SdFile>)

object SdFiles {
    /** The files named after the game first, then by path. */
    fun sortedForGame(files: List<SdFile>, fileName: String, name: String): List<SdFile> {
        fun matches(f: SdFile): Boolean {
            val n = f.fileName.lowercase()
            return n.startsWith(fileName.lowercase()) || n.startsWith(name.lowercase())
        }
        return files.sortedWith(compareBy<SdFile> { !matches(it) }.thenBy(String.CASE_INSENSITIVE_ORDER) { it.path })
    }
}

/**
 * Local edits of a cheat list, saved as a whole with `If-Match`
 * (docs/specs/04-api-v1.md, Cheats), mirror of `CheatEditor.swift`.
 *
 * Edits stay local until [update] is sent. If the file changed on the
 * console meanwhile, the pak answers `412` ([isConflict]): the edits are
 * never sent again with another etag; the only way forward is to reload
 * ([load]), which discards them. Unknown keys ([Cheat.extra]) travel back
 * untouched. Each cheat carries a local id (never sent) for the screen.
 */
class CheatEditor(list: CheatList) {
    /** A cheat of the list being edited, with its local id. */
    data class Entry(val id: String, val cheat: Cheat)

    var original: List<Cheat> = list.cheats
        private set
    var etag: String = list.etag
        private set
    var entries: List<Entry> = list.cheats.map(::entry)
        private set

    val cheats: List<Cheat> get() = entries.map { it.cheat }

    /** Bumped by every change of the shown list: a read started before a change never overwrites it. */
    var revision: Int = 0
        private set

    /** A newer list of the console held back by local edits; [discardChanges] shows it. */
    var consoleList: CheatList? = null
        private set

    /** What a list read from the pak did ([applyRead]). */
    enum class ReadOutcome {
        /** Shown (no edit since the request, or no edit at all). */
        APPLIED,

        /** The same list (same etag): the local edits stay, still savable. */
        UNCHANGED,

        /** Another list: the local edits stay (never clobbered) and can no longer be saved over it. */
        HELD,
    }

    /**
     * A list read from the pak, whose request started at [since]
     * ([revision] then). It replaces the shown list only if nothing was
     * edited since the request (or nothing is edited at all): edits made
     * while the read was in flight are never lost (mirror of `CheatsLoader.swift`).
     */
    fun applyRead(list: CheatList, since: Int): ReadOutcome {
        if (revision == since || !hasChanges) {
            load(list)
            return ReadOutcome.APPLIED
        }
        if (list.etag == etag) {
            consoleList = null
            return ReadOutcome.UNCHANGED
        }
        consoleList = list
        return ReadOutcome.HELD
    }

    /** Edits not saved yet (order, content, additions, deletions). */
    val hasChanges: Boolean get() = cheats != original

    /** Replaces everything with a list read from the pak: local edits are dropped. */
    fun load(list: CheatList) {
        original = list.cheats
        etag = list.etag
        entries = list.cheats.map(::entry)
        consoleList = null
        revision++
    }

    /** Drops the local edits; a newer list of the console, if one was held back, is shown. */
    fun discardChanges() {
        val held = consoleList
        if (held != null) {
            load(held)
        } else {
            entries = original.map(::entry)
            revision++
        }
    }

    /** Adds a cheat at the end; false when it is not valid. */
    fun add(description: String, code: String, enabled: Boolean): Boolean {
        val (d, c) = validated(description, code) ?: return false
        entries = entries + entry(Cheat(description = d, code = c, enabled = enabled))
        revision++
        return true
    }

    /** Changes a cheat (its `extra` keys are kept); false when not valid or gone. */
    fun update(id: String, description: String, code: String, enabled: Boolean): Boolean {
        val (d, c) = validated(description, code) ?: return false
        if (entries.none { it.id == id }) return false
        entries = entries.map { if (it.id == id) it.copy(cheat = it.cheat.copy(description = d, code = c, enabled = enabled)) else it }
        revision++
        return true
    }

    fun setEnabled(id: String, enabled: Boolean) {
        entries = entries.map { if (it.id == id) it.copy(cheat = it.cheat.copy(enabled = enabled)) else it }
        revision++
    }

    fun remove(id: String) {
        entries = entries.filter { it.id != id }
        revision++
    }

    /** Moves a cheat one place up (-1) or down (+1). */
    fun move(id: String, by: Int) {
        val from = entries.indexOfFirst { it.id == id }
        if (from < 0) return
        val to = (from + by).coerceIn(0, entries.size - 1)
        if (to == from) return
        val list = entries.toMutableList()
        list.add(to, list.removeAt(from))
        entries = list
        revision++
    }

    /** The body of `PUT /cheats`: the whole list in order, renumbered. */
    fun update(): CheatsUpdate = CheatsUpdate(cheats.mapIndexed { i, c -> c.copy(index = i) })

    companion object {
        private fun entry(cheat: Cheat) = Entry(UUID.randomUUID().toString(), cheat)

        /** A failed save because the file changed on the console (`412`). */
        fun isConflict(error: Throwable): Boolean = (error as? ApiException.Server)?.status == 412

        /** Trimmed description and code, both required and on one line (a line break would end the value in the `.cht`). */
        fun validated(description: String, code: String): Pair<String, String>? {
            val d = singleLine(description)
            val c = singleLine(code)
            if (d.isEmpty() || c.isEmpty()) return null
            return d to c
        }

        private fun singleLine(text: String) = text.split("\r\n", "\n", "\r", " ", " ").joinToString(" ").trim()
    }
}
