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
    onTrash: () -> Unit = {},
    onScreenshots: () -> Unit = {},
    deviceId: String,
    onBack: () -> Unit,
    onTransfers: () -> Unit,
    /** M23: the storage opens the Files tab. */
    onFiles: () -> Unit = {},
    onForgotten: () -> Unit,
) {
    val app = LocalContext.current.applicationContext as Application
    val vm: DashboardViewModel = viewModel(key = "dashboard-$deviceId") { DashboardViewModel(app, deviceId) }
    val status by vm.status.collectAsState()
    val trash by vm.trash.collectAsState()
    val activeTransfers by vm.activeTransfers.collectAsState()
    val unpairError by vm.unpairError.collectAsState()
    var unpairing by remember { mutableStateOf(false) }
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
                Dashboard(
                    device, modifier, trash, activeTransfers, unpairError,
                    onTransfers, onScreenshots, onTrash, onFiles, onUnpair = { unpairing = true },
                )
            }
        }
    }
    if (unpairing) {
        ConfirmDialog(
            title = stringResource(R.string.unpair_title),
            message = stringResource(R.string.unpair_message),
            confirm = stringResource(R.string.unpair),
            onConfirm = { vm.unpair(onForgotten) },
            onDismiss = { unpairing = false },
        )
    }
}

/**
 * The Console tab (docs/specs/06-app-ios.md §4.3, M23): the rows of
 * Transfers (the uploads and jobs running) and Trash (its entries), the
 * screenshots, the console's state, the storage (which opens the Files
 * tab), the devices connected, and Unpair.
 */
@Composable
private fun Dashboard(
    device: DeviceStatus,
    modifier: Modifier,
    trash: link.reminis.core.TrashList?,
    activeTransfers: Int,
    unpairError: Throwable?,
    onTransfers: () -> Unit,
    onScreenshots: () -> Unit,
    onTrash: () -> Unit,
    onFiles: () -> Unit,
    onUnpair: () -> Unit,
) {
    val context = LocalContext.current
    LazyColumn(modifier.fillMaxSize().testTag("dashboard")) {
        item {
            NavigationRow(stringResource(R.string.transfers_title), activeTransfers.takeIf { it > 0 }, onTransfers, "openTransfers")
            NavigationRow(stringResource(R.string.trash_title), trash?.entries?.size?.takeIf { it > 0 }, onTrash, "openTrash")
            // M22: the screenshots taken on the console.
            NavigationRow(stringResource(R.string.screenshots_title), null, onScreenshots, "openScreenshots")
            HorizontalDivider()
            Info(stringResource(R.string.dashboard_model), modelName(context, device.model))
            Info(stringResource(R.string.dashboard_os), listOfNotNull(device.os, device.osVersion).joinToString(" "))
            Info(stringResource(R.string.dashboard_pak), device.pakVersion)
            Info(stringResource(R.string.dashboard_ip), device.network.ipv4)
            device.network.ssid?.let { Info(stringResource(R.string.dashboard_wifi), it) }
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
            Column(
                Modifier.fillMaxWidth().clickable(onClick = onFiles).padding(16.dp).testTag("storage"),
                verticalArrangement = Arrangement.spacedBy(6.dp),
            ) {
                Text(storage.label)
                LinearProgressIndicator(
                    progress = { if (storage.totalBytes > 0) storage.usedBytes.toFloat() / storage.totalBytes else 0f },
                    modifier = Modifier.fillMaxWidth(),
                )
                Text(
                    stringResource(R.string.storage_free, formatBytes(context, storage.freeBytes), formatBytes(context, storage.totalBytes)),
                    style = MaterialTheme.typography.bodySmall,
                )
                trash?.totalBytes?.takeIf { it > 0 }?.let {
                    Text(stringResource(R.string.dashboard_storage_trash, formatBytes(context, it)), style = MaterialTheme.typography.bodySmall)
                }
            }
        }
        item { Footnote(stringResource(R.string.dashboard_storage_footer)) }
        if (device.connectedClients.isNotEmpty()) {
            item {
                HorizontalDivider()
                Info(stringResource(R.string.connected_devices), device.connectedClients.joinToString(", ") { it.clientName })
            }
        }
        item {
            Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                unpairError?.let { Text(errorMessage(context, it), color = MaterialTheme.colorScheme.error, modifier = Modifier.testTag("unpairError")) }
                OutlinedButton(onClick = onUnpair, modifier = Modifier.fillMaxWidth().testTag("unpair")) {
                    Text(stringResource(R.string.unpair), color = MaterialTheme.colorScheme.error)
                }
            }
        }
    }
}

/** A row that opens a screen, with a count in a badge (M23: running transfers, trash entries). */
@Composable
private fun NavigationRow(title: String, count: Int?, onClick: () -> Unit, tag: String) {
    ListItem(
        headlineContent = { Text(title) },
        trailingContent = {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                if (count != null) {
                    androidx.compose.material3.Badge(modifier = Modifier.testTag("$tag-count")) { Text(count.toString()) }
                }
                Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, contentDescription = null)
            }
        },
        modifier = Modifier.clickable(onClick = onClick).testTag(tag),
    )
}

@Composable
private fun Info(label: String, value: String) {
    ListItem(headlineContent = { Text(value) }, overlineContent = { Text(label) })
}

// MARK: library

/** The game chosen in the Library (saved state of its entry). */
class LibrarySelection(private val handle: androidx.lifecycle.SavedStateHandle) : androidx.lifecycle.ViewModel() {
    val selected: kotlinx.coroutines.flow.StateFlow<String?> = handle.getStateFlow("selected", null)

    fun select(gameId: String?) {
        handle["selected"] = gameId
    }
}

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
    /** Browse by Console (the systems), or a system opened from the systems shown in place of the list. */
    onBrowse: () -> Unit = {},
    onSystem: (link.reminis.core.GameSystem) -> Unit = {},
) {
    // In a ViewModel of the Library's entry (saved state): a rename that ends while another screen
    // or another tab is shown still replaces the game chosen (Codex review 1).
    val selection: LibrarySelection = viewModel()
    val selected by selection.selected.collectAsState()
    androidx.compose.foundation.layout.BoxWithConstraints(Modifier.fillMaxSize().testTag("libraryPanes")) {
        val twoPane = isTwoPane(maxWidth, maxHeight)
        val game = selected
        // Back from the game in a single pane shows the list again.
        androidx.activity.compose.BackHandler(enabled = !twoPane && game != null) { selection.select(null) }
        Row(Modifier.fillMaxSize()) {
            if (twoPane || game == null) {
                key("list") {
                    Box(if (twoPane) Modifier.width(380.dp).fillMaxHeight() else Modifier.fillMaxSize()) {
                        LibraryScreen(
                            deviceId = deviceId,
                            onBack = onBack,
                            onGame = { selection.select(it) },
                            onAdd = onAdd,
                            onImportArchive = { onImportArchive(null) },
                            selectedGameId = if (twoPane) game else null,
                            onBrowse = onBrowse,
                            onSystem = onSystem,
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
                            onBack = if (twoPane) null else ({ selection.select(null) }),
                            onSave = { onSave(game) },
                            onStates = { name -> onStates(game, name) },
                            onCheats = { onCheats(game) },
                            onPatch = { onPatch(game) },
                            onDeleted = { selection.select(null) },
                            onImportArchive = { onImportArchive(game) },
                            // M23: the game under its new identifier.
                            onRenamed = { selection.select(it) },
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
    onBrowse: () -> Unit = {},
    onSystem: (link.reminis.core.GameSystem) -> Unit = {},
) {
    val app = LocalContext.current.applicationContext as Application
    val vm: LibraryViewModel = viewModel(key = "library-$deviceId") { LibraryViewModel(app, deviceId) }
    val content by vm.content.collectAsState()
    val libraryQuery by vm.libraryQuery.collectAsState()
    val query = libraryQuery.text
    val deleted by LocalContext.current.container.lastDeletedGame.collectAsState()
    val working by vm.working.collectAsState()
    val notice by vm.notice.collectAsState()
    val error by vm.error.collectAsState()
    var menu by remember { mutableStateOf(false) }
    var filtering by androidx.compose.runtime.saveable.rememberSaveable { mutableStateOf(false) }
    var emptying by androidx.compose.runtime.saveable.rememberSaveable { mutableStateOf(false) }
    var deleting by androidx.compose.runtime.saveable.rememberSaveable { mutableStateOf<String?>(null) }
    var deletingName by androidx.compose.runtime.saveable.rememberSaveable { mutableStateOf("") }

    AppScaffold(
        title = stringResource(R.string.library),
        onBack = onBack,
        actions = {
            IconButton(onClick = { vm.refresh() }) { Icon(Icons.Filled.Refresh, contentDescription = stringResource(R.string.refresh)) }
            if ((content as? Load.Loaded)?.value?.bySystem == false) {
                IconButton(onClick = { filtering = true }, modifier = Modifier.testTag("libraryFilterButton")) {
                    Icon(
                        AppIcons.FilterList,
                        contentDescription = stringResource(R.string.library_filter),
                        tint = if (libraryQuery.isFiltered) MaterialTheme.colorScheme.primary else androidx.compose.material3.LocalContentColor.current,
                    )
                }
            }
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
                    DropdownMenuItem(
                        text = { Text(stringResource(R.string.library_by_console)) },
                        onClick = { menu = false; onBrowse() },
                        modifier = Modifier.testTag("libraryMenuBySystem"),
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
        Column(modifier.fillMaxSize()) {
            if (working) LinearProgressIndicator(Modifier.fillMaxWidth())
            MessageCard(notice, error, vm::dismissMessages)
            // After a deletion (iOS DeleteGameView): the space is freed only when the trash is emptied.
            deleted?.takeIf { it.deviceId == deviceId }?.let { gone ->
                DeletedGameCard(gone.text, onEmpty = { emptying = true }, onDismiss = { LocalContextHolder.dismissDeleted(app, gone) })
            }
            LoadContent(content, onRetry = { vm.refresh() }) { library ->
                if (library.bySystem) {
                    // More than 20 000 games, or a pak older than M11: browse per system (iOS LibraryMode).
                    SystemsList(deviceId, onSystem, footer = if (library.truncated) stringResource(R.string.library_truncated_browse) else null)
                    return@LoadContent
                }
                val presentation = remember(library, libraryQuery) { vm.present(library, libraryQuery) }
                val games = presentation.games
                Column(Modifier.fillMaxSize()) {
                    OutlinedTextField(
                        value = query,
                        onValueChange = vm::setQuery,
                        leadingIcon = { Icon(Icons.Filled.Search, contentDescription = null) },
                        placeholder = { Text(stringResource(R.string.search_games)) },
                        singleLine = true,
                        modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp).testTag("search"),
                    )
                    if (libraryQuery.isFiltered) {
                        Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp), verticalAlignment = Alignment.CenterVertically) {
                            Text(filterSummary(libraryQuery), style = MaterialTheme.typography.bodySmall, modifier = Modifier.weight(1f).testTag("filterSummary"))
                            TextButton(onClick = { vm.setLibraryQuery(libraryQuery.clearedFilters()) }, modifier = Modifier.testTag("libraryClearFilters")) {
                                Text(stringResource(R.string.library_clear_filters))
                            }
                        }
                    }
                    if (games.isEmpty()) {
                        Text(
                            stringResource(
                                when {
                                    library.games.isEmpty() -> R.string.library_empty
                                    query.isNotBlank() -> R.string.library_no_match
                                    else -> R.string.library_no_match_filters
                                },
                            ),
                            modifier = Modifier.padding(24.dp),
                        )
                    }
                    LazyColumn(Modifier.fillMaxSize().testTag("games"), state = vm.listState) {
                        if (games.isNotEmpty()) {
                            item(key = "count") {
                                Text(
                                    pluralStringResource(R.plurals.library_game_count, games.size, games.size),
                                    style = MaterialTheme.typography.labelMedium,
                                    modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp).testTag("gameCount"),
                                )
                            }
                        }
                        items(games, key = { it.id }) { game ->
                            val context = LocalContext.current
                            SwipeToDelete(onDelete = { deletingName = game.name; deleting = game.id }) {
                                ListItem(
                                    leadingContent = { link.reminis.app.GameArt(deviceId, game.id, game.hasArt, 44.dp) },
                                    headlineContent = { Text(game.name) },
                                    supportingContent = {
                                        Text(gameBadges(game.hasSave, game.stateCount, game.cheatCount, presentation.rowLabels[game.systemId], game.folderPath, formatBytes(context, game.sizeBytes)))
                                    },
                                    colors = if (game.id == selectedGameId) {
                                        androidx.compose.material3.ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.secondaryContainer)
                                    } else {
                                        androidx.compose.material3.ListItemDefaults.colors()
                                    },
                                    modifier = Modifier.clickable { onGame(game.id) }.testTag("game-${game.name}"),
                                )
                            }
                            HorizontalDivider()
                        }
                    }
                }
                if (filtering) {
                    LibraryFilterDialog(
                        libraryQuery, presentation.groups, onChange = vm::setLibraryQuery, onBrowse = onBrowse, onDismiss = { filtering = false },
                    )
                }
            }
        }
    }
    deleting?.let { id -> DeleteGameFromList(deviceId, id, deletingName, onDone = { deleting = null }) }
    if (emptying) {
        ConfirmDialog(
            title = stringResource(R.string.trash_empty_title),
            message = stringResource(R.string.trash_empty_all_message),
            confirm = stringResource(R.string.trash_empty_action),
            onConfirm = { vm.emptyTrash() },
            onDismiss = { emptying = false },
        )
    }
}

/** The filters set, as iOS sums them up above the list. */
@Composable
private fun filterSummary(query: link.reminis.core.LibraryQuery): String = listOfNotNull(
    query.systemLabel,
    if (query.hasSave) stringResource(R.string.library_has_save) else null,
    if (query.hasStates) stringResource(R.string.library_has_states) else null,
    if (query.hasCheats) stringResource(R.string.library_has_cheats) else null,
).joinToString(" · ")

/** "<game> was moved to the trash of the console", with Empty Trash Now (iOS, after a deletion). */
@Composable
private fun DeletedGameCard(text: String, onEmpty: () -> Unit, onDismiss: () -> Unit) {
    androidx.compose.material3.Card(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp).testTag("deletedGame")) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(text)
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedButton(onClick = onEmpty, modifier = Modifier.testTag("emptyTrashNow")) { Text(stringResource(R.string.trash_empty_now)) }
                TextButton(onClick = onDismiss) { Text(stringResource(R.string.sd_done)) }
            }
            Text(stringResource(R.string.trash_empty_now_help), style = MaterialTheme.typography.bodySmall)
        }
    }
}

/** Clears the notice of the last deletion. */
private object LocalContextHolder {
    fun dismissDeleted(app: Application, notice: link.reminis.app.GameNotice) {
        app.container.lastDeletedGame.compareAndSet(notice, null)
    }
}

/**
 * The deletion of a game swiped in a list (iOS: swipe, Delete): the same
 * confirmation as on its screen, with the exact list of what moves.
 */
@Composable
fun DeleteGameFromList(deviceId: String, gameId: String, name: String, onDone: () -> Unit) {
    val app = LocalContext.current.applicationContext as Application
    val vm: GameViewModel = viewModel(key = "game-$deviceId-$gameId") { GameViewModel(app, deviceId, gameId) }
    androidx.compose.runtime.LaunchedEffect(gameId) { if (vm.deletion.value == null) vm.previewDeletion(keepSave = false) }
    DeleteGameDialog(vm, name, onDeleted = {}, onDismiss = { vm.closeDeletion(); onDone() })
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
    /** M23: renamed, the game has a new identifier (the screens above it close). */
    onRenamed: (String) -> Unit = {},
) {
    val app = LocalContext.current.applicationContext as Application
    val vm: GameViewModel = viewModel(key = "game-$deviceId-$gameId") { GameViewModel(app, deviceId, gameId) }
    var renaming by androidx.compose.runtime.saveable.rememberSaveable { mutableStateOf(false) }
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
                        text = { Text(stringResource(R.string.game_rename)) },
                        onClick = { menu = false; renaming = true },
                        modifier = Modifier.testTag("renameGame"),
                    )
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
                deviceId, detail, modifier, vm.listState, working, notice, error, vm::dismissMessages,
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
    if (renaming && loaded != null) {
        RenameGameDialog(loaded, onRename = { name -> renaming = false; vm.rename(name, onRenamed) }, onDismiss = { renaming = false })
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

/**
 * Renames a game (M23): the name without its extension, which is kept and
 * said, with what follows the game; checked with the rules of the pak
 * before anything is sent.
 */
@Composable
private fun RenameGameDialog(game: GameDetail, onRename: (String) -> Unit, onDismiss: () -> Unit) {
    var name by androidx.compose.runtime.saveable.rememberSaveable { mutableStateOf(link.reminis.core.GameNameRule.editableName(game.fileName, game.isFolder)) }
    val extension = link.reminis.core.GameNameRule.keptExtension(game.fileName, game.isFolder)
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.game_rename_title)) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(
                    value = name,
                    onValueChange = { name = it },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth().testTag("renameName"),
                )
                val help = stringResource(R.string.game_rename_help)
                Text(
                    if (extension.isEmpty()) help else stringResource(R.string.game_rename_extension, extension) + " " + help,
                    style = MaterialTheme.typography.bodySmall,
                )
            }
        },
        confirmButton = {
            TextButton(onClick = { onRename(name) }, modifier = Modifier.testTag("renameConfirm")) { Text(stringResource(R.string.game_rename_action)) }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_cancel)) } },
    )
}

@Composable
private fun GameDetailView(
    deviceId: String,
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
            // The art the console shows in its game list.
            val art = game.files.firstOrNull { it.role == GameFileRoles.ART }
            if (art != null) {
                key(art.modifiedAt, art.sizeBytes) {
                    link.reminis.app.GameArt(deviceId, game.id, true, 120.dp, Modifier.padding(start = 16.dp, top = 8.dp))
                }
            }
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
                headlineContent = {
                    // M23: the number of cheats; Yes or No from an older pak.
                    val count = game.cheatCount
                    Text(
                        if (count != null) pluralStringResource(R.plurals.cheat_count, count, count)
                        else stringResource(if (game.cheatFileCount > 0) R.string.game_cheats_yes else R.string.game_cheats_no),
                        modifier = Modifier.testTag("cheatCount"),
                    )
                },
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
