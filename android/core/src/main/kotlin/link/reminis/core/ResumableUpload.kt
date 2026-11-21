package link.reminis.core

import java.io.File
import java.security.MessageDigest
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext

/** Why an upload stopped, besides the errors of [ApiClient]. */
class UploadFailure(val reason: Reason) : Exception("upload: ${reason.name.lowercase()}") {
    enum class Reason {
        /**
         * The connection was lost after `complete` was sent and the console
         * no longer knows the upload: it may or may not have been written.
         */
        OUTCOME_UNKNOWN,

        /** Stopped between two chunks on request. [ResumableUpload.run] resumes it. */
        PAUSED,

        /** The local file is not the size it had when it was hashed. */
        FILE_CHANGED,

        /** The console keeps forgetting the upload or rejecting the offset. */
        TOO_MANY_RESTARTS,

        /**
         * A cancel got no definite answer: the upload may still be on the
         * console, it is kept and can be resumed or cancelled again.
         */
        CANCEL_NOT_CONFIRMED,
    }
}

/** SHA-256 of a file, streamed (never held in memory). */
object FileDigest {
    suspend fun sha256(file: File, shouldPause: () -> Boolean = { false }, progress: (Long) -> Unit = {}): String =
        withContext(Dispatchers.IO) {
            val digest = MessageDigest.getInstance("SHA-256")
            val buffer = ByteArray(256 * 1024)
            var read = 0L
            file.inputStream().use { input ->
                while (true) {
                    ensureActive()
                    if (shouldPause()) throw UploadFailure(UploadFailure.Reason.PAUSED)
                    val n = input.read(buffer)
                    if (n < 0) break
                    digest.update(buffer, 0, n)
                    read += n
                    progress(read)
                }
            }
            digest.digest().toHex()
        }
}

/** How network failures are retried inside one run. */
class RetryPolicy(
    /** Delays before each retry, in milliseconds; the run fails once they are used up. */
    val delaysMillis: List<Long> = listOf(500, 1_000, 2_000, 4_000),
    val sleep: suspend (Long) -> Unit = { delay(it) },
) {
    companion object {
        /** No wait: for tests. */
        val immediate = RetryPolicy(sleep = {})
    }
}

/**
 * One chunked, resumable upload (docs/specs/03-protocole.md §7), mirror of
 * `ResumableUpload.swift`.
 *
 * - Chunks of the size given by the console are streamed from the file
 *   ([FileRegionBody]): the file is never loaded in memory.
 * - After a network error, the run waits, asks `GET /uploads/{id}` for the
 *   offset received and goes on from there. A `409 conflict` on a chunk
 *   carries the expected offset (`details.offset`): the run goes on from it.
 * - An upload the console no longer knows (`404`: purged after 24 h idle,
 *   or lost) is created again from the start, unless `complete` was
 *   already sent.
 * - Once `complete` is sent, the outcome is only known from its answer: if
 *   the answer is lost and the console then no longer knows the upload, the
 *   run fails with [UploadFailure.Reason.OUTCOME_UNKNOWN] and never sends
 *   the file again by itself (this state survives across runs).
 * - `complete` refused with `429 device_busy` (another write held the
 *   console for 10 s) is sent again after `Retry-After`: nothing was written.
 * - Every request is a new signed request (new nonce).
 *
 * A failed run keeps its state: calling [run] again resumes the upload.
 * Runs never overlap (one at a time).
 */
class ResumableUpload(
    val file: File,
    val sizeBytes: Long,
    val sha256: String,
    target: UploadTarget,
    onConflict: OnConflict = OnConflict.FAIL,
    private val retry: RetryPolicy = RetryPolicy(),
) {
    @Volatile
    var target: UploadTarget = target
        private set

    @Volatile
    var onConflict: OnConflict = onConflict
        private set

    @Volatile
    var uploadId: String? = null
        private set

    /** Bytes confirmed by the console. */
    @Volatile
    var offset: Long = 0
        private set

    @Volatile
    var chunkSize: Long = DEFAULT_CHUNK_SIZE
        private set

    /** `complete` was sent and no definite answer came back yet. */
    @Volatile
    var completeSent = false
        private set

    private var needsSync = false
    private val mutex = Mutex()

    /**
     * Changes the conflict rule (fixed at creation, so the upload starts
     * again); returns the id of the previous upload, for the caller to
     * cancel it.
     */
    suspend fun restart(onConflict: OnConflict): String? = mutex.withLock {
        val previous = uploadId
        this.onConflict = onConflict
        uploadId = null
        offset = 0
        needsSync = false
        completeSent = false
        previous
    }

    /**
     * Sends what is left, then completes the upload.
     *
     * @param shouldPause checked between chunks; true stops the run with
     *   [UploadFailure.Reason.PAUSED].
     * @param progress the offset confirmed by the console, after every change.
     * @param chunkSent bytes of the chunk being sent, before the console confirms them.
     * @param willComplete called just before `complete` is sent.
     */
    suspend fun run(
        client: ApiClient,
        shouldPause: () -> Boolean = { false },
        progress: (Long) -> Unit = {},
        chunkSent: (Long) -> Unit = {},
        willComplete: () -> Unit = {},
    ): UploadResult = mutex.withLock { runLocked(client, shouldPause, progress, chunkSent, willComplete) }

    private suspend fun runLocked(
        client: ApiClient,
        shouldPause: () -> Boolean,
        progress: (Long) -> Unit,
        chunkSent: (Long) -> Unit,
        willComplete: () -> Unit,
    ): UploadResult {
        var failures = 0
        var restarts = 0
        var conflicts = 0
        // A previous run sent `complete` and the console forgot the upload.
        if (completeSent && uploadId == null) throw UploadFailure(UploadFailure.Reason.OUTCOME_UNKNOWN)
        if (uploadId != null) needsSync = true
        if (file.length() != sizeBytes) throw UploadFailure(UploadFailure.Reason.FILE_CHANGED)

        while (true) {
            currentCoroutineContextEnsureActive()
            if (shouldPause()) throw UploadFailure(UploadFailure.Reason.PAUSED)
            try {
                val known = uploadId
                if (known == null) {
                    val created = client.createUpload(CreateUploadRequest(target, sizeBytes, sha256, onConflict))
                    uploadId = created.uploadId
                    chunkSize = created.chunkSize.coerceIn(1, MAX_CHUNK_SIZE)
                    offset = created.offset.coerceIn(0, sizeBytes)
                    needsSync = false
                    progress(offset)
                } else if (needsSync) {
                    val status = client.uploadStatus(known)
                    needsSync = false
                    if (status.sizeBytes != sizeBytes || status.offset !in 0..sizeBytes) {
                        // Not the upload we know. After `complete` the file may be written: never start over then.
                        if (completeSent) {
                            uploadId = null
                            throw UploadFailure(UploadFailure.Reason.OUTCOME_UNKNOWN)
                        }
                        restarts = forget(restarts)
                        continue
                    }
                    offset = status.offset
                    progress(offset)
                }
                val id = uploadId ?: continue

                while (offset < sizeBytes) {
                    if (shouldPause()) throw UploadFailure(UploadFailure.Reason.PAUSED)
                    currentCoroutineContextEnsureActive()
                    val length = minOf(chunkSize, sizeBytes - offset)
                    val next = try {
                        client.sendChunk(id, offset, file, length) { chunkSent(minOf(it, length)) }
                    } catch (e: ApiException.Server) {
                        // The console has another offset (a chunk whose answer was lost was stored after all).
                        val expected = e.expectedOffset ?: throw e
                        conflicts += 1
                        if (conflicts > 8 || expected !in 0..sizeBytes) {
                            throw UploadFailure(UploadFailure.Reason.TOO_MANY_RESTARTS)
                        }
                        expected
                    } catch (e: Exception) {
                        // The chunk ended without confirmation: only the confirmed offset counts.
                        progress(offset)
                        throw e
                    }
                    offset = next.coerceIn(0, sizeBytes)
                    failures = 0
                    progress(offset)
                }

                if (shouldPause()) throw UploadFailure(UploadFailure.Reason.PAUSED)
                willComplete()
                val sentBefore = completeSent
                completeSent = true
                val result = try {
                    client.completeUpload(id)
                } catch (e: ApiException) {
                    // A definite refusal (conflict, checksum, or an upload unknown to a
                    // first `complete`): nothing was written. A lost answer keeps the doubt.
                    if (e is ApiException.Server && !isTransient(e) && (e.code != ErrorCodes.NOT_FOUND || !sentBefore)) {
                        completeSent = false
                    }
                    // Refused before it started (device busy, either form): nothing was written, sent again.
                    if (e is ApiException.Server && e.isDeviceBusy) completeSent = sentBefore
                    throw e
                }
                completeSent = false
                uploadId = null
                return result
            } catch (e: CancellationException) {
                throw e
            } catch (e: ApiException) {
                if (isTransient(e)) {
                    failures += 1
                    if (failures > retry.delaysMillis.size) {
                        needsSync = uploadId != null
                        throw e
                    }
                    var wait = retry.delaysMillis[failures - 1]
                    (e as? ApiException.Server)?.retryAfter?.let { wait = maxOf(wait, it * 1_000L) }
                    retry.sleep(wait)
                    needsSync = uploadId != null
                    continue
                }
                if (e is ApiException.Server && e.code == ErrorCodes.NOT_FOUND && uploadId != null) {
                    // The console lost the upload (purged, or lost).
                    if (completeSent) {
                        uploadId = null
                        throw UploadFailure(UploadFailure.Reason.OUTCOME_UNKNOWN)
                    }
                    restarts = forget(restarts)
                    continue
                }
                if (e is ApiException.Server && e.code == ErrorCodes.CHECKSUM_MISMATCH) {
                    // The console deleted the upload.
                    uploadId = null
                    offset = 0
                }
                throw e
            }
        }
    }

    /** What a cancellation achieved. */
    sealed interface CancelOutcome {
        /** Nothing written on the console. */
        data object Cancelled : CancelOutcome

        /** `complete` had been sent and the console no longer knows the upload: the file may be written. */
        data object OutcomeUnknown : CancelOutcome

        /** No definite answer (lost connection, `5xx`...): the upload is kept (resume, or cancel again). */
        data class NotConfirmed(val error: Throwable?) : CancelOutcome
    }

    /**
     * Cancels the upload on the console and forgets it, but only once the
     * console confirmed: `2xx`, or `404` (already gone) before `complete` was
     * sent. Otherwise the upload is kept, to resume or cancel again.
     */
    suspend fun cancel(client: ApiClient): CancelOutcome {
        val id = uploadId ?: return if (completeSent) CancelOutcome.OutcomeUnknown else CancelOutcome.Cancelled
        val outcome: CancelOutcome = try {
            client.cancelUpload(id)
            CancelOutcome.Cancelled
        } catch (e: CancellationException) {
            throw e
        } catch (e: ApiException.Server) {
            if (e.code != ErrorCodes.NOT_FOUND) return CancelOutcome.NotConfirmed(e)
            if (completeSent) CancelOutcome.OutcomeUnknown else CancelOutcome.Cancelled
        } catch (e: Exception) {
            return CancelOutcome.NotConfirmed(e)
        }
        uploadId = null
        offset = 0
        if (outcome == CancelOutcome.Cancelled) completeSent = false
        return outcome
    }

    private fun forget(restarts: Int): Int {
        if (restarts + 1 > 3) throw UploadFailure(UploadFailure.Reason.TOO_MANY_RESTARTS)
        uploadId = null
        offset = 0
        needsSync = false
        return restarts + 1
    }

    private suspend fun currentCoroutineContextEnsureActive() {
        kotlinx.coroutines.currentCoroutineContext().ensureActive()
    }

    companion object {
        const val MAX_CHUNK_SIZE = 16L shl 20
        const val DEFAULT_CHUNK_SIZE = 4L shl 20

        /** Errors retried in the same run: the connection, or a busy console. */
        fun isTransient(error: ApiException): Boolean = when (error) {
            is ApiException.Network -> true
            is ApiException.Server -> error.code == ErrorCodes.TOO_MANY_ATTEMPTS ||
                error.code == ErrorCodes.DEVICE_BUSY || error.code == ErrorCodes.SHUTTING_DOWN
            else -> false
        }
    }
}
