package link.reminis.app.ui

import android.app.Application
import android.net.Uri
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import link.reminis.app.GameNotice
import link.reminis.app.LocalFiles
import link.reminis.app.PickedFile
import link.reminis.app.R
import link.reminis.app.container
import link.reminis.core.LibraryGame
import link.reminis.core.LibrarySearch
import link.reminis.core.PatchInput
import link.reminis.core.PatchMismatchDetails
import link.reminis.core.PatchNaming
import link.reminis.core.PatchOutputMode
import link.reminis.core.PatchRefusal
import link.reminis.core.PatchRun
import link.reminis.core.PatchStep
import link.reminis.core.SdFile
import link.reminis.core.SdFileType
import link.reminis.core.SdFiles
import link.reminis.core.TrackedJob
import link.reminis.core.UploadFailure

/** The game a patch is applied to. */
data class PatchGame(val id: String, val name: String, val fileName: String, val kind: String) {
    val refusal: PatchRefusal? get() = PatchNaming.refusal(kind, fileName)
}

enum class PatchFrom { FILES, SD_CARD }

/** The inputs of the Patch screen. */
data class PatchForm(
    val deviceId: String?,
    val game: PatchGame? = null,
    val from: PatchFrom = PatchFrom.FILES,
    val file: PickedFile? = null,
    val fileError: Throwable? = null,
    val sdFiles: Load<List<SdFile>>? = null,
    val sdFileId: String? = null,
    val mode: PatchOutputMode = PatchOutputMode.NEW_FILE,
    val fileName: String = "",
    val loadError: Throwable? = null,
) {
    val outputName: String? get() = game?.let { PatchNaming.outputName(fileName, it.fileName) }
}

/**
 * Patch screen (docs/specs/06-app-ios.md §4.5, M6): choose the patch (a
 * file of the phone, or the SD card), choose the output (a new file with a
 * suggested name, or replace the game), follow the job. Opened from a game,
 * or by a shared `.ips`, `.bps` or `.ups` (the console, then the game, are
 * chosen first). The app proposes no source of patches.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class PatchViewModel(
    app: Application,
    private val fixedDeviceId: String?,
    private val fixedGameId: String?,
    private val shareToken: String?,
) : androidx.lifecycle.AndroidViewModel(app) {
    private val container = app.container
    private val _form = MutableStateFlow(
        PatchForm(fixedDeviceId ?: container.consoles.all().singleOrNull()?.deviceId),
    )
    val form: StateFlow<PatchForm> = _form.asStateFlow()

    private val _run = MutableStateFlow<PatchRun?>(null)
    val run: StateFlow<PatchRun?> = _run.asStateFlow()

    val step: StateFlow<PatchStep> = _run.flatMapLatest { it?.step ?: flowOf(PatchStep.Idle) }
        .stateIn(viewModelScope, SharingStarted.Eagerly, PatchStep.Idle)
    val cancelling: StateFlow<Boolean> = _run.flatMapLatest { it?.cancelling ?: flowOf(false) }
        .stateIn(viewModelScope, SharingStarted.Eagerly, false)
    val cancelError: StateFlow<Throwable?> = _run.flatMapLatest { it?.cancelError ?: flowOf(null) }
        .stateIn(viewModelScope, SharingStarted.Eagerly, null)

    /** The job of the patch, as last polled. */
    val job: StateFlow<TrackedJob?> = container.jobs.jobs.flatMapLatest { list ->
        flowOf(_run.value?.let { r -> r.jobId?.let { id -> list.firstOrNull { it.deviceId == r.deviceId && it.jobId == id } } })
    }.stateIn(viewModelScope, SharingStarted.Eagerly, null)

    private val _games = MutableStateFlow<Load<List<LibraryGame>>?>(null)
    val games: StateFlow<Load<List<LibraryGame>>?> = _games.asStateFlow()

    val consoles get() = container.consoles.all()
    val choosesConsole: Boolean get() = fixedDeviceId == null && container.consoles.all().size > 1
    val choosesGame: Boolean get() = fixedGameId == null

    init {
        makeRun()
        shareToken?.let { token -> container.shareInbox.get(token)?.firstOrNull()?.let(::pickFile) }
        if (fixedGameId != null) loadGame(fixedGameId)
    }

    private fun makeRun() {
        val deviceId = _form.value.deviceId ?: return
        if (_run.value?.deviceId == deviceId) return
        _run.value = PatchRun(
            deviceId, viewModelScope, container.jobs, { container.client(deviceId) },
            onLocalCopyDone = { input -> container.backgroundScope.launch(Dispatchers.IO) { input.file.parentFile?.deleteRecursively() } },
            // The answer of POST /patch, its job and a cancellation outlive this screen.
            durableScope = container.scope,
            pairing = { container.consoles.get(deviceId)?.let { it.pin to it.pairedAt } },
        )
    }

    /** Once something was sent to a console, it stays. */
    fun chooseConsole(deviceId: String) {
        if (_run.value?.isBoundToConsole == true || _form.value.deviceId == deviceId) return
        _form.update { it.copy(deviceId = deviceId, game = null, sdFiles = null, sdFileId = null) }
        _games.value = null
        makeRun()
    }

    private fun loadGame(gameId: String) {
        val deviceId = _form.value.deviceId ?: return
        viewModelScope.launch {
            try {
                val detail = container.client(deviceId).game(gameId)
                setGame(PatchGame(detail.id, detail.name, detail.fileName, detail.kind))
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                _form.update { it.copy(loadError = e) }
            }
        }
    }

    fun setGame(game: PatchGame) {
        _form.update { it.copy(game = game, fileName = PatchNaming.suggestedFileName(game.fileName), loadError = null) }
        inputChanged()
        if (_form.value.from == PatchFrom.SD_CARD) loadSdFiles()
    }

    /** The games of the console that can be patched, for a shared patch. */
    fun loadGames() {
        val deviceId = _form.value.deviceId ?: return
        if (_games.value is Load.Loaded || _games.value == Load.Loading) return
        _games.value = Load.Loading
        viewModelScope.launch {
            val result = try {
                Load.Loaded(container.client(deviceId).allGames().games.filter { PatchNaming.refusal(it.kind, it.fileName) == null })
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                Load.Failed(e)
            }
            if (_form.value.deviceId == deviceId) _games.value = result
        }
    }

    fun filteredGames(games: List<LibraryGame>, query: String) = LibrarySearch.filter(games, query)

    fun setFrom(from: PatchFrom) {
        _form.update { it.copy(from = from) }
        inputChanged()
        if (from == PatchFrom.SD_CARD && _form.value.sdFiles == null) loadSdFiles()
    }

    fun setMode(mode: PatchOutputMode) {
        _form.update { it.copy(mode = mode) }
        inputChanged()
    }

    fun setFileName(name: String) {
        _form.update { it.copy(fileName = name) }
        inputChanged()
    }

    fun chooseSdFile(id: String) {
        _form.update { it.copy(sdFileId = id) }
        inputChanged()
    }

    private fun inputChanged() = _run.value?.inputChanged()

    private fun loadSdFiles() {
        val deviceId = _form.value.deviceId ?: return
        val game = _form.value.game ?: return
        _form.update { it.copy(sdFiles = Load.Loading) }
        viewModelScope.launch {
            val result = try {
                Load.Loaded(SdFiles.sortedForGame(container.client(deviceId).sdFiles(SdFileType.PATCH), game.fileName, game.name))
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                Load.Failed(e)
            }
            _form.update { it.copy(sdFiles = result) }
        }
    }

    /** A patch chosen with the document picker (or shared), copied into the app. */
    fun pickFile(uri: Uri) {
        viewModelScope.launch {
            try {
                val picked = LocalFiles.copyIn(getApplication(), uri, container.transferDirectory)
                val previous = _form.value.file
                _form.update { it.copy(file = picked, fileError = null) }
                inputChanged()
                // The previous copy, unless it is being sent.
                if (previous != null && _run.value?.step?.value?.isBusy != true) withContext(Dispatchers.IO) { previous.remove() }
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                _form.update { it.copy(fileError = e) }
            }
        }
    }

    val canStart: Boolean
        get() {
            val f = _form.value
            val run = _run.value ?: return false
            if (f.game == null || f.game.refusal != null || run.step.value.isBusy || run.cancelling.value) return false
            val hasSource = when (f.from) {
                PatchFrom.FILES -> f.file != null
                PatchFrom.SD_CARD -> f.sdFileId != null
            }
            return hasSource && (f.mode == PatchOutputMode.REPLACE || f.outputName != null)
        }

    fun start() {
        if (!canStart) return
        val f = _form.value
        val game = f.game ?: return
        val input = when (f.from) {
            PatchFrom.FILES -> f.file!!.let { PatchInput.LocalFile(it.file, it.name, it.file.path) }
            PatchFrom.SD_CARD -> PatchInput.SdCard(f.sdFileId!!)
        }
        val title = getApplication<Application>().getString(R.string.patch_job_title, game.name)
        _run.value?.start(game.id, input, f.mode, if (f.mode == PatchOutputMode.NEW_FILE) f.outputName else null, title)
    }

    fun cancel() = _run.value?.cancel()

    suspend fun tick() = _run.value?.tick()

    /** The text of a success, also shown on the game once the screen closed by itself. */
    fun successText(late: Boolean): String {
        val app = getApplication<Application>()
        return when {
            late -> app.getString(R.string.patch_applied_late)
            _form.value.mode == PatchOutputMode.REPLACE -> app.getString(R.string.patch_applied_replace)
            else -> app.getString(R.string.patch_applied_new)
        }
    }

    /** Applied: the notice goes to the game the screen closes on. */
    fun leaveNotice(deviceId: String, gameId: String, text: String) {
        container.gameNotice.value = GameNotice(deviceId, gameId, text)
        container.libraryChanged.tryEmit(deviceId)
    }

    override fun onCleared() {
        _run.value?.discard(container.backgroundScope)
        val file = _form.value.file
        val busy = _run.value?.step?.value?.holdsTheScreen == true
        if (file != null && !busy) container.backgroundScope.launch(Dispatchers.IO) { file.remove() }
        shareToken?.let(container.shareInbox::remove)
    }
}

/**
 * Patch screen. Once applied, it closes by itself on the game (the new
 * one, or the patched one), which says so; an IPS whose source could not be
 * verified keeps the screen, for its warning ([onFinished] is then called
 * from Done or Open the New Game).
 */
@Composable
fun PatchScreen(
    deviceId: String?,
    gameId: String?,
    shareToken: String?,
    onBack: () -> Unit,
    onFinished: (deviceId: String, gameId: String) -> Unit,
) {
    val app = LocalContext.current.applicationContext as Application
    val vm: PatchViewModel = viewModel(key = "patch-$deviceId-$gameId-$shareToken") { PatchViewModel(app, deviceId, gameId, shareToken) }
    val form by vm.form.collectAsState()
    val step by vm.step.collectAsState()
    val cancelling by vm.cancelling.collectAsState()
    val cancelError by vm.cancelError.collectAsState()
    val job by vm.job.collectAsState()
    val run by vm.run.collectAsState()
    val context = LocalContext.current
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri -> uri?.let(vm::pickFile) }
    var confirmReplace by remember { mutableStateOf(false) }
    var pickingGame by remember { mutableStateOf(false) }
    val locked = step.isBusy || cancelling || step is PatchStep.Succeeded

    PollWhileVisible(run) { vm.tick() }
    // Back while the app side runs cancels it first (the screen never closes on an upload in flight).
    BackHandler(enabled = step.holdsTheScreen && !cancelling) { vm.cancel() }
    LaunchedEffect(step) {
        val s = step as? PatchStep.Succeeded ?: return@LaunchedEffect
        val device = form.deviceId ?: return@LaunchedEffect
        val game = form.game ?: return@LaunchedEffect
        if (s.verified == false) return@LaunchedEffect
        val target = s.gameId ?: game.id
        vm.leaveNotice(device, target, vm.successText(s.late))
        onFinished(device, target)
    }
    LaunchedEffect(Unit) { if (vm.choosesGame && form.game == null && form.deviceId != null) pickingGame = true }

    AppScaffold(
        title = stringResource(R.string.patch_title),
        onBack = { if (step.holdsTheScreen && !cancelling) vm.cancel() else onBack() },
    ) { modifier ->
        Column(modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp).testTag("patchScreen"), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            if (vm.choosesConsole) {
                Text(stringResource(R.string.add_console_choice), style = MaterialTheme.typography.titleSmall)
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    vm.consoles.forEach { console ->
                        FilterChip(
                            selected = form.deviceId == console.deviceId,
                            onClick = { vm.chooseConsole(console.deviceId); pickingGame = true },
                            enabled = run?.isBoundToConsole != true,
                            label = { Text(console.deviceName) },
                        )
                    }
                }
            }
            form.loadError?.let { ErrorCard(errorMessage(context, it)) }
            // Game
            ListItem(
                overlineContent = { Text(stringResource(R.string.patch_game)) },
                headlineContent = { Text(form.game?.name ?: stringResource(R.string.patch_choose)) },
                supportingContent = if (vm.choosesGame && form.game == null) {
                    { Text(stringResource(R.string.patch_choose_game_help)) }
                } else {
                    null
                },
                modifier = Modifier.testTag("patchGame").let {
                    if (vm.choosesGame && !locked && form.deviceId != null) it.clickable { pickingGame = true } else it
                },
            )
            val game = form.game
            if (game != null) {
                val refusal = game.refusal
                if (refusal != null) {
                    Text(
                        stringResource(if (refusal == PatchRefusal.MULTI_DISC) R.string.patch_refused_multi else R.string.patch_refused_archive),
                        modifier = Modifier.testTag("patchRefused"),
                    )
                } else {
                    PatchStatus(vm, step, job, cancelling, cancelError, form.mode, onFinished = { target -> form.deviceId?.let { onFinished(it, target) } }, gameId = game.id)
                    HorizontalDivider()
                    PatchSourceSection(vm, form, locked, onChoose = { picker.launch(arrayOf("*/*")) })
                    HorizontalDivider()
                    PatchOutputSection(vm, form, locked)
                    if (!step.isBusy && step !is PatchStep.Succeeded) {
                        Button(
                            onClick = { if (form.mode == PatchOutputMode.REPLACE) confirmReplace = true else vm.start() },
                            enabled = vm.canStart,
                            modifier = Modifier.fillMaxWidth().testTag("patchApply"),
                        ) { Text(stringResource(R.string.patch_apply)) }
                    }
                    if (step.holdsTheScreen && !cancelling) {
                        OutlinedButton(
                            onClick = vm::cancel,
                            enabled = step != PatchStep.CancellingUpload,
                            modifier = Modifier.fillMaxWidth().testTag("patchCancel"),
                        ) { Text(stringResource(R.string.action_cancel)) }
                    }
                }
            }
        }
    }
    if (confirmReplace) {
        ConfirmDialog(
            title = stringResource(R.string.patch_replace_title),
            message = stringResource(R.string.patch_replace_warning),
            confirm = stringResource(R.string.patch_replace_game),
            onConfirm = vm::start,
            onDismiss = { confirmReplace = false },
        )
    }
    if (pickingGame && vm.choosesGame && form.deviceId != null) {
        PatchGamePicker(vm, onPicked = { g -> vm.setGame(PatchGame(g.id, g.name, g.fileName, g.kind)); pickingGame = false }, onDismiss = { pickingGame = false })
    }
}

@Composable
private fun ErrorCard(text: String) {
    Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.errorContainer), modifier = Modifier.fillMaxWidth()) {
        Text(text, modifier = Modifier.padding(16.dp).testTag("patchError"))
    }
}

@Composable
private fun PatchStatus(
    vm: PatchViewModel,
    step: PatchStep,
    job: TrackedJob?,
    cancelling: Boolean,
    cancelError: Throwable?,
    mode: PatchOutputMode,
    gameId: String,
    onFinished: (String) -> Unit,
) {
    val context = LocalContext.current
    when (step) {
        PatchStep.Idle -> Unit
        PatchStep.Preparing -> Progress(stringResource(R.string.patch_preparing), null)
        is PatchStep.Sending -> Progress(stringResource(R.string.patch_sending, stringResource(R.string.percent, (step.fraction * 100).toInt())), step.fraction)
        PatchStep.Starting -> Progress(stringResource(R.string.patch_starting), null)
        PatchStep.CancellingUpload -> Progress(stringResource(R.string.patch_cancelling), null)
        PatchStep.Patching -> {
            val fraction = job?.job?.progress?.fraction
            Progress(
                if (fraction != null) stringResource(R.string.patch_applying_percent, stringResource(R.string.percent, (fraction * 100).toInt())) else stringResource(R.string.patch_applying),
                fraction,
            )
            if (!cancelling && job?.cancelRequested != true) {
                OutlinedButton(onClick = vm::cancel, modifier = Modifier.fillMaxWidth().testTag("patchCancelJob")) {
                    Text(stringResource(R.string.patch_cancel_job))
                }
            }
            Text(stringResource(R.string.patch_can_close), style = MaterialTheme.typography.bodySmall)
            job?.pollError?.let {
                Text(errorMessage(context, it), color = MaterialTheme.colorScheme.error)
                Text(stringResource(R.string.patch_poll_footer), style = MaterialTheme.typography.bodySmall)
            }
        }
        is PatchStep.Succeeded -> {
            Text(vm.successText(step.late), modifier = Modifier.testTag("patchSucceeded"))
            if (step.verified == false) {
                Text(stringResource(R.string.patch_unverified), style = MaterialTheme.typography.bodySmall, modifier = Modifier.testTag("patchUnverified"))
            }
            val target = step.gameId
            if (target != null && target != gameId) {
                Button(onClick = { onFinished(target) }, modifier = Modifier.fillMaxWidth().testTag("patchOpenGame")) {
                    Text(stringResource(R.string.patch_open_new_game))
                }
            } else {
                Button(onClick = { onFinished(gameId) }, modifier = Modifier.fillMaxWidth().testTag("patchDone")) {
                    Text(stringResource(R.string.patch_done))
                }
            }
        }
        is PatchStep.Failed -> {
            val error = step.error
            val reason = (error as? UploadFailure)?.reason
            ErrorCard(errorMessage(context, patchError(error)))
            step.mismatch?.let { MismatchRows(it) }
            if (reason == UploadFailure.Reason.CANCEL_NOT_CONFIRMED) {
                OutlinedButton(onClick = vm::cancel, modifier = Modifier.fillMaxWidth()) { Text(stringResource(R.string.patch_cancel_again)) }
            }
        }
        PatchStep.Cancelled -> Text(stringResource(R.string.patch_cancelled), modifier = Modifier.testTag("patchCancelled"))
    }
    if (cancelling || (step.isBusy && job?.cancelRequested == true)) Progress(stringResource(R.string.patch_cancelling), null)
    cancelError?.let {
        Text(stringResource(R.string.patch_cancel_error, errorMessage(context, it)), color = MaterialTheme.colorScheme.error)
        OutlinedButton(onClick = vm::cancel) { Text(stringResource(R.string.action_retry)) }
    }
}

@Composable
private fun MismatchRows(details: PatchMismatchDetails) {
    val context = LocalContext.current
    fun describe(size: Long?, crc: String?) = listOfNotNull(size?.let { formatBytes(context, it) }, crc?.let { "CRC32 $it" }).joinToString(" · ")
    if (details.expectedSize != null || details.expectedCrc32 != null) {
        Text(stringResource(R.string.patch_expected_rom, describe(details.expectedSize, details.expectedCrc32)), style = MaterialTheme.typography.bodySmall)
    }
    if (details.actualSize != null || details.actualCrc32 != null) {
        Text(stringResource(R.string.patch_console_rom, describe(details.actualSize, details.actualCrc32)), style = MaterialTheme.typography.bodySmall)
    }
}

@Composable
private fun Progress(text: String, fraction: Float?) {
    Column(verticalArrangement = Arrangement.spacedBy(6.dp), modifier = Modifier.testTag("patchProgress")) {
        Text(text)
        if (fraction != null) LinearProgressIndicator(progress = { fraction }, modifier = Modifier.fillMaxWidth()) else LinearProgressIndicator(Modifier.fillMaxWidth())
    }
}

@Composable
private fun PatchSourceSection(vm: PatchViewModel, form: PatchForm, locked: Boolean, onChoose: () -> Unit) {
    val context = LocalContext.current
    Text(stringResource(R.string.patch_source), style = MaterialTheme.typography.titleSmall)
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        FilterChip(selected = form.from == PatchFrom.FILES, onClick = { vm.setFrom(PatchFrom.FILES) }, enabled = !locked, label = { Text(stringResource(R.string.source_files)) })
        FilterChip(
            selected = form.from == PatchFrom.SD_CARD, onClick = { vm.setFrom(PatchFrom.SD_CARD) }, enabled = !locked,
            label = { Text(stringResource(R.string.source_sd_card)) }, modifier = Modifier.testTag("patchFromSd"),
        )
    }
    when (form.from) {
        PatchFrom.FILES -> {
            form.file?.let { Text("${it.name} · ${formatBytes(context, it.sizeBytes)}", modifier = Modifier.testTag("patchFile")) }
            form.fileError?.let { Text(errorMessage(context, it), color = MaterialTheme.colorScheme.error) }
            OutlinedButton(onClick = onChoose, enabled = !locked, modifier = Modifier.fillMaxWidth().testTag("patchChoose")) {
                Text(stringResource(if (form.file == null) R.string.choose_file else R.string.choose_another_file))
            }
            Text(stringResource(R.string.patch_files_help), style = MaterialTheme.typography.bodySmall)
        }
        PatchFrom.SD_CARD -> SdFilePicker(
            form.sdFiles, form.sdFileId, enabled = !locked, onPick = vm::chooseSdFile,
            empty = stringResource(R.string.patch_sd_empty), footer = stringResource(R.string.patch_sd_help),
        )
    }
}

@Composable
private fun PatchOutputSection(vm: PatchViewModel, form: PatchForm, locked: Boolean) {
    Text(stringResource(R.string.patch_output), style = MaterialTheme.typography.titleSmall)
    for (mode in PatchOutputMode.entries) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier.fillMaxWidth().selectable(selected = form.mode == mode, enabled = !locked) { vm.setMode(mode) }
                .testTag(if (mode == PatchOutputMode.NEW_FILE) "patchNewFile" else "patchReplace"),
        ) {
            RadioButton(selected = form.mode == mode, onClick = null)
            Text(
                stringResource(if (mode == PatchOutputMode.NEW_FILE) R.string.patch_new_file else R.string.patch_replace_game),
                modifier = Modifier.padding(start = 8.dp),
            )
        }
    }
    if (form.mode == PatchOutputMode.NEW_FILE) {
        OutlinedTextField(
            value = form.fileName,
            onValueChange = vm::setFileName,
            label = { Text(stringResource(R.string.patch_file_name)) },
            singleLine = true,
            enabled = !locked,
            isError = form.outputName == null,
            modifier = Modifier.fillMaxWidth().testTag("patchFileName"),
        )
        val name = form.outputName
        if (name == null) {
            Text(stringResource(R.string.patch_name_error), color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
        } else {
            Text(stringResource(R.string.patch_final_name, name), style = MaterialTheme.typography.bodySmall, modifier = Modifier.testTag("patchFinalName"))
        }
    } else {
        Text(stringResource(R.string.patch_replace_warning), modifier = Modifier.testTag("patchReplaceWarning"))
    }
}

/** The games of the console that can be patched (a shared patch), with a search. */
@Composable
private fun PatchGamePicker(vm: PatchViewModel, onPicked: (LibraryGame) -> Unit, onDismiss: () -> Unit) {
    val games by vm.games.collectAsState()
    var query by remember { mutableStateOf("") }
    val context = LocalContext.current
    LaunchedEffect(Unit) { vm.loadGames() }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.patch_choose_game)) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(
                    value = query, onValueChange = { query = it }, singleLine = true,
                    placeholder = { Text(stringResource(R.string.search_games)) }, modifier = Modifier.fillMaxWidth().testTag("patchGameSearch"),
                )
                when (val g = games) {
                    null, Load.Loading -> LinearProgressIndicator(Modifier.fillMaxWidth())
                    is Load.Failed -> Text(errorMessage(context, g.error))
                    is Load.Loaded -> LazyColumn(Modifier.heightIn(max = 360.dp)) {
                        items(vm.filteredGames(g.value, query), key = { it.id }) { game ->
                            ListItem(
                                headlineContent = { Text(game.name) },
                                supportingContent = { Text(game.fileName) },
                                modifier = Modifier.clickable { onPicked(game) }.testTag("patchGameItem"),
                            )
                        }
                    }
                }
            }
        },
        confirmButton = {},
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_cancel)) } },
    )
}
