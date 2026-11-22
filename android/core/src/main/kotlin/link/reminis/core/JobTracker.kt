package link.reminis.core

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

/** A job of a console followed by the app (a patch, a pak installation), as shown. */
data class TrackedJob(
    val deviceId: String,
    val jobId: String,
    /** What is shown (the game, the archive name). */
    val title: String,
    val kind: String,
    /** The last state read, null until the first poll. */
    val job: Job? = null,
    /** The console no longer knows it (`404`: restarted): final. */
    val lost: Boolean = false,
    /** The last poll failed (the job itself goes on on the console). */
    val pollError: Throwable? = null,
    /** A cancellation was sent and acknowledged; the final state is the console's. */
    val cancelSent: Boolean = false,
    /** Cleared from the Transfers screen: still followed for the screens that wait for its outcome. */
    val hidden: Boolean = false,
    /** Its owner (the Patch screen, the pak installer) read its outcome. */
    val consumed: Boolean = false,
) {
    val isFinished: Boolean get() = lost || job?.isFinished == true

    /** A cancellation was asked here or from another screen (`cancelRequested`). */
    val cancelRequested: Boolean get() = cancelSent || job?.cancelRequested == true
}

/**
 * The jobs the app follows, for every console (mirror of the job part of
 * `TransferQueue.swift`). Nothing polls by itself: the screens that show
 * a job (Patch, Apps, Transfers) call [refresh] every second while they
 * are visible (docs/specs/03-protocole.md §8). The client is asked again
 * for every poll ([client]): a console forgotten or paired again never
 * gets a request from a stale client.
 */
class JobTracker(private val client: suspend (deviceId: String) -> ApiClient) {
    private val _jobs = MutableStateFlow<List<TrackedJob>>(emptyList())
    val jobs: StateFlow<List<TrackedJob>> = _jobs.asStateFlow()
    private val polling = Mutex()

    /** Follows a job (idempotent). */
    fun track(deviceId: String, jobId: String, title: String, kind: String) {
        _jobs.update { list ->
            if (list.any { it.deviceId == deviceId && it.jobId == jobId }) list else list + TrackedJob(deviceId, jobId, title, kind)
        }
    }

    fun get(deviceId: String, jobId: String): TrackedJob? = _jobs.value.firstOrNull { it.deviceId == deviceId && it.jobId == jobId }

    /** Polls once every unfinished job of [deviceId] (one poll round at a time). */
    suspend fun refresh(deviceId: String) = polling.withLock {
        for (tracked in _jobs.value.filter { it.deviceId == deviceId && !it.isFinished }) {
            val result = pollJob({ client(deviceId) }, tracked.jobId)
            change(deviceId, tracked.jobId) { current ->
                result.fold(
                    onSuccess = { current.copy(job = it, pollError = null) },
                    onFailure = { e -> if (e is JobLostException) current.copy(lost = true, pollError = null) else current.copy(pollError = e) },
                )
            }
        }
    }

    /**
     * `DELETE /jobs/{jobId}`: the answer is the job in its real state (a
     * running job stays `running` with `cancelRequested` until it stops).
     * Throws when the cancellation could not be sent: the job goes on.
     */
    suspend fun cancel(deviceId: String, jobId: String) {
        val job = try {
            client(deviceId).cancelJob(jobId)
        } catch (e: ApiException.Server) {
            if (e.status == 404) {
                change(deviceId, jobId) { it.copy(lost = true) }
                return
            }
            throw e
        }
        change(deviceId, jobId) { it.copy(job = job, cancelSent = true, pollError = null) }
    }

    /**
     * Hides the finished jobs of a console (or of every console) from the
     * Transfers screen. They stay known: a screen or the pak installer that
     * has not read an outcome yet still finds it.
     */
    fun clearFinished(deviceId: String? = null) = _jobs.update { list ->
        list.mapNotNull {
            when {
                !it.isFinished || (deviceId != null && it.deviceId != deviceId) -> it
                // Nobody waits for it any more: gone.
                it.consumed -> null
                else -> it.copy(hidden = true)
            }
        }
    }

    /**
     * The owner of a finished job read its outcome: a row already cleared
     * from the Transfers screen goes (the list never grows for the life of
     * the process, like `TransferQueue.swift`).
     */
    fun consume(deviceId: String, jobId: String) = _jobs.update { list ->
        list.mapNotNull {
            if (it.deviceId != deviceId || it.jobId != jobId || !it.isFinished) it else if (it.hidden) null else it.copy(consumed = true)
        }
    }

    /** A forgotten console: its jobs are no longer followed. */
    fun dropConsole(deviceId: String) = _jobs.update { list -> list.filterNot { it.deviceId == deviceId } }

    private fun change(deviceId: String, jobId: String, transform: (TrackedJob) -> TrackedJob) = _jobs.update { list ->
        list.map { if (it.deviceId == deviceId && it.jobId == jobId && !(it.isFinished)) transform(it) else it }
    }
}
