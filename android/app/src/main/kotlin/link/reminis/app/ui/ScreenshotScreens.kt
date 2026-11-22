package link.reminis.app.ui

import android.app.Application
import android.content.ClipData
import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import android.net.Uri
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.Share
import androidx.compose.material.icons.filled.Warning
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.BottomAppBar
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.FilterQuality
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import java.io.File
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import link.reminis.app.R
import link.reminis.app.ScreenshotManager
import link.reminis.app.container
import link.reminis.core.Screenshot
import link.reminis.core.ScreenshotGridLayout
import link.reminis.core.ScreenshotList
import link.reminis.core.ScreenshotSelection

/** What a screenshot action left to show: a notice, an error, files to share, or the demo's note. */
open class ScreenshotActionsViewModel(app: Application, protected val deviceId: String) : AndroidViewModel(app) {
    protected val container = app.container
    val manager: ScreenshotManager = container.screenshots(deviceId)
    val isDemo: Boolean get() = container.isDemo(deviceId)

    private val _working = MutableStateFlow(false)
    val working: StateFlow<Boolean> = _working.asStateFlow()
    private val _notice = MutableStateFlow<String?>(null)
    val notice: StateFlow<String?> = _notice.asStateFlow()
    protected val _error = MutableStateFlow<Throwable?>(null)
    val error: StateFlow<Throwable?> = _error.asStateFlow()

    /** Copies ready for the share sheet (consumed by the screen). */
    private val _toShare = MutableStateFlow<List<File>?>(null)
    val toShare: StateFlow<List<File>?> = _toShare.asStateFlow()

    /** Screenshots waiting for the folder picker (Android 8 and 9). */
    var pendingGallery: List<Screenshot> = emptyList()
        private set

    fun dismiss() {
        _notice.value = null
        _error.value = null
    }

    fun shared() {
        _toShare.value = null
    }

    protected fun perform(action: suspend () -> Unit) {
        if (_working.value) return
        _working.value = true
        _error.value = null
        _notice.value = null
        viewModelScope.launch {
            try {
                action()
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                _error.value = e
            } finally {
                _working.value = false
            }
        }
    }

    fun share(shots: List<Screenshot>) {
        if (shots.isEmpty()) return
        perform { _toShare.value = manager.exportForSharing(shots) }
    }

    /** Android 10 and later save at once; Android 8 and 9 ask for a folder first ([needsFolder]). */
    val needsFolder: Boolean get() = Build.VERSION.SDK_INT < Build.VERSION_CODES.Q

    fun askFolderFor(shots: List<Screenshot>) {
        pendingGallery = shots
    }

    fun saveToGallery(shots: List<Screenshot>, folder: Uri? = null) {
        if (shots.isEmpty()) return
        pendingGallery = emptyList()
        perform {
            val count = manager.saveToGallery(shots, folder)
            val app = getApplication<Application>()
            _notice.value = if (count == 1) app.getString(R.string.screenshot_saved) else app.resources.getQuantityString(R.plurals.screenshots_saved, count, count)
        }
    }

    private val _pendingTrash = MutableStateFlow<Set<String>?>(null)

    /**
     * The listed versions a confirmation asks to move (kept across a
     * rotation). It closes by itself when one of them leaves the list or
     * is replaced under the same name (Codex review round 1).
     */
    val pendingTrash: StateFlow<Set<String>?> = _pendingTrash.asStateFlow()

    fun askTrash(shots: List<Screenshot>) {
        if (shots.isNotEmpty()) _pendingTrash.value = shots.map { it.thumbnailKey }.toSet()
    }

    fun cancelTrash() {
        _pendingTrash.value = null
    }

    /** The list changed: a confirmation for a version no longer listed closes, and says why. */
    protected fun checkPendingTrash() {
        val pending = _pendingTrash.value ?: return
        val listed = manager.screenshots.map { it.thumbnailKey }.toSet()
        if (!listed.containsAll(pending)) {
            _pendingTrash.value = null
            _error.value = link.reminis.core.ScreenshotChangedException()
        }
    }

    /** Moves exactly the versions confirmed (refused, and the list refreshed, if one changed). */
    protected fun confirmTrash(after: () -> Unit) {
        val versions = _pendingTrash.value ?: return
        _pendingTrash.value = null
        perform {
            manager.trashExact(versions)
            after()
        }
    }
}

/** The grid of screenshots of a console, and its selection (kept across configuration changes). */
class ScreenshotsViewModel(app: Application, deviceId: String) : ScreenshotActionsViewModel(app, deviceId) {
    private val _selecting = MutableStateFlow(false)
    val selecting: StateFlow<Boolean> = _selecting.asStateFlow()
    private val _selection = MutableStateFlow(ScreenshotSelection())
    val selection: StateFlow<ScreenshotSelection> = _selection.asStateFlow()

    /** The position in the grid, kept while the screen leaves the composition (Codex review round 1). */
    val gridState = androidx.compose.foundation.lazy.grid.LazyGridState()

    init {
        manager.load()
        // A reload drops from the selection what is gone or was replaced (another version).
        viewModelScope.launch {
            manager.revision.collect {
                _selection.value = _selection.value.kept(manager.screenshots)
                checkPendingTrash()
            }
        }
    }

    fun setSelecting(on: Boolean) {
        _selecting.value = on
        if (!on) _selection.value = ScreenshotSelection()
    }

    fun toggle(shot: Screenshot) {
        _selection.value = _selection.value.toggled(shot)
    }

    fun toggleAll() {
        _selection.value = _selection.value.toggledAll(manager.screenshots)
    }

    fun trashConfirmed() = confirmTrash { setSelecting(false) }
}

/**
 * The screenshots taken on a console (M22, docs/specs/06-app-ios.md §11
 * bis): previews grouped by day (the phone's time zone), as many columns as
 * the window's width allows; tap one to open it, Select (or a long press)
 * to share, save to the gallery or move several to the trash of the console.
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun ScreenshotsScreen(deviceId: String, onBack: () -> Unit, onOpen: (String) -> Unit) {
    val app = LocalContext.current.applicationContext as Application
    val vm: ScreenshotsViewModel = viewModel(key = "screenshots-$deviceId") { ScreenshotsViewModel(app, deviceId) }
    val list by vm.manager.list.collectAsState()
    val selecting by vm.selecting.collectAsState()
    val selection by vm.selection.collectAsState()
    val working by vm.working.collectAsState()
    val notice by vm.notice.collectAsState()
    val error by vm.error.collectAsState()
    val toTrash by vm.pendingTrash.collectAsState()
    var demoNote by rememberSaveable { mutableStateOf(false) }
    val shots = (list as? Load.Loaded)?.value?.screenshots.orEmpty()
    val picked = selection.selected(shots)
    val gallery = rememberGalleryActions(vm, onDemo = { demoNote = true })

    AppScaffold(
        title = stringResource(R.string.screenshots_title),
        onBack = onBack,
        readable = false,
        actions = {
            IconButton(onClick = { vm.manager.load() }) { Icon(Icons.Filled.Refresh, contentDescription = stringResource(R.string.refresh)) }
            TextButton(
                onClick = { vm.setSelecting(!selecting) },
                enabled = selecting || shots.isNotEmpty(),
                modifier = Modifier.testTag("screenshotsSelect"),
            ) { Text(stringResource(if (selecting) R.string.screenshots_done else R.string.screenshots_select)) }
        },
        bottomBar = {
            if (selecting) {
                BottomAppBar(Modifier.testTag("screenshotsBar")) {
                    IconButton(onClick = { gallery.share(picked) }, enabled = picked.isNotEmpty() && !working, modifier = Modifier.testTag("screenshotsShare")) {
                        Icon(Icons.Filled.Share, contentDescription = stringResource(R.string.screenshots_share))
                    }
                    val all = shots.isNotEmpty() && selection.count == shots.size
                    IconButton(onClick = vm::toggleAll, modifier = Modifier.testTag("screenshotsSelectAll")) {
                        Icon(
                            Icons.Filled.CheckCircle,
                            contentDescription = stringResource(if (all) R.string.screenshots_deselect_all else R.string.screenshots_select_all),
                            tint = if (all) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    Box(Modifier.weight(1f), contentAlignment = Alignment.Center) {
                        if (working) {
                            LinearProgressIndicator(Modifier.padding(horizontal = 8.dp))
                        } else {
                            Text(
                                if (picked.isEmpty()) stringResource(R.string.screenshots_select_prompt) else pluralStringResource(R.plurals.screenshots_selected, picked.size, picked.size),
                                style = MaterialTheme.typography.bodySmall,
                                maxLines = 1,
                                modifier = Modifier.testTag("screenshotsCount"),
                            )
                        }
                    }
                    IconButton(onClick = { vm.askTrash(picked) }, enabled = picked.isNotEmpty() && !working, modifier = Modifier.testTag("screenshotsTrash")) {
                        Icon(Icons.Filled.Delete, contentDescription = stringResource(R.string.screenshots_trash))
                    }
                    var more by remember { mutableStateOf(false) }
                    Box {
                        IconButton(onClick = { more = true }, enabled = picked.isNotEmpty() && !working, modifier = Modifier.testTag("screenshotsMore")) {
                            Icon(Icons.Filled.MoreVert, contentDescription = stringResource(R.string.more_actions))
                        }
                        DropdownMenu(expanded = more, onDismissRequest = { more = false }) {
                            DropdownMenuItem(
                                text = { Text(stringResource(R.string.screenshots_save_gallery)) },
                                onClick = { more = false; gallery.save(picked) },
                                modifier = Modifier.testTag("screenshotsGallery"),
                            )
                        }
                    }
                }
            }
        },
    ) { modifier ->
        when (val l = list) {
            Load.Loading -> LoadingView(modifier)
            is Load.Failed -> ErrorView(l.error, onRetry = { vm.manager.load() }, modifier = modifier)
            is Load.Loaded -> BoxWithConstraints(modifier.fillMaxSize()) {
                val columns = ScreenshotGridLayout.columns(maxWidth.value)
                val days = remember(l.value) { ScreenshotList.byDay(l.value.screenshots) }
                LazyVerticalGrid(
                    columns = GridCells.Fixed(columns),
                    state = vm.gridState,
                    horizontalArrangement = Arrangement.spacedBy(ScreenshotGridLayout.SPACING.dp),
                    verticalArrangement = Arrangement.spacedBy(ScreenshotGridLayout.SPACING.dp),
                    modifier = Modifier.fillMaxSize().testTag("screenshotsGrid"),
                ) {
                    item(span = { GridItemSpan(maxLineSpan) }) {
                        Column {
                            if (working && !selecting) LinearProgressIndicator(Modifier.fillMaxWidth())
                            MessageCard(notice?.let { Notice(R.string.plain_text, listOf(it)) }, error, vm::dismiss)
                            if (l.value.truncated) Footnote(stringResource(R.string.screenshots_truncated))
                            if (l.value.screenshots.isEmpty()) {
                                Column(Modifier.fillMaxWidth().padding(32.dp).testTag("screenshotsEmpty"), horizontalAlignment = Alignment.CenterHorizontally) {
                                    Text(stringResource(R.string.screenshots_empty_title), style = MaterialTheme.typography.titleMedium)
                                    Text(stringResource(R.string.screenshots_empty), style = MaterialTheme.typography.bodyMedium)
                                }
                            }
                        }
                    }
                    days.forEach { day ->
                        item(span = { GridItemSpan(maxLineSpan) }, key = "day-${day.date}") {
                            Surface(color = MaterialTheme.colorScheme.surface) {
                                Text(
                                    day.date.format(DateTimeFormatter.ofLocalizedDate(FormatStyle.FULL)),
                                    style = MaterialTheme.typography.titleSmall,
                                    modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp).semantics { heading() },
                                )
                            }
                        }
                        items(day.screenshots, key = { it.screenshotId }) { shot ->
                            val selected = shot in selection
                            ScreenshotCell(
                                shot, vm.manager, selecting, selected,
                                modifier = Modifier.combinedClickable(
                                    onClick = { if (selecting) vm.toggle(shot) else onOpen(shot.screenshotId) },
                                    onLongClick = {
                                        if (!selecting) vm.setSelecting(true)
                                        vm.toggle(shot)
                                    },
                                ),
                            )
                        }
                    }
                }
            }
        }
    }
    toTrash?.let { versions ->
        val n = versions.size
        ConfirmDialog(
            title = if (n == 1) stringResource(R.string.screenshot_trash_title) else pluralStringResource(R.plurals.screenshots_trash_title, n, n),
            message = stringResource(if (n == 1) R.string.screenshot_trash_message else R.string.screenshots_trash_message),
            confirm = stringResource(R.string.screenshots_trash),
            onConfirm = vm::trashConfirmed,
            onDismiss = vm::cancelTrash,
        )
    }
    if (demoNote) DemoScreenshotsDialog { demoNote = false }
}

/** A cell of the grid: 4:3, filled, from memory or the console; a pictogram when the pak makes no preview. */
@Composable
private fun ScreenshotCell(shot: Screenshot, manager: ScreenshotManager, selecting: Boolean, selected: Boolean, modifier: Modifier) {
    val bitmap by produceState(manager.cachedThumbnail(shot), shot.thumbnailKey) {
        if (value == null) value = manager.thumbnail(shot)
    }
    val description = "${shot.title}, ${formatDate(shot.takenAt)}"
    Box(
        modifier
            .aspectRatio(4f / 3f)
            .background(MaterialTheme.colorScheme.surfaceVariant)
            .semantics {
                contentDescription = description
                if (selecting) this.selected = selected
            }
            .testTag("screenshotCell"),
        contentAlignment = Alignment.Center,
    ) {
        val image = bitmap
        if (image != null) {
            Image(image.asImageBitmap(), contentDescription = null, contentScale = ContentScale.Crop, modifier = Modifier.fillMaxSize())
        } else if (manager.hasNoPreview(shot)) {
            Icon(Icons.Filled.Warning, contentDescription = stringResource(R.string.screenshot_no_preview), tint = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        if (selecting) {
            if (selected) Box(Modifier.fillMaxSize().background(Color.White.copy(alpha = 0.18f)))
            if (selected) {
                Icon(
                    Icons.Filled.CheckCircle,
                    contentDescription = null,
                    tint = MaterialTheme.colorScheme.primary,
                    modifier = Modifier.align(Alignment.BottomEnd).padding(6.dp).background(Color.White, CircleShape).testTag("screenshotChecked"),
                )
            } else {
                // An empty circle: not selected.
                Box(
                    Modifier.align(Alignment.BottomEnd).padding(8.dp).size(20.dp)
                        .background(Color.Black.copy(alpha = 0.25f), CircleShape)
                        .border(2.dp, Color.White, CircleShape).testTag("screenshotUnchecked"),
                )
            }
        }
    }
}

/** The demo mode never lets a file leave the app (as iOS): a note instead of the share sheet or the gallery. */
@Composable
private fun DemoScreenshotsDialog(onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.screenshots_demo_title)) },
        text = { Text(stringResource(R.string.screenshots_demo_message)) },
        confirmButton = { TextButton(onClick = onDismiss, modifier = Modifier.testTag("demoScreenshotsOk")) { Text(stringResource(R.string.action_continue)) } },
    )
}

/** Share and Save to Gallery for a screen of screenshots: the share sheet, the folder picker (Android 8, 9), the demo's note. */
private class GalleryActions(val share: (List<Screenshot>) -> Unit, val save: (List<Screenshot>) -> Unit)

@Composable
private fun rememberGalleryActions(vm: ScreenshotActionsViewModel, onDemo: () -> Unit): GalleryActions {
    val context = LocalContext.current
    val toShare by vm.toShare.collectAsState()
    LaunchedEffect(toShare) {
        val files = toShare ?: return@LaunchedEffect
        vm.shared()
        shareFiles(context, files)
    }
    val folder = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocumentTree()) { uri ->
        val pending = vm.pendingGallery
        if (uri != null && pending.isNotEmpty()) vm.saveToGallery(pending, uri)
    }
    return remember(vm) {
        GalleryActions(
            share = { shots -> if (vm.isDemo) onDemo() else vm.share(shots) },
            save = { shots ->
                when {
                    vm.isDemo -> onDemo()
                    vm.needsFolder -> {
                        vm.askFolderFor(shots)
                        folder.launch(null)
                    }
                    else -> vm.saveToGallery(shots)
                }
            },
        )
    }
}

/** Opens the share sheet for copies of the share area, through the app's FileProvider (read access granted). */
fun shareFiles(context: Context, files: List<File>) {
    if (files.isEmpty()) return
    val authority = "${context.packageName}.files"
    val uris = files.map { androidx.core.content.FileProvider.getUriForFile(context, authority, it) }
    val intent = if (uris.size == 1) {
        Intent(Intent.ACTION_SEND).putExtra(Intent.EXTRA_STREAM, uris[0])
    } else {
        Intent(Intent.ACTION_SEND_MULTIPLE).putParcelableArrayListExtra(Intent.EXTRA_STREAM, ArrayList(uris))
    }
    intent.type = "image/*"
    intent.clipData = ClipData.newRawUri(null, uris[0]).apply { uris.drop(1).forEach { addItem(ClipData.Item(it)) } }
    intent.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
    val chooser = Intent.createChooser(intent, context.getString(R.string.screenshots_share_chooser))
    if (context !is android.app.Activity) chooser.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
    context.startActivity(chooser)
}

// MARK: one screenshot

class ScreenshotViewModel(app: Application, deviceId: String, private val screenshotId: String) : ScreenshotActionsViewModel(app, deviceId) {
    private val _image = MutableStateFlow<Load<Bitmap>>(Load.Loading)
    val image: StateFlow<Load<Bitmap>> = _image.asStateFlow()

    private val _shown = MutableStateFlow<Screenshot?>(null)

    /**
     * The listed version whose image and details are shown (Codex review
     * round 1): a screenshot replaced on the console under the same name is
     * another version; the image is then read again and a confirmation
     * open for the previous version closes.
     */
    val shown: StateFlow<Screenshot?> = _shown.asStateFlow()

    /** The screenshot as listed now (null when the list was not read, or it left). */
    private val listed: Screenshot? get() = manager.find(screenshotId)

    private var maxPixels = 0
    private var job: kotlinx.coroutines.Job? = null

    init {
        viewModelScope.launch {
            manager.revision.collect {
                val now = listed
                val before = _shown.value
                if (before != null && now?.thumbnailKey != before.thumbnailKey) {
                    checkPendingTrash()
                    if (now != null && maxPixels > 0) show(now) else if (now == null) {
                        job?.cancel()
                        _shown.value = null
                    }
                } else if (before == null && now != null && maxPixels > 0) {
                    show(now)
                }
            }
        }
    }

    /** Downloads and decodes the image of the listed version (at most [maxPixels] on its longest side). */
    fun load(maxPixels: Int, again: Boolean = false) {
        if (this.maxPixels > 0 && !again) return
        this.maxPixels = maxPixels
        val shot = listed ?: run {
            manager.load()
            return
        }
        show(shot)
    }

    private fun show(shot: Screenshot) {
        job?.cancel()
        _shown.value = shot
        _image.value = Load.Loading
        job = viewModelScope.launch {
            val result = try {
                Load.Loaded(manager.displayImage(shot, maxPixels))
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                Load.Failed(e)
            }
            if (_shown.value?.thumbnailKey == shot.thumbnailKey) _image.value = result
        }
    }

    fun askTrash() {
        _shown.value?.let { askTrash(listOf(it)) }
    }

    fun moveToTrash(onDone: () -> Unit) = confirmTrash(onDone)
}

/** One screenshot, whole: its game, date, size and file name, with Share, Save to Gallery and Move to Trash. */
@Composable
fun ScreenshotScreen(deviceId: String, screenshotId: String, onBack: () -> Unit) {
    val app = LocalContext.current.applicationContext as Application
    val vm: ScreenshotViewModel = viewModel(key = "screenshot-$deviceId-$screenshotId") { ScreenshotViewModel(app, deviceId, screenshotId) }
    val image by vm.image.collectAsState()
    val working by vm.working.collectAsState()
    val notice by vm.notice.collectAsState()
    val error by vm.error.collectAsState()
    val list by vm.manager.list.collectAsState()
    val shot = vm.shown.collectAsState().value
    var menu by remember { mutableStateOf(false) }
    val confirmTrash = vm.pendingTrash.collectAsState().value != null
    var demoNote by rememberSaveable { mutableStateOf(false) }
    val gallery = rememberGalleryActions(vm, onDemo = { demoNote = true })
    val configuration = LocalConfiguration.current
    val density = LocalDensity.current
    // At most the size of the screen, in pixels: a large image is subsampled while it is decoded.
    val maxPixels = with(density) { maxOf(configuration.screenWidthDp, configuration.screenHeightDp).dp.roundToPx() }.coerceIn(320, 4096)
    LaunchedEffect(Unit) { vm.load(maxPixels) }

    AppScaffold(
        title = shot?.title.orEmpty(),
        onBack = onBack,
        actions = {
            if (shot != null) {
                IconButton(onClick = { gallery.share(listOf(shot)) }, enabled = !working, modifier = Modifier.testTag("screenshotShare")) {
                    Icon(Icons.Filled.Share, contentDescription = stringResource(R.string.screenshots_share))
                }
                Box {
                    IconButton(onClick = { menu = true }, enabled = !working, modifier = Modifier.testTag("screenshotMore")) {
                        Icon(Icons.Filled.MoreVert, contentDescription = stringResource(R.string.more_actions))
                    }
                    DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                        DropdownMenuItem(
                            text = { Text(stringResource(R.string.screenshots_save_gallery)) },
                            onClick = { menu = false; gallery.save(listOf(shot)) },
                            modifier = Modifier.testTag("screenshotGallery"),
                        )
                        DropdownMenuItem(
                            text = { Text(stringResource(R.string.screenshots_trash_ellipsis)) },
                            onClick = { menu = false; vm.askTrash() },
                            modifier = Modifier.testTag("screenshotTrash"),
                        )
                    }
                }
            }
        },
    ) { modifier ->
        if (shot == null) {
            when (val l = list) {
                is Load.Failed -> ErrorView(l.error, onRetry = { vm.manager.load() }, modifier = modifier)
                is Load.Loaded -> ErrorView(UserMessage(R.string.screenshot_gone), onRetry = null, modifier = modifier)
                Load.Loading -> LoadingView(modifier)
            }
            return@AppScaffold
        }
        val context = LocalContext.current
        Column(modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp).testTag("screenshotScreen"), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            when (val i = image) {
                Load.Loading -> Box(Modifier.fillMaxWidth().aspectRatio(4f / 3f).background(MaterialTheme.colorScheme.surfaceVariant), contentAlignment = Alignment.Center) {
                    LinearProgressIndicator(Modifier.widthIn(max = 160.dp))
                }
                is Load.Failed -> Text(errorMessage(context, i.error), color = MaterialTheme.colorScheme.error, modifier = Modifier.testTag("screenshotImageError"))
                is Load.Loaded -> Image(
                    i.value.asImageBitmap(),
                    contentDescription = shot.title,
                    contentScale = ContentScale.Fit,
                    // Pixel art stays sharp.
                    filterQuality = FilterQuality.None,
                    modifier = Modifier.fillMaxWidth()
                        .aspectRatio(i.value.width.toFloat() / i.value.height.coerceAtLeast(1))
                        .testTag("screenshotImage"),
                )
            }
            if (working) LinearProgressIndicator(Modifier.fillMaxWidth())
            MessageCard(notice?.let { Notice(R.string.plain_text, listOf(it)) }, error, vm::dismiss)
            Text(shot.title, style = MaterialTheme.typography.titleLarge)
            ListItem(overlineContent = { Text(stringResource(R.string.screenshot_taken)) }, headlineContent = { Text(formatDate(shot.takenAt)) })
            ListItem(overlineContent = { Text(stringResource(R.string.screenshot_size)) }, headlineContent = { Text(formatBytes(context, shot.sizeBytes)) })
            ListItem(overlineContent = { Text(stringResource(R.string.screenshot_file)) }, headlineContent = { Text(shot.name) })
        }
    }
    if (confirmTrash) {
        ConfirmDialog(
            title = stringResource(R.string.screenshot_trash_title),
            message = stringResource(R.string.screenshot_trash_message),
            confirm = stringResource(R.string.screenshots_trash),
            onConfirm = { vm.moveToTrash(onDone = onBack) },
            onDismiss = vm::cancelTrash,
        )
    }
    if (demoNote) DemoScreenshotsDialog { demoNote = false }
}
