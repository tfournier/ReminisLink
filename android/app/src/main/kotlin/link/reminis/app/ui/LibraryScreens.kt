package link.reminis.app.ui

import android.app.Application
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
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
    val title = (status as? Load.Loaded)?.value?.deviceName ?: vm.console?.deviceName.orEmpty()

    AppScaffold(
        title = title,
        onBack = onBack,
        actions = {
            IconButton(onClick = { vm.refresh(reconnect = true) }) {
                Icon(Icons.Filled.Refresh, contentDescription = stringResource(R.string.refresh))
            }
        },
    ) { modifier ->
        LoadContent(status, onRetry = { vm.refresh(reconnect = true) }, modifier = modifier) { device ->
            Dashboard(device, modifier, onLibrary, onTransfers, onForget = { forgetting = true })
        }
    }
    if (forgetting) {
        ForgetDialog(title, onConfirm = { vm.forget(); forgetting = false; onForgotten() }, onDismiss = { forgetting = false })
    }
}

@Composable
private fun Dashboard(device: DeviceStatus, modifier: Modifier, onLibrary: () -> Unit, onTransfers: () -> Unit, onForget: () -> Unit) {
    val context = LocalContext.current
    LazyColumn(modifier.fillMaxSize().testTag("dashboard")) {
        item {
            Info(stringResource(R.string.dashboard_model), DeviceModels.displayName(device.model))
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

@Composable
fun LibraryScreen(deviceId: String, onBack: () -> Unit, onGame: (String) -> Unit, onAdd: () -> Unit) {
    val app = LocalContext.current.applicationContext as Application
    val vm: LibraryViewModel = viewModel(key = "library-$deviceId") { LibraryViewModel(app, deviceId) }
    val content by vm.content.collectAsState()
    val query by vm.query.collectAsState()

    AppScaffold(
        title = stringResource(R.string.library),
        onBack = onBack,
        actions = {
            IconButton(onClick = vm::refresh) { Icon(Icons.Filled.Refresh, contentDescription = stringResource(R.string.refresh)) }
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
                LazyColumn(Modifier.fillMaxSize().testTag("games")) {
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
fun GameScreen(deviceId: String, gameId: String, onBack: () -> Unit, onSave: (String) -> Unit, onStates: (String) -> Unit) {
    val app = LocalContext.current.applicationContext as Application
    val vm: GameViewModel = viewModel(key = "game-$deviceId-$gameId") { GameViewModel(app, deviceId, gameId) }
    val game by vm.game.collectAsState()
    val working by vm.working.collectAsState()
    val notice by vm.notice.collectAsState()
    val error by vm.error.collectAsState()
    var menu by remember { mutableStateOf(false) }
    var removingArt by remember { mutableStateOf(false) }
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
                }
            }
        },
    ) { modifier ->
        LoadContent(game, onRetry = { vm.refresh() }, modifier = modifier) { detail ->
            GameDetailView(
                detail, modifier, working, notice, error, vm::dismissMessages,
                onSave = { onSave(detail.name) }, onStates = { onStates(detail.name) },
                onDownload = { file, uri -> vm.download(file, detail.name, uri) },
            )
        }
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
    working: Boolean,
    notice: Notice?,
    error: Throwable?,
    onDismiss: () -> Unit,
    onSave: () -> Unit,
    onStates: () -> Unit,
    onDownload: (GameFile, android.net.Uri) -> Unit,
) {
    val context = LocalContext.current
    val creator = rememberDocumentCreator()
    LazyColumn(modifier.fillMaxSize().testTag("game")) {
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
            Info(stringResource(R.string.game_cheats), game.cheatFileCount.toString())
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
