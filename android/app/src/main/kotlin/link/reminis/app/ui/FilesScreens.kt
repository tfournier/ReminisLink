package link.reminis.app.ui

import android.app.Application
import android.net.Uri
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.BottomAppBar
import androidx.compose.material3.Checkbox
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import link.reminis.app.LocalFiles
import link.reminis.app.R
import link.reminis.core.ApiException
import link.reminis.core.DownloadSource
import link.reminis.core.OnConflict
import link.reminis.core.SdEntry
import link.reminis.core.SdFolder
import link.reminis.core.SdFolderRequest
import link.reminis.core.SdSelection
import link.reminis.core.SdTrashRequest
import link.reminis.core.TransferDirection
import link.reminis.core.UploadTarget

/**
 * One folder of the SD card (M12, M23; docs/specs/06-app-ios.md §11): the
 * Files tab and the picker of "Install from the SD card". Only opaque
 * identifiers go back to the console; the data of ReminisLink is never
 * listed by the pak.
 *
 * Where the pak says so (`writable`, `deletable`), files are added (resumable
 * `sd-file` uploads, never over another file), folders made, and items moved
 * to the trash of the console after a confirmation that names them. The
 * selection keeps each entry as it was when checked (its version is sent),
 * and a refresh drops the entries that changed or left.
 */
class SdFolderViewModel(app: Application, deviceId: String, private val folderId: String?) : ActionViewModel(app, deviceId) {
    private val _folder = MutableStateFlow<Load<SdFolder>>(Load.Loading)
    val folder: StateFlow<Load<SdFolder>> = _folder.asStateFlow()
    private val _selecting = MutableStateFlow(false)
    val selecting: StateFlow<Boolean> = _selecting.asStateFlow()
    private val _selection = MutableStateFlow(SdSelection())
    val selection: StateFlow<SdSelection> = _selection.asStateFlow()

    /** The entries of the confirmation shown (kept at a rotation), as they were when chosen. */
    private val _pendingTrash = MutableStateFlow<List<SdEntry>>(emptyList())
    val pendingTrash: StateFlow<List<SdEntry>> = _pendingTrash.asStateFlow()

    /** Only the last read publishes. */
    private var generation = 0

    init {
        load()
        // M14: a change of the card by any client (an upload installed, another device), or a new stream.
        reloadOnLibraryChanges { load(quiet = true) }
        // A file of this folder sent from this phone.
        viewModelScope.launch {
            container.transfers.finished.collect { item ->
                val target = item.target as? UploadTarget.SdFile ?: return@collect
                if (item.deviceId == deviceId && item.direction == TransferDirection.UPLOAD && target.folderId == folderId) load(quiet = true)
            }
        }
    }

    /** `GET /sd/browse`; [quiet] keeps the list shown while reading, and a failure keeps it. */
    fun load(quiet: Boolean = false) {
        val current = ++generation
        if (!quiet || _folder.value !is Load.Loaded) _folder.value = Load.Loading
        viewModelScope.launch {
            val result = try {
                Load.Loaded(container.client(deviceId).browseSd(folderId))
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                Load.Failed(browseError(e))
            }
            if (current != generation) return@launch
            if (result is Load.Loaded) {
                // An entry that changed or left is no longer the one selected.
                _selection.value = _selection.value.prune(result.value.entries)
            }
            if (!quiet || result is Load.Loaded || _folder.value !is Load.Loaded) _folder.value = result
        }
    }

    private fun browseError(e: Throwable): Throwable {
        val status = (e as? ApiException.UnexpectedStatus)?.status ?: (e as? ApiException.Server)?.status
        return when {
            folderId == null && (status == 404 || status == 405) -> UserMessage(R.string.sd_needs_newer, cause = e)
            status == 404 -> UserMessage(R.string.sd_entry_gone, cause = e)
            else -> e
        }
    }

    fun startSelecting() {
        _selecting.value = true
    }

    fun finishSelecting() {
        _selecting.value = false
        _selection.value = SdSelection()
    }

    /** One request, all or nothing: at most 100 entries. */
    fun toggle(entry: SdEntry) {
        if (!entry.deletable) return
        val next = _selection.value.toggle(entry)
        if (next == null) {
            showFailure(UserMessage(R.string.sd_too_many))
        } else {
            _selection.value = next
            if ((error.value as? UserMessage)?.id == R.string.sd_too_many) dismissMessages()
        }
    }

    fun askTrash(entries: List<SdEntry>) {
        _pendingTrash.value = entries
    }

    fun cancelTrash() {
        _pendingTrash.value = emptyList()
    }

    /**
     * `POST /sd/trash` with the versions shown (one request, all or
     * nothing); the folder is read again whatever the outcome.
     */
    fun trash() {
        val entries = _pendingTrash.value
        _pendingTrash.value = emptyList()
        if (entries.isEmpty()) return
        val request = SdTrashRequest(entries)
        val label = if (request.items.size == 1) request.items[0].name else null
        perform(label?.let { Notice(R.string.sd_moved, listOf(it)) }) {
            try {
                if (request.items.size > SdTrashRequest.MAX_ITEMS) throw UserMessage(R.string.sd_too_many)
                // A listing without versions comes from a pak that cannot check them.
                if (!request.isComplete) throw UserMessage(R.string.files_need_newer)
                val moved = try {
                    container.client(deviceId).trashSdEntries(request)
                } catch (e: kotlinx.coroutines.CancellationException) {
                    throw e
                } catch (e: Exception) {
                    throw sdWriteError(e)
                }
                finishSelecting()
                container.trashChanged.tryEmit(deviceId)
                if (label == null) showNotice(Notice(R.string.sd_moved, listOf(moved.moved.size.toString())))
            } finally {
                load(quiet = true)
            }
        }
    }

    /** `POST /sd/folders`: a new folder here. */
    fun createFolder(name: String) {
        val clean = name.trim()
        if (clean.isEmpty()) return
        perform(Notice(R.string.sd_folder_made, listOf(clean))) {
            try {
                container.client(deviceId).createSdFolder(SdFolderRequest(folderId, clean))
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                throw sdWriteError(e)
            } finally {
                load(quiet = true)
            }
        }
    }

    /**
     * Files chosen on the phone: each is copied into the app, then sent by
     * the transfer queue (`sd-file`, resumable, `onConflict: fail`; a taken
     * name waits in Transfers with Keep Both only).
     */
    fun addFiles(uris: List<Uri>, folderTitle: String) = perform(null) {
        val app = getApplication<Application>()
        var queued = 0
        for (uri in uris) {
            val copy = LocalFiles.copyIn(app, uri, container.transferDirectory)
            container.transfers.enqueueUpload(
                deviceId, copy.file, copy.name, UploadTarget.SdFile(folderId, copy.name), folderTitle,
                onConflict = OnConflict.FAIL, ownsFile = true,
            )
            queued++
        }
        if (queued > 0) showNotice(Notice(R.string.sd_title, listOf(queued), plural = R.plurals.sd_queued, quantity = queued))
    }

    /** Downloads a file of the card into the document the user created (through the transfer queue). */
    fun download(entry: SdEntry, folderTitle: String, destination: Uri) =
        enqueueDownload(DownloadSource.SdFile(entry.sdEntryId), entry.name, folderTitle, destination, entry.sizeBytes)
}

/** The Files tab, or a folder of it. */
@Composable
fun SdFolderScreen(
    deviceId: String,
    folderId: String?,
    title: String?,
    onBack: (() -> Unit)?,
    onOpenFolder: (SdEntry) -> Unit,
) {
    val app = LocalContext.current.applicationContext as Application
    val vm: SdFolderViewModel = viewModel(key = "sd-$deviceId-${folderId.orEmpty()}") { SdFolderViewModel(app, deviceId, folderId) }
    val folder by vm.folder.collectAsState()
    val selecting by vm.selecting.collectAsState()
    val selection by vm.selection.collectAsState()
    val pendingTrash by vm.pendingTrash.collectAsState()
    val working by vm.working.collectAsState()
    val notice by vm.notice.collectAsState()
    val error by vm.error.collectAsState()
    val shownTitle = title?.takeIf { folderId != null } ?: stringResource(R.string.sd_title)
    val loaded = (folder as? Load.Loaded)?.value
    var menu by remember { mutableStateOf(false) }
    var naming by rememberSaveable { mutableStateOf(false) }
    val creator = rememberDocumentCreator()
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { uris ->
        if (uris.isNotEmpty()) vm.addFiles(uris, shownTitle)
    }
    BackHandler(enabled = selecting) { vm.finishSelecting() }
    HideTabBarWhile(selecting)

    AppScaffold(
        title = shownTitle,
        onBack = onBack,
        actions = {
            if (selecting) {
                TextButton(onClick = vm::finishSelecting, modifier = Modifier.testTag("sdSelectDone")) { Text(stringResource(R.string.sd_done)) }
            } else {
                IconButton(onClick = { vm.load() }) { Icon(Icons.Filled.Refresh, contentDescription = stringResource(R.string.refresh)) }
                if (loaded?.writable == true) {
                    Box {
                        IconButton(onClick = { menu = true }, modifier = Modifier.testTag("sdAdd")) {
                            Icon(Icons.Filled.Add, contentDescription = stringResource(R.string.add_files))
                        }
                        DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                            DropdownMenuItem(
                                text = { Text(stringResource(R.string.sd_add_files)) },
                                onClick = { menu = false; picker.launch(arrayOf("*/*")) },
                                modifier = Modifier.testTag("sdAddFiles"),
                            )
                            DropdownMenuItem(
                                text = { Text(stringResource(R.string.sd_new_folder)) },
                                onClick = { menu = false; naming = true },
                                modifier = Modifier.testTag("sdNewFolder"),
                            )
                        }
                    }
                }
                if (loaded?.entries?.any { it.deletable } == true) {
                    IconButton(onClick = vm::startSelecting, modifier = Modifier.testTag("sdSelect")) {
                        Icon(Icons.Filled.CheckCircle, contentDescription = stringResource(R.string.sd_select))
                    }
                }
            }
        },
        bottomBar = {
            if (selecting) {
                BottomAppBar {
                    Text(
                        pluralStringResource(R.plurals.screenshots_selected, selection.count, selection.count),
                        modifier = Modifier.weight(1f).padding(start = 16.dp),
                    )
                    TextButton(
                        onClick = { vm.askTrash(selection.entries) },
                        enabled = selection.count > 0,
                        modifier = Modifier.testTag("sdSelectTrash"),
                    ) { Text(stringResource(R.string.sd_trash)) }
                }
            }
        },
    ) { modifier ->
        Column(modifier.fillMaxSize()) {
            if (working) LinearProgressIndicator(Modifier.fillMaxWidth())
            MessageCard(notice, error, vm::dismissMessages)
            LoadContent(folder, onRetry = { vm.load() }) { content ->
                SdEntries(
                    content,
                    picking = false,
                    selecting = selecting,
                    selection = selection,
                    onOpen = onOpenFolder,
                    onToggle = vm::toggle,
                    onDownload = { entry -> creator.create(entry.name) { vm.download(entry, shownTitle, it) } },
                    onTrash = { vm.askTrash(listOf(it)) },
                )
            }
        }
    }
    if (pendingTrash.isNotEmpty()) {
        SdTrashDialog(pendingTrash, onConfirm = vm::trash, onDismiss = vm::cancelTrash)
    }
    if (naming) {
        NewFolderDialog(onCreate = { naming = false; vm.createFolder(it) }, onDismiss = { naming = false })
    }
}

/** The entries of a folder (browser or picker). */
@Composable
private fun SdEntries(
    folder: SdFolder,
    picking: Boolean,
    selecting: Boolean,
    selection: SdSelection,
    onOpen: (SdEntry) -> Unit,
    onToggle: (SdEntry) -> Unit = {},
    onDownload: (SdEntry) -> Unit = {},
    onTrash: (SdEntry) -> Unit = {},
    onPick: (SdEntry) -> Unit = {},
) {
    val context = LocalContext.current
    LazyColumn(Modifier.fillMaxSize().testTag("sdFolder")) {
        if (folder.entries.isEmpty()) {
            item { Text(stringResource(R.string.sd_empty), modifier = Modifier.padding(24.dp).testTag("sdEmpty")) }
        }
        items(folder.entries, key = { it.sdEntryId }) { entry ->
            val details = listOfNotNull(entry.sizeBytes?.takeIf { !entry.isFolder }?.let { formatBytes(context, it) }, formatDate(entry.modifiedAt))
                .joinToString(" · ")
            val icon = when {
                entry.isPakFolder || entry.isZip -> AppIcons.Package
                entry.isFolder -> AppIcons.Folder
                else -> AppIcons.File
            }
            val dimmed = (picking && !entry.isInstallableAsPak && !entry.isFolder) || (selecting && !entry.deletable)
            val click: (() -> Unit)? = when {
                selecting -> if (entry.deletable) ({ onToggle(entry) }) else null
                picking && entry.isInstallableAsPak -> ({ onPick(entry) })
                entry.isFolder -> ({ onOpen(entry) })
                else -> null
            }
            var menu by remember { mutableStateOf(false) }
            ListItem(
                leadingContent = {
                    if (selecting) {
                        Checkbox(checked = selection.contains(entry.sdEntryId), onCheckedChange = null, enabled = entry.deletable)
                    } else {
                        Icon(icon, contentDescription = null, tint = if (entry.isFolder) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                },
                headlineContent = { Text(entry.name) },
                supportingContent = { Text(details) },
                trailingContent = {
                    when {
                        picking && entry.isInstallableAsPak ->
                            Text(stringResource(R.string.sd_install), color = MaterialTheme.colorScheme.primary, style = MaterialTheme.typography.labelLarge)
                        !picking && !selecting && (entry.isFile || entry.deletable) -> Box {
                            IconButton(onClick = { menu = true }, modifier = Modifier.testTag("sdMenu-${entry.name}")) {
                                Icon(Icons.Filled.MoreVert, contentDescription = stringResource(R.string.more_actions))
                            }
                            DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                                if (entry.isFile) {
                                    DropdownMenuItem(
                                        text = { Text(stringResource(R.string.action_download)) },
                                        onClick = { menu = false; onDownload(entry) },
                                        modifier = Modifier.testTag("sdDownload"),
                                    )
                                }
                                if (entry.deletable) {
                                    DropdownMenuItem(
                                        text = { Text(stringResource(R.string.sd_trash_ellipsis)) },
                                        onClick = { menu = false; onTrash(entry) },
                                        modifier = Modifier.testTag("sdTrashOne"),
                                    )
                                }
                            }
                        }
                    }
                },
                modifier = Modifier
                    .then(if (click != null) Modifier.clickable(onClick = click) else Modifier)
                    .alpha(if (dimmed) 0.5f else 1f)
                    .testTag("sd-${entry.name}"),
            )
            HorizontalDivider()
        }
        item {
            Column(Modifier.padding(vertical = 8.dp)) {
                if (folder.truncated) Footnote(stringResource(R.string.sd_truncated), Modifier.testTag("sdTruncated"))
                when {
                    picking -> Footnote(stringResource(R.string.sd_pick_help))
                    folder.entries.none { it.version != null } && !folder.writable && folder.isRoot -> Footnote(stringResource(R.string.sd_old_pak_footer))
                    !folder.writable -> Footnote(stringResource(R.string.sd_read_only), Modifier.testTag("sdReadOnly"))
                    folder.isRoot -> Footnote(stringResource(R.string.sd_root_footer))
                }
            }
        }
    }
}

/** The confirmation of a move to the trash: what goes (five names at most), folders with all they hold, 30 days. */
@Composable
private fun SdTrashDialog(entries: List<SdEntry>, onConfirm: () -> Unit, onDismiss: () -> Unit) {
    val names = entries.take(5).joinToString("\n") { it.name }
    val more = if (entries.size > 5) "\n" + pluralStringResource(R.plurals.sd_trash_more, entries.size - 5, entries.size - 5) else ""
    val folders = if (entries.any { it.isFolder }) stringResource(R.string.sd_trash_folders) + " " else ""
    AlertDialog(
        onDismissRequest = onDismiss,
        title = {
            Text(
                if (entries.size == 1) stringResource(R.string.sd_trash_title_one)
                else pluralStringResource(R.plurals.sd_trash_title_many, entries.size, entries.size),
            )
        },
        text = {
            Text(
                names + more + "\n\n" + folders + stringResource(R.string.sd_trash_footer),
                modifier = Modifier.heightIn(max = 420.dp).verticalScroll(rememberScrollState()).testTag("sdTrashMessage"),
            )
        },
        confirmButton = {
            TextButton(onClick = onConfirm, modifier = Modifier.testTag("sdTrashConfirm")) { Text(stringResource(R.string.sd_trash)) }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_cancel)) } },
    )
}

@Composable
private fun NewFolderDialog(onCreate: (String) -> Unit, onDismiss: () -> Unit) {
    var name by rememberSaveable { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.sd_new_folder_title)) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(stringResource(R.string.sd_new_folder_message))
                OutlinedTextField(
                    value = name,
                    onValueChange = { name = it },
                    label = { Text(stringResource(R.string.sd_name)) },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth().testTag("sdNewFolderName"),
                )
            }
        },
        confirmButton = {
            TextButton(onClick = { onCreate(name) }, enabled = name.isNotBlank(), modifier = Modifier.testTag("sdNewFolderCreate")) {
                Text(stringResource(R.string.sd_create))
            }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_cancel)) } },
    )
}

/** A folder of the picker, opened from the root (id and title). */
private data class PickerFolder(val id: String?, val title: String)

/**
 * "Install from the SD card" (M12): the same browser in its own full-screen
 * dialog, where only zip archives and `.pak` folders can be chosen (tapping
 * a `.pak` folder chooses it), the other files dimmed.
 */
@Composable
fun SdPickerDialog(deviceId: String, onPick: (SdEntry) -> Unit, onDismiss: () -> Unit) {
    val app = LocalContext.current.applicationContext as Application
    val rootTitle = stringResource(R.string.sd_pick_title)
    var stack by rememberSaveable(
        saver = androidx.compose.runtime.saveable.listSaver(
            save = { s -> s.value.flatMap { listOf(it.id.orEmpty(), it.title) } },
            restore = { flat -> mutableStateOf(flat.chunked(2).map { PickerFolder(it[0].ifEmpty { null }, it[1]) }) },
        ),
    ) { mutableStateOf(listOf(PickerFolder(null, rootTitle))) }
    val current = stack.last()
    Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        BackHandler { if (stack.size > 1) stack = stack.dropLast(1) else onDismiss() }
        androidx.compose.material3.Surface(Modifier.fillMaxSize().testTag("sdPicker")) {
            val vm: SdFolderViewModel = viewModel(key = "sd-pick-$deviceId-${current.id.orEmpty()}") { SdFolderViewModel(app, deviceId, current.id) }
            val folder by vm.folder.collectAsState()
            AppScaffold(
                title = current.title,
                onBack = { if (stack.size > 1) stack = stack.dropLast(1) else onDismiss() },
                actions = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_cancel)) } },
            ) { modifier ->
                Box(modifier.fillMaxSize()) {
                    LoadContent(folder, onRetry = { vm.load() }) { content ->
                        SdEntries(
                            content,
                            picking = true,
                            selecting = false,
                            selection = SdSelection(),
                            onOpen = { stack = stack + PickerFolder(it.sdEntryId, it.name) },
                            onPick = onPick,
                        )
                    }
                }
            }
        }
    }
}


/** Hides the tab bar while [selecting] (a selection has its own bottom bar), as iOS does. */
@androidx.compose.runtime.Composable
fun HideTabBarWhile(selecting: Boolean) {
    androidx.compose.runtime.LaunchedEffect(selecting) { link.reminis.app.TabBar.hidden.value = selecting }
    androidx.compose.runtime.DisposableEffect(Unit) { onDispose { link.reminis.app.TabBar.hidden.value = false } }
}
