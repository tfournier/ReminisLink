package link.reminis.app.ui

import android.app.Application
import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.selection.selectable
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Build
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.AssistChip
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
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
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import link.reminis.app.LocalFiles
import link.reminis.app.PickedFile
import link.reminis.app.R
import link.reminis.core.Pak
import link.reminis.core.PakInstall
import link.reminis.core.PakInstallKind
import link.reminis.core.PakKinds
import link.reminis.core.PakList

/**
 * The apps of a console (M12, docs/specs/06-app-ios.md §11): its tool and
 * emulator paks, grouped, with their version and size. ReminisLink itself
 * is marked and cannot be deleted. Delete moves a pak to the trash of the
 * console after a confirmation. Install sends a zip chosen on the phone
 * through the transfer queue; the console installs it in a job.
 */
class PaksViewModel(app: Application, deviceId: String) : ActionViewModel(app, deviceId) {
    private val _paks = MutableStateFlow<Load<List<Pak>>>(Load.Loading)
    val paks: StateFlow<Load<List<Pak>>> = _paks.asStateFlow()

    /** The installations of this console. */
    val installs: StateFlow<List<PakInstall>> = container.pakInstaller.installs
        .map { list -> list.filter { it.deviceId == deviceId } }
        .stateIn(viewModelScope, SharingStarted.Eagerly, emptyList())

    /** A zip copied into the app, waiting for its options. */
    private val _draft = MutableStateFlow<PickedFile?>(null)
    val draft: StateFlow<PickedFile?> = _draft.asStateFlow()

    private var generation = 0
    private val handled = mutableSetOf<String>()

    init {
        load()
        // An installation that ended: the list is read again.
        viewModelScope.launch {
            installs.collect { list ->
                list.filter { it.phase == PakInstall.Phase.Installed && it.id !in handled }.forEach {
                    handled += it.id
                    load()
                }
            }
        }
    }

    /** `GET /paks`; only the last load publishes. A pak older than M12 (`404`) needs an update. */
    fun load() {
        val current = ++generation
        viewModelScope.launch {
            val result = try {
                Load.Loaded(container.client(deviceId).paks())
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                Load.Failed(pakError(e, routeMissing = true))
            }
            if (current == generation && (result is Load.Loaded || _paks.value !is Load.Loaded)) _paks.value = result
        }
    }

    /** True when the pak is older than M12: Install is disabled. */
    val needsNewerPak: Boolean
        get() = ((_paks.value as? Load.Failed)?.error as? UserMessage)?.id == R.string.pak_error_needs_newer

    /** `DELETE /paks/{pakId}`: the pak goes to the trash of the console; ReminisLink itself is never sent. */
    fun delete(pak: Pak) = perform(Notice(R.string.pak_deleted, listOf(pak.name))) {
        if (pak.isSelf) throw UserMessage(R.string.pak_error_self)
        try {
            container.client(deviceId).deletePak(pak.pakId)
        } catch (e: kotlinx.coroutines.CancellationException) {
            throw e
        } catch (e: Exception) {
            throw pakError(e)
        }
        load()
    }

    /** A zip chosen with the document picker, copied into the app; its options are asked next. */
    fun pick(uri: Uri) = perform(null) {
        val name = LocalFiles.displayName(getApplication<Application>().contentResolver, uri)
        if (!name.lowercase().endsWith(".zip")) throw UserMessage(R.string.pak_not_a_zip)
        _draft.value = LocalFiles.copyIn(getApplication(), uri, container.transferDirectory)
    }

    fun cancelDraft() {
        val file = _draft.value ?: return
        _draft.value = null
        viewModelScope.launch(Dispatchers.IO) { file.remove() }
    }

    /** Starts the installation (`onConflict: fail`); the copy is the installer's from now on. */
    fun install(kind: PakInstallKind) {
        val file = _draft.value ?: return
        _draft.value = null
        container.pakInstaller.install(deviceId, file.file, file.name, kind)
    }

    fun replace(install: PakInstall) = container.pakInstaller.replace(install.id)

    fun keepInstalled(install: PakInstall) = container.pakInstaller.keepInstalled(install.id)

    fun clearFinished() = container.pakInstaller.clearFinished(deviceId)

    suspend fun pollJobs() = container.jobs.refresh(deviceId)

    override fun onCleared() {
        _draft.value?.let { f -> container.backgroundScope.launch(Dispatchers.IO) { f.remove() } }
    }
}

@Composable
fun PaksScreen(deviceId: String, onBack: () -> Unit) {
    val app = LocalContext.current.applicationContext as Application
    val vm: PaksViewModel = viewModel(key = "paks-$deviceId") { PaksViewModel(app, deviceId) }
    val paks by vm.paks.collectAsState()
    val installs by vm.installs.collectAsState()
    val draft by vm.draft.collectAsState()
    val working by vm.working.collectAsState()
    val notice by vm.notice.collectAsState()
    val error by vm.error.collectAsState()
    val context = LocalContext.current
    var toDelete by remember { mutableStateOf<Pak?>(null) }
    var toReplace by remember { mutableStateOf<PakInstall?>(null) }
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri -> uri?.let(vm::pick) }

    // The jobs of the installations, every second while visible.
    PollWhileVisible(deviceId) { vm.pollJobs() }
    // A conflict asks once (the row keeps Replace… and Keep the Installed One).
    val asking = installs.firstOrNull { it.phase is PakInstall.Phase.NeedsReplacement }
    var asked by remember { mutableStateOf(setOf<String>()) }
    LaunchedEffect(asking?.id) {
        if (asking != null && asking.id !in asked) {
            asked = asked + asking.id
            toReplace = asking
        }
    }

    AppScaffold(
        title = stringResource(R.string.paks_title),
        onBack = onBack,
        actions = {
            IconButton(onClick = vm::load) { Icon(Icons.Filled.Refresh, contentDescription = stringResource(R.string.refresh)) }
        },
        floatingActionButton = {
            if (!vm.needsNewerPak) {
                ExtendedFloatingActionButton(
                    onClick = { picker.launch(arrayOf("application/zip", "application/x-zip-compressed", "application/octet-stream")) },
                    icon = { Icon(Icons.Filled.Add, contentDescription = null) },
                    text = { Text(stringResource(R.string.paks_install)) },
                    modifier = Modifier.testTag("paksInstall"),
                )
            }
        },
    ) { modifier ->
        LazyColumn(modifier.fillMaxSize().testTag("paks")) {
            item {
                if (working) LinearProgressIndicator(Modifier.fillMaxWidth())
                MessageCard(notice, error, vm::dismissMessages)
            }
            if (installs.isNotEmpty()) {
                item {
                    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                        Text(stringResource(R.string.paks_installations), style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(16.dp).weight(1f))
                        if (installs.any { it.phase.isFinished }) {
                            TextButton(onClick = vm::clearFinished) { Text(stringResource(R.string.transfers_clear)) }
                        }
                    }
                }
                items(installs, key = { it.id }) { install ->
                    PakInstallRow(install, onReplace = { toReplace = install }, onKeep = { vm.keepInstalled(install) })
                    HorizontalDivider()
                }
            }
            when (val p = paks) {
                Load.Loading -> item { LinearProgressIndicator(Modifier.fillMaxWidth().padding(16.dp)) }
                is Load.Failed -> item { ErrorView(p.error, onRetry = vm::load, modifier = Modifier.padding(16.dp)) }
                is Load.Loaded -> {
                    if (p.value.isEmpty()) item { Text(stringResource(R.string.paks_none), modifier = Modifier.padding(24.dp)) }
                    PakList.grouped(p.value).forEach { (kind, group) ->
                        item(key = "kind-$kind") {
                            SectionTitle(
                                when (kind) {
                                    PakKinds.TOOL -> stringResource(R.string.paks_tools)
                                    PakKinds.EMU -> stringResource(R.string.paks_emulators)
                                    else -> kind
                                },
                            )
                        }
                        items(group, key = { it.pakId }) { pak ->
                            ListItem(
                                leadingContent = { Icon(if (pak.kind == PakKinds.EMU) Icons.Filled.PlayArrow else Icons.Filled.Build, contentDescription = null) },
                                headlineContent = { Text(pak.name) },
                                supportingContent = { Text(listOfNotNull(pak.version, formatBytes(context, pak.sizeBytes)).joinToString(" · ")) },
                                overlineContent = if (pak.isSelf) {
                                    { Text(stringResource(R.string.paks_this_app), modifier = Modifier.testTag("pakSelf")) }
                                } else {
                                    null
                                },
                                trailingContent = {
                                    if (!pak.isSelf) {
                                        IconButton(onClick = { toDelete = pak }, enabled = !working, modifier = Modifier.testTag("pakDelete-${pak.name}")) {
                                            Icon(Icons.Filled.Delete, contentDescription = stringResource(R.string.action_delete_ellipsis))
                                        }
                                    }
                                },
                                modifier = Modifier.testTag("pak-${pak.name}"),
                            )
                        }
                        if (group.any { it.isSelf }) item(key = "self-$kind") { Footnote(stringResource(R.string.paks_self_footer)) }
                    }
                }
            }
        }
    }
    toDelete?.let { pak ->
        ConfirmDialog(
            title = stringResource(R.string.pak_delete_title),
            message = stringResource(R.string.pak_delete_message, pak.name),
            confirm = stringResource(R.string.pak_delete_confirm, pak.name),
            onConfirm = { vm.delete(pak) },
            onDismiss = { toDelete = null },
        )
    }
    toReplace?.let { install ->
        val name = (install.phase as? PakInstall.Phase.NeedsReplacement)?.name ?: install.name
        AlertDialog(
            onDismissRequest = { toReplace = null },
            title = { Text(stringResource(R.string.pak_replace_title, name)) },
            text = { Text(stringResource(R.string.pak_replace_message)) },
            confirmButton = {
                TextButton(onClick = { toReplace = null; vm.replace(install) }, modifier = Modifier.testTag("pakReplaceConfirm")) {
                    Text(stringResource(R.string.action_replace))
                }
            },
            dismissButton = {
                TextButton(onClick = { toReplace = null; vm.keepInstalled(install) }) { Text(stringResource(R.string.pak_keep_installed)) }
            },
        )
    }
    draft?.let { file -> PakInstallOptions(file, onInstall = vm::install, onCancel = vm::cancelDraft) }
}

@Composable
private fun PakInstallRow(install: PakInstall, onReplace: () -> Unit, onKeep: () -> Unit) {
    val context = LocalContext.current
    val phase = install.phase
    Column(Modifier.fillMaxWidth().padding(16.dp).testTag("pakInstall"), verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Text(install.name, style = MaterialTheme.typography.titleSmall)
        when (phase) {
            is PakInstall.Phase.Sending -> phase.fraction?.let { LinearProgressIndicator(progress = { it }, modifier = Modifier.fillMaxWidth()) }
            is PakInstall.Phase.Installing -> phase.fraction?.let { LinearProgressIndicator(progress = { it }, modifier = Modifier.fillMaxWidth()) }
                ?: LinearProgressIndicator(Modifier.fillMaxWidth())
            else -> Unit
        }
        val failure = phase is PakInstall.Phase.Failed || (phase is PakInstall.Phase.Sending && phase.failure != null)
        Text(
            when (phase) {
                is PakInstall.Phase.Sending -> when {
                    phase.failure != null -> errorMessage(context, phase.failure!!)
                    phase.paused -> stringResource(R.string.transfer_paused)
                    else -> stringResource(R.string.pak_sending)
                }
                is PakInstall.Phase.Installing -> stringResource(R.string.pak_installing)
                is PakInstall.Phase.NeedsReplacement -> stringResource(R.string.pak_already_installed, phase.name)
                PakInstall.Phase.Installed -> stringResource(R.string.pak_installed)
                is PakInstall.Phase.Failed -> errorMessage(context, pakError(phase.error))
                PakInstall.Phase.Cancelled -> stringResource(R.string.transfer_cancelled)
            },
            style = MaterialTheme.typography.bodySmall,
            color = if (failure) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.testTag("pakInstallStatus"),
        )
        if (phase is PakInstall.Phase.NeedsReplacement) {
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedButton(onClick = onReplace, modifier = Modifier.testTag("pakInstallReplace")) { Text(stringResource(R.string.pak_replace_ellipsis)) }
                OutlinedButton(onClick = onKeep) { Text(stringResource(R.string.pak_keep_installed)) }
            }
        }
    }
}

/**
 * The options of an installation: the file, and where it goes (automatic
 * by default: the console decides from the archive or its `pak.json`).
 */
@Composable
private fun PakInstallOptions(file: PickedFile, onInstall: (PakInstallKind) -> Unit, onCancel: () -> Unit) {
    val context = LocalContext.current
    var kind by remember { mutableStateOf(PakInstallKind.AUTO) }
    AlertDialog(
        onDismissRequest = onCancel,
        title = { Text(stringResource(R.string.pak_install_title)) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp), modifier = Modifier.testTag("pakInstallOptions")) {
                Text("${file.name} · ${formatBytes(context, file.sizeBytes)}")
                Text(stringResource(R.string.pak_install_file_help), style = MaterialTheme.typography.bodySmall)
                Text(stringResource(R.string.pak_install_as), style = MaterialTheme.typography.titleSmall)
                for (k in PakInstallKind.entries) {
                    Row(
                        verticalAlignment = Alignment.CenterVertically,
                        modifier = Modifier.fillMaxWidth().selectable(selected = kind == k) { kind = k },
                    ) {
                        RadioButton(selected = kind == k, onClick = null)
                        Text(
                            stringResource(
                                when (k) {
                                    PakInstallKind.AUTO -> R.string.pak_kind_auto
                                    PakInstallKind.TOOL -> R.string.pak_kind_tool
                                    PakInstallKind.EMU -> R.string.pak_kind_emu
                                },
                            ),
                            modifier = Modifier.padding(start = 8.dp),
                        )
                    }
                }
                Text(stringResource(R.string.pak_install_kind_help), style = MaterialTheme.typography.bodySmall)
                Text(stringResource(R.string.pak_install_trust), style = MaterialTheme.typography.bodySmall)
            }
        },
        confirmButton = {
            TextButton(onClick = { onInstall(kind) }, modifier = Modifier.testTag("pakInstallConfirm")) { Text(stringResource(R.string.paks_install)) }
        },
        dismissButton = { TextButton(onClick = onCancel) { Text(stringResource(R.string.action_cancel)) } },
    )
}
