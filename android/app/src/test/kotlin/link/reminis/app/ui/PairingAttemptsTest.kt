package link.reminis.app.ui

import link.reminis.app.ui.PairingAttempts.State
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class PairingAttemptsTest {
    @Test
    fun aFailureAfterPostPairAllowsAFreshAttempt() {
        val attempts = PairingAttempts()
        val first = attempts.begin()!!
        assertNull("one attempt at a time", attempts.begin())
        assertTrue(attempts.commit(first))
        // The console answered e.g. 429 after POST /pair: the link is kept for Retry.
        assertFalse(attempts.end(first, success = false))
        assertEquals(State.IDLE, attempts.current)
        val second = attempts.begin()
        assertNotNull("Retry begins a new attempt", second)
        assertFalse("the old attempt can no longer commit", attempts.commit(first))
        assertTrue(attempts.commit(second!!))
        assertTrue("success releases the link", attempts.end(second, success = true))
        assertEquals(State.DONE, attempts.current)
        assertNull(attempts.begin())
    }

    @Test
    fun aFailureBeforePostPairAllowsAFreshAttempt() {
        val attempts = PairingAttempts()
        val first = attempts.begin()!!
        assertFalse(attempts.end(first, success = false))
        assertFalse("ending twice is a no-op", attempts.end(first, success = false))
        assertNotNull(attempts.begin())
    }

    @Test
    fun leavingWhileWaitingCancelsAndReleases() {
        val attempts = PairingAttempts()
        val id = attempts.begin()!!
        assertEquals(PairingAttempts.Close(cancelAttempt = true, releaseLink = true), attempts.close())
        assertFalse("the request is never written", attempts.commit(id))
        assertFalse(attempts.end(id, success = false))
        assertNull("no retry once the screen is left", attempts.begin())
        assertEquals(PairingAttempts.Close(cancelAttempt = false, releaseLink = false), attempts.close())
    }

    @Test
    fun leavingAfterAFailureReleasesTheLink() {
        val attempts = PairingAttempts()
        val id = attempts.begin()!!
        attempts.commit(id)
        attempts.end(id, success = false)
        assertEquals(PairingAttempts.Close(cancelAttempt = false, releaseLink = true), attempts.close())
        assertNull(attempts.begin())
    }

    @Test
    fun leavingWhileSentReleasesWhenTheExchangeEnds() {
        for (success in listOf(true, false)) {
            val attempts = PairingAttempts()
            val id = attempts.begin()!!
            assertTrue(attempts.commit(id))
            assertEquals(PairingAttempts.Close(cancelAttempt = false, releaseLink = false), attempts.close())
            // onCleared after the button: still in flight, nothing more to do.
            assertEquals(PairingAttempts.Close(cancelAttempt = false, releaseLink = false), attempts.close())
            assertTrue("released when the exchange ends", attempts.end(id, success))
            assertEquals(State.CLOSED, attempts.current)
            assertNull(attempts.begin())
        }
    }

    @Test
    fun leavingAfterSuccessReleasesAgainHarmlessly() {
        val attempts = PairingAttempts()
        val id = attempts.begin()!!
        attempts.commit(id)
        assertTrue(attempts.end(id, success = true))
        assertTrue(attempts.close().releaseLink)
    }
}
