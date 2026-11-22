package link.reminis.app.ui

import android.app.Application
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Checkbox
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.RadioButton
import androidx.compose.material3.SwipeToDismissBox
import androidx.compose.material3.SwipeToDismissBoxValue
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberSwipeToDismissBoxState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import link.reminis.app.R
import link.reminis.core.FolderListing
import link.reminis.core.GameSummary
import link.reminis.core.GameSystem
import link.reminis.core.LibraryFiltering
import link.reminis.core.LibraryQuery
import link.reminis.core.LibrarySearch
import link.reminis.core.LibrarySort
import link.reminis.core.SystemGroup

// Browse by console (iOS 06 §4.4): the systems, then a system's folders and
// games (`GET /systems/{systemId}/games`). Also the Library itself when the
// flat list cannot be shown (more than 20 000 games, or a pak older than
// M11), and the "Filter and Sort" options of the flat list.

/** The systems of a console with their game count (`GET /systems`). */
class SystemsViewModel(app: Application, deviceId: String) : ActionViewModel(app, deviceId) {
    private val _systems = MutableStateFlow<Load<List<GameSystem>>>(Load.Loading)
    val systems: StateFlow<Load<List<GameSystem>>> = _systems.asStateFlow()
    private var generation = 0

    init {
        load()
        reloadOnLibraryChanges { load(quiet = true) }
    }

    fun load(quiet: Boolean = false) {
        val current = ++generation
        if (!quiet || _systems.value !is Load.Loaded) _systems.value = Load.Loading
        viewModelScope.launch {
            val result = try {
                Load.Loaded(container.client(deviceId).systems().sortedWith { a, b -> link.reminis.core.NaturalOrder.compare(a.label, b.label) })
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                Load.Failed(e)
            }
            if (current == generation && (!quiet || result is Load.Loaded || _systems.value !is Load.Loaded)) _systems.value = result
        }
    }
}

/** The systems list ("Browse by Console"), as a screen of the Library tab. */
@Composable
fun SystemsScreen(deviceId: String, onBack: () -> Unit, onOpen: (GameSystem) -> Unit) {
    AppScaffold(title = stringResource(R.string.library_by_console), onBack = onBack) { modifier ->
        Box(modifier.fillMaxSize()) { SystemsList(deviceId, onOpen, footer = null) }
    }
}

/** The systems of a console, each with its game count (and a note when its emulator is missing). */
@Composable
fun SystemsList(deviceId: String, onOpen: (GameSystem) -> Unit, footer: String?) {
    val app = LocalContext.current.applicationContext as Application
    val vm: SystemsViewModel = viewModel(key = "systems-$deviceId") { SystemsViewModel(app, deviceId) }
    val systems by vm.systems.collectAsState()
    LoadContent(systems, onRetry = { vm.load() }) { list ->
        LazyColumn(Modifier.fillMaxSize().testTag("systems")) {
            footer?.let { item { Footnote(it, Modifier.testTag("libraryTruncated")) } }
            if (list.isEmpty()) item { Text(stringResource(R.string.library_empty), modifier = Modifier.padding(24.dp)) }
            items(list, key = { it.id }) { system ->
                ListItem(
                    headlineContent = { Text(system.label) },
                    supportingContent = if (system.emulatorInstalled) null else ({ Text(stringResource(R.string.library_emulator_missing)) }),
                    trailingContent = {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            Text(pluralStringResource(R.plurals.library_game_count, system.gameCount, system.gameCount))
                            Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, contentDescription = null)
                        }
                    },
                    modifier = Modifier.clickable { onOpen(system) }.testTag("system-${system.tag}"),
                )
                HorizontalDivider()
            }
        }
    }
}

/** One folder of a system: its subfolders and its games, searched and sorted here. */
class FolderViewModel(app: Application, deviceId: String, private val systemId: String, private val folderId: String?) :
    ActionViewModel(app, deviceId) {
    private val _listing = MutableStateFlow<Load<FolderListing>>(Load.Loading)
    val listing: StateFlow<Load<FolderListing>> = _listing.asStateFlow()
    val query = MutableStateFlow("")
    val sort = MutableStateFlow(LibrarySort.NAME)
    private var generation = 0

    init {
        load()
        // M14: a change of another system leaves this folder as it is.
        onConsoleChanges { change ->
            when (change) {
                is link.reminis.app.ConsoleChange.Library -> if (change.affects(systemId)) load(quiet = true)
                is link.reminis.app.ConsoleChange.CatchUp -> load(quiet = true)
                else -> Unit
            }
        }
        viewModelScope.launch { container.libraryChanged.collect { if (it == deviceId) load(quiet = true) } }
    }

    fun load(quiet: Boolean = false) {
        val current = ++generation
        if (!quiet || _listing.value !is Load.Loaded) _listing.value = Load.Loading
        viewModelScope.launch {
            val result = try {
                Load.Loaded(container.client(deviceId).systemGames(systemId, folderId))
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                Load.Failed(e)
            }
            if (current == generation && (!quiet || result is Load.Loaded || _listing.value !is Load.Loaded)) _listing.value = result
        }
    }

    fun shown(listing: FolderListing, query: String, sort: LibrarySort): List<GameSummary> = LibraryFiltering.sorted(
        listing.games.filter { LibrarySearch.matches(it.name, query) || LibrarySearch.matches(it.fileName, query) },
        sort, { it.name }, { it.modifiedAt }, { it.sizeBytes },
    )
}

@Composable
fun FolderScreen(
    deviceId: String,
    systemId: String,
    folderId: String?,
    title: String,
    onBack: () -> Unit,
    onFolder: (link.reminis.core.LibraryFolder) -> Unit,
    onGame: (String) -> Unit,
) {
    val app = LocalContext.current.applicationContext as Application
    val vm: FolderViewModel = viewModel(key = "folder-$deviceId-$systemId-${folderId.orEmpty()}") { FolderViewModel(app, deviceId, systemId, folderId) }
    val listing by vm.listing.collectAsState()
    val query by vm.query.collectAsState()
    val sort by vm.sort.collectAsState()
    var sortMenu by remember { mutableStateOf(false) }
    var deleting by rememberSaveable { mutableStateOf<String?>(null) }
    var deletingName by rememberSaveable { mutableStateOf("") }
    AppScaffold(
        title = title,
        onBack = onBack,
        actions = {
            IconButton(onClick = { vm.load() }) { Icon(Icons.Filled.Refresh, contentDescription = stringResource(R.string.refresh)) }
            Box {
                TextButton(onClick = { sortMenu = true }, modifier = Modifier.testTag("folderSort")) { Text(stringResource(R.string.library_sort_by)) }
                DropdownMenu(expanded = sortMenu, onDismissRequest = { sortMenu = false }) {
                    for (s in LibrarySort.entries) {
                        DropdownMenuItem(
                            text = { Text(sortLabel(s)) },
                            leadingIcon = { RadioButton(selected = sort == s, onClick = null) },
                            onClick = { vm.sort.value = s; sortMenu = false },
                        )
                    }
                }
            }
        },
    ) { modifier ->
        LoadContent(listing, onRetry = { vm.load() }, modifier = modifier) { content ->
            val games = remember(content, query, sort) { vm.shown(content, query, sort) }
            LazyColumn(modifier.fillMaxSize().testTag("folder")) {
                item {
                    OutlinedTextField(
                        value = query,
                        onValueChange = { vm.query.value = it },
                        leadingIcon = { Icon(Icons.Filled.Search, contentDescription = null) },
                        placeholder = { Text(stringResource(R.string.library_search_folder)) },
                        singleLine = true,
                        modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp),
                    )
                }
                if (content.folders.isNotEmpty()) {
                    item { SectionTitle(stringResource(R.string.library_folders)) }
                    items(content.folders, key = { "f-" + it.id }) { folder ->
                        ListItem(
                            leadingContent = { Icon(AppIcons.Folder, contentDescription = null, tint = MaterialTheme.colorScheme.primary) },
                            headlineContent = { Text(folder.name) },
                            trailingContent = { Text(pluralStringResource(R.plurals.library_game_count, folder.gameCount, folder.gameCount)) },
                            modifier = Modifier.clickable { onFolder(folder) }.testTag("folder-${folder.name}"),
                        )
                        HorizontalDivider()
                    }
                }
                if (games.isEmpty() && content.folders.isEmpty()) {
                    item { Text(stringResource(R.string.library_folder_empty), modifier = Modifier.padding(24.dp)) }
                }
                items(games, key = { it.id }) { game ->
                    SwipeToDelete(onDelete = { deletingName = game.name; deleting = game.id }) {
                        ListItem(
                            leadingContent = { link.reminis.app.GameArt(deviceId, game.id, game.hasArt, 44.dp) },
                            headlineContent = { Text(game.name) },
                            supportingContent = { Text(gameBadges(game.hasSave, game.stateCount, game.cheatCount, LocalContext.current.let { formatBytes(it, game.sizeBytes) })) },
                            modifier = Modifier.clickable { onGame(game.id) }.testTag("folderGame"),
                        )
                    }
                    HorizontalDivider()
                }
            }
        }
    }
    deleting?.let { id -> DeleteGameFromList(deviceId, id, deletingName, onDone = { deleting = null }) }
}

/** The badges of a game row: save, states, cheats, then [extra]. */
@Composable
fun gameBadges(hasSave: Boolean, stateCount: Int, cheatCount: Int, vararg extra: String?): String = buildList {
    extra.filterNotNull().filter { it.isNotEmpty() }.forEach(::add)
    if (hasSave) add(stringResource(R.string.has_save))
    if (stateCount > 0) add(pluralStringResource(R.plurals.state_count, stateCount, stateCount))
    if (cheatCount > 0) add(pluralStringResource(R.plurals.cheat_count, cheatCount, cheatCount))
}.joinToString(" · ")

@Composable
fun sortLabel(sort: LibrarySort): String = stringResource(
    when (sort) {
        LibrarySort.NAME -> R.string.library_sort_name
        LibrarySort.DATE -> R.string.library_sort_date
        LibrarySort.SIZE -> R.string.library_sort_size
    },
)

/** A row that a swipe to the start deletes (after the confirmation of [onDelete]); the row stays meanwhile. */
@Composable
fun SwipeToDelete(onDelete: () -> Unit, content: @Composable () -> Unit) {
    val state = rememberSwipeToDismissBoxState(
        confirmValueChange = { value ->
            if (value == SwipeToDismissBoxValue.EndToStart) onDelete()
            // Never dismissed here: the row goes only when the console moved the game.
            false
        },
    )
    SwipeToDismissBox(
        state = state,
        enableDismissFromStartToEnd = false,
        backgroundContent = {
            Box(
                Modifier.fillMaxSize().background(MaterialTheme.colorScheme.errorContainer).padding(horizontal = 24.dp),
                contentAlignment = Alignment.CenterEnd,
            ) {
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Icon(Icons.Filled.Delete, contentDescription = null, tint = MaterialTheme.colorScheme.onErrorContainer)
                    Text(stringResource(R.string.action_delete), color = MaterialTheme.colorScheme.onErrorContainer)
                }
            }
        },
        modifier = Modifier.testTag("swipeToDelete"),
    ) {
        Box(Modifier.background(MaterialTheme.colorScheme.surface)) { content() }
    }
}

/** "Filter and Sort" of the flat library: the console, save/states/cheats, the order, Clear Filters, Browse by Console. */
@Composable
fun LibraryFilterDialog(
    query: LibraryQuery,
    groups: List<SystemGroup>,
    onChange: (LibraryQuery) -> Unit,
    onBrowse: () -> Unit,
    onDismiss: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.library_filter)) },
        text = {
            Column(Modifier.heightIn(max = 520.dp).verticalScroll(rememberScrollState()).testTag("libraryFilter")) {
                Text(stringResource(R.string.library_filter_console), style = MaterialTheme.typography.titleSmall)
                ChoiceRow(stringResource(R.string.library_all_consoles), query.systemLabel == null, "filterAll") { onChange(query.copy(systemLabel = null)) }
                for (group in groups) {
                    ChoiceRow(group.label, query.systemLabel == group.label, "filterConsole-${group.label}") { onChange(query.copy(systemLabel = group.label)) }
                }
                HorizontalDivider(Modifier.padding(vertical = 8.dp))
                CheckRow(stringResource(R.string.library_has_save), query.hasSave, "filterSave") { onChange(query.copy(hasSave = it)) }
                CheckRow(stringResource(R.string.library_has_states), query.hasStates, "filterStates") { onChange(query.copy(hasStates = it)) }
                CheckRow(stringResource(R.string.library_has_cheats), query.hasCheats, "filterCheats") { onChange(query.copy(hasCheats = it)) }
                HorizontalDivider(Modifier.padding(vertical = 8.dp))
                Text(stringResource(R.string.library_sort_by), style = MaterialTheme.typography.titleSmall)
                for (s in LibrarySort.entries) {
                    ChoiceRow(sortLabel(s), query.sort == s, "sort-${s.name.lowercase()}") { onChange(query.copy(sort = s)) }
                }
                HorizontalDivider(Modifier.padding(vertical = 8.dp))
                if (query.isFiltered) {
                    TextButton(onClick = { onChange(query.clearedFilters()) }, modifier = Modifier.testTag("filterClear")) {
                        Text(stringResource(R.string.library_clear_filters), color = MaterialTheme.colorScheme.error)
                    }
                }
                TextButton(onClick = { onDismiss(); onBrowse() }, modifier = Modifier.testTag("libraryBySystem")) {
                    Text(stringResource(R.string.library_by_console))
                }
            }
        },
        confirmButton = { TextButton(onClick = onDismiss, modifier = Modifier.testTag("filterDone")) { Text(stringResource(R.string.sd_done)) } },
    )
}

@Composable
private fun ChoiceRow(label: String, selected: Boolean, tag: String, onClick: () -> Unit) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier.fillMaxWidth().selectable(selected = selected, onClick = onClick).testTag(tag),
    ) {
        RadioButton(selected = selected, onClick = null)
        Text(label, modifier = Modifier.padding(start = 8.dp))
    }
}

@Composable
private fun CheckRow(label: String, checked: Boolean, tag: String, onChange: (Boolean) -> Unit) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier.fillMaxWidth().selectable(selected = checked, onClick = { onChange(!checked) }).testTag(tag),
    ) {
        Checkbox(checked = checked, onCheckedChange = null)
        Text(label, modifier = Modifier.padding(start = 8.dp))
    }
}
