package link.reminis.app.ui

import android.app.Application
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.key
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.material3.AlertDialog
import androidx.compose.ui.Alignment
import link.reminis.app.container
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.Button
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.TextButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import link.reminis.app.R
import link.reminis.core.DeviceModels
import link.reminis.core.DeviceStatus
import link.reminis.core.GameDetail
import link.reminis.core.GameFile
import link.reminis.core.GameFileRoles

// MARK: dashboard

@Composable
fun DashboardScreen(
    onApps: () -> Unit = {},
    onTrash: () -> Unit = {},
    onScreenshots: () -> Unit = {},
    deviceId: String,
    onBack: () -> Unit,
    onLibrary: () -> Unit,
    onTransfers: () -> Unit,
    onForgotten: () -> Unit,
) {
    val app = LocalContext.current.applicationContext as Application
    val vm: DashboardViewModel = viewModel(key = "dashboard-$deviceId") { DashboardViewModel(app, deviceId) }
    val status by vm.status.collectAsState()
    var forgetting by remember { mutableStateOf(false) }
    val title = (status as? Load.Loaded)?.value?.deviceName ?: vm.consoleName

    AppScaffold(
        title = title,
        onBack = onBack,
        actions = {
            IconButton(onClick = vm::retry) {
                Icon(Icons.Filled.Refresh, contentDescription = stringResource(R.string.refresh))
            }
        },
    ) { modifier ->
        val failure = (status as? Load.Failed)?.error
        if ((failure as? link.reminis.core.ApiException.Server)?.code == link.reminis.core.ErrorCodes.DISCONNECTED) {
            // M10: only the user lifts a disconnection decided on the console (`POST /clients/me/connect`).
            ErrorView(
                failure, onRetry = vm::reconnectToConsole, modifier = modifier,
                message = stringResource(R.string.dashboard_disconnected), retryLabel = R.string.reconnect,
            )
        } else {
            LoadContent(status, onRetry = vm::retry, modifier = modifier) { device ->
                Dashboard(device, modifier, onLibrary, onTransfers, onApps, onScreenshots, onTrash, onForget = { forgetting = true })
            }
        }
    }
    if (forgetting) {
        ForgetDialog(title, onConfirm = { vm.forget(); forgetting = false; onForgotten() }, onDismiss = { forgetting = false })
    }
}

@Composable
private fun Dashboard(
    device: DeviceStatus,
    modifier: Modifier,
    onLibrary: () -> Unit,
    onTransfers: () -> Unit,
    onApps: () -> Unit,
    onScreenshots: () -> Unit,
    onTrash: () -> Unit,
    onForget: () -> Unit,
) {
    val context = LocalContext.current
    LazyColumn(modifier.fillMaxSize().testTag("dashboard")) {
        item {
            Info(stringResource(R.string.dashboard_model), modelName(context, device.model))
            Info(stringResource(R.string.dashboard_os), listOfNotNull(device.os, device.osVersion).joinToString(" "))
            Info(stringResource(R.string.dashboard_pak), device.pakVersion)
            Info(stringResource(R.string.dashboard_ip), device.network.ipv4)
            Info(
                stringResource(R.string.battery),
                device.battery?.let {
                    stringResource(if (it.charging) R.string.battery_charging else R.string.battery_percent, it.percent)
                } ?: stringResource(R.string.battery_unknown),
            )
            HorizontalDivider()
            Text(
                stringResource(R.string.storage),
                style = MaterialTheme.typography.titleMedium,
                modifier = Modifier.padding(start = 16.dp, top = 16.dp),
            )
        }
        items(device.storage, key = { it.id }) { storage ->
            Column(Modifier.fillMaxWidth().padding(16.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                Text(storage.label)
                LinearProgressIndicator(
                    progress = { if (storage.totalBytes > 0) storage.usedBytes.toFloat() / storage.totalBytes else 0f },
                    modifier = Modifier.fillMaxWidth(),
                )
                Text(
                    stringResource(R.string.storage_free, formatBytes(context, storage.freeBytes), formatBytes(context, storage.totalBytes)),
                    style = MaterialTheme.typography.bodySmall,
                )
            }
        }
        if (device.connectedClients.isNotEmpty()) {
            item {
                HorizontalDivider()
                Info(stringResource(R.string.connected_devices), device.connectedClients.joinToString(", ") { it.clientName })
            }
        }
        item {
            Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Button(onClick = onLibrary, modifier = Modifier.fillMaxWidth().testTag("openLibrary")) {
                    Text(stringResource(R.string.library))
                }
                OutlinedButton(onClick = onTransfers, modifier = Modifier.fillMaxWidth().testTag("openTransfers")) {
                    Text(stringResource(R.string.transfers_title))
                }
                OutlinedButton(onClick = onApps, modifier = Modifier.fillMaxWidth().testTag("openApps")) {
                    Text(stringResource(R.string.paks_title))
                }
                // M22: the screenshots taken on the console.
                OutlinedButton(onClick = onScreenshots, modifier = Modifier.fillMaxWidth().testTag("openScreenshots")) {
                    Text(stringResource(R.string.screenshots_title))
                }
                OutlinedButton(onClick = onTrash, modifier = Modifier.fillMaxWidth().testTag("openTrash")) {
                    Text(stringResource(R.string.trash_title))
                }
                OutlinedButton(onClick = onForget, modifier = Modifier.fillMaxWidth()) { Text(stringResource(R.string.forget)) }
            }
        }
    }
}

@Composable
private fun Info(label: String, value: String) {
    ListItem(headlineContent = { Text(value) }, overlineContent = { Text(label) })
}

// MARK: library

/** A window wide enough for the list and a game side by side (and tall enough: not a phone in landscape). */
fun isTwoPane(width: androidx.compose.ui.unit.Dp, height: androidx.compose.ui.unit.Dp) = width >= 720.dp && height >= 480.dp

/**
 * The Library of a console (M15 on Android, like the iOS split view): in a
 * wide window, the list and the game chosen side by side; otherwise one at
 * a time (back from the game shows the list). The game chosen is saved
 * state: a rotation, a window resize or a fold keeps it, and moves it from
 * one layout to the other (its screen state lives in its ViewModel).
 */
@Composable
fun LibraryPanes(
    deviceId: String,
    onBack: () -> Unit,
    onAdd: () -> Unit,
    onImportArchive: (gameId: String?) -> Unit,
    onSave: (gameId: String) -> Unit,
    onStates: (gameId: String, name: String) -> Unit,
    onCheats: (gameId: String) -> Unit,
    onPatch: (gameId: String) -> Unit,
) {
    var selected by androidx.compose.runtime.saveable.rememberSaveable { mutableStateOf<String?>(null) }
    androidx.compose.foundation.layout.BoxWithConstraints(Modifier.fillMaxSize().testTag("libraryPanes")) {
        val twoPane = isTwoPane(maxWidth, maxHeight)
        val game = selected
        // Back from the game in a single pane shows the list again.
        androidx.activity.compose.BackHandler(enabled = !twoPane && game != null) { selected = null }
        Row(Modifier.fillMaxSize()) {
            if (twoPane || game == null) {
                key("list") {
                    Box(if (twoPane) Modifier.width(380.dp).fillMaxHeight() else Modifier.fillMaxSize()) {
                        LibraryScreen(
                            deviceId = deviceId,
                            onBack = onBack,
                            onGame = { selected = it },
                            onAdd = onAdd,
                            onImportArchive = { onImportArchive(null) },
                            selectedGameId = if (twoPane) game else null,
                        )
                    }
                }
            }
            if (twoPane) androidx.compose.material3.VerticalDivider()
            if (game != null) {
                key("game") {
                    Box(Modifier.weight(1f).fillMaxHeight().testTag("gamePane")) {
                        GameScreen(
                            deviceId = deviceId,
                            gameId = game,
                            onBack = if (twoPane) null else ({ selected = null }),
                            onSave = { onSave(game) },
                            onStates = { name -> onStates(game, name) },
                            onCheats = { onCheats(game) },
                            onPatch = { onPatch(game) },
                            onDeleted = { selected = null },
                            onImportArchive = { onImportArchive(game) },
                        )
                    }
                }
            } else if (twoPane) {
                Box(Modifier.weight(1f).fillMaxHeight().testTag("gamePanePlaceholder"), contentAlignment = Alignment.Center) {
                    Column(horizontalAlignment = Alignment.CenterHorizontally, modifier = Modifier.padding(24.dp)) {
                        Text(stringResource(R.string.library_select_game), style = MaterialTheme.typography.titleMedium)
                        Text(stringResource(R.string.library_select_game_help), style = MaterialTheme.typography.bodyMedium)
                    }
                }
            }
        }
    }
}

@Composable
fun LibraryScreen(
    deviceId: String,
    onBack: () -> Unit,
    onGame: (String) -> Unit,
    onAdd: () -> Unit,
    onImportArchive: () -> Unit = {},
    /** The game shown beside the list (a wide window): highlighted. */
    selectedGameId: String? = null,
) {
    val app = LocalContext.current.applicationContext as Application
    val vm: LibraryViewModel = viewModel(key = "library-$deviceId") { LibraryViewModel(app, deviceId) }
    val content by vm.content.collectAsState()
    val query by vm.query.collectAsState()
    var menu by remember { mutableStateOf(false) }

    AppScaffold(
        title = stringResource(R.string.library),
        onBack = onBack,
        actions = {
            IconButton(onClick = vm::refresh) { Icon(Icons.Filled.Refresh, contentDescription = stringResource(R.string.refresh)) }
            Box {
                IconButton(onClick = { menu = true }, modifier = Modifier.testTag("libraryMenu")) {
                    Icon(Icons.Filled.MoreVert, contentDescription = stringResource(R.string.more_actions))
                }
                DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                    // M19: a game archive, whose target the console suggests.
                    DropdownMenuItem(
                        text = { Text(stringResource(R.string.library_import_archive)) },
                        onClick = { menu = false; onImportArchive() },
                        modifier = Modifier.testTag("libraryImportArchive"),
                    )
                }
            }
        },
        floatingActionButton = {
            ExtendedFloatingActionButton(
                onClick = onAdd,
                icon = { Icon(Icons.Filled.Add, contentDescription = null) },
                text = { Text(stringResource(R.string.add_files)) },
                modifier = Modifier.testTag("addFilesButton"),
            )
        },
    ) { modifier ->
        LoadContent(content, onRetry = vm::refresh, modifier = modifier) { library ->
            val games = remember(library, query) { vm.filtered(library, query) }
            Column(modifier.fillMaxSize()) {
                OutlinedTextField(
                    value = query,
                    onValueChange = vm::setQuery,
                    leadingIcon = { Icon(Icons.Filled.Search, contentDescription = null) },
                    placeholder = { Text(stringResource(R.string.search_games)) },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp).testTag("search"),
                )
                if (library.truncated) {
                    Text(
                        stringResource(R.string.library_truncated),
                        style = MaterialTheme.typography.bodySmall,
                        modifier = Modifier.padding(horizontal = 16.dp),
                    )
                }
                if (games.isEmpty()) {
                    Text(
                        stringResource(if (library.games.isEmpty()) R.string.library_empty else R.string.library_no_match),
                        modifier = Modifier.padding(24.dp),
                    )
                }
                LazyColumn(Modifier.fillMaxSize().testTag("games"), state = vm.listState) {
                    items(games, key = { it.id }) { game ->
                        val badges = buildList {
                            library.systems[game.systemId]?.let(::add)
                            game.folderPath?.let(::add)
                            if (game.hasSave) add(stringResource(R.string.has_save))
                            if (game.stateCount > 0) add(pluralStringResource(R.plurals.state_count, game.stateCount, game.stateCount))
                            if (game.cheatCount > 0) add(pluralStringResource(R.plurals.cheat_count, game.cheatCount, game.cheatCount))
                        }
                        ListItem(
                            headlineContent = { Text(game.name) },
                            supportingContent = { Text(badges.joinToString(" · ")) },
                            colors = if (game.id == selectedGameId) {
                                androidx.compose.material3.ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.secondaryContainer)
                            } else {
                                androidx.compose.material3.ListItemDefaults.colors()
                            },
                            modifier = Modifier.clickable { onGame(game.id) },
                        )
                        HorizontalDivider()
                    }
                }
            }
        }
    }
}

// MARK: game

@Composable
fun GameScreen(
    deviceId: String,
    gameId: String,
    /** Null in the detail pane of a wide window (the list stays beside it). */
    onBack: (() -> Unit)?,
    onSave: (String) -> Unit,
    onStates: (String) -> Unit,
    onCheats: () -> Unit = {},
    onPatch: () -> Unit = {},
    onDeleted: () -> Unit = {},
    onImportArchive: () -> Unit = {},
) {
    val app = LocalContext.current.applicationContext as Application
    val vm: GameViewModel = viewModel(key = "game-$deviceId-$gameId") { GameViewModel(app, deviceId, gameId) }
    val game by vm.game.collectAsState()
    val working by vm.working.collectAsState()
    val notice by vm.notice.collectAsState()
    val error by vm.error.collectAsState()
    val archiveExport by vm.archiveExport.collectAsState()
    var menu by remember { mutableStateOf(false) }
    var removingArt by androidx.compose.runtime.saveable.rememberSaveable { mutableStateOf(false) }
    var deleting by androidx.compose.runtime.saveable.rememberSaveable { mutableStateOf(false) }
    val gameNotice by LocalContext.current.container.gameNotice.collectAsState()
    androidx.compose.runtime.LaunchedEffect(gameNotice) { vm.takeNotice(gameNotice) }
    val picture = rememberLauncherForActivityResult(ActivityResultContracts.GetContent()) { uri -> uri?.let(vm::setArt) }
    val loaded = (game as? Load.Loaded)?.value
    AppScaffold(
        title = loaded?.name.orEmpty(),
        onBack = onBack,
        actions = {
            if (loaded != null) {
                IconButton(onClick = { menu = true }, modifier = Modifier.testTag("gameMenu")) {
                    Icon(Icons.Filled.MoreVert, contentDescription = stringResource(R.string.more_actions))
                }
                DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                    DropdownMenuItem(
                        text = { Text(stringResource(R.string.art_change)) },
                        onClick = { menu = false; picture.launch("image/*") },
                    )
                    if (loaded.files.any { it.role == GameFileRoles.ART }) {
                        DropdownMenuItem(
                            text = { Text(stringResource(R.string.art_remove)) },
                            onClick = { menu = false; removingArt = true },
                        )
                    }
                    DropdownMenuItem(
                        text = { Text(stringResource(R.string.game_delete)) },
                        onClick = { menu = false; deleting = true; vm.previewDeletion(keepSave = false) },
                        modifier = Modifier.testTag("deleteGame"),
                    )
                }
            }
        },
    ) { modifier ->
        LoadContent(game, onRetry = { vm.refresh() }, modifier = modifier) { detail ->
            GameDetailView(
                detail, modifier, vm.listState, working, notice, error, vm::dismissMessages,
                onSave = { onSave(detail.name) }, onStates = { onStates(detail.name) }, onCheats = onCheats, onPatch = onPatch,
                onDownload = { file, uri -> vm.download(file, detail.name, uri) },
                archive = ArchiveActions(
                    export = archiveExport,
                    onExport = { rom, uri -> vm.exportArchive(rom, detail.name, uri) },
                    onCancel = vm::cancelArchiveExport,
                    onDismiss = vm::dismissArchiveExport,
                    onImport = onImportArchive,
                ),
            )
        }
    }
    if (deleting) {
        // Restored after the process died: the preview is asked again.
        androidx.compose.runtime.LaunchedEffect(Unit) { if (vm.deletion.value == null) vm.previewDeletion(keepSave = false) }
        DeleteGameDialog(vm, (game as? Load.Loaded)?.value?.name.orEmpty(), onDeleted = onDeleted, onDismiss = { deleting = false; vm.closeDeletion() })
    }
    if (removingArt) {
        ConfirmDialog(
            title = stringResource(R.string.art_remove_title),
            message = stringResource(R.string.art_remove_message),
            confirm = stringResource(R.string.art_remove),
            onConfirm = vm::removeArt,
            onDismiss = { removingArt = false },
        )
    }
}

@Composable
private fun GameDetailView(
    game: GameDetail,
    modifier: Modifier,
    listState: androidx.compose.foundation.lazy.LazyListState,
    working: Boolean,
    notice: Notice?,
    error: Throwable?,
    onDismiss: () -> Unit,
    onSave: () -> Unit,
    onStates: () -> Unit,
    onCheats: () -> Unit,
    onPatch: () -> Unit,
    onDownload: (GameFile, android.net.Uri) -> Unit,
    archive: ArchiveActions,
) {
    val context = LocalContext.current
    val creator = rememberDocumentCreator()
    var askExport by androidx.compose.runtime.saveable.rememberSaveable { mutableStateOf(false) }
    LazyColumn(modifier.fillMaxSize().testTag("game"), state = listState) {
        item {
            if (working) LinearProgressIndicator(Modifier.fillMaxWidth())
            MessageCard(notice, error, onDismiss)
            Info(stringResource(R.string.game_file), game.fileName)
            Info(stringResource(R.string.game_size), formatBytes(context, game.totalBytes))
            ListItem(
                headlineContent = {
                    Text(stringResource(if (game.hasSave) R.string.game_save_yes else R.string.game_save_no))
                },
                overlineContent = { Text(stringResource(R.string.game_save)) },
                trailingContent = { Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, contentDescription = null) },
                modifier = Modifier.clickable(onClick = onSave).testTag("openSave"),
            )
            ListItem(
                headlineContent = { Text(pluralStringResource(R.plurals.state_count, game.stateCount, game.stateCount)) },
                overlineContent = { Text(stringResource(R.string.game_states)) },
                trailingContent = { Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, contentDescription = null) },
                modifier = Modifier.clickable(onClick = onStates).testTag("openStates"),
            )
            ListItem(
                headlineContent = { Text(stringResource(if (game.cheatFileCount > 0) R.string.game_cheats_file else R.string.game_cheats_none)) },
                overlineContent = { Text(stringResource(R.string.cheats_title)) },
                trailingContent = { Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, contentDescription = null) },
                modifier = Modifier.clickable(onClick = onCheats).testTag("openCheats"),
            )
            ListItem(
                headlineContent = { Text(stringResource(R.string.patch_apply_ellipsis)) },
                supportingContent = { Text(stringResource(R.string.patch_row_help)) },
                trailingContent = { Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, contentDescription = null) },
                modifier = Modifier.clickable(onClick = onPatch).testTag("openPatch"),
            )
            HorizontalDivider()
            ArchiveSection(archive, onAskExport = { askExport = true })
            HorizontalDivider()
            Text(
                stringResource(R.string.game_files),
                style = MaterialTheme.typography.titleMedium,
                modifier = Modifier.padding(start = 16.dp, top = 16.dp),
            )
        }
        items(game.files, key = { it.fileId ?: it.path }) { file ->
            ListItem(
                headlineContent = { Text(file.name) },
                overlineContent = { Text(roleLabel(file)) },
                supportingContent = { Text(formatBytes(context, file.sizeBytes)) },
                trailingContent = {
                    // A pak older than M11 sends no fileId: no download.
                    if (file.fileId != null) {
                        TextButton(onClick = { creator.create(file.name) { onDownload(file, it) } }) {
                            Text(stringResource(R.string.action_download))
                        }
                    }
                },
            )
        }
    }
    if (askExport) {
        ExportArchiveDialog(
            game,
            onChoose = { rom -> creator.create(link.reminis.core.GameArchiveManifest.exportName(game.name)) { archive.onExport(rom, it) } },
            onDismiss = { askExport = false },
        )
    }
}

/** M19: what the Archive section of a game shows and does. */
class ArchiveActions(
    /** The export started from this screen, as the transfer queue shows it. */
    val export: link.reminis.core.TransferItem?,
    val onExport: (rom: Boolean, android.net.Uri) -> Unit,
    val onCancel: () -> Unit,
    val onDismiss: () -> Unit,
    val onImport: () -> Unit,
)

/**
 * The Archive section of a game (M19, docs/specs/06-app-ios.md §4.5):
 * export the game's data as a zip into a document the user creates, or
 * import one into this game.
 */
@Composable
private fun ArchiveSection(archive: ArchiveActions, onAskExport: () -> Unit) {
    val context = LocalContext.current
    Text(
        stringResource(R.string.archive_section),
        style = MaterialTheme.typography.titleMedium,
        modifier = Modifier.padding(start = 16.dp, top = 16.dp),
    )
    val export = archive.export
    if (export != null && !export.phase.isFinished && export.phase != link.reminis.core.TransferPhase.FAILED) {
        ListItem(
            headlineContent = { Text(stringResource(R.string.archive_preparing, formatBytes(context, export.doneBytes))) },
            supportingContent = { LinearProgressIndicator(Modifier.fillMaxWidth().padding(top = 4.dp)) },
            trailingContent = {
                if (export.canCancel) {
                    TextButton(onClick = archive.onCancel, modifier = Modifier.testTag("archiveCancelExport")) {
                        Text(stringResource(R.string.action_cancel))
                    }
                }
            },
            modifier = Modifier.testTag("archiveExporting"),
        )
    } else {
        ListItem(
            headlineContent = { Text(stringResource(R.string.archive_export)) },
            supportingContent = when {
                export?.phase == link.reminis.core.TransferPhase.DONE -> {
                    { Text(stringResource(R.string.archive_exported, export.fileName), modifier = Modifier.testTag("archiveExported")) }
                }
                export?.failure != null -> {
                    { Text(errorMessage(context, archiveError(export.failure!!, routeMissing = true)), color = MaterialTheme.colorScheme.error, modifier = Modifier.testTag("archiveExportError")) }
                }
                else -> null
            },
            modifier = Modifier.clickable { archive.onDismiss(); onAskExport() }.testTag("archiveExport"),
        )
    }
    ListItem(
        headlineContent = { Text(stringResource(R.string.archive_import)) },
        modifier = Modifier.clickable(onClick = archive.onImport).testTag("archiveImport"),
    )
    Footnote(stringResource(R.string.archive_footer))
}

/** "Export the data of <game>": without the game file, or with it (single-file games only). */
@Composable
private fun ExportArchiveDialog(game: GameDetail, onChoose: (rom: Boolean) -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.archive_export_title, game.name)) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text(stringResource(R.string.archive_export_message), style = MaterialTheme.typography.bodyMedium)
                TextButton(onClick = { onDismiss(); onChoose(false) }, modifier = Modifier.fillMaxWidth().testTag("archiveExportData")) {
                    Text(stringResource(R.string.archive_without_rom))
                }
                if (game.kind == "file") {
                    TextButton(onClick = { onDismiss(); onChoose(true) }, modifier = Modifier.fillMaxWidth().testTag("archiveExportRom")) {
                        Text(stringResource(R.string.archive_with_rom))
                    }
                }
            }
        },
        confirmButton = {},
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_cancel)) } },
    )
}

@Composable
private fun roleLabel(file: GameFile): String = when (file.role) {
    GameFileRoles.ROM -> stringResource(R.string.role_rom)
    GameFileRoles.SAVE -> stringResource(R.string.role_save)
    GameFileRoles.RTC -> stringResource(R.string.role_rtc)
    GameFileRoles.STATE -> file.slot?.let { stringResource(R.string.role_state, it + 1) } ?: stringResource(R.string.role_state_noslot)
    GameFileRoles.CHEATS -> stringResource(R.string.role_cheats)
    GameFileRoles.ART -> stringResource(R.string.role_art)
    else -> file.role
}

/**
 * Deletion of a game (docs/specs/06-app-ios.md §4.5): what moves to the
 * trash of the console, read from the console, and the option to keep the
 * save and the states; everything stays in the trash at least 30 days.
 */
@Composable
private fun DeleteGameDialog(vm: GameViewModel, name: String, onDeleted: () -> Unit, onDismiss: () -> Unit) {
    val deletion by vm.deletion.collectAsState()
    val context = LocalContext.current
    val keepSave = deletion?.keepSave ?: false
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.game_delete_title, name)) },
        text = {
            Column(
                verticalArrangement = Arrangement.spacedBy(8.dp),
                modifier = Modifier.heightIn(max = 520.dp).verticalScroll(rememberScrollState()).testTag("deleteGameDialog"),
            ) {
                when (val d = deletion?.preview) {
                    null, Load.Loading -> LinearProgressIndicator(Modifier.fillMaxWidth())
                    is Load.Failed -> Text(errorMessage(context, d.error), color = MaterialTheme.colorScheme.error)
                    is Load.Loaded -> {
                        // The exact list, with the paths relative to the SD card (docs/specs/04-api-v1.md).
                        Text(
                            pluralStringResource(R.plurals.game_delete_moved, d.value.moved.size, d.value.moved.size, formatBytes(context, d.value.movedBytes)),
                        )
                        d.value.moved.forEach {
                            Text(it.name, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace, modifier = Modifier.testTag("deleteMoved"))
                        }
                        if (d.value.kept.isNotEmpty()) {
                            Text(stringResource(R.string.game_delete_kept))
                            d.value.kept.forEach {
                                Text(it.name, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace, modifier = Modifier.testTag("deleteKept"))
                            }
                        }
                    }
                }
                Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.fillMaxWidth()) {
                    Text(stringResource(R.string.game_delete_keep_save), modifier = Modifier.weight(1f))
                    androidx.compose.material3.Switch(
                        checked = keepSave,
                        onCheckedChange = { vm.previewDeletion(it) },
                        modifier = Modifier.testTag("deleteKeepSave"),
                    )
                }
                Text(stringResource(R.string.game_delete_footer), style = MaterialTheme.typography.bodySmall)
            }
        },
        confirmButton = {
            TextButton(
                onClick = { vm.delete { onDeleted() }; onDismiss() },
                enabled = deletion?.preview is Load.Loaded,
                modifier = Modifier.testTag("deleteGameConfirm"),
            ) { Text(stringResource(R.string.trash_move_to)) }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_cancel)) } },
    )
}
