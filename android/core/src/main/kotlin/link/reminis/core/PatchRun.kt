package link.reminis.core

import java.io.File
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job as CoroutineJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/** Where the patch comes from. */
sealed interface PatchInput {
    /** A local copy of a file chosen on the phone (or shared); [choice] identifies it. */
    data class LocalFile(val file: File, val name: String, val choice: String) : PatchInput

    /** A patch of the SD card. */
    data class SdCard(val sdFileId: String) : PatchInput

    val key: String get() = when (this) {
        is LocalFile -> "file:$choice"
        is SdCard -> "sd:$sdFileId"
    }
}

/** A failed patch job: its error, as the console reports it. */
class PatchJobFailure(val server: ApiException.Server) : Exception("patch job: ${server.code}")

/** Steps of [PatchRun] (docs/specs/06-app-ios.md §4.5). */
sealed interface PatchStep {
    data object Idle : PatchStep

    /** Computing the SHA-256 of the file. */
    data object Preparing : PatchStep

    /** Sending the file to the staging area. */
    data class Sending(val fraction: Float) : PatchStep

    /** `POST /games/{gameId}/patch` is in flight. */
    data object Starting : PatchStep

    /** The console applies the patch (the job of [PatchRun.jobId]). */
    data object Patching : PatchStep

    /**
     * Applied: [gameId] of the produced ROM; [late]: the cancellation came
     * after the patch was in place; [verified]: false for IPS (no checksum).
     */
    data class Succeeded(val gameId: String?, val late: Boolean, val verified: Boolean?) : PatchStep

    data class Failed(val error: Throwable, val mismatch: PatchMismatchDetails? = null) : PatchStep

    /** Confirmed: nothing was applied. */
    data object Cancelled : PatchStep

    /** The cancel of the upload waits for the console's answer: nothing is final yet. */
    data object CancellingUpload : PatchStep

    /** Something runs: the inputs are locked. */
    val isBusy: Boolean get() = this is Preparing || this is Sending || this is Starting || this is Patching || this is CancellingUpload

    /** The app side runs: leaving the screen would stop it. */
    val holdsTheScreen: Boolean get() = this is Preparing || this is Sending || this is Starting || this is CancellingUpload
}

/**
 * Applies a patch to a game, mirror of `PatchFlow.swift` (M6).
 *
 * A file of the phone goes to the staging area with a resumable upload,
 * then is used by its `stagingId`; a patch of the SD card is used by its
 * `sdFileId`. `POST /games/{gameId}/patch` then starts a job, followed by
 * [jobs] (it also shows on the Transfers screen) and polled by [tick],
 * called every second by the screen while it is visible. A cancellation
 * never leads to a patch; the final state of a cancelled job is always the
 * one the console reports. The client is asked again for every request
 * ([client]): no stale client after a console was forgotten or paired again.
 *
 * Every method runs on [scope]'s dispatcher (the main thread in the app).
 */
class PatchRun(
    val deviceId: String,
    private val scope: CoroutineScope,
    private val jobs: JobTracker,
    private val client: suspend () -> ApiClient,
    private val retry: RetryPolicy = RetryPolicy(),
    private val cancelPollMillis: Long = 1_000,
    /** The local copy of a file, once the console has it (or the upload is cancelled). */
    private val onLocalCopyDone: (PatchInput.LocalFile) -> Unit = {},
    /**
     * Outlives the screen: once `POST /patch` is sent, its answer is read,
     * its job followed (Transfers) and a cancellation asked meanwhile sent,
     * even if the screen closed in between.
     */
    private val durableScope: CoroutineScope = scope,
    /**
     * The identity of the current pairing of the console (null when it is
     * forgotten). Taken when `POST /patch` is sent: a late `202` whose
     * pairing was forgotten or replaced meanwhile is dropped (no job
     * tracked, no cancel sent through another pairing).
     */
    private val pairing: () -> Any? = { Unit },
) {
    private val _step = MutableStateFlow<PatchStep>(PatchStep.Idle)
    val step: StateFlow<PatchStep> = _step.asStateFlow()

    private val _cancelling = MutableStateFlow(false)

    /** A cancellation was asked and the console has not confirmed it yet. */
    val cancelling: StateFlow<Boolean> = _cancelling.asStateFlow()

    private val _cancelError = MutableStateFlow<Throwable?>(null)

    /** The cancellation could not be sent (the job goes on). */
    val cancelError: StateFlow<Throwable?> = _cancelError.asStateFlow()

    /** The job followed now. */
    @Volatile
    var jobId: String? = null
        private set

    private var staged: Pair<String, String>? = null
    private var upload: Pair<String, ResumableUpload>? = null
    private var uploadInput: PatchInput.LocalFile? = null
    @Volatile private var cancelRequested = false

    /** The pairing the job was started with: no cancel is sent through another one. */
    @Volatile private var jobPairing: Any? = null

    /** `POST /patch` was sent: its outcome belongs to [durableScope], not to the screen. */
    @Volatile private var posted = false
    private val cancelledJobs = java.util.Collections.synchronizedSet(mutableSetOf<String>())
    private var running: CoroutineJob? = null

    /** Something was sent to this console: the console can no longer be changed. */
    val isBoundToConsole: Boolean get() = _step.value.isBusy || staged != null || upload != null

    /** Starts (or resumes) the patch of [gameId] ([title] names its job). */
    fun start(gameId: String, input: PatchInput, mode: PatchOutputMode, fileName: String?, title: String) {
        if (_step.value.isBusy || _cancelling.value) return
        cancelRequested = false
        posted = false
        _cancelError.value = null
        _step.value = PatchStep.Starting
        running = scope.launch {
            try {
                val source = staged?.takeIf { it.first == input.key }?.let { PatchSource.Staging(it.second) } ?: when (input) {
                    is PatchInput.SdCard -> PatchSource.Sd(input.sdFileId)
                    is PatchInput.LocalFile -> PatchSource.Staging(stage(input))
                }
                // Never after a cancellation.
                if (cancelRequested) return@launch finishCancelledRequest()
                _step.value = PatchStep.Starting
                posted = true
                // The request, its job and a cancellation asked meanwhile survive the screen.
                durableScope.launch { postAndFollow(gameId, PatchRequest(source, mode, fileName), title) }.join()
            } catch (e: CancellationException) {
                if (!posted && _step.value != PatchStep.CancellingUpload && cancelRequested) finishCancelledRequest()
                throw e
            } catch (e: Exception) {
                fail(e)
            }
        }
    }

    /** `POST /games/{gameId}/patch`, then its job followed; cancelled at once if asked meanwhile. */
    private suspend fun postAndFollow(gameId: String, request: PatchRequest, title: String) {
        val identity = pairing()
        val accepted = try {
            client().patchGame(gameId, request)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            fail(e)
            return
        }
        if (identity == null || pairing() != identity) {
            // The console was forgotten (or paired again) meanwhile: nothing more is sent for this pairing.
            _cancelling.value = false
            _step.value = PatchStep.Failed(ConsoleForgottenException())
            return
        }
        jobId = accepted.jobId
        jobPairing = identity
        jobs.track(deviceId, accepted.jobId, title, JobKinds.PATCH)
        _step.value = PatchStep.Patching
        if (cancelRequested) cancelJob(accepted.jobId)
    }

    /** Sends the file to the staging area and returns its `stagingId`. */
    private suspend fun stage(input: PatchInput.LocalFile): String {
        val current = upload?.takeIf { it.first == input.key }?.second ?: run {
            _step.value = PatchStep.Preparing
            val size = input.file.length()
            val sha = FileDigest.sha256(input.file)
            ResumableUpload(input.file, size, sha, UploadTarget.Staging(input.name), OnConflict.RENAME, retry).also {
                upload = input.key to it
                uploadInput = input
            }
        }
        _step.value = PatchStep.Sending(0f)
        val size = current.sizeBytes
        val result = current.run(client(), progress = { offset ->
            if (_step.value is PatchStep.Sending) _step.value = PatchStep.Sending(if (size > 0) (offset.toFloat() / size).coerceIn(0f, 1f) else 1f)
        })
        val stagingId = result.stagingId ?: throw ApiException.InvalidResponse("no stagingId")
        staged = input.key to stagingId
        upload = null
        uploadInput = null
        onLocalCopyDone(input)
        return stagingId
    }

    /**
     * Polls the job followed now and moves on when it ends. Called every
     * second by the screen while it is visible; paused while a
     * cancellation is in progress.
     */
    suspend fun tick() {
        val id = jobId ?: return
        if (_cancelling.value || _step.value != PatchStep.Patching) return
        jobs.refresh(deviceId)
        if (jobs.get(deviceId, id)?.job?.cancelRequested == true) cancelledJobs += id
        advance()
    }

    private fun advance() {
        val id = jobId ?: return
        val tracked = jobs.get(deviceId, id) ?: return
        if (!tracked.isFinished || _step.value != PatchStep.Patching) return
        _cancelling.value = false
        jobs.consume(deviceId, id)
        if (tracked.lost) {
            _step.value = PatchStep.Failed(JobLostException())
            return
        }
        val job = tracked.job ?: return
        val late = id in cancelledJobs
        _step.value = when (job.state) {
            JobStates.SUCCEEDED -> PatchStep.Succeeded(job.resultGameId, late, job.resultVerified)
            JobStates.FAILED -> job.serverError?.let { PatchStep.Failed(PatchJobFailure(it), it.patchMismatchDetails) }
                ?: PatchStep.Failed(ApiException.InvalidResponse("failed job without error"))
            JobStates.CANCELLED -> PatchStep.Cancelled
            else -> PatchStep.Failed(ApiException.InvalidResponse("job state ${job.state}"))
        }
    }

    /**
     * Stops what runs: the upload (also on the console, final only once the
     * console confirmed), or the job (`DELETE /jobs/{id}`, then its real end).
     */
    fun cancel() {
        if (_cancelling.value) return
        _cancelError.value = null
        val step = _step.value
        when {
            step is PatchStep.Preparing || step is PatchStep.Sending ||
                (step is PatchStep.Failed && (step.error as? UploadFailure)?.reason == UploadFailure.Reason.CANCEL_NOT_CONFIRMED) ||
                (step is PatchStep.Failed && upload != null) -> {
                cancelRequested = true
                running?.cancel()
                _step.value = PatchStep.CancellingUpload
                scope.launch { cancelUpload() }
            }
            step is PatchStep.Starting -> {
                // The request answers first; its job is then cancelled.
                cancelRequested = true
                _cancelling.value = true
            }
            step is PatchStep.Patching -> {
                cancelRequested = true
                val id = jobId
                if (id != null) durableScope.launch { cancelJob(id) } else _cancelling.value = true
            }
            else -> Unit
        }
    }

    private suspend fun cancelUpload() {
        val current = upload?.second
        val outcome = if (current == null) {
            ResumableUpload.CancelOutcome.Cancelled
        } else {
            try {
                current.cancel(client())
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                ResumableUpload.CancelOutcome.NotConfirmed(e)
            }
        }
        when (outcome) {
            is ResumableUpload.CancelOutcome.NotConfirmed -> {
                cancelRequested = false
                _step.value = PatchStep.Failed(UploadFailure(UploadFailure.Reason.CANCEL_NOT_CONFIRMED))
            }
            else -> {
                uploadInput?.let(onLocalCopyDone)
                upload = null
                uploadInput = null
                _step.value = PatchStep.Cancelled
            }
        }
    }

    /** `DELETE /jobs/{id}`, then the final state the console reports (polled until it is final, 30 times at most). */
    private suspend fun cancelJob(id: String) {
        _cancelling.value = true
        cancelledJobs += id
        if (pairing() != jobPairing) {
            _cancelling.value = false
            _cancelError.value = ConsoleForgottenException()
            return
        }
        try {
            jobs.cancel(deviceId, id)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            _cancelling.value = false
            _cancelError.value = e
            return
        }
        repeat(30) {
            if (jobs.get(deviceId, id)?.isFinished == true) return@repeat
            delay(cancelPollMillis)
            jobs.refresh(deviceId)
        }
        _cancelling.value = false
        advance()
    }

    private fun finishCancelledRequest() {
        _cancelling.value = false
        _step.value = PatchStep.Cancelled
    }

    private fun fail(e: Throwable) {
        if (_step.value == PatchStep.CancellingUpload) return
        if (cancelRequested && _step.value != PatchStep.Cancelled && e is CancellationException) return finishCancelledRequest()
        _cancelling.value = false
        _step.value = PatchStep.Failed(e)
    }

    /** An input changed: a final failure or cancellation is cleared. */
    fun inputChanged() {
        val s = _step.value
        if (s is PatchStep.Cancelled || (s is PatchStep.Failed && upload == null)) _step.value = PatchStep.Idle
    }

    /**
     * The screen goes away: an upload left on the console (paused or
     * failed) is cancelled there and its local copy deleted, from
     * [cleanupScope] (the screen's scope is gone).
     */
    fun discard(cleanupScope: CoroutineScope) {
        if (_step.value.holdsTheScreen) return
        val current = upload?.second ?: return
        val input = uploadInput
        upload = null
        uploadInput = null
        cleanupScope.launch {
            runCatching { current.cancel(client()) }
            input?.let(onLocalCopyDone)
        }
    }
}
