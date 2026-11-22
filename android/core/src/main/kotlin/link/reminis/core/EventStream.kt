package link.reminis.core

import java.util.concurrent.atomic.AtomicLong
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

/**
 * The delays of an [EventStream] and the clock it measures them with (tests
 * drive their own). Same values as `EventStreamTiming` of `EventStream.swift`.
 */
class EventStreamTiming(
    /** No byte for this long (keep-alives included): the connection is lost. The pak sends one at least every 15 s. */
    val livenessTimeoutMillis: Long = 35_000,
    /** The wait before reconnecting after a stream that was live. */
    val initialBackoffMillis: Long = 1_000,
    /** The longest wait between two failed attempts. */
    val maxBackoffMillis: Long = 15_000,
    /** The wait after `429` without `Retry-After`. */
    val defaultRetryAfterMillis: Long = 5_000,
    /** Milliseconds since some fixed origin. */
    val now: () -> Long = { System.nanoTime() / 1_000_000 },
    val sleep: suspend (Long) -> Unit = { delay(it) },
) {
    /**
     * The wait before the next attempt: `Retry-After` after `429`; otherwise
     * [initialBackoffMillis] doubled for each attempt that failed in a row
     * since the stream was last live, at most [maxBackoffMillis].
     */
    fun reconnectDelay(failures: Int, retryAfterMillis: Long? = null): Long {
        if (retryAfterMillis != null) return retryAfterMillis.coerceAtLeast(0)
        var delay = initialBackoffMillis
        repeat(failures.coerceAtLeast(0)) {
            delay *= 2
            if (delay >= maxBackoffMillis) return maxBackoffMillis
        }
        return delay.coerceAtMost(maxBackoffMillis)
    }
}

/**
 * The event stream of a console (`GET /events`, M14): opens it, parses it,
 * watches its liveness and reconnects as its owner decides. Port of
 * `EventStream.swift`.
 *
 * - Every (re)connection calls [connect], which signs a new request.
 * - Liveness: any byte (an event, `: keep-alive`, an error answer) counts;
 *   [EventStreamTiming.livenessTimeoutMillis] without one, across
 *   reconnections, ends the stream with [End.LivenessLost].
 * - When a connection ends, [onEnd] decides: reconnect (after the backoff,
 *   or `Retry-After` for `429`) or stop.
 * - `shutdown` ends the connection at once (nothing is read after it).
 * - Unknown event types and unreadable data are ignored.
 *
 * [start], [stop] and the callbacks run on [scope]'s dispatcher (the main
 * thread in the app); only the reads go to the IO dispatcher.
 */
class EventStream(
    private val scope: CoroutineScope,
    val timing: EventStreamTiming = EventStreamTiming(),
    private val connect: suspend () -> EventByteSource,
    private val onEvent: suspend (PakEvent) -> Unit,
    private val onEnd: suspend (End) -> Decision,
) {
    sealed interface End {
        /** `shutdown` arrived: the pak is closing. */
        data class Shutdown(val shutdown: EventShutdown) : End

        /** The pak ended the stream without `shutdown`. */
        data object EndOfStream : End

        /** The stream could not open (an error answer, no connection) or broke while open. */
        data class Failed(val error: Throwable) : End

        /** Nothing received for the liveness timeout. Final: [onEnd]'s decision is ignored. */
        data object LivenessLost : End
    }

    enum class Decision { RECONNECT, STOP }

    @Volatile
    var isRunning = false
        private set

    /** `hello` arrived on the current connection. */
    @Volatile
    var isLive = false
        private set

    /** Connections tried since the start. */
    @Volatile
    var attempts = 0
        private set

    private val received = AtomicLong()

    /** Bytes read since the start (tests wait on it). */
    val bytesReceived: Long get() = received.get()

    private var loopJob: Job? = null
    private var watchdogJob: Job? = null

    @Volatile
    private var generation = 0
    private val lastActivity = AtomicLong()

    private fun touch() = lastActivity.set(timing.now())

    fun start() {
        if (isRunning) return
        isRunning = true
        val current = ++generation
        touch()
        loopJob = scope.launch { loop(current) }
        watchdogJob = scope.launch {
            while (currentCoroutineContext().isActive) {
                val elapsed = timing.now() - lastActivity.get()
                if (elapsed >= timing.livenessTimeoutMillis) {
                    livenessExpired(current)
                    return@launch
                }
                timing.sleep(timing.livenessTimeoutMillis - elapsed)
            }
        }
    }

    /** Stops at once; nothing is reported after this. */
    fun stop() {
        if (!isRunning) return
        isRunning = false
        isLive = false
        generation++
        val loop = loopJob
        val watchdog = watchdogJob
        loopJob = null
        watchdogJob = null
        loop?.cancel()
        watchdog?.cancel()
    }

    private fun livenessExpired(current: Int) {
        if (!isRunning || generation != current) return
        stop()
        // Reported from a new coroutine: the watchdog itself is cancelled by stop().
        scope.launch { onEnd(End.LivenessLost) }
    }

    private suspend fun loop(current: Int) {
        var failures = 0
        while (currentCoroutineContext().isActive && generation == current) {
            attempts++
            val (end, wasLive) = runOnce(current)
            if (!currentCoroutineContext().isActive || generation != current) return
            isLive = false
            failures = if (wasLive) 0 else failures + 1
            val decision = onEnd(end)
            if (!currentCoroutineContext().isActive || generation != current) return
            if (decision != Decision.RECONNECT) {
                stop()
                return
            }
            val busy = ((end as? End.Failed)?.error as? ApiException.Server)?.takeIf { it.status == 429 }
            val retryAfter = busy?.let { it.retryAfter?.let { s -> s * 1_000L } ?: timing.defaultRetryAfterMillis }
            timing.sleep(timing.reconnectDelay(if (wasLive) 0 else failures, retryAfter))
        }
    }

    /** One connection, until it ends. Returns how, and whether it was live. */
    private suspend fun runOnce(current: Int): Pair<End, Boolean> {
        val source = try {
            connect()
        } catch (e: kotlinx.coroutines.CancellationException) {
            throw e
        } catch (e: ApiException) {
            // An answer, even an error, shows the console is reachable.
            if (e is ApiException.Server || e is ApiException.UnexpectedStatus) touch()
            return End.Failed(e) to false
        } catch (e: Exception) {
            return End.Failed(ApiException.Network(e)) to false
        }
        touch()
        val live = booleanArrayOf(false)
        return try {
            coroutineScope {
                // A read blocked on the socket ends as soon as this coroutine is cancelled (stop()).
                val watcher = launch {
                    try {
                        awaitCancellation()
                    } finally {
                        source.cancel()
                    }
                }
                try {
                    read(source, current, live)
                } finally {
                    watcher.cancel()
                }
            } to live[0]
        } catch (e: kotlinx.coroutines.CancellationException) {
            throw e
        } catch (e: ApiException) {
            End.Failed(e) to live[0]
        } catch (e: Exception) {
            End.Failed(ApiException.Network(e)) to live[0]
        } finally {
            source.cancel()
        }
    }

    private suspend fun read(source: EventByteSource, current: Int, live: BooleanArray): End {
        val parser = SseParser()
        val buffer = ByteArray(8 * 1024)
        while (true) {
            val n = source.read(buffer)
            if (n < 0) return End.EndOfStream
            if (n == 0) continue
            touch()
            received.addAndGet(n.toLong())
            for (i in 0 until n) {
                val item = parser.feed(buffer[i]) as? SseItem.Event ?: continue
                val event = PakEvent.parse(item.event) ?: continue
                if (generation != current) return End.EndOfStream
                when (event) {
                    is PakEvent.Shutdown -> return End.Shutdown(event.shutdown)
                    is PakEvent.Hello -> {
                        live[0] = true
                        isLive = true
                    }
                    else -> Unit
                }
                onEvent(event)
                if (generation != current) return End.EndOfStream
            }
        }
    }
}
