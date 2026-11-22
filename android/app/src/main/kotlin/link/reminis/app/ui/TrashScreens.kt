package link.reminis.app.ui

import android.app.Application
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.Warning
import androidx.compose.material3.Button
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
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
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import link.reminis.app.R
import link.reminis.core.ApiException
import link.reminis.core.TrashEntry
import link.reminis.core.TrashKeptAs
import link.reminis.core.TrashList
import link.reminis.core.TrashNoFreeName
import link.reminis.core.TrashRestoreConflict
import link.reminis.core.TrashRestoreItem
import link.reminis.core.TrashRestorePreview
import link.reminis.core.TrashRestoreResult
import link.reminis.core.trashRestoreConflicts

/** The trash of the console: entries, restore (M18), permanent deletion with confirmation. */
class TrashViewModel(app: Application, deviceId: String) : ActionViewModel(app, deviceId) {
    private val _trash = MutableStateFlow<Load<TrashList>>(Load.Loading)
    val trash: StateFlow<Load<TrashList>> = _trash.asStateFlow()

    init {
        // M14: any change of the SD card may fill or empty the trash (a `library` event, any client),
        // and screenshots moved to the trash or restored (M22, `screenshots`).
        onConsoleChanges { change ->
            if (change is link.reminis.app.ConsoleChange.Library || change is link.reminis.app.ConsoleChange.CatchUp ||
                change is link.reminis.app.ConsoleChange.Screenshots
            ) {
                load()
            }
        }
    }

    /** Only the last load shows its answer. */
    private var generation = 0

    fun load() {
        val current = ++generation
        if (_trash.value !is Load.Loaded) _trash.value = Load.Loading
        viewModelScope.launch {
            val result = try {
                Load.Loaded(container.client(deviceId).trash())
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                Load.Failed(e)
            }
            if (current != generation) return@launch
            if (result is Load.Loaded || _trash.value !is Load.Loaded) _trash.value = result
        }
    }

    /** Permanently empties the trash (`X-RL-Confirm: empty-trash`). */
    fun empty() = perform(Notice(R.string.trash_emptied)) {
        container.client(deviceId).emptyTrash()
        load()
    }

    /** Permanently deletes one entry (`X-RL-Confirm: empty-trash`). */
    fun delete(entry: TrashEntry, label: String) = perform(Notice(R.string.trash_entry_deleted, listOf(label))) {
        container.client(deviceId).deleteTrashEntry(entry.id)
        load()
    }
}

@Composable
fun TrashScreen(deviceId: String, onBack: () -> Unit, onRestore: (TrashEntry) -> Unit) {
    val app = LocalContext.current.applicationContext as Application
    val vm: TrashViewModel = viewModel(key = "trash-$deviceId") { TrashViewModel(app, deviceId) }
    val trash by vm.trash.collectAsState()
    val working by vm.working.collectAsState()
    val notice by vm.notice.collectAsState()
    val error by vm.error.collectAsState()
    val context = LocalContext.current
    var confirmEmpty by androidx.compose.runtime.saveable.rememberSaveable { mutableStateOf(false) }
    var toDeleteId by androidx.compose.runtime.saveable.rememberSaveable { mutableStateOf<String?>(null) }
    val entries = (trash as? Load.Loaded)?.value?.entries.orEmpty()

    // Read again whenever the screen shows (after a restore, too).
    LifecycleResumeEffect(Unit) {
        vm.load()
        onPauseOrDispose {}
    }
    AppScaffold(
        title = stringResource(R.string.trash_title),
        onBack = onBack,
        actions = {
            IconButton(onClick = vm::load) { Icon(Icons.Filled.Refresh, contentDescription = stringResource(R.string.refresh)) }
            TextButton(onClick = { confirmEmpty = true }, enabled = entries.isNotEmpty() && !working, modifier = Modifier.testTag("emptyTrash")) {
                Text(stringResource(R.string.trash_empty_action))
            }
        },
    ) { modifier ->
        LoadContent(trash, onRetry = vm::load, modifier = modifier) { list ->
            LazyColumn(Modifier.then(modifier).fillMaxSize().testTag("trash")) {
                item {
                    if (working) LinearProgressIndicator(Modifier.fillMaxWidth())
                    MessageCard(notice, error, vm::dismissMessages)
                    if (list.entries.isEmpty()) {
                        Text(stringResource(R.string.trash_is_empty), modifier = Modifier.padding(24.dp).testTag("trashEmpty"))
                    }
                }
                items(list.entries, key = { it.id }) { entry ->
                    val shownLabel = trashLabel(entry)
                    ListItem(
                        headlineContent = { Text(shownLabel, modifier = Modifier.testTag("trashLabel-${entry.id}")) },
                        supportingContent = {
                            Column {
                                Text(formatBytes(context, entry.sizeBytes))
                                if (entry.held) {
                                    // M22: never purged automatically.
                                    Text(stringResource(R.string.trash_held), style = MaterialTheme.typography.bodySmall, modifier = Modifier.testTag("trashHeld"))
                                } else {
                                    Text(stringResource(R.string.trash_expires, formatDay(entry.expiresAt)), style = MaterialTheme.typography.bodySmall)
                                }
                            }
                        },
                        trailingContent = {
                            IconButton(onClick = { toDeleteId = entry.id }, enabled = !working, modifier = Modifier.testTag("trashDelete")) {
                                Icon(Icons.Filled.Delete, contentDescription = stringResource(R.string.action_delete_ellipsis))
                            }
                        },
                        modifier = Modifier.clickable(enabled = !working) { onRestore(entry.copy(label = shownLabel)) }.testTag("trashEntry"),
                    )
                    HorizontalDivider()
                }
                item { Footnote(stringResource(R.string.trash_footer)) }
            }
        }
    }
    if (confirmEmpty) {
        ConfirmDialog(
            title = stringResource(R.string.trash_empty_title),
            message = stringResource(
                R.string.trash_empty_message,
                formatBytes(context, (trash as? Load.Loaded)?.value?.totalBytes ?: 0),
            ),
            confirm = stringResource(R.string.trash_empty_action),
            onConfirm = vm::empty,
            onDismiss = { confirmEmpty = false },
        )
    }
    entries.firstOrNull { it.id == toDeleteId }?.let { entry ->
        val label = trashLabel(entry)
        ConfirmDialog(
            title = stringResource(R.string.trash_delete_title),
            message = stringResource(R.string.trash_delete_message, label),
            confirm = stringResource(R.string.action_delete),
            onConfirm = { vm.delete(entry, label) },
            onDismiss = { toDeleteId = null },
        )
    }
}

/**
 * The label shown for an entry: the pak's, except for several screenshots
 * (M22), counted in the user's language (the pak's label is English).
 */
@Composable
fun trashLabel(entry: TrashEntry): String {
    val count = entry.screenshotCount
    return if (count != null && count > 1) pluralStringResource(R.plurals.trash_screenshots, count, count) else entry.label
}

/** A date of the console (RFC 3339), as a day in the phone's time zone. */
fun formatDay(text: String): String = try {
    OffsetDateTime.parse(text).atZoneSameInstant(ZoneId.systemDefault()).format(DateTimeFormatter.ofLocalizedDate(FormatStyle.MEDIUM))
} catch (_: Exception) {
    text
}

// MARK: restore

/**
 * Restore of a trash entry (M18, docs/specs/06-app-ios.md §4.3): shows
 * exactly what comes back and where, read from the console
 * (`GET /trash/{entryId}/restore`), then puts it back. A file already on
 * the SD card is never replaced: when a place is taken, the screen says so
 * and offers the same free name for every file instead; the save of a game
 * still on the console can replace its current save, after a confirmation
 * that says where the current save goes.
 */
class TrashRestoreViewModel(app: Application, deviceId: String, private val entryId: String) : ActionViewModel(app, deviceId) {
    private val _preview = MutableStateFlow<Load<TrashRestorePreview>>(Load.Loading)
    val preview: StateFlow<Load<TrashRestorePreview>> = _preview.asStateFlow()
    private val _result = MutableStateFlow<TrashRestoreResult?>(null)
    val result: StateFlow<TrashRestoreResult?> = _result.asStateFlow()

    /** The console answered `409` to a restore in place: a file appeared since the preview, which was read again. */
    private val _conflictAppeared = MutableStateFlow(false)
    val conflictAppeared: StateFlow<Boolean> = _conflictAppeared.asStateFlow()

    init {
        load()
    }

    fun load() {
        viewModelScope.launch {
            val loaded = try {
                Load.Loaded(container.client(deviceId).trashRestorePreview(entryId))
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                Load.Failed(trashRestoreError(e))
            }
            _preview.value = loaded
        }
    }

    fun restore(onConflict: TrashRestoreConflict) = perform(null) {
        try {
            _result.value = container.client(deviceId).restoreTrashEntry(entryId, onConflict)
            container.libraryChanged.tryEmit(deviceId)
        } catch (e: ApiException.Server) {
            if (e.trashRestoreConflicts != null) {
                // A file appeared since the preview: show what is taken now.
                _conflictAppeared.value = onConflict == TrashRestoreConflict.FAIL
                load()
                return@perform
            }
            throw trashRestoreError(e)
        } catch (e: kotlinx.coroutines.CancellationException) {
            throw e
        } catch (e: Exception) {
            throw trashRestoreError(e)
        }
    }
}

@Composable
fun TrashRestoreScreen(deviceId: String, entryId: String, label: String, onBack: () -> Unit) {
    val app = LocalContext.current.applicationContext as Application
    val vm: TrashRestoreViewModel = viewModel(key = "restore-$deviceId-$entryId") { TrashRestoreViewModel(app, deviceId, entryId) }
    val preview by vm.preview.collectAsState()
    val result by vm.result.collectAsState()
    val working by vm.working.collectAsState()
    val error by vm.error.collectAsState()
    val conflictAppeared by vm.conflictAppeared.collectAsState()
    var confirmReplace by androidx.compose.runtime.saveable.rememberSaveable { mutableStateOf(false) }

    AppScaffold(
        title = if (result == null) stringResource(R.string.trash_restore_title, label) else stringResource(R.string.trash_restored_title),
        onBack = onBack,
    ) { modifier ->
        val done = result
        if (done != null) {
            RestoreDone(done, modifier, onBack)
            return@AppScaffold
        }
        LoadContent(preview, onRetry = vm::load, modifier = modifier) { p ->
            val context = LocalContext.current
            LazyColumn(Modifier.then(modifier).fillMaxSize().testTag("trashRestore")) {
                item {
                    if (working) LinearProgressIndicator(Modifier.fillMaxWidth())
                    MessageCard(null, error, vm::dismissMessages)
                    SectionTitle(stringResource(R.string.trash_restore_items))
                }
                items(p.items, key = { it.name }) { RestoreItemRow(it, renamed = p.canRename) }
                item {
                    Footnote(stringResource(R.string.trash_restore_total, formatBytes(context, p.restoredBytes)))
                    Footnote(stringResource(R.string.trash_restore_locks))
                    HorizontalDivider()
                    Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                        RestoreActions(p, working, conflictAppeared, onRestore = vm::restore, onReplace = { confirmReplace = true })
                    }
                }
            }
            if (confirmReplace) {
                val installOnly = p.replaceDisplaced.isEmpty()
                ConfirmDialog(
                    title = stringResource(if (installOnly) R.string.trash_make_save_title else R.string.trash_replace_title),
                    message = replaceExplanation(p),
                    confirm = stringResource(if (installOnly) R.string.trash_make_save else R.string.trash_replace_save),
                    onConfirm = { vm.restore(TrashRestoreConflict.REPLACE) },
                    onDismiss = { confirmReplace = false },
                )
            }
        }
    }
}

@Composable
private fun RestoreActions(
    p: TrashRestorePreview,
    working: Boolean,
    conflictAppeared: Boolean,
    onRestore: (TrashRestoreConflict) -> Unit,
    onReplace: () -> Unit,
) {
    if (p.conflict) {
        Text(
            stringResource(if (conflictAppeared) R.string.trash_conflict_appeared else R.string.trash_conflict),
            color = MaterialTheme.colorScheme.error,
            modifier = Modifier.testTag("trashRestoreConflict"),
        )
        if (p.canRename) {
            Button(onClick = { onRestore(TrashRestoreConflict.RENAME) }, enabled = !working, modifier = Modifier.fillMaxWidth().testTag("trashRestoreRename")) {
                Text(stringResource(R.string.trash_restore_rename))
            }
            Text(stringResource(R.string.trash_restore_rename_help), style = MaterialTheme.typography.bodySmall)
        } else {
            // No free name exists: never offer a doomed action.
            Text(
                stringResource(if (p.freeNameUnavailable == TrashNoFreeName.PATH_BLOCKED) R.string.trash_no_free_name_blocked else R.string.trash_no_free_name_taken),
                modifier = Modifier.testTag("trashRestoreNoFreeName"),
            )
        }
        ReplaceAction(p, working, onReplace)
    } else {
        Button(onClick = { onRestore(TrashRestoreConflict.FAIL) }, enabled = !working, modifier = Modifier.fillMaxWidth().testTag("trashRestoreConfirm")) {
            Text(stringResource(R.string.action_restore))
        }
        Text(
            stringResource(
                if (p.replaceable && p.replaceKeptAs == TrashKeptAs.BACKUP) R.string.trash_restore_backup_help else R.string.trash_restore_help,
            ),
            style = MaterialTheme.typography.bodySmall,
        )
        // Also says why the current save cannot be replaced now (place-blocked, backup-taken).
        if (p.replaceable || p.replaceBlocked) ReplaceAction(p, working, onReplace)
    }
}

/** "Replace the Current Save", or "Make It the Game's Save" when nothing is in place. */
@Composable
private fun ReplaceAction(p: TrashRestorePreview, working: Boolean, onReplace: () -> Unit) {
    if (p.replaceBlocked) {
        Text(stringResource(R.string.trash_replace_unavailable), modifier = Modifier.testTag("trashReplaceUnavailable"))
    }
    if (!p.replaceable) return
    val installOnly = p.replaceDisplaced.isEmpty()
    OutlinedButton(onClick = onReplace, enabled = !working, modifier = Modifier.fillMaxWidth().testTag("trashRestoreReplace")) {
        Text(stringResource(if (installOnly) R.string.trash_make_save else R.string.trash_replace_save))
    }
    Text(replaceExplanation(p), style = MaterialTheme.typography.bodySmall)
}

/** Where the current files go with `replace` (the pak's answer, and the clock file of a copy). */
@Composable
private fun replaceExplanation(p: TrashRestorePreview): String = stringResource(
    when {
        p.replaceKeptAs == TrashKeptAs.BACKUP && p.withClock -> R.string.trash_replace_backup_clock
        p.replaceKeptAs == TrashKeptAs.BACKUP -> R.string.trash_replace_backup
        p.replaceKeptAs == null -> R.string.trash_replace_no_save
        else -> R.string.trash_replace_trash
    },
)

@Composable
private fun RestoreItemRow(item: TrashRestoreItem, renamed: Boolean) {
    val context = LocalContext.current
    ListItem(
        headlineContent = { Text(item.fileName) },
        supportingContent = {
            Column {
                Text(item.name, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace)
                if (item.conflict) {
                    Text(stringResource(R.string.trash_item_taken), color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
                }
                val free = item.freeName
                if (renamed && free != null && free != item.name) {
                    Text(stringResource(R.string.trash_item_comes_back_as, free), style = MaterialTheme.typography.bodySmall)
                }
            }
        },
        trailingContent = { Text(formatBytes(context, item.size)) },
        leadingContent = if (item.conflict) {
            { Icon(Icons.Filled.Warning, contentDescription = null) }
        } else {
            null
        },
    )
}

@Composable
private fun RestoreDone(result: TrashRestoreResult, modifier: Modifier, onDone: () -> Unit) {
    LazyColumn(modifier.fillMaxSize().testTag("trashRestoreDone")) {
        item { SectionTitle(stringResource(R.string.trash_restore_back)) }
        items(result.restored) { name ->
            Text(name, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp))
        }
        item {
            Footnote(
                stringResource(
                    when {
                        result.replacedKeptAs == TrashKeptAs.BACKUP -> R.string.trash_done_backup
                        result.replacedKeptAs != null -> R.string.trash_done_trash
                        result.renamed -> R.string.trash_done_renamed
                        else -> R.string.trash_done_original
                    },
                ),
            )
            Button(onClick = onDone, modifier = Modifier.fillMaxWidth().padding(16.dp).testTag("trashRestoreFinished")) {
                Text(stringResource(R.string.patch_done))
            }
        }
    }
}
