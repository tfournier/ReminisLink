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
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Switch
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
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import link.reminis.app.LocalFiles
import link.reminis.app.PickedFile
import link.reminis.app.R
import link.reminis.core.ApiException
import link.reminis.core.CheatEditor
import link.reminis.core.CheatImportMode
import link.reminis.core.CheatImportRequest
import link.reminis.core.CheatImportResult
import link.reminis.core.CheatImportSource
import link.reminis.core.ErrorCodes
import link.reminis.core.FileDigest
import link.reminis.core.GameDetail
import link.reminis.core.OnConflict
import link.reminis.core.ResumableUpload
import link.reminis.core.SdFile
import link.reminis.core.SdFileType
import link.reminis.core.SdFiles
import link.reminis.core.UploadTarget

/** The cheat list as shown: a snapshot of the editor. */
data class CheatsUi(val entries: List<CheatEditor.Entry>, val hasChanges: Boolean, val originalEmpty: Boolean)

/** Where imported cheats come from. */
enum class CheatImportFrom { FILES, SD_CARD }

/** The Import sheet. */
data class CheatImportUi(
    val from: CheatImportFrom = CheatImportFrom.FILES,
    val mode: CheatImportMode = CheatImportMode.MERGE,
    val file: PickedFile? = null,
    val sdFiles: Load<List<SdFile>>? = null,
    val sdFileId: String? = null,
    val importing: Boolean = false,
    val error: Throwable? = null,
) {
    val canImport: Boolean get() = !importing && when (from) {
        CheatImportFrom.FILES -> file != null
        CheatImportFrom.SD_CARD -> sdFileId != null
    }
}

/**
 * The cheats of a game (docs/specs/06-app-ios.md §4.5): switches, add,
 * edit, delete, reorder, import. Changes stay on this phone until saved;
 * the whole list is then sent with `If-Match`. If the file changed on the
 * console meanwhile (`412`), nothing is overwritten: the user can only
 * reload, which discards the changes.
 */
class CheatsViewModel(app: Application, deviceId: String, private val gameId: String) : ActionViewModel(app, deviceId) {
    private var editor: CheatEditor? = null
    private val _ui = MutableStateFlow<Load<CheatsUi>>(Load.Loading)
    val ui: StateFlow<Load<CheatsUi>> = _ui.asStateFlow()
    private val _game = MutableStateFlow<GameDetail?>(null)
    val game: StateFlow<GameDetail?> = _game.asStateFlow()

    /** The save was refused with `412`: ask to reload. */
    private val _conflict = MutableStateFlow(false)
    val conflict: StateFlow<Boolean> = _conflict.asStateFlow()

    private val _import = MutableStateFlow<CheatImportUi?>(null)
    val import: StateFlow<CheatImportUi?> = _import.asStateFlow()

    init {
        load()
    }

    /** Replaces the list (first load, Reload after a conflict, after an import): local edits are dropped. */
    fun load() {
        if (_ui.value !is Load.Loaded) _ui.value = Load.Loading
        _conflict.value = false
        viewModelScope.launch {
            try {
                val client = container.client(deviceId)
                if (_game.value == null) _game.value = runCatching { client.game(gameId) }.getOrNull()
                val list = client.cheats(gameId)
                editor?.load(list) ?: run { editor = CheatEditor(list) }
                publish()
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                if (_ui.value is Load.Loaded) showError(e) else _ui.value = Load.Failed(e)
            }
        }
    }

    private fun publish() {
        val e = editor ?: return
        _ui.value = Load.Loaded(CheatsUi(e.entries, e.hasChanges, e.original.isEmpty()))
    }

    private fun edit(change: (CheatEditor) -> Unit) {
        editor?.let(change)
        publish()
    }

    fun setEnabled(id: String, enabled: Boolean) = edit { it.setEnabled(id, enabled) }

    fun add(description: String, code: String, enabled: Boolean): Boolean {
        val ok = editor?.add(description, code, enabled) == true
        publish()
        return ok
    }

    fun update(id: String, description: String, code: String, enabled: Boolean): Boolean {
        val ok = editor?.update(id, description, code, enabled) == true
        publish()
        return ok
    }

    fun remove(id: String) = edit { it.remove(id) }

    fun move(id: String, by: Int) = edit { it.move(id, by) }

    fun discardChanges() {
        edit { it.discardChanges() }
        dismissMessages()
    }

    /** Sends the whole list with `If-Match`; `412`: the list changed on the console, nothing is overwritten. */
    fun save() {
        val e = editor ?: return
        val update = e.update()
        val etag = e.etag
        perform(Notice(R.string.cheats_saved)) {
            try {
                val saved = container.client(deviceId).replaceCheats(gameId, update, etag)
                e.load(saved)
                publish()
            } catch (error: ApiException.Server) {
                if (CheatEditor.isConflict(error)) {
                    _conflict.value = true
                    showNotice(Notice(R.string.cheats_not_saved))
                    return@perform
                }
                throw error
            }
        }
    }

    fun dismissConflict() {
        _conflict.value = false
    }

    private fun showError(e: Throwable) = perform(null) { throw e }

    // MARK: import

    fun openImport() {
        _import.value = CheatImportUi()
    }

    fun closeImport() {
        val current = _import.value ?: return
        if (current.importing) return
        current.file?.let { f -> viewModelScope.launch(Dispatchers.IO) { f.remove() } }
        _import.value = null
    }

    fun setImportFrom(from: CheatImportFrom) {
        _import.update { it?.copy(from = from, error = null) }
        if (from == CheatImportFrom.SD_CARD && _import.value?.sdFiles == null) loadSdFiles()
    }

    fun setImportMode(mode: CheatImportMode) = _import.update { it?.copy(mode = mode) }

    fun chooseSdFile(id: String) = _import.update { it?.copy(sdFileId = id, error = null) }

    private fun loadSdFiles() {
        _import.update { it?.copy(sdFiles = Load.Loading) }
        viewModelScope.launch {
            val result = try {
                val files = container.client(deviceId).sdFiles(SdFileType.CHT)
                val game = _game.value
                Load.Loaded(if (game != null) SdFiles.sortedForGame(files, game.fileName, game.name) else files)
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                Load.Failed(e)
            }
            _import.update { it?.copy(sdFiles = result) }
        }
    }

    /** A `.cht` chosen with the document picker, copied into the app. */
    fun pickFile(uri: Uri) {
        viewModelScope.launch {
            try {
                val picked = LocalFiles.copyIn(getApplication(), uri, container.transferDirectory)
                val previous = _import.value?.file
                _import.update { it?.copy(file = picked, error = null) }
                previous?.let { withContext(Dispatchers.IO) { it.remove() } }
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                _import.update { it?.copy(error = e) }
            }
        }
    }

    /**
     * A file of the phone is sent to the staging area (resumable upload,
     * then `source.type = "staging"`); a file of the SD card is used by its
     * id. `422 unsupported_format`: no cheat in the file.
     */
    fun runImport(onDone: (CheatImportResult) -> Unit = {}) {
        val state = _import.value ?: return
        if (!state.canImport) return
        _import.update { it?.copy(importing = true, error = null) }
        viewModelScope.launch {
            try {
                val source = when (state.from) {
                    CheatImportFrom.FILES -> CheatImportSource.Staging(stage(state.file!!))
                    CheatImportFrom.SD_CARD -> CheatImportSource.Sd(state.sdFileId!!)
                }
                val result = container.client(deviceId).importCheats(gameId, CheatImportRequest(source, state.mode))
                _import.value = null
                showNotice(Notice(R.string.cheats_imported, listOf(result.imported, result.total)))
                load()
                onDone(result)
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                val shown = if ((e as? ApiException.Server)?.code == ErrorCodes.UNSUPPORTED_FORMAT) {
                    UserMessage(R.string.cheats_import_no_cheat, cause = e)
                } else {
                    e
                }
                _import.update { it?.copy(importing = false, error = shown, file = if (state.from == CheatImportFrom.FILES) null else it.file) }
            }
        }
    }

    /** Sends the file to the staging area; the local copy is deleted however it ends. */
    private suspend fun stage(file: PickedFile): String {
        val upload = ResumableUpload(
            file.file, file.sizeBytes, FileDigest.sha256(file.file), UploadTarget.Staging(file.name), OnConflict.RENAME,
        )
        try {
            val result = upload.run(container.client(deviceId))
            return result.stagingId ?: throw ApiException.InvalidResponse("no stagingId")
        } catch (e: Exception) {
            runCatching { upload.cancel(container.client(deviceId)) }
            throw e
        } finally {
            withContext(Dispatchers.IO) { file.remove() }
        }
    }

    override fun onCleared() {
        _import.value?.file?.let { f -> container.backgroundScope.launch(Dispatchers.IO) { f.remove() } }
    }
}

@Composable
fun CheatsScreen(deviceId: String, gameId: String, onBack: () -> Unit) {
    val app = LocalContext.current.applicationContext as Application
    val vm: CheatsViewModel = viewModel(key = "cheats-$deviceId-$gameId") { CheatsViewModel(app, deviceId, gameId) }
    val ui by vm.ui.collectAsState()
    val working by vm.working.collectAsState()
    val notice by vm.notice.collectAsState()
    val error by vm.error.collectAsState()
    val conflict by vm.conflict.collectAsState()
    val import by vm.import.collectAsState()
    val hasChanges = (ui as? Load.Loaded)?.value?.hasChanges == true
    var editing by remember { mutableStateOf<CheatEditor.Entry?>(null) }
    var adding by remember { mutableStateOf(false) }
    var menu by remember { mutableStateOf(false) }
    var confirmDiscard by remember { mutableStateOf(false) }
    var confirmEmpty by remember { mutableStateOf(false) }

    // Unsaved changes are never lost by going back: save or discard first.
    BackHandler(enabled = hasChanges) { confirmDiscard = true }
    AppScaffold(
        title = stringResource(R.string.cheats_title),
        onBack = { if (hasChanges) confirmDiscard = true else onBack() },
        actions = {
            if (hasChanges) {
                TextButton(onClick = { confirmDiscard = true }, enabled = !working, modifier = Modifier.testTag("cheatsDiscard")) {
                    Text(stringResource(R.string.cheats_discard))
                }
                TextButton(
                    onClick = {
                        val loaded = (ui as? Load.Loaded)?.value
                        if (loaded != null && loaded.entries.isEmpty() && !loaded.originalEmpty) confirmEmpty = true else vm.save()
                    },
                    enabled = !working,
                    modifier = Modifier.testTag("cheatsSave"),
                ) { Text(stringResource(R.string.action_save)) }
            } else {
                IconButton(onClick = vm::load) { Icon(Icons.Filled.Refresh, contentDescription = stringResource(R.string.refresh)) }
                IconButton(onClick = { menu = true }, enabled = ui is Load.Loaded, modifier = Modifier.testTag("cheatsAdd")) {
                    Icon(Icons.Filled.Add, contentDescription = stringResource(R.string.cheats_add_menu))
                }
                DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                    DropdownMenuItem(
                        text = { Text(stringResource(R.string.cheats_add)) },
                        onClick = { menu = false; adding = true },
                        modifier = Modifier.testTag("cheatsAddCheat"),
                    )
                    DropdownMenuItem(
                        text = { Text(stringResource(R.string.cheats_import_action)) },
                        onClick = { menu = false; vm.openImport() },
                        modifier = Modifier.testTag("cheatsImport"),
                    )
                }
            }
        },
    ) { modifier ->
        LoadContent(ui, onRetry = vm::load, modifier = modifier) { cheats ->
            LazyColumn(Modifier.then(modifier).fillMaxSize().testTag("cheatsScreen")) {
                item {
                    if (working) LinearProgressIndicator(Modifier.fillMaxWidth())
                    MessageCard(notice, error, vm::dismissMessages)
                    if (cheats.entries.isEmpty()) {
                        Text(stringResource(R.string.cheats_empty), modifier = Modifier.padding(16.dp).testTag("cheatsEmpty"))
                    } else {
                        SectionTitle(pluralStringResource(R.plurals.cheats_count, cheats.entries.size, cheats.entries.size))
                    }
                }
                itemsIndexed(cheats.entries, key = { _, e -> e.id }) { index, entry ->
                    CheatRow(
                        entry, index, cheats.entries.size, enabled = !working,
                        onToggle = { vm.setEnabled(entry.id, it) }, onEdit = { editing = entry },
                        onMove = { vm.move(entry.id, it) }, onDelete = { vm.remove(entry.id) },
                    )
                    HorizontalDivider()
                }
                item {
                    when {
                        cheats.hasChanges -> Footnote(stringResource(R.string.cheats_unsaved), Modifier.testTag("cheatsUnsaved"))
                        cheats.entries.isNotEmpty() -> Footnote(stringResource(R.string.cheats_footer))
                    }
                }
            }
        }
    }
    editing?.let { entry ->
        CheatEditDialog(entry, onSave = { d, c, e -> vm.update(entry.id, d, c, e) }, onDismiss = { editing = null })
    }
    if (adding) CheatEditDialog(null, onSave = vm::add, onDismiss = { adding = false })
    import?.let { CheatImportDialog(vm, it) }
    if (confirmDiscard) {
        ConfirmDialog(
            title = stringResource(R.string.cheats_discard_title),
            message = stringResource(R.string.cheats_discard_message),
            confirm = stringResource(R.string.cheats_discard_confirm),
            onConfirm = vm::discardChanges,
            onDismiss = { confirmDiscard = false },
        )
    }
    if (confirmEmpty) {
        ConfirmDialog(
            title = stringResource(R.string.cheats_empty_title),
            message = stringResource(R.string.cheats_empty_message),
            confirm = stringResource(R.string.cheats_empty_confirm),
            onConfirm = vm::save,
            onDismiss = { confirmEmpty = false },
        )
    }
    if (conflict) {
        ConfirmDialog(
            title = stringResource(R.string.cheats_conflict_title),
            message = stringResource(R.string.cheats_conflict_message),
            confirm = stringResource(R.string.cheats_reload),
            onConfirm = vm::load,
            onDismiss = vm::dismissConflict,
        )
    }
}

@Composable
private fun CheatRow(
    entry: CheatEditor.Entry,
    index: Int,
    count: Int,
    enabled: Boolean,
    onToggle: (Boolean) -> Unit,
    onEdit: () -> Unit,
    onMove: (Int) -> Unit,
    onDelete: () -> Unit,
) {
    var menu by remember { mutableStateOf(false) }
    ListItem(
        headlineContent = { Text(entry.cheat.description) },
        supportingContent = { Text(entry.cheat.code, fontFamily = FontFamily.Monospace, maxLines = 2) },
        trailingContent = {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Switch(
                    checked = entry.cheat.enabled,
                    onCheckedChange = onToggle,
                    enabled = enabled,
                    modifier = Modifier.testTag("cheatToggle$index"),
                )
                IconButton(onClick = { menu = true }, enabled = enabled, modifier = Modifier.testTag("cheatMenu$index")) {
                    Icon(Icons.Filled.MoreVert, contentDescription = stringResource(R.string.more_actions))
                }
                DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                    DropdownMenuItem(text = { Text(stringResource(R.string.cheats_edit_action)) }, onClick = { menu = false; onEdit() })
                    if (index > 0) {
                        DropdownMenuItem(text = { Text(stringResource(R.string.cheats_move_up)) }, onClick = { menu = false; onMove(-1) })
                    }
                    if (index < count - 1) {
                        DropdownMenuItem(text = { Text(stringResource(R.string.cheats_move_down)) }, onClick = { menu = false; onMove(1) })
                    }
                    DropdownMenuItem(
                        text = { Text(stringResource(R.string.action_delete)) },
                        onClick = { menu = false; onDelete() },
                        modifier = Modifier.testTag("cheatDelete$index"),
                    )
                }
            }
        },
        modifier = Modifier.clickable(enabled = enabled, onClick = onEdit).testTag("cheat$index"),
    )
}

/** Adds or edits a cheat: description, code, on or off. */
@Composable
private fun CheatEditDialog(entry: CheatEditor.Entry?, onSave: (String, String, Boolean) -> Boolean, onDismiss: () -> Unit) {
    var description by remember { mutableStateOf(entry?.cheat?.description.orEmpty()) }
    var code by remember { mutableStateOf(entry?.cheat?.code.orEmpty()) }
    var enabled by remember { mutableStateOf(entry?.cheat?.enabled ?: true) }
    val valid = CheatEditor.validated(description, code) != null
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(if (entry == null) R.string.cheats_new_title else R.string.cheats_edit_title)) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp), modifier = Modifier.verticalScroll(rememberScrollState())) {
                OutlinedTextField(
                    value = description,
                    onValueChange = { description = it },
                    label = { Text(stringResource(R.string.cheats_description)) },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth().testTag("cheatDescription"),
                )
                OutlinedTextField(
                    value = code,
                    onValueChange = { code = it },
                    label = { Text(stringResource(R.string.cheats_code)) },
                    textStyle = MaterialTheme.typography.bodyLarge.copy(fontFamily = FontFamily.Monospace),
                    modifier = Modifier.fillMaxWidth().testTag("cheatCode"),
                )
                Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.fillMaxWidth()) {
                    Text(stringResource(R.string.cheats_enabled), modifier = Modifier.weight(1f))
                    Switch(checked = enabled, onCheckedChange = { enabled = it }, modifier = Modifier.testTag("cheatEnabled"))
                }
                Text(stringResource(R.string.cheats_code_help), style = MaterialTheme.typography.bodySmall)
                if (entry != null && entry.cheat.extra.isNotEmpty()) {
                    Text(stringResource(R.string.cheats_extra_kept), style = MaterialTheme.typography.bodySmall)
                }
            }
        },
        confirmButton = {
            TextButton(
                onClick = { if (onSave(description, code, enabled)) onDismiss() },
                enabled = valid,
                modifier = Modifier.testTag("cheatDone"),
            ) { Text(stringResource(if (entry == null) R.string.cheats_add_confirm else R.string.cheats_done)) }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_cancel)) } },
    )
}

/**
 * Imports cheats into the game (docs/specs/04-api-v1.md, `cheats/import`):
 * from a file of the phone (Storage Access Framework, sent to the staging
 * area first) or a `.cht` of the SD card; added to the list or replacing it.
 */
@Composable
private fun CheatImportDialog(vm: CheatsViewModel, state: CheatImportUi) {
    val context = LocalContext.current
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri -> uri?.let(vm::pickFile) }
    var confirmReplace by remember { mutableStateOf(false) }
    AlertDialog(
        onDismissRequest = vm::closeImport,
        title = { Text(stringResource(R.string.cheats_import_title)) },
        text = {
            Column(
                verticalArrangement = Arrangement.spacedBy(8.dp),
                modifier = Modifier.heightIn(max = 520.dp).verticalScroll(rememberScrollState()).testTag("cheatImport"),
            ) {
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    FilterChip(
                        selected = state.from == CheatImportFrom.FILES,
                        onClick = { vm.setImportFrom(CheatImportFrom.FILES) },
                        enabled = !state.importing,
                        label = { Text(stringResource(R.string.source_files)) },
                    )
                    FilterChip(
                        selected = state.from == CheatImportFrom.SD_CARD,
                        onClick = { vm.setImportFrom(CheatImportFrom.SD_CARD) },
                        enabled = !state.importing,
                        label = { Text(stringResource(R.string.source_sd_card)) },
                        modifier = Modifier.testTag("importFromSd"),
                    )
                }
                state.error?.let {
                    Text(errorMessage(context, it), color = MaterialTheme.colorScheme.error, modifier = Modifier.testTag("importError"))
                }
                when (state.from) {
                    CheatImportFrom.FILES -> {
                        state.file?.let {
                            Text("${it.name} · ${formatBytes(context, it.sizeBytes)}", modifier = Modifier.testTag("importFile"))
                        }
                        OutlinedButton(
                            onClick = { picker.launch(arrayOf("*/*")) },
                            enabled = !state.importing,
                            modifier = Modifier.fillMaxWidth().testTag("importChoose"),
                        ) { Text(stringResource(if (state.file == null) R.string.choose_file else R.string.choose_another_file)) }
                        Text(stringResource(R.string.cheats_import_files_help), style = MaterialTheme.typography.bodySmall)
                    }
                    CheatImportFrom.SD_CARD -> SdFilePicker(
                        state.sdFiles, state.sdFileId, enabled = !state.importing, onPick = vm::chooseSdFile,
                        empty = stringResource(R.string.cheats_import_sd_empty), footer = stringResource(R.string.cheats_import_sd_help),
                    )
                }
                HorizontalDivider()
                for (mode in CheatImportMode.entries) {
                    Row(
                        verticalAlignment = Alignment.CenterVertically,
                        modifier = Modifier.fillMaxWidth()
                            .selectable(selected = state.mode == mode, enabled = !state.importing) { vm.setImportMode(mode) },
                    ) {
                        RadioButton(selected = state.mode == mode, onClick = null)
                        Text(
                            stringResource(if (mode == CheatImportMode.MERGE) R.string.cheats_import_merge else R.string.cheats_import_replace),
                            modifier = Modifier.padding(start = 8.dp),
                        )
                    }
                }
                Text(
                    stringResource(
                        if (state.mode == CheatImportMode.MERGE) R.string.cheats_import_merge_help else R.string.cheats_import_replace_help,
                    ),
                    style = MaterialTheme.typography.bodySmall,
                )
                if (state.importing) LinearProgressIndicator(Modifier.fillMaxWidth())
            }
        },
        confirmButton = {
            TextButton(
                onClick = { if (state.mode == CheatImportMode.REPLACE) confirmReplace = true else vm.runImport() },
                enabled = state.canImport,
                modifier = Modifier.testTag("importRun"),
            ) { Text(stringResource(R.string.cheats_import_run)) }
        },
        dismissButton = {
            TextButton(onClick = vm::closeImport, enabled = !state.importing) { Text(stringResource(R.string.action_cancel)) }
        },
    )
    if (confirmReplace) {
        ConfirmDialog(
            title = stringResource(R.string.cheats_import_replace_title),
            message = stringResource(R.string.cheats_import_replace_message),
            confirm = stringResource(R.string.cheats_import_replace),
            onConfirm = { vm.runImport() },
            onDismiss = { confirmReplace = false },
        )
    }
}

/** A choice among the files of `GET /sd/files` (the file name, then its informative path). */
@Composable
fun SdFilePicker(files: Load<List<SdFile>>?, selected: String?, enabled: Boolean, onPick: (String) -> Unit, empty: String, footer: String) {
    val context = LocalContext.current
    when (files) {
        null, Load.Loading -> LinearProgressIndicator(Modifier.fillMaxWidth())
        is Load.Failed -> Text(errorMessage(context, files.error), color = MaterialTheme.colorScheme.error)
        is Load.Loaded -> {
            if (files.value.isEmpty()) Text(empty, style = MaterialTheme.typography.bodyMedium)
            files.value.forEach { file ->
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    modifier = Modifier.fillMaxWidth().selectable(selected = selected == file.id, enabled = enabled) { onPick(file.id) }
                        .testTag("sdFile"),
                ) {
                    RadioButton(selected = selected == file.id, onClick = null)
                    Column(Modifier.padding(start = 8.dp)) {
                        Text(file.fileName)
                        Text(file.path, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace)
                    }
                }
            }
        }
    }
    Text(footer, style = MaterialTheme.typography.bodySmall)
}
