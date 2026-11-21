package link.reminis.core

import java.text.Normalizer
import java.util.Locale

/** Search of the library: case and accent insensitive, every word must match. */
object LibrarySearch {
    private val marks = Regex("\\p{Mn}+")

    fun fold(text: String): String =
        marks.replace(Normalizer.normalize(text, Normalizer.Form.NFD), "").lowercase(Locale.ROOT)

    fun matches(name: String, query: String): Boolean {
        val words = fold(query).split(' ').filter { it.isNotBlank() }
        if (words.isEmpty()) return true
        val folded = fold(name)
        return words.all { it in folded }
    }

    /** Games whose name or file name match, sorted by name. */
    fun filter(games: List<LibraryGame>, query: String): List<LibraryGame> =
        games.filter { matches(it.name, query) || matches(it.fileName, query) }
            .sortedWith(compareBy({ fold(it.name) }, { it.id }))
}
