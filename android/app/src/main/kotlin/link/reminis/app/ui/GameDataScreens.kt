package link.reminis.app.ui

import android.app.Application
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.Warning
import androidx.compose.material3.AlertDialog
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
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import link.reminis.app.R
import link.reminis.app.container
import link.reminis.core.Backup
import link.reminis.core.BackupLabel
import link.reminis.core.BackupReasons
import link.reminis.core.LockStatus
import link.reminis.core.Slot

// MARK: save

@Composable
fun SaveScreen(deviceId: String, gameId: String, onBack: () -> Unit, vmKey: String = gameId) {
    val app = LocalContext.current.applicationContext as Application
    val vm: SaveViewModel = viewModel(key = "save-$deviceId-$vmKey") { SaveViewModel(app, deviceId, gameId) }
    val content by vm.content.collectAsState()
    val working by vm.working.collectAsState()
    val notice by vm.notice.collectAsState()
    val error by vm.error.collectAsState()
    val context = LocalContext.current
    val creator = rememberDocumentCreator()
    var selected by remember { mutableStateOf<Backup?>(null) }
    var toRestore by remember { mutableStateOf<Backup?>(null) }
    var toDelete by remember { mutableStateOf<Backup?>(null) }
    var creating by remember { mutableStateOf(false) }
    var replacement by remember { mutableStateOf<android.net.Uri?>(null) }
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri -> replacement = uri }

    AppScaffold(
        title = stringResource(R.string.save_title),
        onBack = onBack,
        actions = {
            IconButton(onClick = { vm.load() }) { Icon(Icons.Filled.Refresh, contentDescription = stringResource(R.string.refresh)) }
        },
    ) { modifier ->
        LoadContent(content, onRetry = { vm.load() }, modifier = modifier) { save ->
            val gameName = save.game.name
            LazyColumn(Modifier.then(modifier).fillMaxSize().testTag("saveScreen")) {
                item {
                    if (working) LinearProgressIndicator(Modifier.fillMaxWidth())
                    MessageCard(notice, error, vm::dismissMessages)
                    SectionTitle(stringResource(R.string.save_current))
                    val current = save.save
                    if (current != null) {
                        ListItem(
                            headlineContent = { Text(current.name) },
                            supportingContent = { Text("${formatDate(current.modifiedAt)} · ${formatBytes(context, current.sizeBytes)}") },
                        )
                    } else {
                        Text(
                            stringResource(R.string.game_save_no),
                            modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp).testTag("noSave"),
                        )
                    }
                    Column(Modifier.padding(horizontal = 16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        if (current != null) {
                            OutlinedButton(
                                onClick = { creator.create(current.name) { vm.downloadSave(current, gameName, it) } },
                                enabled = !working,
                                modifier = Modifier.fillMaxWidth().testTag("downloadSave"),
                            ) { Text(stringResource(R.string.action_download)) }
                            OutlinedButton(
                                onClick = { creating = true },
                                enabled = !working,
                                modifier = Modifier.fillMaxWidth().testTag("createBackup"),
                            ) { Text(stringResource(R.string.backup_make)) }
                        }
                        OutlinedButton(
                            onClick = { picker.launch(arrayOf("*/*")) },
                            enabled = !working,
                            modifier = Modifier.fillMaxWidth().testTag("replaceSave"),
                        ) { Text(stringResource(R.string.save_replace)) }
                    }
                    Footnote(stringResource(R.string.save_footer))
                    HorizontalDivider()
                    SectionTitle(stringResource(R.string.backups_title))
                    if (save.backups.isEmpty()) {
                        Text(stringResource(R.string.backups_none), modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp))
                    }
                }
                items(save.backups, key = { it.id }) { backup ->
                    ListItem(
                        headlineContent = { Text(backupTitle(backup)) },
                        overlineContent = if (backup.isAutomatic) {
                            { Text(stringResource(R.string.backup_automatic)) }
                        } else {
                            null
                        },
                        supportingContent = { Text("${formatDate(backup.createdAt)} · ${formatBytes(context, backup.sizeBytes)}") },
                        modifier = Modifier.clickable(enabled = !working) { selected = backup }.testTag("backup"),
                    )
                }
                item { Footnote(stringResource(R.string.backups_footer)) }
            }
            selected?.let { backup ->
                ActionsDialog(
                    title = backupTitle(backup),
                    subtitle = "${formatDate(backup.createdAt)} · ${formatBytes(context, backup.sizeBytes)}",
                    actions = listOf(
                        stringResource(R.string.backup_restore_action) to { toRestore = backup },
                        stringResource(R.string.action_download) to {
                            val name = backupFileName(save.save?.name ?: "${save.game.fileName}.sav", backup)
                            creator.create(name) { vm.downloadBackup(backup, name, gameName, it) }
                        },
                        stringResource(R.string.action_delete_ellipsis) to { toDelete = backup },
                    ),
                    onDismiss = { selected = null },
                )
            }
            toRestore?.let { backup ->
                ConfirmDialog(
                    title = stringResource(R.string.backup_restore_title),
                    message = stringResource(R.string.backup_restore_message, formatDate(backup.createdAt)),
                    confirm = stringResource(R.string.action_restore),
                    onConfirm = { vm.restore(backup) },
                    onDismiss = { toRestore = null },
                )
            }
            toDelete?.let { backup ->
                ConfirmDialog(
                    title = stringResource(R.string.backup_delete_title),
                    message = stringResource(R.string.backup_delete_message),
                    confirm = stringResource(R.string.action_delete),
                    onConfirm = { vm.delete(backup) },
                    onDismiss = { toDelete = null },
                )
            }
            replacement?.let { uri ->
                val name = remember(uri) { link.reminis.app.LocalFiles.displayName(context.contentResolver, uri) }
                ConfirmDialog(
                    title = stringResource(R.string.save_replace_title),
                    message = if (save.save != null) {
                        stringResource(R.string.save_replace_message, name, gameName)
                    } else {
                        stringResource(R.string.save_replace_message_none, name, gameName)
                    },
                    confirm = stringResource(R.string.action_replace),
                    onConfirm = { vm.replace(uri, gameName) },
                    onDismiss = { replacement = null },
                )
            }
            if (creating) {
                CreateBackupDialog(onCreate = { vm.createBackup(it); creating = false }, onDismiss = { creating = false })
            }
        }
    }
}

/** Automatic copies are named after their reason (the console's label is not translated); manual ones after their label. */
@Composable
private fun backupTitle(backup: Backup): String = when (backup.reason) {
    BackupReasons.MANUAL -> backup.label ?: stringResource(R.string.backup_default_title)
    BackupReasons.BEFORE_REPLACE -> stringResource(R.string.backup_before_replace)
    BackupReasons.BEFORE_RESTORE -> stringResource(R.string.backup_before_restore)
    BackupReasons.BEFORE_DELETE -> stringResource(R.string.backup_before_delete)
    BackupReasons.BEFORE_PATCH -> stringResource(R.string.backup_before_patch)
    BackupReasons.BEFORE_IMPORT -> stringResource(R.string.backup_before_import)
    else -> backup.label ?: stringResource(R.string.backup_automatic_title)
}

/** `<save name without .sav> (<date>).sav`: a suggested name only, the user may change it. */
private fun backupFileName(saveName: String, backup: Backup): String {
    val dot = saveName.lastIndexOf('.')
    val base = if (dot > 0) saveName.substring(0, dot) else saveName
    val ext = if (dot > 0) saveName.substring(dot) else ".sav"
    val date = backup.createdAt.take(10)
    return "$base ($date)$ext"
}

@Composable
private fun CreateBackupDialog(onCreate: (String?) -> Unit, onDismiss: () -> Unit) {
    var label by remember { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.backup_new_title)) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(
                    value = label,
                    onValueChange = { label = BackupLabel.limited(it) },
                    label = { Text(stringResource(R.string.backup_label)) },
                    placeholder = { Text(stringResource(R.string.backup_label_optional)) },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth().testTag("backupLabel"),
                )
                Text(
                    stringResource(R.string.backup_new_help, label.codePointCount(0, label.length), BackupLabel.MAX_LENGTH),
                    style = MaterialTheme.typography.bodySmall,
                )
            }
        },
        confirmButton = {
            TextButton(onClick = { onCreate(BackupLabel.normalized(label)) }, modifier = Modifier.testTag("backupCreate")) {
                Text(stringResource(R.string.action_create))
            }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_cancel)) } },
    )
}

// MARK: states

private sealed interface StateConfirmation {
    val slot: Slot

    data class Unlock(override val slot: Slot) : StateConfirmation
    data class Restore(override val slot: Slot) : StateConfirmation
    data class Delete(override val slot: Slot) : StateConfirmation
}

@Composable
fun StatesScreen(deviceId: String, gameId: String, gameName: String, onBack: () -> Unit, vmKey: String = gameId) {
    val app = LocalContext.current.applicationContext as Application
    val vm: StatesViewModel = viewModel(key = "states-$deviceId-$vmKey") { StatesViewModel(app, deviceId, gameId) }
    val slots by vm.slots.collectAsState()
    val restored by vm.restored.collectAsState()
    val thumbnails by vm.thumbnails.collectAsState()
    val working by vm.working.collectAsState()
    val notice by vm.notice.collectAsState()
    val error by vm.error.collectAsState()
    val context = LocalContext.current
    val creator = rememberDocumentCreator()
    var selected by remember { mutableStateOf<Slot?>(null) }
    var confirmation by remember { mutableStateOf<StateConfirmation?>(null) }
    val history by vm.history.collectAsState()
    var restoringResume by remember { mutableStateOf<link.reminis.core.ResumeHistoryEntry?>(null) }
    val demo = LocalContext.current.container.isDemo(deviceId)

    AppScaffold(
        title = stringResource(R.string.states_title),
        onBack = onBack,
        actions = {
            IconButton(onClick = { vm.load() }) { Icon(Icons.Filled.Refresh, contentDescription = stringResource(R.string.refresh)) }
        },
    ) { modifier ->
        LoadContent(slots, onRetry = { vm.load() }, modifier = modifier) { list ->
            val title: @Composable (Slot) -> String = { slot ->
                if (slot.slot == list.resumeSlot) {
                    stringResource(R.string.state_resume)
                } else {
                    stringResource(R.string.state_slot, slot.displayNumber)
                }
            }
            LazyColumn(Modifier.then(modifier).fillMaxSize().testTag("statesScreen")) {
                item {
                    if (working) LinearProgressIndicator(Modifier.fillMaxWidth())
                    MessageCard(notice, error, vm::dismissMessages)
                    SectionTitle(stringResource(R.string.states_slots))
                    if (list.userSlots.isEmpty()) {
                        Text(stringResource(R.string.states_none), modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp))
                    }
                }
                val shown = list.userSlots + listOfNotNull(list.resume)
                items(shown, key = { it.slot }) { slot ->
                    StateRow(
                        slot = slot,
                        title = title(slot),
                        restored = slot.wasRestored || slot.slot in restored,
                        thumbnail = thumbnails[slot.slot],
                        onClick = { if (!working) selected = slot },
                    )
                }
                // The resume history (M5): the last versions of the automatic resume state.
                val entries = history?.entries
                if (entries != null && (list.resume != null || entries.isNotEmpty())) {
                    item { SectionTitle(stringResource(R.string.resume_history)) }
                    if (entries.isEmpty()) {
                        item { Text(stringResource(R.string.resume_none), modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp)) }
                    }
                    items(entries, key = { "rh-" + it.id }) { entry ->
                        ListItem(
                            headlineContent = { Text(formatDate(entry.capturedAt)) },
                            trailingContent = { Text(formatBytes(context, entry.sizeBytes)) },
                            modifier = Modifier.clickable(enabled = !working) { restoringResume = entry }.testTag("resumeEntry"),
                        )
                    }
                    item {
                        // The demo names no real product.
                        Footnote(stringResource(if (demo) R.string.resume_footer_demo else R.string.resume_footer))
                        Footnote(stringResource(R.string.resume_footer_archive))
                    }
                }
                item {
                    Footnote(stringResource(R.string.states_lock_help))
                    Footnote(stringResource(R.string.states_trash_help))
                }
            }
            selected?.let { slot ->
                val slotTitle = title(slot)
                ActionsDialog(
                    title = slotTitle,
                    subtitle = if (slot.locked) {
                        stringResource(R.string.state_locked_cannot_delete)
                    } else {
                        "${formatDate(slot.modifiedAt)} · ${formatBytes(context, slot.sizeBytes)}"
                    },
                    actions = buildList {
                        if (slot.canRestore) add(stringResource(R.string.state_restore_action) to { confirmation = StateConfirmation.Restore(slot) })
                        if (slot.locked) {
                            add(stringResource(R.string.state_unlock_action) to { confirmation = StateConfirmation.Unlock(slot) })
                        } else {
                            add(stringResource(R.string.state_lock_action) to { vm.lock(slot, slotTitle) })
                        }
                        if (slot.canDownload) {
                            add(
                                stringResource(R.string.action_download) to {
                                    val name = stateFileName(gameName, slot)
                                    creator.create(name) { vm.download(slot, name, gameName, it) }
                                },
                            )
                        }
                        if (slot.canDelete) add(stringResource(R.string.action_delete_ellipsis) to { confirmation = StateConfirmation.Delete(slot) })
                    },
                    onDismiss = { selected = null },
                )
            }
            restoringResume?.let { entry ->
                ConfirmDialog(
                    title = stringResource(R.string.resume_restore_title),
                    message = stringResource(R.string.resume_restore_message, formatDate(entry.capturedAt)),
                    confirm = stringResource(R.string.action_restore),
                    onConfirm = { vm.restoreResume(entry) },
                    onDismiss = { restoringResume = null },
                )
            }
            confirmation?.let { c ->
                val slotTitle = title(c.slot)
                when (c) {
                    is StateConfirmation.Unlock -> ConfirmDialog(
                        title = stringResource(R.string.state_unlock_title, slotTitle),
                        message = stringResource(
                            if (c.slot.matchesLockedCopy) R.string.state_unlock_message_same else R.string.state_unlock_message_differs,
                        ),
                        confirm = stringResource(R.string.state_unlock),
                        onConfirm = { vm.unlock(c.slot, slotTitle) },
                        onDismiss = { confirmation = null },
                    )
                    is StateConfirmation.Restore -> ConfirmDialog(
                        title = stringResource(R.string.state_restore_title, slotTitle),
                        message = stringResource(R.string.state_restore_message),
                        confirm = stringResource(R.string.action_restore),
                        onConfirm = { vm.restore(c.slot, slotTitle) },
                        onDismiss = { confirmation = null },
                    )
                    is StateConfirmation.Delete -> ConfirmDialog(
                        title = stringResource(R.string.state_delete_title, slotTitle),
                        message = stringResource(R.string.state_delete_message),
                        confirm = stringResource(R.string.action_delete),
                        onConfirm = { vm.delete(c.slot, slotTitle) },
                        onDismiss = { confirmation = null },
                    )
                }
            }
        }
    }
}

/** A suggested name for a downloaded state: `<game>.st<N>`, the user may change it. */
private fun stateFileName(gameName: String, slot: Slot) = "$gameName.st${slot.slot}"

@Composable
private fun StateRow(slot: Slot, title: String, restored: Boolean, thumbnail: android.graphics.Bitmap?, onClick: () -> Unit) {
    val context = LocalContext.current
    val warning = lockWarning(slot)
    ListItem(
        leadingContent = {
            Box(Modifier.size(64.dp, 48.dp).background(MaterialTheme.colorScheme.surfaceVariant)) {
                if (thumbnail != null) {
                    Image(
                        thumbnail.asImageBitmap(), contentDescription = null, contentScale = ContentScale.Crop,
                        modifier = Modifier.fillMaxSize(),
                    )
                }
            }
        },
        headlineContent = {
            Text(if (restored) "$title · ${stringResource(R.string.state_restored_tag)}" else title)
        },
        supportingContent = {
            Column {
                if (!slot.isMissing) Text("${formatDate(slot.modifiedAt)} · ${formatBytes(context, slot.sizeBytes)}")
                if (warning != null) Text(warning, color = MaterialTheme.colorScheme.error)
            }
        },
        trailingContent = {
            if (slot.locked) {
                Icon(
                    if (slot.matchesLockedCopy) Icons.Filled.Lock else Icons.Filled.Warning,
                    contentDescription = stringResource(R.string.state_locked_a11y),
                    modifier = Modifier.testTag("locked${slot.slot}"),
                )
            }
        },
        modifier = Modifier.clickable(onClick = onClick).testTag("slot${slot.slot}"),
    )
}

/** What a locked slot needs the user to know when it is not plainly protected (an unknown status never looks protected). */
@Composable
private fun lockWarning(slot: Slot): String? {
    if (!slot.locked || slot.matchesLockedCopy) return null
    return when (slot.lockStatus) {
        LockStatus.MISSING -> stringResource(R.string.state_warning_missing)
        LockStatus.MODIFIED -> stringResource(R.string.state_warning_modified)
        LockStatus.CORE_CHANGED -> stringResource(R.string.state_warning_core)
        else -> stringResource(R.string.state_warning_unknown)
    }
}

// MARK: pieces

@Composable
fun SectionTitle(text: String) {
    Text(text, style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(start = 16.dp, top = 16.dp, bottom = 4.dp))
}

@Composable
fun Footnote(text: String, modifier: Modifier = Modifier) {
    Text(
        text,
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = modifier.padding(horizontal = 16.dp, vertical = 8.dp),
    )
}
