package link.reminis.app.ui

import android.app.Application
import android.content.Context
import android.net.Uri
import android.text.format.DateUtils
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material3.Button
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import java.io.File
import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import java.util.UUID
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import link.reminis.app.R
import link.reminis.app.ScreenshotManager
import link.reminis.app.SyncPhase
import link.reminis.app.VaultPlace
import link.reminis.app.container
import link.reminis.core.ApiClient
import link.reminis.core.UploadTarget
import link.reminis.core.vault.SaveSyncReport
import link.reminis.core.vault.SaveVault
import link.reminis.core.vault.SaveVersion
import link.reminis.core.vault.StateVersion
import link.reminis.core.vault.VaultConsole
import link.reminis.core.vault.VaultGame
import link.reminis.core.vault.VaultLocation
import link.reminis.core.vault.VaultUnavailableException

// Increment 7 (M9, M19 of iOS, docs/specs/10-app-android.md §6 septies):
// the Save Sync section of the dashboard, the synced saves of the consoles
// and the versions of one game, with restore and share, and the settings of
// the sync (when, and where the copies go).

/** Opens the Android folder picker (`ACTION_OPEN_DOCUMENT_TREE`) and takes the folder chosen for the synced copies. */
@Composable
fun rememberVaultFolderPicker(onError: (Throwable) -> Unit = {}): () -> Unit {
    val container = LocalContext.current.container
    val scope = rememberCoroutineScope()
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocumentTree()) { tree: Uri? ->
        if (tree != null) {
            scope.launch {
                try {
                    container.sync.chooseFolder(tree)
                } catch (e: kotlinx.coroutines.CancellationException) {
                    throw e
                } catch (e: Exception) {
                    onError(UserMessage(R.string.sync_folder_refused, cause = e))
                }
            }
        }
    }
    return remember(launcher) { { launcher.launch(container.sync.initialFolderForTests) } }
}

/** Where the synced copies are, said plainly (the privacy text names the place). */
@Composable
fun VaultLocationNotice(place: VaultPlace, modifier: Modifier = Modifier) {
    val text = when (place.location) {
        VaultLocation.INTERNAL -> stringResource(R.string.sync_location_internal)
        VaultLocation.FOLDER -> stringResource(R.string.sync_location_folder, place.name ?: "?")
        VaultLocation.DEMO -> stringResource(R.string.sync_location_demo)
    }
    Footnote(text, modifier.testTag("syncLocation-${place.location.name.lowercase()}"))
}

/** The folder chosen can no longer be reached: say it, and offer another folder or the app's storage. Nothing is lost. */
@Composable
fun VaultUnavailableCard(onChoose: () -> Unit) {
    val container = LocalContext.current.container
    Column(Modifier.padding(16.dp).testTag("syncUnavailable"), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Text(stringResource(R.string.sync_error_unavailable), color = MaterialTheme.colorScheme.error)
        OutlinedButton(onClick = onChoose, modifier = Modifier.fillMaxWidth().testTag("syncChooseOther")) {
            Text(stringResource(R.string.sync_choose_other_folder))
        }
        OutlinedButton(onClick = { container.sync.useInternalStorage() }, modifier = Modifier.fillMaxWidth().testTag("syncUseInternal")) {
            Text(stringResource(R.string.sync_use_internal))
        }
    }
}

/** The Save Sync section of a console's dashboard (iOS `SaveSyncSection`). */
@Composable
fun SyncSection(deviceId: String, onSyncedSaves: () -> Unit) {
    val context = LocalContext.current
    val container = context.container
    val states by container.sync.states.collectAsState()
    val place by container.sync.place.collectAsState()
    val state = states[deviceId] ?: link.reminis.app.SyncState()
    var pickerError by remember { mutableStateOf<Throwable?>(null) }
    val choose = rememberVaultFolderPicker { pickerError = it }
    Column(Modifier.fillMaxWidth().testTag("syncSection")) {
        SectionTitle(stringResource(R.string.sync_section))
        when (val phase = state.phase) {
            is SyncPhase.Running -> Column(Modifier.padding(horizontal = 16.dp).testTag("syncProgress"), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(stringResource(R.string.sync_running), modifier = Modifier.weight(1f))
                    TextButton(onClick = { container.sync.cancel(deviceId) }, modifier = Modifier.testTag("syncCancel")) {
                        Text(stringResource(R.string.action_cancel))
                    }
                }
                LinearProgressIndicator(progress = { phase.progress.fraction }, modifier = Modifier.fillMaxWidth())
                if (phase.progress.total > 0) {
                    Text(stringResource(R.string.sync_saves_checked, phase.progress.checked, phase.progress.total), style = MaterialTheme.typography.bodySmall)
                }
                if (phase.progress.statesTotal > 0) {
                    Text(stringResource(R.string.sync_states_checked, phase.progress.statesChecked, phase.progress.statesTotal), style = MaterialTheme.typography.bodySmall)
                }
            }
            else -> Button(
                onClick = { container.sync.start(deviceId) },
                modifier = Modifier.padding(horizontal = 16.dp).fillMaxWidth().testTag("syncNow"),
            ) { Text(stringResource(R.string.sync_now)) }
        }
        val failure = (state.phase as? SyncPhase.Failed)?.error
        when {
            failure is VaultUnavailableException -> VaultUnavailableCard(choose)
            failure != null -> Text(
                errorMessage(context, failure),
                color = MaterialTheme.colorScheme.error,
                modifier = Modifier.padding(16.dp).testTag("syncError"),
            )
        }
        pickerError?.let { Text(errorMessage(context, it), color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(16.dp)) }
        val report = state.lastReport
        if (report != null && !state.isRunning) SyncReportView(report)
        ListItem(
            headlineContent = { Text(stringResource(R.string.synced_title)) },
            trailingContent = { Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, contentDescription = null) },
            modifier = Modifier.clickable(onClick = onSyncedSaves).testTag("openSyncedSaves"),
        )
        VaultLocationNotice(place)
    }
}

/** The outcome of the last sync. */
@Composable
fun SyncReportView(report: SaveSyncReport) {
    val context = LocalContext.current
    var showFailures by rememberSaveable { mutableStateOf(false) }
    Column(Modifier.padding(horizontal = 16.dp, vertical = 8.dp).testTag("syncReport"), verticalArrangement = Arrangement.spacedBy(2.dp)) {
        Text(
            if (report.stored.isEmpty()) {
                stringResource(R.string.sync_up_to_date, report.total)
            } else {
                stringResource(R.string.sync_new_versions, report.stored.size, report.total)
            },
        )
        val small = MaterialTheme.typography.bodySmall
        Text(
            stringResource(R.string.sync_last, DateUtils.getRelativeTimeSpanString(report.finishedAtMillis, System.currentTimeMillis(), DateUtils.MINUTE_IN_MILLIS).toString()),
            style = small,
        )
        if (report.truncated) Text(stringResource(R.string.sync_truncated), style = small)
        report.states?.let { states ->
            if (states.stored.isEmpty() && states.listingError == null) {
                Text(stringResource(R.string.sync_states_up_to_date, states.total), style = small)
            } else if (states.stored.isNotEmpty()) {
                Text(stringResource(R.string.sync_states_new, states.stored.size, states.total), style = small)
            }
            if (states.listingError != null) Text(stringResource(R.string.sync_states_incomplete), style = small)
            if (states.truncated) Text(stringResource(R.string.sync_states_truncated), style = small)
        }
        val failures = report.failures + report.states?.failures.orEmpty()
        if (failures.isNotEmpty()) {
            TextButton(onClick = { showFailures = !showFailures }, modifier = Modifier.testTag("syncFailures")) {
                Text(pluralStringResource(R.plurals.sync_failures, failures.size, failures.size), color = MaterialTheme.colorScheme.error)
            }
            if (showFailures) {
                failures.forEach { failure ->
                    Text(
                        failure.slot?.let { stringResource(R.string.sync_failure_state, failure.gameName, it) } ?: failure.gameName,
                        style = MaterialTheme.typography.bodyMedium,
                    )
                    Text(errorMessage(context, failure.error), style = small)
                }
            }
        }
    }
}

// MARK: the synced copies

/** The consoles with synced copies (Settings), or the games of one console. */
class SyncedListViewModel(app: Application, private val deviceId: String?) : AndroidViewModel(app) {
    private val container = app.container
    private val _consoles = MutableStateFlow<Load<List<VaultConsole>>>(Load.Loading)
    val consoles: StateFlow<Load<List<VaultConsole>>> = _consoles.asStateFlow()
    private val _games = MutableStateFlow<Load<List<VaultGame>>>(Load.Loading)
    val games: StateFlow<Load<List<VaultGame>>> = _games.asStateFlow()
    private var generation = 0

    init {
        load()
        // A sync that ends while the screen is open shows its versions; a folder changed in Settings is read.
        viewModelScope.launch {
            container.sync.states.map { states -> states.values.count { it.isRunning } }.distinctUntilChanged().collect { if (it == 0) load(quiet = true) }
        }
        viewModelScope.launch { container.sync.place.collect { load(quiet = true) } }
    }

    fun load(quiet: Boolean = false) {
        val current = ++generation
        viewModelScope.launch {
            val vault = container.sync.vault(deviceId)
            if (deviceId == null) {
                if (!quiet) _consoles.value = Load.Loading
                val result = runCatching { withContext(Dispatchers.IO) { vault.consoles() } }
                if (current == generation) _consoles.value = result.fold({ Load.Loaded(it) }, { Load.Failed(it) })
            } else {
                if (!quiet) _games.value = Load.Loading
                val result = runCatching { withContext(Dispatchers.IO) { vault.games(deviceId) } }
                if (current == generation) _games.value = result.fold({ Load.Loaded(it) }, { Load.Failed(it) })
            }
        }
    }
}

@Composable
private fun VaultLoadFailure(error: Throwable, onRetry: () -> Unit, modifier: Modifier) {
    if (error is VaultUnavailableException) {
        val choose = rememberVaultFolderPicker()
        Column(modifier) { VaultUnavailableCard(choose) }
    } else {
        ErrorView(error, onRetry, modifier)
    }
}

/** Settings > Saves on This Device: the consoles with synced copies. */
@Composable
fun SyncedConsolesScreen(onBack: () -> Unit, onOpen: (VaultConsole) -> Unit) {
    val app = LocalContext.current.applicationContext as Application
    val container = LocalContext.current.container
    val vm: SyncedListViewModel = viewModel(key = "synced-consoles") { SyncedListViewModel(app, null) }
    val consoles by vm.consoles.collectAsState()
    val place by container.sync.place.collectAsState()
    AppScaffold(title = stringResource(R.string.synced_title), onBack = onBack) { modifier ->
        when (val load = consoles) {
            Load.Loading -> LoadingView(modifier)
            is Load.Failed -> VaultLoadFailure(load.error, { vm.load() }, modifier)
            is Load.Loaded -> LazyColumn(modifier.fillMaxSize().testTag("syncedConsoles")) {
                if (load.value.isEmpty()) item { EmptyNotice(stringResource(R.string.synced_empty_title), stringResource(R.string.synced_empty_all)) }
                items(load.value, key = { it.id }) { console ->
                    ListItem(
                        headlineContent = { Text(container.consoles.get(console.id)?.deviceName ?: container.consoleName(console.id).ifEmpty { console.info.name }) },
                        trailingContent = { Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, contentDescription = null) },
                        modifier = Modifier.clickable { onOpen(console) }.testTag("syncedConsole-${console.folderName}"),
                    )
                }
                item { VaultLocationNotice(place) }
            }
        }
    }
}

@Composable
private fun EmptyNotice(title: String, text: String) {
    Column(Modifier.fillMaxWidth().padding(24.dp).testTag("syncedEmpty"), verticalArrangement = Arrangement.spacedBy(8.dp), horizontalAlignment = Alignment.CenterHorizontally) {
        Text(title, style = MaterialTheme.typography.titleMedium)
        Text(text, style = MaterialTheme.typography.bodyMedium)
    }
}

/** The synced copies of one console, by system (iOS `SyncedSavesView`). */
@Composable
fun SyncedSavesScreen(deviceId: String, title: String, onBack: () -> Unit, onOpenGame: (VaultGame) -> Unit) {
    val app = LocalContext.current.applicationContext as Application
    val container = LocalContext.current.container
    val vm: SyncedListViewModel = viewModel(key = "synced-$deviceId") { SyncedListViewModel(app, deviceId) }
    val games by vm.games.collectAsState()
    val place by container.sync.place.collectAsState()
    val name = container.consoles.get(deviceId)?.deviceName ?: container.consoleName(deviceId).ifEmpty { title }
    AppScaffold(title = name.ifEmpty { stringResource(R.string.synced_title) }, onBack = onBack) { modifier ->
        when (val load = games) {
            Load.Loading -> LoadingView(modifier)
            is Load.Failed -> VaultLoadFailure(load.error, { vm.load() }, modifier)
            is Load.Loaded -> LazyColumn(modifier.fillMaxSize().testTag("syncedGames")) {
                if (load.value.isEmpty()) item { EmptyNotice(stringResource(R.string.synced_empty_title), stringResource(R.string.synced_empty_console)) }
                load.value.groupBy { it.info.systemName }.toSortedMap(link.reminis.core.NaturalOrder).forEach { (system, systemGames) ->
                    item(key = "system-$system") { SectionTitle(system) }
                    items(systemGames, key = { it.folderPath }) { game ->
                        ListItem(
                            headlineContent = { Text(game.title) },
                            supportingContent = {
                                Column {
                                    game.latest?.let {
                                        Text(stringResource(R.string.synced_versions, game.versionCount, formatSyncDate(it.info.syncedAt, seconds = false)))
                                    }
                                    if (game.stateSlots.isNotEmpty()) Text(stringResource(R.string.synced_states, game.stateSlots.size))
                                }
                            },
                            trailingContent = { Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, contentDescription = null) },
                            modifier = Modifier.clickable { onOpenGame(game) }.testTag("syncedGame-${game.title}"),
                        )
                    }
                }
                item { VaultLocationNotice(place) }
            }
        }
    }
}

/** The versions of one synced game, and of its states; the vault they were listed from is kept for restore and share. */
class SyncedGameViewModel(app: Application, deviceId: String, private val gameId: String) : ActionViewModel(app, deviceId) {
    private val _versions = MutableStateFlow<Load<Pair<List<SaveVersion>, List<StateVersion>>>>(Load.Loading)
    val versions: StateFlow<Load<Pair<List<SaveVersion>, List<StateVersion>>>> = _versions.asStateFlow()
    private var vault: SaveVault? = null

    init {
        load()
    }

    fun load() {
        viewModelScope.launch {
            val vault = container.sync.vault(deviceId)
            val result = runCatching {
                withContext(Dispatchers.IO) { vault.versions(deviceId, gameId) to vault.stateVersions(deviceId, gameId) }
            }
            if (result.isSuccess) this@SyncedGameViewModel.vault = vault
            _versions.value = result.fold({ Load.Loaded(it) }, { Load.Failed(it) })
        }
    }

    /**
     * Restores a version of the save to the console (iOS M9): read back
     * from the vault that listed it and checked against its SHA-256 (else
     * nothing is sent), then `PUT /games/{gameId}/save` (the transfer queue
     * above 64 MiB); the console first keeps a copy of its save. Only the
     * save is sent: the console keeps its clock file.
     */
    fun restore(version: SaveVersion, gameName: String) = perform(null) {
        val vault = vault ?: return@perform
        val exported = withContext(Dispatchers.IO) { vault.export(deviceId, gameId, version, container.transferDirectory) }
        val save = exported.main
        withContext(Dispatchers.IO) { exported.files.drop(1).forEach { it.delete() } }
        if (save.length() > ApiClient.MAX_SAVE_BODY_BYTES) {
            container.transfers.enqueueUpload(deviceId, save, version.info.save.name, UploadTarget.Save(gameId), gameName, ownsFile = true)
            showNotice(Notice(R.string.synced_restore_queued))
            return@perform
        }
        try {
            container.client(deviceId).replaceSave(gameId, save)
            showNotice(Notice(R.string.synced_restored))
            container.libraryChanged.tryEmit(deviceId)
        } finally {
            withContext(Dispatchers.IO) { exported.directory.deleteRecursively() }
        }
    }

    /**
     * Restores a state version (iOS M19): checked copy, then sent by the
     * transfer queue (`target.kind = "state"`, followed in Transfers); the
     * console first moves its current state to its trash, a locked slot
     * refuses. Only the state is sent (no route writes a thumbnail).
     */
    fun restoreState(version: StateVersion, gameName: String) = perform(null) {
        val vault = vault ?: return@perform
        val exported = withContext(Dispatchers.IO) { vault.exportState(deviceId, gameId, version, container.transferDirectory) }
        withContext(Dispatchers.IO) { exported.files.drop(1).forEach { it.delete() } }
        container.transfers.enqueueUpload(
            deviceId, exported.main, version.info.state.name, UploadTarget.State(gameId, version.info.slot), gameName, ownsFile = true,
        )
        showNotice(Notice(R.string.synced_state_queued))
    }

    /** Copies a version (save and clock file, or state and thumbnail), checked, into the share area. */
    fun share(version: Any, onReady: (List<File>) -> Unit) = perform(null) {
        val vault = vault ?: return@perform
        val app = getApplication<Application>()
        val files = withContext(Dispatchers.IO) {
            ScreenshotManager.purgeShared(app)
            val area = File(ScreenshotManager.sharedDirectory(app), UUID.randomUUID().toString()).apply { mkdirs() }
            try {
                val exported = when (version) {
                    is SaveVersion -> vault.export(deviceId, gameId, version, area)
                    is StateVersion -> vault.exportState(deviceId, gameId, version, area)
                    else -> error("not a version")
                }
                File(area, ScreenshotManager.READY_MARKER).createNewFile()
                exported.files
            } catch (e: Exception) {
                area.deleteRecursively()
                throw e
            }
        }
        onReady(files)
    }
}

/** A sync date (RFC 3339) in the phone's zone; with seconds for a version (two syncs a minute apart stay distinct). */
fun formatSyncDate(text: String, seconds: Boolean = true): String = try {
    OffsetDateTime.parse(text).atZoneSameInstant(ZoneId.systemDefault())
        .format(DateTimeFormatter.ofLocalizedDateTime(FormatStyle.MEDIUM, if (seconds) FormatStyle.MEDIUM else FormatStyle.SHORT))
} catch (_: Exception) {
    text
}

/** The versions of one synced game: restore one to the console, or share it (iOS `SyncedGameView`). */
@Composable
fun SyncedGameScreen(deviceId: String, gameId: String, title: String, systemName: String, onBack: () -> Unit) {
    val context = LocalContext.current
    val app = context.applicationContext as Application
    val container = context.container
    val vm: SyncedGameViewModel = viewModel(key = "synced-$deviceId-$gameId") { SyncedGameViewModel(app, deviceId, gameId) }
    val load by vm.versions.collectAsState()
    val working by vm.working.collectAsState()
    val notice by vm.notice.collectAsState()
    val error by vm.error.collectAsState()
    var selected by remember { mutableStateOf<SaveVersion?>(null) }
    var selectedState by remember { mutableStateOf<StateVersion?>(null) }
    var toRestore by remember { mutableStateOf<SaveVersion?>(null) }
    var stateToRestore by remember { mutableStateOf<StateVersion?>(null) }
    var demoNote by remember { mutableStateOf(false) }
    val share: (Any) -> Unit = { version ->
        if (container.isDemo(deviceId)) demoNote = true else vm.share(version) { files -> shareFiles(context, files, "application/octet-stream", R.string.synced_share_chooser) }
    }

    AppScaffold(title = title, onBack = onBack) { modifier ->
        when (val current = load) {
            Load.Loading -> LoadingView(modifier)
            is Load.Failed -> VaultLoadFailure(current.error, vm::load, modifier)
            // The notice and the progress stay above the list, wherever it is scrolled.
            is Load.Loaded -> Column(modifier.fillMaxSize()) {
            MessageCard(notice, error, vm::dismissMessages)
            if (working) LinearProgressIndicator(Modifier.fillMaxWidth().padding(16.dp))
            LazyColumn(Modifier.fillMaxSize().testTag("syncedVersions")) {
                val (versions, states) = current.value
                if (versions.isNotEmpty()) {
                    item { SectionTitle(systemName) }
                    versions.forEachIndexed { index, version ->
                        item(key = "v-${version.id}") {
                            VersionRow(
                                formatSyncDate(version.info.syncedAt), index == 0,
                                "${version.info.save.name} · ${formatBytes(context, version.info.save.sizeBytes)}",
                                if (version.info.rtc != null) stringResource(R.string.synced_with_clock) else null,
                                "syncedVersion$index",
                            ) { selected = version }
                        }
                    }
                    item { Footnote(stringResource(R.string.synced_limit)) }
                }
                states.groupBy { it.info.slot }.toSortedMap().forEach { (slot, slotVersions) ->
                    item(key = "slot-$slot") {
                        HorizontalDivider()
                        SectionTitle(stringResource(R.string.synced_state_slot, slot))
                    }
                    slotVersions.forEachIndexed { index, version ->
                        item(key = "s-${version.id}") {
                            VersionRow(
                                formatSyncDate(version.info.syncedAt), index == 0,
                                "${version.info.state.name} · ${formatBytes(context, version.info.state.sizeBytes)}", null,
                                "syncedState$slot-$index",
                            ) { selectedState = version }
                        }
                    }
                }
            }
            }
        }
    }
    selected?.let { version ->
        ActionsDialog(
            formatSyncDate(version.info.syncedAt), null,
            listOf(
                stringResource(R.string.synced_restore) to { toRestore = version },
                stringResource(R.string.synced_share) to { share(version) },
            ),
        ) { selected = null }
    }
    selectedState?.let { version ->
        ActionsDialog(
            formatSyncDate(version.info.syncedAt), null,
            listOf(
                stringResource(R.string.synced_restore) to { stateToRestore = version },
                stringResource(R.string.synced_share) to { share(version) },
            ),
        ) { selectedState = null }
    }
    toRestore?.let { version ->
        ConfirmDialog(
            stringResource(R.string.synced_restore_title),
            stringResource(R.string.synced_restore_message, title, formatSyncDate(version.info.syncedAt)),
            stringResource(R.string.action_restore),
            onConfirm = { vm.restore(version, title) },
            onDismiss = { toRestore = null },
        )
    }
    stateToRestore?.let { version ->
        ConfirmDialog(
            stringResource(R.string.synced_restore_state_title),
            stringResource(R.string.synced_restore_state_message, version.info.slot, title, formatSyncDate(version.info.syncedAt)),
            stringResource(R.string.action_restore),
            onConfirm = { vm.restoreState(version, title) },
            onDismiss = { stateToRestore = null },
        )
    }
    if (demoNote) {
        androidx.compose.material3.AlertDialog(
            onDismissRequest = { demoNote = false },
            text = { Text(stringResource(R.string.synced_share_demo)) },
            confirmButton = { TextButton(onClick = { demoNote = false }, modifier = Modifier.testTag("demoNoteOk")) { Text(stringResource(android.R.string.ok)) } },
        )
    }
}

@Composable
private fun VersionRow(date: String, latest: Boolean, detail: String, extra: String?, tag: String, onClick: () -> Unit) {
    ListItem(
        headlineContent = {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(date)
                if (latest) androidx.compose.material3.AssistChip(onClick = onClick, label = { Text(stringResource(R.string.synced_latest)) })
            }
        },
        supportingContent = {
            Column {
                Text(detail)
                extra?.let { Text(it) }
            }
        },
        modifier = Modifier.clickable(onClick = onClick).testTag(tag),
    )
}

// MARK: settings

/** The Save Sync section of the settings (iOS §4.9): when, where, and what leaves the device. */
@Composable
fun SyncSettings(onSyncedSaves: () -> Unit) {
    val context = LocalContext.current
    val container = context.container
    val onConnect by container.sync.syncOnConnect.collectAsState()
    val place by container.sync.place.collectAsState()
    var pickerError by remember { mutableStateOf<Throwable?>(null) }
    val choose = rememberVaultFolderPicker { pickerError = it }
    Column(Modifier.fillMaxWidth().testTag("syncSettings")) {
        SectionTitle(stringResource(R.string.sync_section))
        ListItem(
            headlineContent = { Text(stringResource(R.string.sync_on_connect)) },
            trailingContent = {
                Switch(checked = onConnect, onCheckedChange = { container.sync.setSyncOnConnect(it) }, modifier = Modifier.testTag("syncOnConnect"))
            },
        )
        ListItem(
            headlineContent = { Text(stringResource(R.string.synced_title)) },
            trailingContent = { Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, contentDescription = null) },
            modifier = Modifier.clickable(onClick = onSyncedSaves).testTag("settingsSyncedSaves"),
        )
        ListItem(
            overlineContent = { Text(stringResource(R.string.sync_folder_label)) },
            headlineContent = {
                Text(
                    when (place.location) {
                        VaultLocation.INTERNAL -> stringResource(R.string.sync_place_internal)
                        VaultLocation.FOLDER -> place.name ?: stringResource(R.string.sync_place_folder)
                        VaultLocation.DEMO -> stringResource(R.string.sync_place_demo)
                    },
                    modifier = Modifier.testTag("syncPlace"),
                )
            },
        )
        if (place.location != VaultLocation.DEMO) {
            Row(Modifier.padding(horizontal = 16.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedButton(onClick = choose, modifier = Modifier.testTag("syncChooseFolder")) { Text(stringResource(R.string.sync_choose_folder)) }
                if (place.location == VaultLocation.FOLDER) {
                    OutlinedButton(onClick = { container.sync.useInternalStorage() }, modifier = Modifier.testTag("syncUseInternalSettings")) {
                        Text(stringResource(R.string.sync_use_internal))
                    }
                }
            }
        }
        pickerError?.let { Text(errorMessage(context, it), color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(16.dp).testTag("syncFolderError")) }
        Footnote(
            stringResource(if (place.location == VaultLocation.DEMO) R.string.sync_privacy_demo else R.string.sync_privacy),
            Modifier.testTag("syncPrivacy"),
        )
        VaultLocationNotice(place)
        if (place.location != VaultLocation.DEMO) Footnote(stringResource(R.string.sync_folder_changed_note))
    }
}

/** A context's text for a sync failure (used by [errorMessage]). */
internal fun syncErrorMessage(context: Context, error: Throwable): String? = when (error) {
    is link.reminis.core.vault.SyncChecksumException -> context.getString(R.string.sync_error_checksum)
    is VaultUnavailableException -> context.getString(R.string.sync_error_unavailable)
    is link.reminis.core.vault.SaveVaultException.VersionMismatch -> context.getString(R.string.sync_error_version_changed)
    is link.reminis.core.vault.SaveVaultException -> context.getString(R.string.sync_error_vault)
    else -> null
}
