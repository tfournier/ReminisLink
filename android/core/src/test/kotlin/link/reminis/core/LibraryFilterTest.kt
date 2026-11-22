package link.reminis.core

import kotlinx.coroutines.runBlocking
import link.reminis.core.demo.DemoConsole
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The Library of the sixth increment, second part: Filter and Sort (as
 * LibraryFilter.swift), browsing by console (`GET /systems/{id}/games`,
 * through the fake pak), the modes of an older or larger library, and the
 * names of a console kept on the phone.
 */
class LibraryFilterTest {
    private val demo = DemoConsole("Demo Handheld") { "Demo cheat $it" }

    @After
    fun tearDown() = demo.close()

    private fun system(id: String, label: String, tag: String) = GameSystem(id, label, "$label ($tag)", tag, 1)

    private fun game(id: String, name: String, system: String, modified: String = "2026-11-18T17:00:00Z", size: Long = 1, save: Boolean = false, states: Int = 0, cheats: Int = 0) =
        LibraryGame(id, name, "$name.gba", "file", size, modified, save, states, 0, cheats, false, system)

    @Test
    fun filterAndSortAsIos() {
        val systems = listOf(system("a", "Game Boy Advance", "GBA"), system("b", "Game Boy Advance", "MGBA"), system("c", "Pocket", "PK"), system("d", "Empty", "EM"))
        val games = listOf(
            game("1", "Moss 10", "a", "2026-11-18T10:00:00Z", 30, save = true),
            game("2", "Moss 2", "b", "2026-11-19T10:00:00Z", 10, states = 2),
            game("3", "Élan", "c", "2026-11-17T10:00:00Z", 20, cheats = 3),
        )
        val groups = LibraryFiltering.groups(systems, games)
        // Merged by label, systems without games left out, sorted by label.
        assertEquals(listOf("Game Boy Advance", "Pocket"), groups.map { it.label })
        assertEquals(listOf("GBA", "MGBA"), groups[0].tags)
        assertEquals(2, groups[0].gameCount)
        // The tag only where two systems with games share a label.
        val rows = LibraryFiltering.rowLabels(systems, games)
        assertEquals("Game Boy Advance (MGBA)", rows["b"])
        assertEquals("Pocket", rows["c"])
        fun names(q: LibraryQuery) = LibraryFiltering.apply(q, games, groups).map { it.name }
        // Natural order, accents ignored.
        assertEquals(listOf("Élan", "Moss 2", "Moss 10"), names(LibraryQuery()))
        assertEquals(listOf("Moss 2", "Moss 10", "Élan"), names(LibraryQuery(sort = LibrarySort.DATE)))
        assertEquals(listOf("Moss 10", "Élan", "Moss 2"), names(LibraryQuery(sort = LibrarySort.SIZE)))
        assertEquals(listOf("Moss 2", "Moss 10"), names(LibraryQuery(systemLabel = "Game Boy Advance")))
        assertEquals(listOf("Moss 10"), names(LibraryQuery(hasSave = true)))
        assertEquals(listOf("Moss 2"), names(LibraryQuery(hasStates = true)))
        assertEquals(listOf("Élan"), names(LibraryQuery(hasCheats = true)))
        assertEquals(listOf("Élan"), names(LibraryQuery(text = "elan")))
        val filtered = LibraryQuery(text = "moss", systemLabel = "Pocket", hasSave = true, sort = LibrarySort.SIZE)
        assertTrue(filtered.isFiltered)
        assertEquals(LibraryQuery(text = "moss", sort = LibrarySort.SIZE), filtered.clearedFilters())
    }

    @Test
    fun browseByConsoleAndTheModes() = runBlocking {
        val client = demo.client
        val ha = client.systems().first { it.tag == "HA" }
        val root = client.systemGames(ha.id)
        assertTrue(root.games.isNotEmpty())
        val homebrew = root.folders.first { it.name == "Homebrew" }
        assertTrue(homebrew.gameCount > 0)
        val inside = client.systemGames(ha.id, homebrew.id)
        assertEquals(homebrew.gameCount, inside.games.size)
        assertTrue(inside.games.none { g -> root.games.any { it.id == g.id } })

        demo.pak.libraryTruncated = true
        assertTrue(client.allGames().truncated)
        demo.pak.allGamesUnsupported = true
        try {
            client.allGames()
            error("GET /games answered")
        } catch (e: ApiException.Server) {
            assertTrue(LibraryMode.meansUnsupported(e))
        }
        assertTrue(!LibraryMode.meansUnsupported(ApiException.Network(java.io.IOException("x"))))
    }

    @Test
    fun aNameChosenInTheAppStaysUntilTheConsoleIsRenamed() {
        val paired = PairedConsole("d", "Fern Handheld", pin = "p", pairedAt = 1, consoleName = "Fern Handheld")
        val chosen = paired.copy(deviceName = "Living room")
        assertEquals("Living room", chosen.reporting("Fern Handheld").deviceName)
        assertEquals("Moss Handheld", chosen.reporting("Moss Handheld").deviceName)
        // Paired before the names were kept apart: the console's name is taken.
        assertEquals("Moss Handheld", paired.copy(consoleName = null, deviceName = "Old").reporting("Moss Handheld").deviceName)
        assertEquals(null, PairedConsole.cleanName("   "))
        assertEquals(64, PairedConsole.cleanName("x".repeat(80))!!.length)
    }
}
