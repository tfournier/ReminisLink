package link.reminis.core

import java.io.File
import java.io.OutputStream
import java.util.UUID
import java.util.concurrent.atomic.AtomicBoolean
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull

enum class TransferDirection { UPLOAD, DOWNLOAD }

enum class TransferPhase {
    /** Computing the SHA-256 of the file (uploads). */
    HASHING,

    /** Waiting for its turn. */
    WAITING,

    /** Sending or receiving. */
    RUNNING,

    /** Stopped (connection lost, console closed): resumes at the offset confirmed by the console. */
    PAUSED,
    FAILED,

    /**
     * A cancel is in progress: an upload waits for the console's answer, a
     * download for its call to stop and its partial document to be deleted.
     */
    CANCELLING,
    DONE,
    CANCELLED,
    ;

    val isFinished: Boolean get() = this == DONE || this == CANCELLED

    /** Work in progress or to come (keeps the transfer service running). */
    val isActive: Boolean get() = this == HASHING || this == WAITING || this == RUNNING || this == CANCELLING
}

/** Where a download is written: a document chosen by the user (Storage Access Framework). */
interface DownloadSink {
    /** Opens the document for writing, truncated. */
    fun open(): OutputStream

    /**
     * Commits what was written: the download only succeeds if this returns
     * (a provider may refuse the document only when it is closed).
     */
    fun finish(out: OutputStream) {
        out.flush()
        out.close()
    }

    /** Deletes the partial document (cancelled, failed or dropped); false when it could not be deleted. */
    fun discard(): Boolean
}

/** A cancelled or failed download whose partial document could not be deleted. */
class PartialDocumentException(cause: Throwable? = null) : Exception("partial document not deleted", cause)

/** The console was forgotten on this phone while the transfer was waiting or running. */
class ConsoleForgottenException : Exception("console forgotten")

/** A transfer as shown: an immutable snapshot of the queue. */
data class TransferItem(
    val id: String,
    val deviceId: String,
    val direction: TransferDirection,
    val fileName: String,
    /** Where it goes or comes from, for display (system or game name). */
    val destination: String,
    /** Null for a download until the console announces its length. */
    val sizeBytes: Long?,
    val phase: TransferPhase,
    /** Bytes confirmed by the console (upload) or written (download), plus the chunk in flight while it runs. */
    val doneBytes: Long,
    val hashedBytes: Long,
    val bytesPerSecond: Double?,
    val secondsRemaining: Long?,
    /** Why it is paused or failed. */
    val failure: Throwable?,
    /** `complete` is in flight: the outcome is up to the console, it can no longer be cancelled. */
    val isCompleting: Boolean,
    val result: UploadResult?,
    val target: UploadTarget?,
    /** A new conflict rule is being applied: no other action until it is. */
    val isReserved: Boolean = false,
    /** Its console was forgotten: it never starts again. */
    val isDropped: Boolean = false,
) {
    /** 0..1 of the current step (hashing, then transfer); null when the size is unknown. */
    val fraction: Float?
        get() {
            val size = sizeBytes ?: return null
            if (size <= 0) return if (phase == TransferPhase.DONE) 1f else 0f
            val bytes = if (phase == TransferPhase.HASHING) hashedBytes else doneBytes
            return (bytes.toFloat() / size).coerceIn(0f, 1f)
        }

    /** The upload stopped on `409 conflict` at completion: retry with another rule. */
    val isNameConflict: Boolean
        get() = direction == TransferDirection.UPLOAD && phase == TransferPhase.FAILED && !isReserved && !isDropped &&
            failure?.serverCode == ErrorCodes.CONFLICT && target is UploadTarget.Rom

    /**
     * Resume (paused) or try again (a failed upload). A failed download is
     * final: its partial document was deleted, the user downloads again.
     */
    val canResume: Boolean
        get() = !isReserved && !isDropped && (
            phase == TransferPhase.PAUSED || (phase == TransferPhase.FAILED && direction == TransferDirection.UPLOAD)
            )

    val canCancel: Boolean
        get() = !phase.isFinished && !isCompleting && !isReserved && phase != TransferPhase.CANCELLING &&
            !(phase == TransferPhase.FAILED && direction == TransferDirection.DOWNLOAD)
}

/**
 * The uploads and downloads of every console (docs/specs/06-app-ios.md §4.7,
 * mirror of `TransferQueue.swift`).
 *
 * Uploads run one at a time, in order; downloads too, beside them. Each
 * upload is hashed (streamed), then sent by a [ResumableUpload]. A lost
 * connection, or the console closing ReminisLink, pauses it: it resumes at
 * the offset confirmed by the console ([resume], [resumePaused]). A cancel is
 * final only once the console confirmed it. The queue lives in memory, as on
 * iOS: it does not survive the death of the process (the app keeps the
 * process alive with a foreground service while transfers run).
 *
 * Downloads stream into a document chosen by the user ([DownloadSink]),
 * never through memory; a download succeeds only once the document is
 * committed, and a cancelled or failed download deletes its partial
 * document before it is shown cancelled or failed.
 *
 * Every state change happens under one lock: a worker selects a waiting
 * transfer, marks it started and installs its job in the same critical
 * section, and each later step checks the transfer was not cancelled,
 * paused or dropped meanwhile.
 */
class TransferQueue(
    private val scope: CoroutineScope,
    private val retry: RetryPolicy = RetryPolicy(),
    /**
     * Called (outside the lock) whenever work is added or resumed: the app
     * starts its foreground service then, every time (starting it is
     * idempotent), so no start is lost to a busy flag flickering.
     */
    private val onActive: () -> Unit = {},
    /** A signed, connected client for a console; `refresh` looks for it again (its address may have changed). */
    private val client: suspend (deviceId: String, refresh: Boolean) -> ApiClient,
) {
    private class Entry(
        val id: String,
        val deviceId: String,
        val direction: TransferDirection,
        val fileName: String,
        val destination: String,
        var sizeBytes: Long?,
        // Uploads
        val file: File? = null,
        val ownsFile: Boolean = false,
        var target: UploadTarget? = null,
        var onConflict: OnConflict = OnConflict.FAIL,
        // Downloads
        val source: DownloadSource? = null,
        val sink: DownloadSink? = null,
    ) {
        var phase = TransferPhase.WAITING
        var doneBytes = 0L
        var inFlightBytes = 0L
        var hashedBytes = 0L
        var failure: Throwable? = null
        var isCompleting = false
        var result: UploadResult? = null
        var sha256: String? = null
        var upload: ResumableUpload? = null
        var job: Job? = null
        var pauseFlag = AtomicBoolean(false)
        var refreshClient = false
        var reserved = false
        var dropped = false

        /** A failed download deleting its partial document (counts as busy). */
        var cleaning = false

        /** pauseAll() came while a new conflict rule was being applied: it lands paused. */
        var pauseRequested = false

        /** The open document of a running download (closed by a cancel: it unblocks a stuck write). */
        var stream: OutputStream? = null

        /** A cancelled download whose job did not stop in time: the queue goes on without it. */
        var abandoned = false
        val rate = TransferRate()

        val isStarted: Boolean get() = phase == TransferPhase.HASHING || phase == TransferPhase.RUNNING
    }

    private val lock = Any()
    private val entries = mutableListOf<Entry>()
    private val workers = mutableMapOf<TransferDirection, Job>()
    private var lastProgressPublish = 0L

    private val _items = MutableStateFlow<List<TransferItem>>(emptyList())
    val items: StateFlow<List<TransferItem>> = _items.asStateFlow()

    private val _busy = MutableStateFlow(false)

    /** Transfers in progress, waiting, or being cancelled (cleanup included). */
    val isBusy: StateFlow<Boolean> = _busy.asStateFlow()

    private val _finished = MutableSharedFlow<TransferItem>(extraBufferCapacity = 64)

    /** Every transfer that reached `DONE` (screens reload what it changed). */
    val finished: SharedFlow<TransferItem> = _finished.asSharedFlow()

    /** Tests only: runs between a transfer marked started and its job starting. */
    internal var beforeStartForTests: (suspend (String) -> Unit)? = null

    /** Tests only: runs after the last chunk, right before the decision to send `complete`. */
    internal var beforeCompleteDecisionForTests: (() -> Unit)? = null

    /** Tests only: runs between a new conflict rule chosen and applied. */
    internal var beforeRuleAppliedForTests: (suspend (String) -> Unit)? = null

    // MARK: adding

    /**
     * Adds an upload at the end of the queue.
     *
     * @param file a local copy made for this upload ([ownsFile]: its
     *   directory is deleted once the upload is done or cancelled).
     */
    fun enqueueUpload(
        deviceId: String,
        file: File,
        fileName: String,
        target: UploadTarget,
        destination: String,
        onConflict: OnConflict = OnConflict.FAIL,
        ownsFile: Boolean,
    ): String = add(
        Entry(
            id = UUID.randomUUID().toString(), deviceId = deviceId, direction = TransferDirection.UPLOAD,
            fileName = fileName, destination = destination, sizeBytes = file.length(),
            file = file, ownsFile = ownsFile, target = target, onConflict = onConflict,
        ),
    )

    /** Adds a download of [source] into [sink]. */
    fun enqueueDownload(
        deviceId: String,
        source: DownloadSource,
        fileName: String,
        destination: String,
        sink: DownloadSink,
        sizeBytes: Long? = null,
    ): String = add(
        Entry(
            id = UUID.randomUUID().toString(), deviceId = deviceId, direction = TransferDirection.DOWNLOAD,
            fileName = fileName, destination = destination, sizeBytes = sizeBytes, source = source, sink = sink,
        ),
    )

    private fun add(entry: Entry): String {
        synchronized(lock) {
            entries += entry
            publish()
            pump(entry.direction)
        }
        onActive()
        return entry.id
    }

    // MARK: actions

    /**
     * Cancels a transfer. Ignored while `complete` is in flight or while a
     * new conflict rule is applied. An upload known to the console is
     * `CANCELLING` until the console answers: confirmed, `CANCELLED` (local
     * copy deleted); no definite answer, `FAILED` with
     * [UploadFailure.Reason.CANCEL_NOT_CONFIRMED], kept to resume or cancel
     * again; gone after a `complete` whose answer was lost, `FAILED` with
     * [UploadFailure.Reason.OUTCOME_UNKNOWN]. A download is `CANCELLING`
     * until its call stopped and its partial document is deleted.
     */
    fun cancel(id: String) {
        val entry: Entry
        val upload: ResumableUpload?
        synchronized(lock) {
            entry = entries.firstOrNull { it.id == id } ?: return
            if (entry.phase.isFinished || entry.isCompleting || entry.phase == TransferPhase.CANCELLING || entry.reserved) return
            if (entry.direction == TransferDirection.DOWNLOAD) {
                if (entry.phase == TransferPhase.FAILED) return
                cancelDownloadLocked(entry, failureIfKept = PartialDocumentException())
                return
            }
            entry.failure = null
            upload = entry.upload
            if (upload == null || (upload.uploadId == null && !upload.completeSent)) {
                // Nothing on the console yet.
                entry.phase = TransferPhase.CANCELLED
                entry.job?.cancel()
                cleanUp(entry)
                publish()
                return
            }
            entry.phase = TransferPhase.CANCELLING
            publish()
        }
        val running = entry.job
        running?.cancel()
        scope.launch {
            // The run stops first: no chunk is sent after the cancel.
            running?.join()
            val outcome = try {
                upload!!.cancel(client(entry.deviceId, false))
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                ResumableUpload.CancelOutcome.NotConfirmed(e)
            }
            synchronized(lock) {
                if (entry.phase != TransferPhase.CANCELLING) return@launch
                when (outcome) {
                    ResumableUpload.CancelOutcome.Cancelled -> {
                        entry.phase = TransferPhase.CANCELLED
                        cleanUp(entry)
                    }
                    ResumableUpload.CancelOutcome.OutcomeUnknown -> {
                        entry.phase = TransferPhase.FAILED
                        entry.failure = UploadFailure(UploadFailure.Reason.OUTCOME_UNKNOWN)
                    }
                    is ResumableUpload.CancelOutcome.NotConfirmed -> {
                        entry.phase = TransferPhase.FAILED
                        entry.failure = UploadFailure(UploadFailure.Reason.CANCEL_NOT_CONFIRMED)
                    }
                }
                publish()
            }
        }
    }

    /**
     * `CANCELLING` until the job stopped (its call cancelled) and the partial
     * document is deleted; then `CANCELLED`, or `FAILED` with
     * [PartialDocumentException] when the provider refused the deletion.
     */
    private fun cancelDownloadLocked(entry: Entry, failureIfKept: Throwable) {
        entry.phase = TransferPhase.CANCELLING
        entry.failure = null
        val job = entry.job
        job?.cancel()
        val stream = entry.stream
        publish()
        scope.launch(Dispatchers.IO) {
            // A provider that stopped reading can block a write forever: closing the
            // document from here unblocks it (the call itself is cancelled by the job).
            stream?.let { runCatching { it.close() } }
            if (job != null && withTimeoutOrNull(stopTimeoutMillis) { job.join() } == null) {
                // Still stuck: the queue no longer waits for it.
                synchronized(lock) { entry.abandoned = true }
            }
            val deleted = runCatching { entry.sink?.discard() ?: true }.getOrDefault(false)
            synchronized(lock) {
                if (entry.phase != TransferPhase.CANCELLING) return@launch
                if (deleted) {
                    entry.phase = TransferPhase.CANCELLED
                } else {
                    entry.phase = TransferPhase.FAILED
                    entry.failure = failureIfKept
                }
                publish()
            }
        }
    }

    /** Resumes a paused transfer, or tries a failed upload again. */
    fun resume(id: String) {
        synchronized(lock) {
            val entry = entries.firstOrNull { it.id == id } ?: return
            resumeLocked(entry)
            publish()
        }
        onActive()
    }

    private fun resumeLocked(entry: Entry) {
        if (entry.reserved || entry.dropped) return
        val resumable = entry.phase == TransferPhase.PAUSED ||
            (entry.phase == TransferPhase.FAILED && entry.direction == TransferDirection.UPLOAD)
        if (!resumable) return
        entry.phase = TransferPhase.WAITING
        entry.failure = null
        pump(entry.direction)
    }

    /** Resumes the paused transfers of a console (it answers again). */
    fun resumePaused(deviceId: String) {
        val resumed = synchronized(lock) {
            val paused = entries.filter { it.deviceId == deviceId && it.phase == TransferPhase.PAUSED }
            paused.forEach { resumeLocked(it) }
            publish()
            paused.isNotEmpty()
        }
        if (resumed) onActive()
    }

    /**
     * Starts a failed upload again with another conflict rule ("Keep Both"
     * is `rename`, "Replace" is `overwrite`). The entry is reserved until the
     * rule is applied (no resume, no cancel, no second choice meanwhile); the
     * previous upload is cancelled on the console, best effort.
     */
    fun retry(id: String, onConflict: OnConflict) {
        val entry = synchronized(lock) {
            val e = entries.firstOrNull { it.id == id } ?: return
            if (e.direction != TransferDirection.UPLOAD || e.reserved || e.dropped) return
            if (e.phase != TransferPhase.FAILED && e.phase != TransferPhase.PAUSED) return
            e.reserved = true
            e.onConflict = onConflict
            publish()
            e
        }
        scope.launch {
            try {
                beforeRuleAppliedForTests?.invoke(id)
                val previous = entry.upload?.restart(onConflict)
                if (previous != null) {
                    runCatching { client(entry.deviceId, false).cancelUpload(previous) }
                }
            } finally {
                val resumed = synchronized(lock) {
                    entry.reserved = false
                    if (entry.pauseRequested) {
                        // The service was stopped meanwhile: nothing starts without a new resume.
                        entry.pauseRequested = false
                        entry.phase = TransferPhase.PAUSED
                        entry.failure = null
                        publish()
                        false
                    } else {
                        resumeLocked(entry)
                        publish()
                        true
                    }
                }
                if (resumed) onActive()
            }
        }
    }

    /**
     * Stops everything (the system ends the transfer service): the running
     * upload after its current chunk, the running download at once (it
     * starts again on resume); waiting transfers are paused.
     */
    fun pauseAll() = synchronized(lock) {
        for (entry in entries) {
            // A new conflict rule being applied: it lands paused instead of starting.
            if (entry.reserved) {
                entry.pauseRequested = true
                continue
            }
            when (entry.phase) {
                TransferPhase.WAITING -> entry.phase = TransferPhase.PAUSED
                TransferPhase.HASHING, TransferPhase.RUNNING -> {
                    entry.pauseFlag.set(true)
                    if (entry.direction == TransferDirection.DOWNLOAD) {
                        entry.job?.cancel()
                        entry.stream?.let { stream -> scope.launch(Dispatchers.IO) { runCatching { stream.close() } } }
                    }
                }
                else -> Unit
            }
        }
        publish()
    }

    /**
     * The console was forgotten on this phone: its transfers stop and never
     * start again. Uploads are cancelled locally (the console purges a
     * partial upload after 24 h; this phone no longer signs for it),
     * downloads delete their partial document. An upload whose `complete` is
     * already in flight keeps its real outcome.
     */
    fun dropConsole(deviceId: String) = synchronized(lock) {
        for (entry in entries.filter { it.deviceId == deviceId && !it.phase.isFinished }) {
            entry.dropped = true
            // A terminal outcome (failed, unknown) or a decision in progress keeps its own state.
            if (entry.isCompleting || entry.phase == TransferPhase.FAILED || entry.phase == TransferPhase.CANCELLING) continue
            if (entry.direction == TransferDirection.DOWNLOAD) {
                cancelDownloadLocked(entry, failureIfKept = PartialDocumentException())
                continue
            }
            entry.job?.cancel()
            if (entry.upload?.completeSent == true) {
                // `complete` went without a definite answer: the file may be on the console.
                // Never shown cancelled, and the local copy is kept.
                entry.phase = TransferPhase.FAILED
                entry.failure = UploadFailure(UploadFailure.Reason.OUTCOME_UNKNOWN)
            } else {
                entry.phase = TransferPhase.CANCELLED
                entry.failure = null
                cleanUp(entry)
            }
        }
        publish()
    }

    /** Removes the finished transfers of a console (all consoles when null) from the list. */
    fun clearFinished(deviceId: String? = null) = synchronized(lock) {
        entries.removeAll {
            val finished = it.phase.isFinished ||
                (it.phase == TransferPhase.FAILED && it.direction == TransferDirection.DOWNLOAD && !it.cleaning)
            finished && (deviceId == null || it.deviceId == deviceId)
        }
        publish()
    }

    // MARK: work

    private fun pump(direction: TransferDirection) {
        if (workers[direction] != null) return
        if (entries.none { it.direction == direction && it.phase == TransferPhase.WAITING }) return
        workers[direction] = scope.launch {
            while (true) {
                // Selected, marked started and given its job in one critical section:
                // a cancel or a pause from now on sees a started transfer and its job.
                val (entry, job) = synchronized(lock) {
                    val next = entries.firstOrNull { it.direction == direction && it.phase == TransferPhase.WAITING }
                    if (next == null) {
                        workers.remove(direction)
                        null
                    } else {
                        next.phase = when {
                            direction == TransferDirection.DOWNLOAD -> TransferPhase.RUNNING
                            next.sha256 == null -> TransferPhase.HASHING
                            else -> TransferPhase.RUNNING
                        }
                        next.failure = null
                        next.hashedBytes = if (next.phase == TransferPhase.HASHING) 0 else next.hashedBytes
                        if (direction == TransferDirection.DOWNLOAD) next.doneBytes = 0
                        next.rate.reset()
                        next.pauseFlag = AtomicBoolean(false)
                        val job = launch(start = CoroutineStart.LAZY) {
                            if (direction == TransferDirection.UPLOAD) processUpload(next) else processDownload(next)
                        }
                        next.job = job
                        publish()
                        next to job
                    }
                } ?: break
                beforeStartForTests?.invoke(entry.id)
                // A job cancelled before this never runs its body.
                job.start()
                while (withTimeoutOrNull(1_000) { job.join() } == null) {
                    // A cancelled download stuck in its provider: the others go on.
                    if (synchronized(lock) { entry.abandoned }) break
                }
                synchronized(lock) {
                    // Cancelled (or paused) before it could settle its own phase.
                    if (entry.isStarted) {
                        entry.phase = TransferPhase.PAUSED
                        entry.isCompleting = false
                        entry.inFlightBytes = 0
                        publish()
                    }
                }
            }
        }
    }

    /** Moves a started transfer to [phase]; false (nothing changed) when it was cancelled, paused or dropped meanwhile. */
    private fun advance(entry: Entry, phase: TransferPhase): Boolean = synchronized(lock) {
        if (!entry.isStarted || entry.dropped) return false
        entry.phase = phase
        publish()
        true
    }

    private suspend fun processUpload(entry: Entry) {
        val flag = entry.pauseFlag
        val file = entry.file ?: return
        try {
            if (entry.sha256 == null) {
                val sha = FileDigest.sha256(file, shouldPause = { flag.get() }) { bytes ->
                    entry.hashedBytes = bytes
                    publishProgress()
                }
                entry.sha256 = sha
            }
            if (flag.get()) throw UploadFailure(UploadFailure.Reason.PAUSED)
            val upload = synchronized(lock) {
                entry.upload ?: ResumableUpload(
                    file, entry.sizeBytes ?: file.length(), entry.sha256!!, entry.target!!, entry.onConflict, retry,
                ).also { entry.upload = it }
            }
            if (!advance(entry, TransferPhase.RUNNING)) return
            val api = client(entry.deviceId, entry.refreshClient)
            entry.refreshClient = false
            val result = upload.run(
                api,
                shouldPause = { flag.get() },
                progress = { offset ->
                    entry.doneBytes = offset
                    entry.inFlightBytes = 0
                    entry.rate.record(offset)
                    publishProgress()
                },
                chunkSent = { bytes ->
                    entry.inFlightBytes = maxOf(entry.inFlightBytes, bytes)
                    publishProgress()
                },
                willComplete = {
                    beforeCompleteDecisionForTests?.invoke()
                    // One decision under the lock, against cancel(), pauseAll() and dropConsole():
                    // either `complete` is sent, or the transfer stops before it.
                    synchronized(lock) {
                        if (!entry.isStarted || entry.dropped) throw CancellationException("transfer stopped")
                        if (flag.get()) throw UploadFailure(UploadFailure.Reason.PAUSED)
                        entry.isCompleting = true
                        publish()
                    }
                },
            )
            val done = synchronized(lock) {
                entry.isCompleting = false
                if (entry.phase == TransferPhase.CANCELLED || entry.phase == TransferPhase.CANCELLING) return
                entry.result = result
                entry.doneBytes = entry.sizeBytes ?: entry.doneBytes
                entry.inFlightBytes = 0
                entry.phase = TransferPhase.DONE
                cleanUp(entry)
                publish()
                snapshot(entry)
            }
            _finished.tryEmit(done)
        } catch (e: Throwable) {
            synchronized(lock) {
                entry.isCompleting = false
                entry.inFlightBytes = 0
                // A cancel, or a forgotten console, already decided the phase.
                if (!entry.isStarted) return
                failed(entry, e)
                publish()
            }
            if (e is CancellationException && !scopeActive()) throw e
        }
    }

    private suspend fun processDownload(entry: Entry) {
        val source = entry.source ?: return
        val sink = entry.sink ?: return
        try {
            val api = client(entry.deviceId, entry.refreshClient)
            entry.refreshClient = false
            // The stream is kept in a variable as soon as the provider returns it: resuming
            // after a cancel drops withContext's return value (prompt cancellation), and the
            // stream must still be closed before the document is deleted.
            var opened: OutputStream? = null
            try {
                withContext(NonCancellable + Dispatchers.IO) { opened = sink.open() }
            } catch (e: CancellationException) {
                opened?.let { stream -> withContext(NonCancellable + Dispatchers.IO) { runCatching { stream.close() } } }
                throw e
            }
            val out = opened!!
            synchronized(lock) { entry.stream = out }
            var committed = false
            try {
                currentCoroutineContext().ensureActive()
                api.download(source, out) { written, total ->
                    entry.doneBytes = written
                    if (total != null) entry.sizeBytes = total
                    entry.rate.record(written)
                    publishProgress()
                }
                // Success only once the provider accepted the document.
                withContext(Dispatchers.IO) { sink.finish(out) }
                committed = true
            } finally {
                synchronized(lock) { entry.stream = null }
                if (!committed) withContext(NonCancellable + Dispatchers.IO) { runCatching { out.close() } }
            }
            val done = synchronized(lock) {
                if (!entry.isStarted) return
                entry.phase = TransferPhase.DONE
                entry.sizeBytes = entry.doneBytes
                publish()
                snapshot(entry)
            }
            _finished.tryEmit(done)
        } catch (e: Throwable) {
            val discard = synchronized(lock) {
                if (!entry.isStarted) return
                failed(entry, e)
                val final = entry.phase == TransferPhase.FAILED
                // Busy until the partial document is deleted: the service stays meanwhile.
                if (final) entry.cleaning = true
                publish()
                final
            }
            if (discard) {
                // A failed download is final: its partial document is deleted.
                val deleted = withContext(NonCancellable + Dispatchers.IO) { runCatching { sink.discard() }.getOrDefault(false) }
                synchronized(lock) {
                    entry.cleaning = false
                    if (!deleted) entry.failure = PartialDocumentException(e)
                    publish()
                }
            }
            if (e is CancellationException && !scopeActive()) throw e
        }
    }

    /** The phase after a failure: paused when it will resume (connection, console closing), failed otherwise. */
    private fun failed(entry: Entry, e: Throwable) {
        entry.failure = null
        when {
            e is UploadFailure && e.reason == UploadFailure.Reason.PAUSED -> entry.phase = TransferPhase.PAUSED
            e is CancellationException -> entry.phase = TransferPhase.PAUSED
            e is ApiException.Network || e is ConsoleNotFoundException -> {
                entry.phase = TransferPhase.PAUSED
                entry.failure = e
                entry.refreshClient = true
            }
            e.serverCode == ErrorCodes.SHUTTING_DOWN || e.serverCode == ErrorCodes.DISCONNECTED -> {
                entry.phase = TransferPhase.PAUSED
                entry.failure = e
                entry.refreshClient = true
            }
            else -> {
                entry.phase = TransferPhase.FAILED
                entry.failure = e
            }
        }
        if (entry.dropped && entry.phase == TransferPhase.PAUSED) {
            // Its console is forgotten: it will never resume.
            entry.phase = TransferPhase.FAILED
            entry.failure = ConsoleForgottenException()
        }
        if (entry.phase == TransferPhase.FAILED && entry.upload?.completeSent == true) {
            // `complete` went and no definite answer came back: whatever stopped it
            // (forgotten console, error), the file may be on the console.
            entry.failure = UploadFailure(UploadFailure.Reason.OUTCOME_UNKNOWN)
        }
    }

    private fun isBusyLocked(): Boolean = entries.any { it.phase.isActive || it.cleaning }

    /**
     * Runs [stop] only if nothing is in progress, decided under the queue's
     * lock: an item added at the same moment either sees the service
     * stopping (and starts it again, [onActive]) or keeps it.
     */
    fun stopIfIdle(stop: () -> Unit): Boolean = synchronized(lock) {
        if (isBusyLocked()) return false
        stop()
        true
    }

    /** How long a cancel waits for a download's job to stop before going on without it (shorter in tests). */
    internal var stopTimeoutMillis = 10_000L

    private fun scopeActive(): Boolean = scope.coroutineContext[Job]?.isActive != false

    private fun cleanUp(entry: Entry) {
        if (!entry.ownsFile) return
        val directory = entry.file?.parentFile ?: return
        scope.launch(Dispatchers.IO) { directory.deleteRecursively() }
    }

    /** Progress is published at most every 100 ms; phase changes at once. */
    private fun publishProgress() = synchronized(lock) {
        val now = System.nanoTime()
        if (now - lastProgressPublish < 100_000_000L) return
        publish()
    }

    private fun publish() {
        lastProgressPublish = System.nanoTime()
        _busy.value = isBusyLocked()
        _items.value = entries.map(::snapshot)
    }

    private fun snapshot(e: Entry): TransferItem {
        val shown = if (e.phase == TransferPhase.RUNNING) e.doneBytes + e.inFlightBytes else e.doneBytes
        val size = e.sizeBytes
        val rate = if (e.phase == TransferPhase.RUNNING) e.rate.bytesPerSecond else null
        return TransferItem(
            id = e.id, deviceId = e.deviceId, direction = e.direction, fileName = e.fileName,
            destination = e.destination, sizeBytes = size,
            phase = e.phase, doneBytes = if (size != null) minOf(shown, size) else shown,
            hashedBytes = e.hashedBytes, bytesPerSecond = rate,
            secondsRemaining = if (rate != null && rate > 0 && size != null) ((size - e.doneBytes) / rate).toLong() else null,
            failure = e.failure, isCompleting = e.isCompleting, result = e.result, target = e.target,
            isReserved = e.reserved, isDropped = e.dropped,
        )
    }
}

/** Speed over the last seconds of a transfer. */
class TransferRate(private val windowNanos: Long = 5_000_000_000L, private val now: () -> Long = System::nanoTime) {
    private val samples = ArrayDeque<Pair<Long, Long>>()

    @Synchronized
    fun reset() = samples.clear()

    @Synchronized
    fun record(bytes: Long) {
        val t = now()
        samples.addLast(t to bytes)
        while (samples.size > 2 && t - samples.first().first > windowNanos) samples.removeFirst()
    }

    val bytesPerSecond: Double?
        @Synchronized get() {
            if (samples.size < 2) return null
            val (t0, b0) = samples.first()
            val (t1, b1) = samples.last()
            val dt = (t1 - t0) / 1e9
            if (dt < 0.5 || b1 <= b0) return null
            return (b1 - b0) / dt
        }
}
