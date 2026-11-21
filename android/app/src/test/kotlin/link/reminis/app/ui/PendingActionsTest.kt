package link.reminis.app.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** The action of a document picker survives a recreation; an orphan result is cleaned up. */
class PendingActionsTest {
    @Test
    fun resultRunsTheWaitingAction() {
        val pending = PendingActions<String>()
        val ran = mutableListOf<String>()
        val orphans = mutableListOf<String>()
        pending.put("k", { ran += it })
        pending.complete("k", "content://doc/1") { orphans += it }
        assertEquals(listOf("content://doc/1"), ran)
        assertTrue(orphans.isEmpty())
        // Once only.
        pending.complete("k", "content://doc/2") { orphans += it }
        assertEquals(listOf("content://doc/2"), orphans)
    }

    @Test
    fun cancelledPickerDropsTheAction() {
        val pending = PendingActions<String>()
        val ran = mutableListOf<String>()
        pending.put("k", { ran += it })
        pending.complete("k", null) { throw AssertionError("no document to delete") }
        pending.complete("k", "content://doc/3") { ran += "orphan:$it" }
        assertEquals(listOf("orphan:content://doc/3"), ran)
    }

    @Test
    fun resultWithoutActionDeletesTheCreatedDocument() {
        // The process died while the picker was open: nobody will fill the document.
        val pending = PendingActions<String>()
        val orphans = mutableListOf<String>()
        pending.complete("lost", "content://doc/4") { orphans += it }
        assertEquals(listOf("content://doc/4"), orphans)
    }
}
