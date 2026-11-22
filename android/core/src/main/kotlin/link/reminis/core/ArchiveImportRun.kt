package link.reminis.core

import java.io.File
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job as CoroutineJob
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/** Steps of [ArchiveImportRun] (docs/specs/06-app-ios.md §4.5, M19). */
sealed interface ArchiveStep {
    data object Idle : ArchiveStep

    /** Asking whether the pak has the archive routes, then hashing the archive. */
    data object Preparing : ArchiveStep

    /** Sending the archive to the staging area (0..1). */
    data class Sending(val fraction: Float) : ArchiveStep

    /** The console checks the archive (`GET /archives/{stagingId}`). */
    data object Inspecting : ArchiveStep

    /** What the import would do is known ([ArchiveImportRun.plan]). */
    data object Ready : ArchiveStep

    /** `POST /archives/{stagingId}/import` is in flight. */
    data object Starting : ArchiveStep

    /** The console imports (the job of [ArchiveImportRun.jobId]). */
    data object Importing : ArchiveStep

    data class Succeeded(val result: ArchiveImportResult) : ArchiveStep
    data class Failed(val error: Throwable) : ArchiveStep

    val isBusy: Boolean get() = this is Preparing || this is Sending || this is Inspecting || this is Starting || this is Importing

    /** The app side runs (leaving the screen stops it). */
    val holdsTheScreen: Boolean get() = this is Preparing || this is Sending || this is Inspecting || this is Starting
}

/** A failed `archive-import` job: its error, as the console reports it. */
class ArchiveJobFailure(val server: ApiException.Server) : Exception("archive import job: ${server.code}")

/**
 * Imports a game archive into a console (M19), mirror of `ArchiveImportFlow`
 * of the iOS app: the pak is first asked whether it has the archive routes
 * (an older one says so before anything is sent), the archive is sent to
 * the staging area with a resumable upload, the console checks it and says
 * what its import would do ([plan]), the user may choose another target
 * ([retarget]), confirms ([confirm]), and an `archive-import` job replaces
 * the files (followed by [jobs], so it also shows on the Transfers screen).
 *
 * An archive sent but not imported leaves the staging area at once
 * (`DELETE /archives/{stagingId}`) when the screen goes ([abandon]), when
 * another archive is chosen, or when the console refuses it. Every request
 * asks [client] again, and every step checks the console is still paired
 * with the identity the run started with: a console forgotten or paired
 * again never gets a request of this run.
 *
 * Every method runs on [scope]'s dispatcher (the main thread in the app).
 */
class ArchiveImportRun(
    val deviceId: String,
    private val scope: CoroutineScope,
    private val jobs: JobTracker,
    private val client: suspend () -> ApiClient,
    /** The target asked for first (a game screen's game), null: the console suggests one. */
    initialTarget: ArchiveTarget?,
    private val retry: RetryPolicy = RetryPolicy(),
    /** Deletes the local copy of the archive once the console has it, or the run gave up. */
    private val onLocalCopyDone: (File) -> Unit = {},
    /** Outlives the screen: the answer of `POST /import`, its job, the cleanups. */
    private val durableScope: CoroutineScope = scope,
    /** The identity of the console's current pairing (null when forgotten). */
    private val pairing: () -> Any? = { Unit },
    /** Runs [block] only if [identity] is still the console's pairing (atomically with forgetting it). */
    private val ifPairing: (identity: Any?, block: () -> Unit) -> Boolean = { identity, block ->
        if (identity != null && pairing() == identity) {
            block()
            true
        } else {
            false
        }
    },
) {
    private val _step = MutableStateFlow<ArchiveStep>(ArchiveStep.Idle)
    val step: StateFlow<ArchiveStep> = _step.asStateFlow()

    private val _plan = MutableStateFlow<ArchivePlan?>(null)
    val plan: StateFlow<ArchivePlan?> = _plan.asStateFlow()

    private val _existingGameId = MutableStateFlow<String?>(null)

    /** The game a `409` named as already holding the archive's ROM ("Import into That Game"). */
    val existingGameId: StateFlow<String?> = _existingGameId.asStateFlow()

    /** The target asked for now (null: the console suggests one). */
    @Volatile
    var target: ArchiveTarget? = initialTarget
        private set

    /** The import job followed now. */
    @Volatile
    var jobId: String? = null
        private set

    /** The archive in the staging area of the console, not imported yet. */
    @Volatile
    var stagingId: String? = null
        private set

    private var upload: ResumableUpload? = null
    private var localCopy: File? = null
    private var running: CoroutineJob? = null
    private var identity: Any? = null

    /** Something was sent to this console: the console can no longer be changed. */
    val isBoundToConsole: Boolean get() = _step.value.isBusy || stagingId != null || upload != null

    /**
     * Sends [file] (a local copy named [name]) to the console, then asks what
     * its import would do. An archive sent earlier and not imported is
     * dropped from the console first.
     */
    fun start(file: File, name: String) {
        running?.cancel()
        dropStaging()
        dropUpload()
        _plan.value = null
        _existingGameId.value = null
        jobId = null
        localCopy = file
        identity = pairing()
        _step.value = ArchiveStep.Preparing
        running = scope.launch {
            try {
                checkPairing()
                // A pak older than M19 says so before the archive is sent (Codex review of M19).
                if (!client().supportsGameArchives()) {
                    releaseLocalCopy()
                    throw ArchivesNeedNewerPakException()
                }
                val sha = FileDigest.sha256(file)
                val current = ResumableUpload(file, file.length(), sha, UploadTarget.Staging(name), OnConflict.RENAME, retry)
                upload = current
                _step.value = ArchiveStep.Sending(0f)
                val size = current.sizeBytes
                checkPairing()
                val result = current.run(client(), progress = { offset ->
                    if (_step.value is ArchiveStep.Sending) {
                        _step.value = ArchiveStep.Sending(if (size > 0) (offset.toFloat() / size).coerceIn(0f, 1f) else 1f)
                    }
                })
                upload = null
                releaseLocalCopy()
                val id = result.stagingId ?: throw ApiException.InvalidResponse("no stagingId")
                if (!ifPairing(identity) { stagingId = id }) throw ConsoleForgottenException()
                inspect()
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                // An upload left on the console is cancelled there (best effort); the local copy goes.
                dropUpload()
                releaseLocalCopy()
                fail(e)
            }
        }
    }

    /** Asks again what the import would do, into [target] (null: the console's suggestion). */
    fun retarget(target: ArchiveTarget?) {
        if (stagingId == null || _step.value.isBusy) return
        this.target = target
        _existingGameId.value = null
        running = scope.launch { inspect() }
    }

    private suspend fun inspect() {
        val id = stagingId ?: return
        _step.value = ArchiveStep.Inspecting
        try {
            checkPairing()
            _plan.value = client().inspectArchive(id, target)
            _step.value = ArchiveStep.Ready
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            fail(e)
        }
    }

    /** Starts the import into the target of the plan ([title] names the job on the Transfers screen). */
    fun confirm(title: String) {
        val id = stagingId ?: return
        val target = _plan.value?.target ?: return
        if (_step.value != ArchiveStep.Ready) return
        _step.value = ArchiveStep.Starting
        // The request and its job survive the screen.
        running = durableScope.launch {
            val started = identity
            val accepted = try {
                checkPairing()
                client().importArchive(id, target)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                fail(e)
                return@launch
            }
            val registered = ifPairing(started) {
                jobId = accepted.jobId
                jobs.track(deviceId, accepted.jobId, title, JobKinds.ARCHIVE_IMPORT, started)
            }
            if (!registered) {
                _step.value = ArchiveStep.Failed(ConsoleForgottenException())
                return@launch
            }
            _step.value = ArchiveStep.Importing
            advance()
        }
    }

    /** Polls the job and moves on when it ends (called every second by the screen; events update the job meanwhile). */
    suspend fun tick() {
        val id = jobId ?: return
        if (_step.value != ArchiveStep.Importing) return
        if (jobs.get(deviceId, id)?.isFinished != true) jobs.refresh(deviceId)
        advance()
    }

    /** Moves on if the job ended (a `job` event may have finished it). */
    fun advance() {
        val id = jobId ?: return
        if (_step.value != ArchiveStep.Importing) return
        val tracked = jobs.get(deviceId, id) ?: return
        if (!tracked.isFinished) return
        jobs.consume(deviceId, id)
        if (tracked.lost) {
            _step.value = ArchiveStep.Failed(JobLostException())
            return
        }
        val job = tracked.job ?: return
        _step.value = when (job.state) {
            JobStates.SUCCEEDED -> ArchiveImportResult.from(job.result)?.let { ArchiveStep.Succeeded(it) }
                ?: ArchiveStep.Failed(ApiException.InvalidResponse("archive import without result"))
            JobStates.FAILED -> job.serverError?.let { ArchiveStep.Failed(ArchiveJobFailure(it)) }
                ?: ArchiveStep.Failed(ApiException.InvalidResponse("failed job without error"))
            else -> ArchiveStep.Failed(ApiException.InvalidResponse("job state ${job.state}"))
        }
        // The console deletes the staging entry when the import succeeds.
        if (_step.value is ArchiveStep.Succeeded) stagingId = null
    }

    private fun fail(e: Throwable) {
        val server = (e as? ApiException.Server) ?: (e as? ArchiveJobFailure)?.server
        if (server != null && server.status == 409 && server.code == ErrorCodes.CONFLICT) {
            _existingGameId.value = (server.details["gameId"] as? kotlinx.serialization.json.JsonPrimitive)
                ?.takeIf { it.isString }?.content
        }
        _step.value = ArchiveStep.Failed(e)
        // A refused archive will not be imported: off the card at once.
        if (server != null && server.code == ErrorCodes.UNSUPPORTED_FORMAT) dropStaging()
    }

    private fun checkPairing() {
        if (identity == null || pairing() != identity) throw ConsoleForgottenException()
    }

    private fun releaseLocalCopy() {
        val copy = localCopy ?: return
        localCopy = null
        onLocalCopyDone(copy)
    }

    /** An upload left on the console (paused, failed or cut) is cancelled there, best effort. */
    private fun dropUpload() {
        val current = upload ?: return
        upload = null
        val started = identity
        durableScope.launch {
            if (started == null || pairing() != started) return@launch
            runCatching { current.cancel(client()) }
        }
    }

    /** `DELETE /archives/{stagingId}`, best effort (the console purges its staging area after 24 h). */
    private fun dropStaging() {
        val id = stagingId ?: return
        stagingId = null
        val started = identity
        durableScope.launch {
            if (started == null || pairing() != started) return@launch
            runCatching { client().deleteStagedArchive(id) }
        }
    }

    /**
     * The screen goes: whatever runs on the app side stops, an upload in
     * flight is cancelled on the console, and an archive sent but not
     * imported leaves the staging area. A running import keeps it (the
     * console deletes it when the import succeeds).
     */
    fun abandon() {
        jobId?.let { jobs.disown(deviceId, it) }
        when (_step.value) {
            is ArchiveStep.Succeeded, ArchiveStep.Starting, ArchiveStep.Importing -> return
            else -> Unit
        }
        running?.cancel()
        running = null
        dropUpload()
        releaseLocalCopy()
        dropStaging()
        if (_step.value.isBusy) _step.value = ArchiveStep.Idle
    }
}
