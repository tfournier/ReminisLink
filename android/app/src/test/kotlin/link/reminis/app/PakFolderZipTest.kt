package link.reminis.app

import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.util.zip.ZipInputStream
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

/**
 * The zip of a `.pak` folder (Codex review 1 of the sixth increment): a
 * fake document provider whose tree holds a cycle, a deep one and a wide
 * one; the walk ends without overflowing nor reading a folder whole.
 */
class PakFolderZipTest {
    /** A fake provider: folders by id, with their children; files hold their id as bytes. */
    private class FakeTree(private val folders: Map<String, () -> Sequence<PakFolderZip.Child>>) : PakFolderZip.Tree {
        var rowsRead = 0
        var openCursors = 0

        override fun children(folderId: String): PakFolderZip.CloseableIterator<PakFolderZip.Child> {
            val rows = folders.getValue(folderId)().iterator()
            openCursors++
            return object : PakFolderZip.CloseableIterator<PakFolderZip.Child> {
                override fun hasNext() = rows.hasNext()
                override fun next() = rows.next().also { rowsRead++ }
                override fun close() {
                    openCursors--
                }
            }
        }

        override fun open(documentId: String) = ByteArrayInputStream(documentId.toByteArray())
    }

    private fun entries(zip: ByteArray): List<String> = ZipInputStream(ByteArrayInputStream(zip)).use { z ->
        generateSequence { z.nextEntry }.map { it.name }.toList()
    }

    @Test
    fun aFolderListedInsideItselfIsZippedOnce() {
        val tree = FakeTree(
            mapOf(
                "root" to { sequenceOf(PakFolderZip.Child("launch", "launch.sh", false), PakFolderZip.Child("lib", "lib", true)) },
                // The provider lists the root (and the folder itself) again inside `lib`.
                "lib" to { sequenceOf(PakFolderZip.Child("so", "core.so", false), PakFolderZip.Child("root", "again", true), PakFolderZip.Child("lib", "lib", true)) },
            ),
        )
        val out = ByteArrayOutputStream()
        PakFolderZip.zip(tree, "root", "Fern.pak", out)
        assertEquals(listOf("Fern.pak/launch.sh", "Fern.pak/lib/", "Fern.pak/lib/core.so"), entries(out.toByteArray()))
        assertEquals("every cursor is closed", 0, tree.openCursors)
    }

    @Test
    fun aTreeTooDeepIsRefused() {
        // Each folder holds a new one: an endless chain of distinct ids.
        val tree = object : PakFolderZip.Tree {
            override fun children(folderId: String) = object : PakFolderZip.CloseableIterator<PakFolderZip.Child> {
                var done = false
                override fun hasNext() = !done
                override fun next() = PakFolderZip.Child("$folderId/d", "d", true).also { done = true }
                override fun close() = Unit
            }

            override fun open(documentId: String) = ByteArrayInputStream(ByteArray(0))
        }
        try {
            PakFolderZip.zip(tree, "root", "Deep.pak", ByteArrayOutputStream())
            fail("a tree deeper than ${PakFolderZip.MAX_DEPTH} levels was zipped")
        } catch (e: PakFolderZip.LimitExceeded) {
            assertEquals("too deep", e.message)
        }
    }

    @Test
    fun aHugeFolderStopsAtTheLimitWithoutBeingReadWhole() {
        val tree = FakeTree(
            mapOf("root" to { generateSequence(0) { it + 1 }.map { PakFolderZip.Child("f$it", "f$it", false) } }),
        )
        try {
            PakFolderZip.zip(tree, "root", "Wide.pak", ByteArrayOutputStream())
            fail("more than ${PakFolderZip.MAX_ENTRIES} entries zipped")
        } catch (e: PakFolderZip.LimitExceeded) {
            assertEquals("too many entries", e.message)
        }
        assertTrue("read row by row, stopped at the limit", tree.rowsRead <= PakFolderZip.MAX_ENTRIES + 1)
        assertEquals(0, tree.openCursors)
    }

    /** Codex review 2: repeated rows and names to skip count too: the reading stops at the limit. */
    @Test
    fun repeatedAndSkippedRowsCountTowardsTheLimit() {
        for (row in listOf(PakFolderZip.Child("same", "same.txt", false), PakFolderZip.Child("x", "..", false))) {
            val tree = FakeTree(mapOf("root" to { generateSequence { row } }))
            try {
                PakFolderZip.zip(tree, "root", "Loop.pak", ByteArrayOutputStream())
                fail("an endless folder of ${row.name} was read to its end")
            } catch (e: PakFolderZip.LimitExceeded) {
                assertEquals("too many entries", e.message)
            }
            assertTrue(tree.rowsRead <= PakFolderZip.MAX_ENTRIES)
        }
    }
}
