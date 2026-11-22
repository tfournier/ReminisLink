package link.reminis.core

import kotlinx.serialization.Serializable

// The flat library (M11), searched, filtered and sorted on the phone, and
// the folder listing used to browse by console. Mirror of
// LibraryFilter.swift (ReminisLinkKit).

/** Sort orders of the library. */
enum class LibrarySort {
    /** By name (natural order, case- and accent-insensitive). */
    NAME,

    /** Most recently modified first. */
    DATE,

    /** Largest first. */
    SIZE,
}

/**
 * A console of the library filter: the systems that share a label, merged
 * as NextUI shows them (GBA and MGBA, both "Game Boy Advance").
 */
data class SystemGroup(val label: String, val systemIds: Set<String>, val tags: List<String>, val gameCount: Int)

/** What the library shows: search text, filters and sort. */
data class LibraryQuery(
    /** Matched against the game name and its file name (every word, accents and case ignored). */
    val text: String = "",
    /** The label of a [SystemGroup]; null for every console. */
    val systemLabel: String? = null,
    val hasSave: Boolean = false,
    val hasStates: Boolean = false,
    val hasCheats: Boolean = false,
    val sort: LibrarySort = LibrarySort.NAME,
) {
    /** A filter other than the search text is set. */
    val isFiltered: Boolean get() = systemLabel != null || hasSave || hasStates || hasCheats

    /** Every filter off (the search text and the sort stay). */
    fun clearedFilters() = copy(systemLabel = null, hasSave = false, hasStates = false, hasCheats = false)
}

object LibraryFiltering {
    /** The consoles of the filter: the systems that have games, merged by label, sorted by label. */
    fun groups(systems: List<GameSystem>, games: List<LibraryGame>): List<SystemGroup> {
        val counts = games.groupingBy { it.systemId }.eachCount()
        return systems.filter { (counts[it.id] ?: 0) > 0 }.groupBy { it.label }.map { (label, list) ->
            SystemGroup(label, list.map { it.id }.toSet(), list.map { it.tag }.sorted(), list.sumOf { counts[it.id] ?: 0 })
        }.sortedWith { a, b -> NaturalOrder.compare(a.label, b.label) }
    }

    /**
     * The console shown under each game, by system id: its label, with its
     * tag when several systems with games share that label ("Game Boy
     * Advance (MGBA)"); the filter keeps them merged.
     */
    fun rowLabels(systems: List<GameSystem>, games: List<LibraryGame>): Map<String, String> {
        val withGames = games.map { it.systemId }.toSet()
        val perLabel = systems.filter { it.id in withGames }.groupingBy { it.label }.eachCount()
        return systems.associate { it.id to if ((perLabel[it.label] ?: 0) > 1) "${it.label} (${it.tag})" else it.label }
    }

    /** The games matching [query], in its order. */
    fun apply(query: LibraryQuery, games: List<LibraryGame>, groups: List<SystemGroup>): List<LibraryGame> {
        val systemIds = query.systemLabel?.let { label -> groups.firstOrNull { it.label == label }?.systemIds ?: emptySet() }
        val matching = games.filter { game ->
            when {
                systemIds != null && game.systemId !in systemIds -> false
                query.hasSave && !game.hasSave -> false
                query.hasStates && game.stateCount == 0 -> false
                query.hasCheats && game.cheatCount == 0 -> false
                else -> LibrarySearch.matches(game.name, query.text) || LibrarySearch.matches(game.fileName, query.text)
            }
        }
        return sorted(matching, query.sort, { it.name }, { it.modifiedAt }, { it.sizeBytes })
    }

    /** Ties keep a stable order (the pak's: system folder, then path). */
    fun <T> sorted(items: List<T>, sort: LibrarySort, name: (T) -> String, modifiedAt: (T) -> String, size: (T) -> Long): List<T> =
        when (sort) {
            LibrarySort.NAME -> items.sortedWith { a, b -> NaturalOrder.compare(name(a), name(b)) }
            // RFC 3339 in UTC from the pak: the text sorts as the date.
            LibrarySort.DATE -> items.sortedByDescending { ApiClient.parseRfc3339Seconds(modifiedAt(it)) ?: 0 }
            LibrarySort.SIZE -> items.sortedByDescending(size)
        }
}

/**
 * The order of `localizedStandardCompare`: accents and case ignored, digits
 * compared as numbers ("Game 2" before "Game 10").
 */
object NaturalOrder : Comparator<String> {
    private val chunks = Regex("\\d+|\\D+")

    override fun compare(a: String, b: String): Int {
        val x = chunks.findAll(LibrarySearch.fold(a)).map { it.value }.toList()
        val y = chunks.findAll(LibrarySearch.fold(b)).map { it.value }.toList()
        for (i in 0 until minOf(x.size, y.size)) {
            val p = x[i]
            val q = y[i]
            val d = if (p[0].isDigit() && q[0].isDigit()) {
                p.trimStart('0').length.compareTo(q.trimStart('0').length).takeIf { it != 0 } ?: p.trimStart('0').compareTo(q.trimStart('0'))
            } else {
                p.compareTo(q)
            }
            if (d != 0) return d
        }
        return x.size.compareTo(y.size)
    }
}

/**
 * Whether the library is one flat list, or browsed per system: a library
 * over 20 000 games (`truncated`), or a pak without `GET /games` (older
 * than M11: `404`, whatever its body, or `405`).
 */
object LibraryMode {
    fun meansUnsupported(error: Throwable): Boolean {
        val status = (error as? ApiException.Server)?.status ?: (error as? ApiException.UnexpectedStatus)?.status
        return status == 404 || status == 405
    }
}

/** A subfolder of `GET /systems/{systemId}/games`. */
@Serializable
data class LibraryFolder(val id: String, val name: String, val gameCount: Int = 0)

/** `GET /systems/{systemId}/games?folderId=`: one folder of a system, its subfolders and games. */
@Serializable
data class FolderListing(val folders: List<LibraryFolder> = emptyList(), val games: List<GameSummary> = emptyList())
