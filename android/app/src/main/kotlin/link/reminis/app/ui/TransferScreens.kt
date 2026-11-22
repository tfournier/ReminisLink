package link.reminis.app.ui

import android.Manifest
import android.app.Application
import android.content.Context
import android.content.pm.PackageManager
import android.os.Build
import android.text.format.DateUtils
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
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
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
import androidx.lifecycle.viewmodel.compose.viewModel
import link.reminis.app.R
import link.reminis.app.container
import link.reminis.core.AddKind
import link.reminis.core.LibraryGame
import link.reminis.core.OnConflict
import link.reminis.core.TransferDirection
import link.reminis.core.TransferItem
import link.reminis.core.TrackedJob
import link.reminis.core.JobKinds
import link.reminis.core.JobStates
import link.reminis.core.PakJobFailure
import link.reminis.core.PatchJobFailure
import kotlinx.coroutines.launch
import link.reminis.core.TransferPhase

// MARK: transfers

/**
 * The uploads and downloads of a console (docs/specs/06-app-ios.md §4.7):
 * progress, speed, time left, cancel, resume, and a new attempt with
 * another rule after a name conflict.
 */
@Composable
fun TransfersScreen(deviceId: String, onBack: () -> Unit, onAdd: () -> Unit) {
    val container = LocalContext.current.container
    val queue = container.transfers
    val all by queue.items.collectAsState()
    val items = all.filter { it.deviceId == deviceId && !it.isHidden }
    val allJobs by container.jobs.jobs.collectAsState()
    val jobs = allJobs.filter { it.deviceId == deviceId && !it.hidden }
    var confirmCancel by remember { mutableStateOf<TransferItem?>(null) }
    var cancelJob by remember { mutableStateOf<TrackedJob?>(null) }
    val scope = androidx.compose.runtime.rememberCoroutineScope()
    var jobError by remember { mutableStateOf<Throwable?>(null) }
    // The jobs of the console (patches, pak installations), every second while visible.
    // Not while the console's event stream is open: its `job` events keep them current (M14).
    PollWhileVisible(deviceId) { if (!container.events.isLive(deviceId)) container.jobs.refresh(deviceId) }

    AppScaffold(
        title = stringResource(R.string.transfers_title),
        onBack = onBack,
        actions = {
            val clearable = items.any {
                it.phase.isFinished || (it.phase == TransferPhase.FAILED && it.direction == TransferDirection.DOWNLOAD)
            } || jobs.any { it.isFinished }
            if (clearable) {
                TextButton(
                    onClick = { queue.clearFinished(deviceId); container.jobs.clearFinished(deviceId) },
                    modifier = Modifier.testTag("clearFinished"),
                ) {
                    Text(stringResource(R.string.transfers_clear))
                }
            }
        },
        floatingActionButton = {
            ExtendedFloatingActionButton(
                onClick = onAdd,
                icon = { Icon(Icons.Filled.Add, contentDescription = null) },
                text = { Text(stringResource(R.string.add_files)) },
            )
        },
    ) { modifier ->
        if (items.isEmpty() && jobs.isEmpty()) {
            Column(modifier.fillMaxSize().padding(24.dp), verticalArrangement = Arrangement.Center) {
                Text(stringResource(R.string.transfers_empty), style = MaterialTheme.typography.bodyLarge)
            }
        } else {
            LazyColumn(modifier.fillMaxSize().testTag("transfers")) {
                jobError?.let { e -> item { MessageCard(null, e) { jobError = null } } }
                items(jobs.reversed(), key = { "job-" + it.jobId }) { job ->
                    JobRow(job, onCancel = { cancelJob = job })
                    HorizontalDivider()
                }
                items(items.reversed(), key = { it.id }) { item ->
                    TransferRow(
                        item,
                        onCancel = { confirmCancel = item },
                        onResume = { queue.resume(item.id) },
                        onRetry = { rule -> queue.retry(item.id, rule) },
                    )
                    HorizontalDivider()
                }
            }
        }
    }
    cancelJob?.let { job ->
        ConfirmDialog(
            title = stringResource(R.string.job_cancel_title, job.title),
            message = stringResource(R.string.job_cancel_message),
            confirm = stringResource(R.string.transfer_cancel),
            onConfirm = {
                scope.launch {
                    jobError = try {
                        container.jobs.cancel(deviceId, job.jobId)
                        null
                    } catch (e: kotlinx.coroutines.CancellationException) {
                        throw e
                    } catch (e: Exception) {
                        e
                    }
                }
            },
            onDismiss = { cancelJob = null },
        )
    }
    confirmCancel?.let { item ->
        ConfirmDialog(
            title = stringResource(R.string.transfer_cancel_title, item.fileName),
            message = stringResource(
                if (item.direction == TransferDirection.UPLOAD) R.string.transfer_cancel_upload else R.string.transfer_cancel_download,
            ),
            confirm = stringResource(R.string.transfer_cancel),
            onConfirm = { queue.cancel(item.id) },
            onDismiss = { confirmCancel = null },
        )
    }
}

@Composable
private fun TransferRow(item: TransferItem, onCancel: () -> Unit, onResume: () -> Unit, onRetry: (OnConflict) -> Unit) {
    val context = LocalContext.current
    Column(Modifier.fillMaxWidth().padding(16.dp).testTag("transfer"), verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text(item.fileName, style = MaterialTheme.typography.titleMedium)
                Text(
                    stringResource(
                        if (item.direction == TransferDirection.UPLOAD) R.string.transfer_to else R.string.transfer_from,
                        item.destination,
                    ),
                    style = MaterialTheme.typography.bodySmall,
                )
            }
            if (item.canCancel) {
                IconButton(onClick = onCancel, modifier = Modifier.testTag("cancelTransfer")) {
                    Icon(Icons.Filled.Close, contentDescription = stringResource(R.string.transfer_cancel))
                }
            }
        }
        if (item.phase == TransferPhase.RUNNING || item.phase == TransferPhase.HASHING) {
            val fraction = item.fraction
            if (fraction != null) {
                LinearProgressIndicator(progress = { fraction }, modifier = Modifier.fillMaxWidth())
            } else {
                LinearProgressIndicator(Modifier.fillMaxWidth())
            }
        }
        Text(status(context, item), style = MaterialTheme.typography.bodyMedium, modifier = Modifier.testTag("transferStatus"))
        item.failure?.let { failure ->
            // M23: a file of the Files tab says a protected folder or a taken name as such.
            val shown = if (item.target is link.reminis.core.UploadTarget.SdFile) sdWriteError(failure) else failure
            Text(errorMessage(context, shown), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
        }
        when {
            // No Keep Both: another name does not lift the protection.
            item.isProtectedFolder && item.phase == TransferPhase.FAILED ->
                Text(stringResource(R.string.transfer_sd_protected), style = MaterialTheme.typography.bodySmall, modifier = Modifier.testTag("transferProtected"))
            // M23: a file of the SD browser is never replaced.
            item.isNameConflict && item.target is link.reminis.core.UploadTarget.SdFile -> Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                OutlinedButton(onClick = { onRetry(OnConflict.RENAME) }, modifier = Modifier.testTag("keepBoth")) {
                    Text(stringResource(R.string.transfer_keep_both))
                }
                Text(stringResource(R.string.transfer_sd_conflict), style = MaterialTheme.typography.bodySmall)
            }
            item.isNameConflict -> Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedButton(onClick = { onRetry(OnConflict.RENAME) }) { Text(stringResource(R.string.transfer_keep_both)) }
                OutlinedButton(onClick = { onRetry(OnConflict.OVERWRITE) }) { Text(stringResource(R.string.transfer_replace)) }
            }
            item.canResume ->
                OutlinedButton(onClick = onResume, modifier = Modifier.testTag("resumeTransfer")) {
                    Text(stringResource(if (item.phase == TransferPhase.PAUSED) R.string.transfer_resume else R.string.action_retry))
                }
            else -> Unit
        }
    }
}

private fun status(context: Context, item: TransferItem): String {
    val size = item.sizeBytes
    fun bytes(n: Long) = formatBytes(context, n)
    return when (item.phase) {
        TransferPhase.HASHING -> context.getString(R.string.transfer_hashing)
        TransferPhase.WAITING -> context.getString(R.string.transfer_waiting)
        TransferPhase.RUNNING -> {
            if (item.isCompleting) return context.getString(R.string.transfer_completing)
            val progress = if (size != null) {
                context.getString(R.string.transfer_progress, bytes(item.doneBytes), bytes(size))
            } else {
                bytes(item.doneBytes)
            }
            val speed = item.bytesPerSecond?.let { context.getString(R.string.transfer_speed, bytes(it.toLong())) }
            val left = item.secondsRemaining?.let {
                context.getString(R.string.transfer_remaining, DateUtils.formatElapsedTime(it))
            }
            listOfNotNull(progress, speed, left).joinToString(" · ")
        }
        TransferPhase.PAUSED -> context.getString(R.string.transfer_paused)
        TransferPhase.FAILED -> context.getString(R.string.transfer_failed)
        TransferPhase.CANCELLING -> context.getString(R.string.transfer_cancelling)
        TransferPhase.DONE -> context.getString(
            if (item.direction == TransferDirection.UPLOAD) R.string.transfer_sent else R.string.transfer_downloaded,
        )
        TransferPhase.CANCELLED -> context.getString(R.string.transfer_cancelled)
    }
}

// MARK: add files

/**
 * Add screen (docs/specs/06-app-ios.md §4.6): files from the system picker
 * (several at once) or shared by another app; each one is a game (system
 * suggested from the extension) or the save of a game.
 */
@Composable
fun AddFilesScreen(deviceId: String?, shareToken: String?, onBack: () -> Unit, onSent: (String) -> Unit) {
    val context = LocalContext.current
    val app = context.applicationContext as Application
    val vm: AddFilesViewModel = viewModel(key = "add-$deviceId-$shareToken") { AddFilesViewModel(app, deviceId, shareToken) }
    val state by vm.state.collectAsState()
    val consoles = remember { context.container.consoles.all().sortedBy { it.deviceName.lowercase() } }
    var pickingGameFor by remember { mutableStateOf<String?>(null) }
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { uris ->
        if (uris.isNotEmpty()) vm.add(uris)
    }
    // The progress notification of the transfers (API 33+); the transfers run without it.
    val notifications = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) {}
    fun askNotifications() {
        if (Build.VERSION.SDK_INT >= 33 &&
            context.checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            notifications.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
    }

    AppScaffold(
        title = stringResource(R.string.add_files),
        onBack = onBack,
        actions = {
            TextButton(
                onClick = { askNotifications(); vm.send(onSent) },
                enabled = state.canSend,
                modifier = Modifier.testTag("sendFiles"),
            ) { Text(stringResource(R.string.action_send)) }
        },
    ) { modifier ->
        LazyColumn(modifier.fillMaxSize().testTag("addFiles")) {
            item {
                if (state.sending) LinearProgressIndicator(Modifier.fillMaxWidth())
                state.error?.let { MessageCard(null, it) {} }
                if (consoles.isEmpty()) {
                    Text(stringResource(R.string.add_no_console), modifier = Modifier.padding(16.dp))
                } else if (deviceId == null && consoles.size > 1) {
                    SectionTitle(stringResource(R.string.add_console_choice))
                    Row(Modifier.padding(horizontal = 16.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        consoles.forEach { console ->
                            FilterChip(
                                selected = state.deviceId == console.deviceId,
                                onClick = { vm.chooseConsole(console.deviceId) },
                                label = { Text(console.deviceName) },
                            )
                        }
                    }
                }
                (state.systems as? Load.Failed)?.let { MessageCard(null, it.error) {} }
                OutlinedButton(
                    onClick = { picker.launch(arrayOf("*/*")) },
                    modifier = Modifier.fillMaxWidth().padding(16.dp).testTag("chooseFiles"),
                ) { Text(stringResource(R.string.add_choose_files)) }
                if (state.files.isEmpty()) Footnote(stringResource(R.string.add_help))
            }
            items(state.files, key = { it.id }) { file ->
                AddFileRow(
                    file = file,
                    systems = (state.systems as? Load.Loaded)?.value.orEmpty(),
                    onKind = { vm.setKind(file.id, it) },
                    onSystem = { vm.setSystem(file.id, it) },
                    onPickGame = { vm.loadGames(); pickingGameFor = file.id },
                    onRemove = { vm.remove(file.id) },
                )
                HorizontalDivider()
            }
        }
    }
    pickingGameFor?.let { id ->
        GamePickerDialog(vm, onPicked = { vm.setGame(id, it); pickingGameFor = null }, onDismiss = { pickingGameFor = null })
    }
}

@Composable
private fun AddFileRow(
    file: AddFile,
    systems: List<link.reminis.core.GameSystem>,
    onKind: (AddKind) -> Unit,
    onSystem: (String) -> Unit,
    onPickGame: () -> Unit,
    onRemove: () -> Unit,
) {
    var systemMenu by remember { mutableStateOf(false) }
    Column(Modifier.fillMaxWidth().padding(16.dp).testTag("addFile"), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(file.name, style = MaterialTheme.typography.titleMedium, modifier = Modifier.weight(1f))
            IconButton(onClick = onRemove) { Icon(Icons.Filled.Delete, contentDescription = stringResource(R.string.action_remove)) }
        }
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            FilterChip(selected = file.kind == AddKind.ROM, onClick = { onKind(AddKind.ROM) }, label = { Text(stringResource(R.string.add_kind_game)) })
            FilterChip(selected = file.kind == AddKind.SAVE, onClick = { onKind(AddKind.SAVE) }, label = { Text(stringResource(R.string.add_kind_save)) })
        }
        when (file.kind) {
            AddKind.ROM -> {
                val label = systems.firstOrNull { it.id == file.systemId }?.let { "${it.label} (${it.tag})" }
                OutlinedButton(onClick = { systemMenu = true }, modifier = Modifier.testTag("chooseSystem")) {
                    Text(label ?: stringResource(R.string.add_choose_system))
                }
                DropdownMenu(expanded = systemMenu, onDismissRequest = { systemMenu = false }) {
                    systems.forEach { system ->
                        DropdownMenuItem(
                            text = { Text("${system.label} (${system.tag})") },
                            onClick = { onSystem(system.id); systemMenu = false },
                        )
                    }
                }
            }
            AddKind.SAVE -> {
                OutlinedButton(onClick = onPickGame, modifier = Modifier.testTag("chooseGame")) {
                    Text(file.game?.name ?: stringResource(R.string.add_choose_game))
                }
                Footnote(stringResource(R.string.add_save_help))
            }
        }
    }
}

@Composable
private fun GamePickerDialog(vm: AddFilesViewModel, onPicked: (LibraryGame) -> Unit, onDismiss: () -> Unit) {
    val games by vm.games.collectAsState()
    var query by remember { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.add_choose_game)) },
        text = {
            Column {
                OutlinedTextField(
                    value = query,
                    onValueChange = { query = it },
                    leadingIcon = { Icon(Icons.Filled.Search, contentDescription = null) },
                    placeholder = { Text(stringResource(R.string.search_games)) },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                when (val load = games) {
                    is Load.Loaded -> {
                        val shown = remember(load, query) { vm.filteredGames(load.value, query) }
                        LazyColumn(Modifier.heightIn(max = 360.dp)) {
                            items(shown, key = { it.id }) { game ->
                                ListItem(
                                    headlineContent = { Text(game.name) },
                                    supportingContent = { Text(game.fileName) },
                                    modifier = Modifier.clickable { onPicked(game) },
                                )
                            }
                        }
                    }
                    is Load.Failed -> Text(errorMessage(LocalContext.current, load.error), modifier = Modifier.padding(top = 8.dp))
                    else -> LinearProgressIndicator(Modifier.fillMaxWidth().padding(top = 16.dp))
                }
            }
        },
        confirmButton = {},
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_cancel)) } },
    )
}

/** A job of the console (a patch, a pak installation): its state as last polled. */
@Composable
private fun JobRow(job: TrackedJob, onCancel: () -> Unit) {
    val context = LocalContext.current
    val state = job.job
    ListItem(
        headlineContent = { Text(job.title) },
        supportingContent = {
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                if (!job.isFinished) {
                    val fraction = state?.progress?.fraction
                    if (fraction != null) LinearProgressIndicator(progress = { fraction }, modifier = Modifier.fillMaxWidth())
                    else LinearProgressIndicator(Modifier.fillMaxWidth())
                }
                Text(
                    when {
                        job.lost -> stringResource(R.string.error_job_lost)
                        state == null -> stringResource(R.string.job_waiting)
                        state.state == JobStates.SUCCEEDED -> stringResource(
                            when (job.kind) {
                                JobKinds.PAK_INSTALL -> R.string.pak_installed
                                JobKinds.ARCHIVE_IMPORT -> R.string.archive_imported_job
                                else -> R.string.job_done
                            },
                        )
                        state.state == JobStates.FAILED -> state.serverError?.let {
                            errorMessage(
                                context,
                                when (job.kind) {
                                    JobKinds.PAK_INSTALL -> pakError(PakJobFailure(it))
                                    JobKinds.ARCHIVE_IMPORT -> archiveError(link.reminis.core.ArchiveJobFailure(it))
                                    else -> patchError(PatchJobFailure(it))
                                },
                            )
                        } ?: stringResource(R.string.transfer_failed)
                        state.state == JobStates.CANCELLED -> stringResource(R.string.transfer_cancelled)
                        job.cancelRequested -> stringResource(R.string.patch_cancelling)
                        job.kind == JobKinds.PAK_INSTALL -> stringResource(R.string.pak_installing)
                        job.kind == JobKinds.ARCHIVE_IMPORT -> stringResource(R.string.archive_importing)
                        else -> stringResource(R.string.patch_applying)
                    },
                    style = MaterialTheme.typography.bodySmall,
                )
                job.pollError?.let { Text(errorMessage(context, it), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error) }
            }
        },
        trailingContent = {
            if (!job.isFinished && !job.cancelRequested) {
                TextButton(onClick = onCancel) { Text(stringResource(R.string.transfer_cancel)) }
            }
        },
        modifier = Modifier.testTag("job"),
    )
}
