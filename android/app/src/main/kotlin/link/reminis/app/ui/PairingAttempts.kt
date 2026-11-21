package link.reminis.app.ui

/**
 * The attempts of one pairing screen (thread-safe, no Android dependency).
 *
 * Each attempt moves from WAITING to SENT at the commit point (`POST /pair`
 * about to be written), or ends: a failure brings the screen back to IDLE so
 * Retry can begin a fresh attempt with the same in-memory link; a success
 * makes it DONE. Leaving the screen ([close]) is final: a WAITING attempt is
 * cancelled at once; a SENT one runs to its end and only then releases the
 * link. Whatever the order, the link is released exactly when the screen is
 * left in a terminal state or the pairing succeeded, never later.
 */
class PairingAttempts {
    enum class State { IDLE, WAITING, SENT, DONE, CLOSED }

    /** What the caller must do after [close]. */
    data class Close(val cancelAttempt: Boolean, val releaseLink: Boolean)

    private var state = State.IDLE
    private var attempt = 0
    private var leaving = false

    val current: State @Synchronized get() = state

    /** IDLE → WAITING: the id of the new attempt, or null if one is running, done or the screen is closed. */
    @Synchronized
    fun begin(): Int? {
        if (state != State.IDLE) return null
        state = State.WAITING
        return ++attempt
    }

    /** The commit point: WAITING → SENT for attempt [id]; false if it was cancelled or superseded. */
    @Synchronized
    fun commit(id: Int): Boolean {
        if (state != State.WAITING || id != attempt) return false
        state = State.SENT
        return true
    }

    /**
     * Attempt [id] ended (idempotent). Returns whether the caller must
     * release the link now: on success, or when the screen was left while
     * the attempt was in flight.
     */
    @Synchronized
    fun end(id: Int, success: Boolean): Boolean {
        if (id != attempt || (state != State.WAITING && state != State.SENT)) return false
        return when {
            leaving -> {
                state = State.CLOSED
                true
            }
            success -> {
                state = State.DONE
                true
            }
            else -> {
                state = State.IDLE
                false
            }
        }
    }

    /** The screen is left (button, system back, ViewModel cleared). */
    @Synchronized
    fun close(): Close = when (state) {
        State.WAITING -> {
            state = State.CLOSED
            Close(cancelAttempt = true, releaseLink = true)
        }
        State.SENT -> {
            leaving = true
            Close(cancelAttempt = false, releaseLink = false)
        }
        State.IDLE, State.DONE -> {
            state = State.CLOSED
            Close(cancelAttempt = false, releaseLink = true)
        }
        State.CLOSED -> Close(cancelAttempt = false, releaseLink = false)
    }
}
