package link.reminis.app.ui

import android.app.Application
import android.net.Uri
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
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.pluralStringResource
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
import kotlinx.coroutines.launch
import link.reminis.app.LocalFiles
import link.reminis.app.PickedFile
import link.reminis.app.R
import link.reminis.app.container
import link.reminis.core.ArchiveActions as Actions
import link.reminis.core.ArchiveImportRun
import link.reminis.core.ArchivePlan
import link.reminis.core.ArchiveRoles
import link.reminis.core.ArchiveSkipReasons
import link.reminis.core.ArchiveStep
import link.reminis.core.ArchiveTarget
import link.reminis.core.GameFileRoles
import link.reminis.core.GameSystem
import link.reminis.core.LibraryGame
import link.reminis.core.LibrarySearch

/**
 * Import of a game archive (M19, docs/specs/06-app-ios.md §4.5): choose the
 * archive (document picker, or a `.reminislink.zip` shared to the app), see
 * what the console will add, replace or skip, choose another target if
 * needed, then import. Opened from a game (its data go there), from the
 * Library menu, or by a shared archive (the console is chosen first when
 * several are paired). Everything replaced goes to the console's trash
 * first, and the save to its backup copies.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class ArchiveImportViewModel(
    app: Application,
    private val fixedDeviceId: String?,
    private val gameId: String?,
    private val shareToken: String?,
) : androidx.lifecycle.AndroidViewModel(app) {
    private val container = app.container

    private val _deviceId = MutableStateFlow(fixedDeviceId ?: container.consoles.all().singleOrNull()?.deviceId)
    val deviceId: StateFlow<String?> = _deviceId.asStateFlow()

    private val _run = MutableStateFlow<ArchiveImportRun?>(null)
    val run: StateFlow<ArchiveImportRun?> = _run.asStateFlow()

    val step: StateFlow<ArchiveStep> = _run.flatMapLatest { it?.step ?: flowOf(ArchiveStep.Idle) }
        .stateIn(viewModelScope, SharingStarted.Eagerly, ArchiveStep.Idle)
    val plan: StateFlow<ArchivePlan?> = _run.flatMapLatest { it?.plan ?: flowOf(null) }
        .stateIn(viewModelScope, SharingStarted.Eagerly, null)
    val existingGameId: StateFlow<String?> = _run.flatMapLatest { it?.existingGameId ?: flowOf(null) }
        .stateIn(viewModelScope, SharingStarted.Eagerly, null)

    private val _fileName = MutableStateFlow<String?>(null)
    val fileName: StateFlow<String?> = _fileName.asStateFlow()
    private val _fileError = MutableStateFlow<Throwable?>(null)
    val fileError: StateFlow<Throwable?> = _fileError.asStateFlow()

    private val _systems = MutableStateFlow<List<GameSystem>>(emptyList())
    val systems: StateFlow<List<GameSystem>> = _systems.asStateFlow()
    private val _games = MutableStateFlow<Load<List<LibraryGame>>?>(null)
    val games: StateFlow<Load<List<LibraryGame>>?> = _games.asStateFlow()

    /** A shared archive copied in, waiting for the console to be chosen. */
    private var pending: PickedFile? = null

    val consoles get() = container.consoles.all()
    val choosesConsole: Boolean get() = fixedDeviceId == null && container.consoles.all().size > 1

    init {
        makeRun()
        loadSystems()
        shareToken?.let { token -> container.shareInbox.get(token)?.firstOrNull()?.let(::pick) }
        // M14: a `job` event finishes the import without waiting for the next poll.
        viewModelScope.launch { container.jobs.jobs.collect { _run.value?.advance() } }
    }

    private fun makeRun() {
        val id = _deviceId.value ?: return
        if (_run.value?.deviceId == id) return
        _run.value = ArchiveImportRun(
            id, viewModelScope, container.jobs, { container.client(id) },
            initialTarget = gameId?.let { ArchiveTarget.Game(it) },
            onLocalCopyDone = { file -> container.backgroundScope.launch(Dispatchers.IO) { file.parentFile?.deleteRecursively() } },
            // The answer of POST /import, its job and the cleanups outlive this screen.
            durableScope = container.scope,
            pairing = { container.pairingIdentity(id) },
            ifPairing = { identity, block -> container.ifPairing(id, identity, block) },
        )
    }

    /** Once an archive was sent to a console, it stays. */
    fun chooseConsole(id: String) {
        if (_run.value?.isBoundToConsole == true || _deviceId.value == id) return
        _deviceId.value = id
        _games.value = null
        makeRun()
        loadSystems()
        pending?.let {
            pending = null
            start(it)
        }
    }

    private fun loadSystems() {
        val id = _deviceId.value ?: return
        viewModelScope.launch {
            val list = runCatching { container.client(id).systems() }.getOrNull() ?: return@launch
            if (_deviceId.value == id) _systems.value = list
        }
    }

    /** The games of the console, for "Choose Another Game…". */
    fun loadGames() {
        val id = _deviceId.value ?: return
        if (_games.value is Load.Loaded || _games.value == Load.Loading) return
        _games.value = Load.Loading
        viewModelScope.launch {
            val result = try {
                Load.Loaded(container.client(id).allGames().games)
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                Load.Failed(e)
            }
            if (_deviceId.value == id) _games.value = result
        }
    }

    fun filteredGames(games: List<LibraryGame>, query: String) = LibrarySearch.filter(games, query)

    /** An archive chosen with the document picker (or shared), copied into the app, then sent. */
    fun pick(uri: Uri) {
        _fileError.value = null
        viewModelScope.launch {
            try {
                val picked = LocalFiles.copyIn(getApplication(), uri, container.transferDirectory)
                if (_deviceId.value == null) {
                    pending?.remove()
                    pending = picked
                    _fileName.value = picked.name
                } else {
                    start(picked)
                }
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                _fileError.value = e
            }
        }
    }

    private fun start(file: PickedFile) {
        val run = _run.value ?: return file.remove()
        _fileName.value = file.name
        run.start(file.file, file.name)
    }

    fun retarget(target: ArchiveTarget?) = _run.value?.retarget(target)

    fun confirm() {
        val plan = plan.value ?: return
        val name = plan.targetName ?: plan.manifest.source.gameName
        _run.value?.confirm(getApplication<Application>().getString(R.string.archive_job_title, name))
    }

    suspend fun tick() = _run.value?.tick()

    /** Imported: the library and the game screens of this console reload. */
    fun imported() {
        _deviceId.value?.let { container.libraryChanged.tryEmit(it) }
    }

    override fun onCleared() {
        // Whatever runs on the app side stops; an archive sent but not imported leaves the console.
        _run.value?.abandon()
        pending?.let { file -> container.backgroundScope.launch(Dispatchers.IO) { file.remove() } }
        shareToken?.let(container.shareInbox::remove)
    }
}

@Composable
fun ArchiveImportScreen(
    deviceId: String?,
    gameId: String?,
    shareToken: String?,
    onBack: () -> Unit,
    onOpenGame: (deviceId: String, gameId: String) -> Unit,
) {
    val app = LocalContext.current.applicationContext as Application
    val vm: ArchiveImportViewModel = viewModel(key = "archive-$deviceId-$gameId-$shareToken") {
        ArchiveImportViewModel(app, deviceId, gameId, shareToken)
    }
    val device by vm.deviceId.collectAsState()
    val step by vm.step.collectAsState()
    val plan by vm.plan.collectAsState()
    val existing by vm.existingGameId.collectAsState()
    val fileName by vm.fileName.collectAsState()
    val fileError by vm.fileError.collectAsState()
    val systems by vm.systems.collectAsState()
    val run by vm.run.collectAsState()
    val context = LocalContext.current
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri -> uri?.let(vm::pick) }
    var confirmReplace by rememberSaveable { mutableStateOf(false) }
    var pickingGame by rememberSaveable { mutableStateOf(false) }
    val done = step is ArchiveStep.Succeeded

    PollWhileVisible(run) { vm.tick() }
    LaunchedEffect(step) { if (step is ArchiveStep.Succeeded) vm.imported() }

    AppScaffold(
        title = stringResource(R.string.archive_import_title),
        onBack = onBack,
        actions = {
            if (step == ArchiveStep.Ready) {
                TextButton(
                    onClick = { if (plan?.replaced?.isNotEmpty() == true) confirmReplace = true else vm.confirm() },
                    enabled = plan?.importsAnything == true,
                    modifier = Modifier.testTag("archiveImportAction"),
                ) { Text(stringResource(R.string.archive_import_action)) }
            }
        },
    ) { modifier ->
        Column(
            modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp).testTag("archiveImportScreen"),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            if (vm.choosesConsole) {
                Text(stringResource(R.string.add_console_choice), style = MaterialTheme.typography.titleSmall)
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    vm.consoles.forEach { console ->
                        FilterChip(
                            selected = device == console.deviceId,
                            onClick = { vm.chooseConsole(console.deviceId) },
                            enabled = run?.isBoundToConsole != true,
                            label = { Text(console.deviceName) },
                        )
                    }
                }
            }
            // The archive
            fileName?.let { ListItem(overlineContent = { Text(stringResource(R.string.archive_section)) }, headlineContent = { Text(it) }, modifier = Modifier.testTag("archiveFile")) }
            fileError?.let { ArchiveErrorCard(errorMessage(context, it)) }
            when (val s = step) {
                ArchiveStep.Preparing -> ArchiveProgress(stringResource(R.string.archive_sending), null, "archiveSending")
                is ArchiveStep.Sending -> ArchiveProgress(stringResource(R.string.archive_sending), s.fraction, "archiveSending")
                ArchiveStep.Inspecting -> ArchiveProgress(stringResource(R.string.archive_inspecting), null, "archiveInspecting")
                ArchiveStep.Starting -> ArchiveProgress(stringResource(R.string.archive_starting), null, "archiveStarting")
                ArchiveStep.Importing -> {
                    ArchiveProgress(stringResource(R.string.archive_importing), null, "archiveImporting")
                    Footnote(stringResource(R.string.archive_can_close))
                }
                is ArchiveStep.Failed -> {
                    ArchiveErrorCard(errorMessage(context, archiveError(s.error)))
                    existing?.let { id ->
                        OutlinedButton(onClick = { vm.retarget(ArchiveTarget.Game(id)) }, modifier = Modifier.fillMaxWidth().testTag("archiveUseExisting")) {
                            Text(stringResource(R.string.archive_use_existing))
                        }
                    }
                }
                is ArchiveStep.Succeeded -> ArchiveResult(s.result, onDone = onBack, onOpenGame = { device?.let { d -> onOpenGame(d, s.result.gameId) } })
                else -> Unit
            }
            if (!step.isBusy && !done) {
                OutlinedButton(
                    onClick = { picker.launch(arrayOf("application/zip", "application/x-zip-compressed", "application/octet-stream")) },
                    enabled = device != null,
                    modifier = Modifier.fillMaxWidth().testTag("archiveChoose"),
                ) { Text(stringResource(if (fileName == null) R.string.archive_choose else R.string.archive_choose_another)) }
                if (fileName == null) Footnote(stringResource(R.string.archive_file_help))
            }
            val shown = plan
            if (shown != null && !done) PlanSections(shown, systems, busy = step.isBusy, onChooseGame = { pickingGame = true }, onNewGame = { vm.retarget(it) })
        }
    }
    if (confirmReplace) {
        val p = plan
        if (p != null) {
            val count = p.replaced.size
            val name = p.targetName.orEmpty()
            val message = when (p.savePromise) {
                ArchivePlan.SavePromise.COPY -> pluralStringResource(R.plurals.archive_replace_save, count, count, name)
                ArchivePlan.SavePromise.COPY_IF_ANY -> pluralStringResource(R.plurals.archive_replace_rtc, count, count, name)
                ArchivePlan.SavePromise.NONE -> pluralStringResource(R.plurals.archive_replace_plain, count, count, name)
            }
            ConfirmDialog(
                title = stringResource(R.string.archive_replace_title),
                message = message,
                confirm = stringResource(R.string.archive_import_action),
                onConfirm = vm::confirm,
                onDismiss = { confirmReplace = false },
            )
        }
    }
    if (pickingGame && device != null) {
        GamePickerDialog(
            title = stringResource(R.string.archive_choose_game),
            games = vm.games.collectAsState().value,
            load = vm::loadGames,
            filter = vm::filteredGames,
            onPicked = { game -> pickingGame = false; vm.retarget(ArchiveTarget.Game(game.id)) },
            onDismiss = { pickingGame = false },
        )
    }
}

@Composable
private fun ArchiveErrorCard(text: String) {
    Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.errorContainer), modifier = Modifier.fillMaxWidth()) {
        Text(text, modifier = Modifier.padding(16.dp).testTag("archiveError"))
    }
}

@Composable
private fun ArchiveProgress(text: String, fraction: Float?, tag: String) {
    Column(verticalArrangement = Arrangement.spacedBy(6.dp), modifier = Modifier.testTag(tag)) {
        Text(text)
        if (fraction != null) LinearProgressIndicator(progress = { fraction }, modifier = Modifier.fillMaxWidth()) else LinearProgressIndicator(Modifier.fillMaxWidth())
    }
}

@Composable
private fun ArchiveResult(result: link.reminis.core.ArchiveImportResult, onDone: () -> Unit, onOpenGame: () -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(8.dp), modifier = Modifier.testTag("archiveResult")) {
        Text(pluralStringResource(R.plurals.archive_imported, result.imported, result.imported), style = MaterialTheme.typography.titleMedium)
        if (result.saveBackupId != null) Footnote(stringResource(R.string.archive_previous_save))
        if (result.trashEntryId != null) Footnote(stringResource(R.string.archive_replaced_trash))
        result.skipped.forEach { skipped ->
            ListItem(
                headlineContent = { Text(archiveRoleTitle(skipped.role, skipped.slot)) },
                supportingContent = { Text(skipText(skipped.reason)) },
                modifier = Modifier.testTag("archiveSkipped"),
            )
        }
        Button(onClick = onOpenGame, modifier = Modifier.fillMaxWidth().testTag("archiveOpenGame")) { Text(stringResource(R.string.archive_open_game)) }
        OutlinedButton(onClick = onDone, modifier = Modifier.fillMaxWidth().testTag("archiveDone")) { Text(stringResource(R.string.archive_done)) }
    }
}

/** What the import will do: the archive, the target (with other choices) and each file. */
@Composable
private fun PlanSections(
    plan: ArchivePlan,
    systems: List<GameSystem>,
    busy: Boolean,
    onChooseGame: () -> Unit,
    onNewGame: (ArchiveTarget) -> Unit,
) {
    val context = LocalContext.current
    HorizontalDivider()
    SectionTitle(stringResource(R.string.archive_section))
    ArchiveInfo(stringResource(R.string.archive_game), plan.manifest.source.gameName)
    ArchiveInfo(stringResource(R.string.archive_system), plan.manifest.source.systemLabel)
    plan.manifest.source.deviceName?.let { ArchiveInfo(stringResource(R.string.archive_from), it) }
    ArchiveInfo(stringResource(R.string.archive_exported_on), formatDate(plan.manifest.createdAt))
    HorizontalDivider()
    SectionTitle(stringResource(R.string.archive_target))
    when (val target = plan.target) {
        is ArchiveTarget.Game -> ArchiveInfo(stringResource(R.string.archive_into), plan.targetName.orEmpty(), "archiveTarget")
        is ArchiveTarget.NewGame -> ArchiveInfo(stringResource(R.string.archive_into), stringResource(R.string.archive_new_game_in, plan.targetName.orEmpty()), "archiveTarget")
        null -> Text(stringResource(R.string.archive_no_target), modifier = Modifier.testTag("archiveNoTarget"))
    }
    if (plan.suggested && plan.target != null) Footnote(stringResource(R.string.archive_suggested))
    if (!busy) {
        OutlinedButton(onClick = onChooseGame, modifier = Modifier.fillMaxWidth().testTag("archiveChooseGame")) {
            Text(stringResource(R.string.archive_choose_game))
        }
        if (plan.manifest.hasRom) {
            var menu by remember { mutableStateOf(false) }
            val tagged = systems.filter { it.tag.equals(plan.manifest.source.systemTag, ignoreCase = true) }.ifEmpty { systems }
            Column {
                OutlinedButton(onClick = { menu = true }, enabled = tagged.isNotEmpty(), modifier = Modifier.fillMaxWidth().testTag("archiveNewGame")) {
                    Text(stringResource(R.string.archive_new_game))
                }
                DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                    tagged.forEach { system ->
                        DropdownMenuItem(
                            text = { Text(system.label) },
                            onClick = { menu = false; onNewGame(ArchiveTarget.NewGame(system.id)) },
                            modifier = Modifier.testTag("archiveNewGameSystem"),
                        )
                    }
                }
            }
        }
    }
    HorizontalDivider()
    SectionTitle(stringResource(R.string.archive_files))
    plan.entries.forEach { entry ->
        ListItem(
            headlineContent = { Text(archiveRoleTitle(entry.role, entry.slot)) },
            supportingContent = {
                Text(
                    when (entry.action) {
                        Actions.CREATE -> stringResource(R.string.archive_added)
                        Actions.REPLACE -> stringResource(R.string.archive_replaces)
                        else -> skipText(entry.reason)
                    },
                )
            },
            trailingContent = { Text(formatBytes(context, entry.sizeBytes), style = MaterialTheme.typography.bodySmall) },
            modifier = Modifier.testTag("archiveEntry-${entry.action}"),
        )
    }
    Footnote(stringResource(R.string.archive_files_footer))
}

@Composable
private fun ArchiveInfo(label: String, value: String, tag: String? = null) {
    ListItem(
        overlineContent = { Text(label) },
        headlineContent = { Text(value) },
        modifier = if (tag != null) Modifier.testTag(tag) else Modifier,
    )
}

/** What a file of an archive is (the role titles of the game screen, plus the state thumbnails). */
@Composable
fun archiveRoleTitle(role: String, slot: Int?): String = when (role) {
    ArchiveRoles.STATE_THUMBNAIL -> slot?.let { stringResource(R.string.archive_role_thumbnail, it + 1) } ?: stringResource(R.string.archive_role_thumbnail_noslot)
    GameFileRoles.ROM -> stringResource(R.string.role_rom)
    GameFileRoles.SAVE -> stringResource(R.string.role_save)
    ArchiveRoles.RTC -> stringResource(R.string.role_rtc)
    GameFileRoles.STATE -> slot?.let { stringResource(R.string.role_state, it + 1) } ?: stringResource(R.string.role_state_noslot)
    GameFileRoles.CHEATS -> stringResource(R.string.role_cheats)
    GameFileRoles.ART -> stringResource(R.string.role_art)
    else -> role
}

/** Why a file of an archive is not imported (an unknown reason: "Skipped"). */
@Composable
private fun skipText(reason: String?): String = when (reason) {
    ArchiveSkipReasons.GAME_EXISTS -> stringResource(R.string.archive_skip_game_exists)
    ArchiveSkipReasons.LOCKED -> stringResource(R.string.archive_skip_locked)
    ArchiveSkipReasons.UNSUPPORTED_FORMAT -> stringResource(R.string.archive_skip_unsupported)
    ArchiveSkipReasons.NO_STATE_FOLDER -> stringResource(R.string.archive_skip_no_state_folder)
    ArchiveSkipReasons.NO_STATE -> stringResource(R.string.archive_skip_no_state)
    ArchiveSkipReasons.NO_TARGET -> stringResource(R.string.archive_skip_no_target)
    else -> stringResource(R.string.archive_skipped)
}

/** The games of a console, with a search (a target of an archive). */
@Composable
fun GamePickerDialog(
    title: String,
    games: Load<List<LibraryGame>>?,
    load: () -> Unit,
    filter: (List<LibraryGame>, String) -> List<LibraryGame>,
    onPicked: (LibraryGame) -> Unit,
    onDismiss: () -> Unit,
) {
    var query by rememberSaveable { mutableStateOf("") }
    val context = LocalContext.current
    LaunchedEffect(Unit) { load() }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(
                    value = query, onValueChange = { query = it }, singleLine = true,
                    placeholder = { Text(stringResource(R.string.search_games)) }, modifier = Modifier.fillMaxWidth().testTag("gamePickerSearch"),
                )
                when (games) {
                    null, Load.Loading -> LinearProgressIndicator(Modifier.fillMaxWidth())
                    is Load.Failed -> Text(errorMessage(context, games.error))
                    is Load.Loaded -> LazyColumn(Modifier.heightIn(max = 360.dp)) {
                        items(filter(games.value, query), key = { it.id }) { game ->
                            ListItem(
                                headlineContent = { Text(game.name) },
                                supportingContent = { Text(game.fileName) },
                                modifier = Modifier.clickable { onPicked(game) }.testTag("gamePickerItem"),
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
