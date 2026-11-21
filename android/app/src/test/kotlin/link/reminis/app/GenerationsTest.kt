package link.reminis.app

import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class GenerationsTest {
    @Test
    fun aNewerStartOrAnInvalidationSupersedesAGeneration() {
        val generations = Generations<String>()
        val first = generations.next("a")
        val second = generations.next("a")
        assertFalse(generations.isCurrent("a", first))
        assertNull(generations.ifCurrent("a", first) { "saved" })
        assertEquals("saved", generations.ifCurrent("a", second) { "saved" })
        // Other keys are independent.
        val other = generations.next("b")
        assertTrue(generations.isCurrent("a", second))
        generations.invalidate("a")
        assertNull(generations.ifCurrent("a", second) { "saved" })
        assertTrue(generations.isCurrent("b", other))
        generations.invalidateAll()
        assertFalse(generations.isCurrent("b", other))
    }

    /**
     * The NSD race: the resolver checked its generation and is about to emit
     * Found when the callback thread reports Lost. The Lost (bump + emit) must
     * wait until Found is emitted, so the consumer sees Found then Lost, never
     * a stale Found after Lost.
     */
    @Test
    fun invalidateWaitsForAPublicationInProgress() {
        val generations = Generations<String>()
        val events = java.util.Collections.synchronizedList(mutableListOf<String>())
        val generation = generations.next("svc")
        val lostStarted = CountDownLatch(1)
        var lostThread: Thread? = null
        val published = generations.ifCurrent("svc", generation) {
            lostThread = Thread {
                lostStarted.countDown()
                generations.invalidate("svc") { events += "Lost" }
            }.apply { start() }
            assertTrue(lostStarted.await(5, TimeUnit.SECONDS))
            Thread.sleep(200)
            // Lost is blocked behind the check-and-send of Found.
            assertTrue(lostThread!!.isAlive)
            events += "Found"
        }
        assertEquals(Unit, published)
        lostThread!!.join(5_000)
        assertEquals(listOf("Found", "Lost"), events.toList())
        // And once Lost went through, the old resolution can no longer publish.
        assertNull(generations.ifCurrent("svc", generation) { events += "Found" })
        assertEquals(listOf("Found", "Lost"), events.toList())
    }

    /** A superseded probe (old address) finishing last must not save over the newer one. */
    @Test
    fun aSupersededProbeFinishingLastSavesNothing() {
        val probes = Generations<String>()
        var saved: String? = null
        val probeA = probes.next("console")
        val probeB = probes.next("console")
        probes.ifCurrent("console", probeB) { saved = "10.0.0.2" }
        probes.ifCurrent("console", probeA) { saved = "10.0.0.1" }
        assertEquals("10.0.0.2", saved)
    }
}
