package link.reminis.core

import java.io.File
import java.util.UUID
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.launch

/** A failed pak installation job: its error, as the console reports it. */
class PakJobFailure(val server: ApiException.Server) : Exception("pak job: ${server.code}")

/** One installation of a pak from a zip of the phone (M12), from the upload to its end. */
data class PakInstall(
    val id: String,
    val deviceId: String,
    /** The file name, as shown. */
    val name: String,
    val kind: PakInstallKind,
    val phase: Phase,
    /** The upload of the current attempt (in the transfer queue). */
    val uploadId: String? = null,
    /** The `pak-install` job of the current attempt. */
    val jobId: String? = null,
    /** The current attempt replaces the installed pak (confirmed): a new conflict is an error, never asked again. */
    val replacing: Boolean = false,
    /** M12: the zip or `.pak` folder of the SD card installed (`POST /paks/install`); null for a file of the phone. */
    val sdEntryId: String? = null,
    /** The installed pak the user agreed to replace. */
    val confirmedName: String? = null,
    /**
     * A replacement installed another pak than the one confirmed (the SD
     * file changed between the conflict and the replacement): what it
     * installed, and whether a pak of that name went to the trash.
     */
    val mismatch: Mismatch? = null,
) {
    data class Mismatch(val installed: String, val replacedOne: Boolean)

    sealed interface Phase {
        /** Uploading through the transfer queue; [fraction] of the file confirmed by the console. */
        data class Sending(val fraction: Float?, val paused: Boolean = false, val failure: Throwable? = null) : Phase

        /** The console installs it (its job). */
        data class Installing(val fraction: Float? = null) : Phase

        /** A pak of this name is installed: waiting for the user. */
        data class NeedsReplacement(val name: String) : Phase
        data object Installed : Phase
        data class Failed(val error: Throwable) : Phase
        data object Cancelled : Phase

        val isFinished: Boolean get() = this is Installed || this is Failed || this is Cancelled
    }
}

/**
 * The pak installations of every console (mirror of `PakManager.swift`,
 * the installation part). A zip of the phone goes through the transfer
 * queue (target `pak`, `onConflict: fail`); its `complete` answers the
 * `jobId` of a `pak-install` job, followed by [jobs] (polled by the screens
 * that show it). A job that fails with `409 conflict` and
 * `details.existing` waits for the user: [replace] sends the same file
 * again with `overwrite`. The local copy is kept until the installation
 * ends ([onDone] deletes it), so that a replacement can send it again.
 */
class PakInstaller(
    private val scope: CoroutineScope,
    private val queue: TransferQueue,
    private val jobs: JobTracker,
    /** The current client of a console: SD installations and the check of a replacement. */
    private val client: (suspend (deviceId: String) -> ApiClient)? = null,
    private val onDone: (File) -> Unit = {},
) {
    /** When the current attempt of an installation started (phone clock, ms). */
    private val attemptStarts = mutableMapOf<String, Long>()
    private val files = mutableMapOf<String, File>()
    private val _installs = MutableStateFlow<List<PakInstall>>(emptyList())
    val installs: StateFlow<List<PakInstall>> = _installs.asStateFlow()
    private val lock = Any()

    init {
        scope.launch {
            // The installations too: an upload that ended before its id was noted is seen on the next change.
            combine(queue.items, jobs.jobs, _installs) { items, tracked, _ -> items to tracked }
                .collect { (items, tracked) -> update(items, tracked) }
        }
    }

    /** Starts an installation of [file] (a local copy owned by this installer from now on). */
    fun install(deviceId: String, file: File, name: String, kind: PakInstallKind): String {
        val id = UUID.randomUUID().toString()
        synchronized(lock) {
            files[id] = file
            _installs.value = _installs.value + PakInstall(id, deviceId, name, kind, PakInstall.Phase.Sending(0f))
        }
        send(id, replacing = false)
        return id
    }

    /**
     * M12: installs a zip or a `.pak` folder of the SD card
     * (`POST /paks/install`, `onConflict: fail`): no upload, the console
     * copies it in a `pak-install` job.
     */
    fun installFromSd(deviceId: String, entry: SdEntry, kind: PakInstallKind): String {
        val id = UUID.randomUUID().toString()
        synchronized(lock) {
            _installs.value = _installs.value + PakInstall(id, deviceId, entry.name, kind, PakInstall.Phase.Installing(), sdEntryId = entry.sdEntryId)
        }
        startSd(id, replacing = false)
        return id
    }

    /** The user confirmed: the same source again, replacing the installed pak. */
    fun replace(id: String) {
        val install = _installs.value.firstOrNull { it.id == id } ?: return
        val phase = install.phase as? PakInstall.Phase.NeedsReplacement ?: return
        synchronized(lock) {
            _installs.value = _installs.value.map { if (it.id == id) it.copy(confirmedName = phase.name) else it }
        }
        if (install.sdEntryId != null) startSd(id, replacing = true) else send(id, replacing = true)
    }

    private fun startSd(id: String, replacing: Boolean) {
        val install = _installs.value.firstOrNull { it.id == id } ?: return
        val entryId = install.sdEntryId ?: return
        val provider = client ?: return set(id, PakInstall.Phase.Failed(ApiException.NotPaired()))
        synchronized(lock) {
            attemptStarts[id] = System.currentTimeMillis()
            _installs.value = _installs.value.map {
                if (it.id == id) it.copy(phase = PakInstall.Phase.Installing(), jobId = null, replacing = replacing, mismatch = null) else it
            }
        }
        scope.launch {
            try {
                val accepted = provider(install.deviceId).installPakFromSd(PakInstallRequest(entryId, install.kind, replacing))
                jobs.track(install.deviceId, accepted.jobId, install.name, JobKinds.PAK_INSTALL)
                synchronized(lock) {
                    _installs.value = _installs.value.map { if (it.id == id && !it.phase.isFinished) it.copy(jobId = accepted.jobId) else it }
                }
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                set(id, PakInstall.Phase.Failed(e))
            }
        }
    }

    /**
     * A replacement installs what the source holds when it runs: an SD file
     * changed since the conflict may install (and replace) another pak than
     * the one confirmed. The console keeps the replaced one in its trash;
     * the app says which one it was (iOS `PakManager.checkReplacements`).
     */
    private fun checkReplacement(install: PakInstall, pakId: String) {
        val confirmed = install.confirmedName ?: return
        val provider = client ?: return
        val since = synchronized(lock) { attemptStarts[install.id] } ?: return
        scope.launch {
            runCatching {
                val api = provider(install.deviceId)
                val installed = api.paks().firstOrNull { it.pakId == pakId }?.name ?: return@runCatching
                if (installed.equals(confirmed, ignoreCase = true)) return@runCatching
                // Console clock, corrected by the measured offset, with 5 s of margin.
                val threshold = since / 1000 + api.clockOffsetSeconds - 5
                val replacedOne = api.trash().entries.any {
                    it.reason == "replace-pak" && it.label.equals(installed, ignoreCase = true) &&
                        (ApiClient.parseRfc3339Seconds(it.trashedAt) ?: 0) >= threshold
                }
                synchronized(lock) {
                    _installs.value = _installs.value.map {
                        if (it.id == install.id) it.copy(mismatch = PakInstall.Mismatch(installed, replacedOne)) else it
                    }
                }
            }
        }
    }

    /** The user kept the installed pak. */
    fun keepInstalled(id: String) {
        if (_installs.value.firstOrNull { it.id == id }?.phase is PakInstall.Phase.NeedsReplacement) set(id, PakInstall.Phase.Cancelled)
    }

    /** Removes the finished installations (of a console) from the list. */
    fun clearFinished(deviceId: String) = synchronized(lock) {
        _installs.value = _installs.value.filterNot { it.deviceId == deviceId && it.phase.isFinished }
    }

    /** A forgotten console: its installations end (copies deleted). */
    fun dropConsole(deviceId: String) {
        _installs.value.filter { it.deviceId == deviceId && !it.phase.isFinished }.forEach { install ->
            install.uploadId?.let(queue::release)
            set(install.id, PakInstall.Phase.Cancelled)
        }
    }

    private fun send(id: String, replacing: Boolean) {
        synchronized(lock) { attemptStarts[id] = System.currentTimeMillis() }
        val file = synchronized(lock) { files[id] } ?: return
        val install = _installs.value.firstOrNull { it.id == id } ?: return
        val uploadId = queue.enqueueUpload(
            install.deviceId, file, install.name, UploadTarget.Pak(install.name, install.kind), install.name,
            onConflict = if (replacing) OnConflict.OVERWRITE else OnConflict.FAIL, ownsFile = false,
            // Clearing the Transfers list only hides it until its outcome is read here.
            pinned = true,
        )
        synchronized(lock) {
            _installs.value = _installs.value.map {
                if (it.id == id) it.copy(phase = PakInstall.Phase.Sending(0f), uploadId = uploadId, jobId = null, replacing = replacing) else it
            }
        }
    }

    private fun update(items: List<TransferItem>, tracked: List<TrackedJob>) {
        for (install in _installs.value) {
            if (install.phase.isFinished || install.phase is PakInstall.Phase.NeedsReplacement) continue
            val jobId = install.jobId
            // An SD installation waits for the answer of `POST /paks/install`.
            if (jobId == null && install.sdEntryId != null) continue
            if (jobId == null) {
                val item = items.firstOrNull { it.id == install.uploadId } ?: continue
                when (item.phase) {
                    TransferPhase.DONE -> {
                        queue.release(item.id)
                        val newJob = item.result?.jobId
                        if (newJob == null) {
                            set(install.id, PakInstall.Phase.Failed(ApiException.InvalidResponse("no jobId")))
                        } else {
                            // Followed before it shows as installing: a poll right after sees it.
                            jobs.track(install.deviceId, newJob, install.name, JobKinds.PAK_INSTALL)
                            synchronized(lock) {
                                _installs.value = _installs.value.map {
                                    if (it.id == install.id) it.copy(jobId = newJob, phase = PakInstall.Phase.Installing()) else it
                                }
                            }
                        }
                    }
                    TransferPhase.CANCELLED -> {
                        queue.release(item.id)
                        set(install.id, PakInstall.Phase.Cancelled)
                    }
                    // Resumed or retried from the Transfers screen; the copy is kept meanwhile.
                    TransferPhase.PAUSED, TransferPhase.FAILED ->
                        setSending(install.id, PakInstall.Phase.Sending(item.fraction, paused = true, failure = item.failure))
                    else -> setSending(install.id, PakInstall.Phase.Sending(item.fraction))
                }
                continue
            }
            val job = tracked.firstOrNull { it.deviceId == install.deviceId && it.jobId == jobId } ?: continue
            if (job.lost) {
                jobs.consume(install.deviceId, jobId)
                set(install.id, PakInstall.Phase.Failed(JobLostException()))
                continue
            }
            val state = job.job ?: continue
            if (state.isFinished) jobs.consume(install.deviceId, jobId)
            when (state.state) {
                JobStates.SUCCEEDED -> {
                    set(install.id, PakInstall.Phase.Installed)
                    if (install.replacing) state.resultPakId?.let { checkReplacement(install, it) }
                }
                JobStates.FAILED -> {
                    val server = state.serverError
                    val existing = server?.conflictingPakName
                    if (existing != null && !install.replacing) {
                        synchronized(lock) {
                            _installs.value = _installs.value.map {
                                if (it.id == install.id) it.copy(phase = PakInstall.Phase.NeedsReplacement(existing)) else it
                            }
                        }
                    } else {
                        set(install.id, PakInstall.Phase.Failed(server?.let(::PakJobFailure) ?: ApiException.InvalidResponse("failed job")))
                    }
                }
                JobStates.CANCELLED -> set(install.id, PakInstall.Phase.Cancelled)
                else -> setSending(install.id, PakInstall.Phase.Installing(state.progress.fraction))
            }
        }
    }

    private fun setSending(id: String, phase: PakInstall.Phase) = synchronized(lock) {
        _installs.value = _installs.value.map { if (it.id == id && !it.phase.isFinished && it.phase != phase) it.copy(phase = phase) else it }
    }

    /** A final phase: the local copy is deleted. */
    private fun set(id: String, phase: PakInstall.Phase) {
        val file = synchronized(lock) {
            _installs.value = _installs.value.map { if (it.id == id) it.copy(phase = phase) else it }
            files.remove(id)
        }
        if (file != null) onDone(file)
    }
}
