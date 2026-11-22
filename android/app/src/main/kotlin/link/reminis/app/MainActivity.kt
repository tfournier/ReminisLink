package link.reminis.app

import android.content.Intent
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.dynamicDarkColorScheme
import androidx.compose.material3.dynamicLightColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.statusBars
import androidx.compose.foundation.layout.navigationBars
import androidx.compose.material.icons.filled.Build
import androidx.compose.ui.unit.dp
import androidx.compose.material3.NavigationBarItem
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.navigation.compose.currentBackStackEntryAsState
import link.reminis.app.ui.DemoBanner
import androidx.compose.ui.platform.LocalContext
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import link.reminis.app.ui.AddConsoleScreen
import link.reminis.app.ui.CheatsScreen
import link.reminis.app.ui.PaksScreen
import link.reminis.app.ui.PatchScreen
import link.reminis.app.ui.TrashRestoreScreen
import link.reminis.app.ui.TrashScreen
import link.reminis.app.ui.AddFilesScreen
import link.reminis.app.ui.ConsolesScreen
import link.reminis.app.ui.DashboardScreen
import link.reminis.app.ui.GameScreen
import link.reminis.app.ui.ArchiveImportScreen
import link.reminis.app.ui.LibraryPanes
import link.reminis.app.ui.ScreenshotScreen
import link.reminis.app.ui.ScreenshotsScreen
import link.reminis.app.ui.PairingScreen
import link.reminis.app.ui.SaveScreen
import link.reminis.app.ui.ScanScreen
import link.reminis.app.ui.StatesScreen
import link.reminis.app.ui.TransfersScreen

/**
 * Navigation routes. Ids are base64url. A pairing link never goes into a
 * route (navigation state is saved): only the opaque token of [PendingLinks].
 */
object Routes {
    const val CONSOLES = "consoles"
    const val ADD = "add"
    const val SCAN = "scan"
    const val PAIR = "pair/{token}"
    const val DASHBOARD = "console/{deviceId}"
    const val LIBRARY = "console/{deviceId}/library"
    const val GAME = "console/{deviceId}/game/{gameId}"
    const val SAVE = "console/{deviceId}/game/{gameId}/save"
    const val STATES = "console/{deviceId}/game/{gameId}/states?name={name}"
    const val TRANSFERS = "console/{deviceId}/transfers"
    const val ADD_FILES = "add-files?deviceId={deviceId}&share={share}"
    const val CHEATS = "console/{deviceId}/game/{gameId}/cheats"
    const val PATCH = "patch?deviceId={deviceId}&gameId={gameId}&share={share}"
    const val APPS = "console/{deviceId}/apps"
    const val TRASH = "console/{deviceId}/trash"
    const val TRASH_RESTORE = "console/{deviceId}/trash/{entryId}?label={label}"
    const val ARCHIVE_IMPORT = "archive-import?deviceId={deviceId}&gameId={gameId}&share={share}"
    const val SCREENSHOTS = "console/{deviceId}/screenshots"
    const val SCREENSHOT = "console/{deviceId}/screenshots/{screenshotId}"

    /** M23: the Files tab (the root of the SD card), or one of its folders. */
    const val FILES = "console/{deviceId}/files?folderId={folderId}&title={title}"
    const val SETTINGS = "settings"

    /** Browse by Console (iOS 06 §4.4): the systems, then a folder of a system. */
    const val SYSTEMS = "console/{deviceId}/systems"
    const val FOLDER = "console/{deviceId}/systems/{systemId}?folderId={folderId}&title={title}"

    fun systems(deviceId: String) = "console/$deviceId/systems"
    fun folder(deviceId: String, systemId: String, folderId: String?, title: String) = buildString {
        append("console/$deviceId/systems/$systemId?")
        folderId?.let { append("folderId=$it&") }
        append("title=${android.net.Uri.encode(title)}")
    }

    fun files(deviceId: String, folderId: String? = null, title: String? = null) = buildString {
        append("console/$deviceId/files")
        val query = listOfNotNull(folderId?.let { "folderId=$it" }, title?.let { "title=${android.net.Uri.encode(it)}" })
        if (query.isNotEmpty()) append("?").append(query.joinToString("&"))
    }

    fun screenshots(deviceId: String) = "console/$deviceId/screenshots"
    fun screenshot(deviceId: String, screenshotId: String) = "console/$deviceId/screenshots/$screenshotId"

    /** M19: the import of a game archive, into a game, from the Library, or of a shared archive (an opaque token of [ShareInbox]). */
    fun archiveImport(deviceId: String? = null, gameId: String? = null, share: String? = null) = buildString {
        append("archive-import")
        val query = listOfNotNull(deviceId?.let { "deviceId=$it" }, gameId?.let { "gameId=$it" }, share?.let { "share=$it" })
        if (query.isNotEmpty()) append("?").append(query.joinToString("&"))
    }

    fun cheats(deviceId: String, gameId: String) = "console/$deviceId/game/$gameId/cheats"
    fun apps(deviceId: String) = "console/$deviceId/apps"
    fun trash(deviceId: String) = "console/$deviceId/trash"
    fun trashRestore(deviceId: String, entryId: String, label: String) =
        "console/$deviceId/trash/$entryId?label=${android.net.Uri.encode(label)}"

    /** The Patch screen of a game, or of a shared patch (an opaque token of [ShareInbox]). */
    fun patch(deviceId: String? = null, gameId: String? = null, share: String? = null) = buildString {
        append("patch")
        val query = listOfNotNull(deviceId?.let { "deviceId=$it" }, gameId?.let { "gameId=$it" }, share?.let { "share=$it" })
        if (query.isNotEmpty()) append("?").append(query.joinToString("&"))
    }

    fun pair(token: String) = "pair/$token"
    fun dashboard(deviceId: String) = "console/$deviceId"
    fun library(deviceId: String) = "console/$deviceId/library"
    fun game(deviceId: String, gameId: String) = "console/$deviceId/game/$gameId"
    fun save(deviceId: String, gameId: String) = "console/$deviceId/game/$gameId/save"
    fun states(deviceId: String, gameId: String, name: String) =
        "console/$deviceId/game/$gameId/states?name=${android.net.Uri.encode(name)}"
    fun transfers(deviceId: String) = "console/$deviceId/transfers"

    /** The Add screen of a console, or of the files shared behind [share] (an opaque token of [ShareInbox]). */
    fun addFiles(deviceId: String? = null, share: String? = null) = buildString {
        append("add-files")
        val query = listOfNotNull(deviceId?.let { "deviceId=$it" }, share?.let { "share=$it" })
        if (query.isNotEmpty()) append("?").append(query.joinToString("&"))
    }
}

class MainActivity : ComponentActivity() {
    /** A pairing link opened from another app (`reminislink://pair?...`). */
    private val incomingLink = mutableStateOf<String?>(null)

    /** Files shared by another app: the token of [ShareInbox]. */
    private val incomingShare = mutableStateOf<String?>(null)

    /** One patch shared by another app (`.ips`, `.bps`, `.ups`): the Patch screen instead of Add. */
    private val incomingPatch = mutableStateOf<String?>(null)

    /** M19: one game archive shared by another app (`.reminislink.zip`): the import screen instead of Add. */
    private val incomingArchive = mutableStateOf<String?>(null)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        // Only a fresh launch: a recreated activity gets its old intent back.
        if (savedInstanceState == null) {
            takePairingLink(intent)?.let { incomingLink.value = it }
            takeShare(intent)
        }
        setContent {
            ReminisLinkTheme {
                AppNavigation(
                    incomingLink = incomingLink.value,
                    onLinkHandled = { incomingLink.value = null },
                    incomingShare = incomingShare.value,
                    onShareHandled = { incomingShare.value = null },
                    incomingPatch = incomingPatch.value,
                    onPatchHandled = { incomingPatch.value = null },
                    incomingArchive = incomingArchive.value,
                    onArchiveHandled = { incomingArchive.value = null },
                )
            }
        }
    }

    // M14: the event stream of the console shown runs in the foreground only.
    override fun onStart() {
        super.onStart()
        container.events.setForeground(true)
    }

    override fun onStop() {
        container.events.setForeground(false)
        super.onStop()
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        takePairingLink(intent)?.let { incomingLink.value = it }
        takeShare(intent)
    }

    /**
     * The files of a share intent (`ACTION_SEND`, `ACTION_SEND_MULTIPLE`),
     * kept in memory behind a token. A share that only carries text (a
     * link) is ignored: the console downloads nothing. Not replayed from the
     * recent apps.
     */
    private fun takeShare(intent: Intent?) {
        if (intent == null || intent.flags and Intent.FLAG_ACTIVITY_LAUNCHED_FROM_HISTORY != 0) return
        val uris: List<android.net.Uri> = when (intent.action) {
            Intent.ACTION_SEND -> listOfNotNull(streamExtra(intent))
            Intent.ACTION_SEND_MULTIPLE -> streamListExtra(intent)
            else -> return
        }.filter { it.scheme == android.content.ContentResolver.SCHEME_CONTENT }
        if (uris.isEmpty()) return
        val token = container.shareInbox.put(uris)
        // One patch file and nothing else opens the Patch screen (docs/specs/06-app-ios.md §4.8).
        val single = uris.singleOrNull()
        val singleName = single?.let { LocalFiles.displayName(contentResolver, it) }
        if (singleName != null && link.reminis.core.PatchNaming.isPatchFileName(singleName)) {
            incomingPatch.value = token
        } else if (singleName != null && link.reminis.core.GameArchiveManifest.isArchiveName(singleName)) {
            // One game archive and nothing else opens the import screen (M19, docs/specs/06-app-ios.md §4.5).
            incomingArchive.value = token
        } else {
            incomingShare.value = token
        }
    }

    @Suppress("DEPRECATION")
    private fun streamExtra(intent: Intent): android.net.Uri? = if (Build.VERSION.SDK_INT >= 33) {
        intent.getParcelableExtra(Intent.EXTRA_STREAM, android.net.Uri::class.java)
    } else {
        intent.getParcelableExtra(Intent.EXTRA_STREAM)
    }

    @Suppress("DEPRECATION")
    private fun streamListExtra(intent: Intent): List<android.net.Uri> = if (Build.VERSION.SDK_INT >= 33) {
        intent.getParcelableArrayListExtra(Intent.EXTRA_STREAM, android.net.Uri::class.java).orEmpty()
    } else {
        intent.getParcelableArrayListExtra<android.net.Uri>(Intent.EXTRA_STREAM).orEmpty()
    }

    /**
     * The pairing link of a VIEW intent. Not replayed when the app is
     * reopened from the recent apps (the system re-delivers the first
     * intent) nor after a recreation (checked by the caller).
     */
    private fun takePairingLink(intent: Intent?): String? {
        if (intent == null || intent.action != Intent.ACTION_VIEW) return null
        if (intent.flags and Intent.FLAG_ACTIVITY_LAUNCHED_FROM_HISTORY != 0) return null
        return intent.dataString?.takeIf { it.startsWith("reminislink:", ignoreCase = true) }
    }
}

@Composable
fun AppNavigation(
    incomingLink: String?,
    onLinkHandled: () -> Unit,
    incomingShare: String? = null,
    onShareHandled: () -> Unit = {},
    incomingPatch: String? = null,
    onPatchHandled: () -> Unit = {},
    incomingArchive: String? = null,
    onArchiveHandled: () -> Unit = {},
) {
    val nav = rememberNavController()
    val links = LocalContext.current.container.pendingLinks
    val appContainer = LocalContext.current.container
    // A link opened during the demo (M16): never paired from the demo; kept in memory only
    // until the user quits the demo or declines.
    var linkDuringDemo by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(incomingLink) {
        if (incomingLink != null) {
            if (appContainer.demo.value != null) {
                linkDuringDemo = incomingLink
            } else {
                // From another app: the pairing screen asks before any network access.
                nav.navigate(Routes.pair(links.put(incomingLink, external = true)))
            }
            onLinkHandled()
        }
    }
    linkDuringDemo?.let { link ->
        androidx.compose.material3.AlertDialog(
            onDismissRequest = { linkDuringDemo = null },
            title = { androidx.compose.material3.Text(androidx.compose.ui.res.stringResource(R.string.demo_pair_title)) },
            text = { androidx.compose.material3.Text(androidx.compose.ui.res.stringResource(R.string.demo_pair_message)) },
            confirmButton = {
                androidx.compose.material3.TextButton(
                    onClick = {
                        linkDuringDemo = null
                        appContainer.quitDemo()
                        nav.popBackStack(Routes.CONSOLES, inclusive = false)
                        nav.navigate(Routes.pair(links.put(link, external = true)))
                    },
                    modifier = Modifier.testTag("demoPairQuit"),
                ) { androidx.compose.material3.Text(androidx.compose.ui.res.stringResource(R.string.demo_quit_description)) }
            },
            dismissButton = {
                androidx.compose.material3.TextButton(onClick = { linkDuringDemo = null }, modifier = Modifier.testTag("demoPairCancel")) {
                    androidx.compose.material3.Text(androidx.compose.ui.res.stringResource(R.string.action_cancel))
                }
            },
        )
    }
    // Files shared by another app are for the real consoles: during the demo, it ends first
    // (as on iOS, DemoMode.swift handle(url:)), then the Add (or Patch) screen opens.
    fun leaveDemoForShare() {
        if (appContainer.demo.value != null) {
            appContainer.quitDemo()
            nav.popBackStack(Routes.CONSOLES, inclusive = false)
        }
    }
    LaunchedEffect(incomingShare) {
        if (incomingShare != null) {
            leaveDemoForShare()
            nav.navigate(Routes.addFiles(share = incomingShare))
            onShareHandled()
        }
    }
    LaunchedEffect(incomingPatch) {
        if (incomingPatch != null) {
            leaveDemoForShare()
            nav.navigate(Routes.patch(share = incomingPatch))
            onPatchHandled()
        }
    }
    LaunchedEffect(incomingArchive) {
        if (incomingArchive != null) {
            leaveDemoForShare()
            nav.navigate(Routes.archiveImport(share = incomingArchive))
            onArchiveHandled()
        }
    }
    val container = LocalContext.current.container
    // M14: the console shown (the deviceId of the current route, null on the list and the pairing
    // screens) is the one whose event stream runs.
    val entry by nav.currentBackStackEntryAsState()
    val shownConsole = entry?.arguments?.getString("deviceId")
    LaunchedEffect(shownConsole) {
        // A screen of a demo that ended with the process (navigation state is restored, the demo is not).
        if (shownConsole != null && container.consoles.get(shownConsole) == null && !container.isDemo(shownConsole)) {
            nav.popBackStack(Routes.CONSOLES, inclusive = false)
            return@LaunchedEffect
        }
        container.events.setOpenConsole(shownConsole)
    }
    // M11: the console closed ReminisLink: back to the list at once (its notice is shown there).
    val closed by container.events.closed.collectAsState()
    LaunchedEffect(closed) {
        val notice = closed ?: return@LaunchedEffect
        if (nav.currentBackStackEntry?.arguments?.getString("deviceId") == notice.deviceId) {
            nav.popBackStack(Routes.CONSOLES, inclusive = false)
        }
    }
    val demo by container.demo.collectAsState()
    val quitDemo = {
        container.quitDemo()
        nav.popBackStack(Routes.CONSOLES, inclusive = false)
    }
    // M23: a `401 disconnected` seen by any request of the console shown: back to its dashboard,
    // which says it with Reconnect (iOS closes the connection the same way).
    val disconnected by container.events.disconnected.collectAsState()
    LaunchedEffect(disconnected, shownConsole) {
        val id = shownConsole ?: return@LaunchedEffect
        if (id in disconnected && entry?.destination?.route != Routes.DASHBOARD) {
            if (!nav.popBackStack(Routes.DASHBOARD, inclusive = false)) nav.navigate(Routes.dashboard(id))
        }
    }
    val tab = ConsoleTab.of(entry?.destination?.route)
    val tabBarHidden by TabBar.hidden.collectAsState()
    val openTab: (ConsoleTab) -> Unit = { target ->
        shownConsole?.let { id -> nav.navigate(target.route(id)) { tabOptions() } }
    }
    Column(Modifier.fillMaxSize()) {
        if (demo != null) DemoBanner(onQuit = { quitDemo() }, modifier = Modifier.windowInsetsPadding(WindowInsets.statusBars))
        val insets = if (demo != null) Modifier.consumeWindowInsets(WindowInsets.statusBars) else Modifier
        // Not a BoxWithConstraints: the nav host must be composed with this screen (a subcomposition
        // would set its graph after the effects above navigate to an incoming link or share).
        // The configuration's width: that of the window (multi-window, a forced size in tests).
        @Suppress("ConfigurationScreenWidthHeight")
        val windowWidth = androidx.compose.ui.platform.LocalConfiguration.current.screenWidthDp.dp
        Box(Modifier.weight(1f).then(insets)) {
            val showTabs = tab != null && shownConsole != null && !tabBarHidden
            // A wide window gets a rail on the side (iOS M15: a top bar or a sidebar), a narrow one a bottom bar.
            val rail = showTabs && windowWidth >= 600.dp
            androidx.compose.foundation.layout.Row(Modifier.fillMaxSize()) {
                if (rail) ConsoleTabRail(tab!!, openTab)
                Column(Modifier.weight(1f)) {
                    val tabInsets = if (showTabs && !rail) Modifier.consumeWindowInsets(WindowInsets.navigationBars) else Modifier
                    Box(Modifier.weight(1f).then(tabInsets)) {
                        AppNavHost(nav, links)
                    }
                    if (showTabs && !rail) ConsoleTabBar(tab!!, openTab)
                }
            }
        }
    }
}

/** The key of the saved state of a game screen that holds the new id of a game renamed there. */
private const val RENAMED_GAME = "renamedGameId"

/** The tabs of an open console (M11, M23): Console, Library, Apps, Files. */
enum class ConsoleTab(@param:androidx.annotation.StringRes val label: Int, val tag: String) {
    CONSOLE(R.string.tab_console, "tabConsole"),
    LIBRARY(R.string.library, "tabLibrary"),
    APPS(R.string.paks_title, "tabApps"),
    FILES(R.string.tab_files, "tabFiles");

    fun route(deviceId: String) = when (this) {
        CONSOLE -> Routes.dashboard(deviceId)
        LIBRARY -> Routes.library(deviceId)
        APPS -> Routes.apps(deviceId)
        FILES -> Routes.files(deviceId)
    }

    companion object {
        /** The tab of a screen; null for the screens shown alone (Add, Patch, import, pairing, list). */
        fun of(route: String?): ConsoleTab? = when (route) {
            Routes.DASHBOARD, Routes.TRANSFERS, Routes.TRASH, Routes.TRASH_RESTORE, Routes.SCREENSHOTS, Routes.SCREENSHOT -> CONSOLE
            Routes.LIBRARY, Routes.GAME, Routes.SAVE, Routes.STATES, Routes.CHEATS, Routes.SYSTEMS, Routes.FOLDER -> LIBRARY
            Routes.APPS -> APPS
            Routes.FILES -> FILES
            else -> null
        }
    }
}

/**
 * Each tab keeps its own stack: going to a tab saves the stack of the one
 * left (above the dashboard) and restores its own.
 */
fun androidx.navigation.NavOptionsBuilder.tabOptions() {
    popUpTo(Routes.DASHBOARD) { saveState = true }
    launchSingleTop = true
    restoreState = true
}

/** Hides the tab bar while a screen shows its own bottom bar (a selection), as iOS does. */
object TabBar {
    val hidden = kotlinx.coroutines.flow.MutableStateFlow(false)
}

@Composable
private fun tabIcon(tab: ConsoleTab) = when (tab) {
    ConsoleTab.CONSOLE -> link.reminis.app.ui.AppIcons.Console
    ConsoleTab.LIBRARY -> link.reminis.app.ui.AppIcons.Library
    ConsoleTab.APPS -> androidx.compose.material.icons.Icons.Filled.Build
    ConsoleTab.FILES -> link.reminis.app.ui.AppIcons.Folder
}

@Composable
private fun ConsoleTabBar(selected: ConsoleTab, onSelect: (ConsoleTab) -> Unit) {
    androidx.compose.material3.NavigationBar(Modifier.testTag("consoleTabs")) {
        for (tab in ConsoleTab.entries) {
            NavigationBarItem(
                selected = tab == selected,
                onClick = { onSelect(tab) },
                icon = { androidx.compose.material3.Icon(tabIcon(tab), contentDescription = null) },
                label = { androidx.compose.material3.Text(androidx.compose.ui.res.stringResource(tab.label)) },
                modifier = Modifier.testTag(tab.tag),
            )
        }
    }
}

@Composable
private fun ConsoleTabRail(selected: ConsoleTab, onSelect: (ConsoleTab) -> Unit) {
    androidx.compose.material3.NavigationRail(Modifier.testTag("consoleTabs")) {
        for (tab in ConsoleTab.entries) {
            androidx.compose.material3.NavigationRailItem(
                selected = tab == selected,
                onClick = { onSelect(tab) },
                icon = { androidx.compose.material3.Icon(tabIcon(tab), contentDescription = null) },
                label = { androidx.compose.material3.Text(androidx.compose.ui.res.stringResource(tab.label)) },
                modifier = Modifier.testTag(tab.tag),
            )
        }
    }
}

@Composable
private fun AppNavHost(nav: androidx.navigation.NavHostController, links: PendingLinks) {
    val container = LocalContext.current.container
    val id = navArgument("deviceId") { type = NavType.StringType }
    NavHost(navController = nav, startDestination = Routes.CONSOLES) {
        composable(Routes.CONSOLES) {
            ConsolesScreen(
                onAdd = { nav.navigate(Routes.ADD) },
                onOpen = {
                    // An action of the user: a closed console or a lost connection may connect again (M10, M11).
                    container.events.userOpened(it)
                    nav.navigate(Routes.dashboard(it))
                },
                onTryDemo = {
                    val demo = container.startDemo()
                    nav.navigate(Routes.dashboard(demo.deviceId))
                },
                onSettings = { nav.navigate(Routes.SETTINGS) },
            )
        }
        composable(Routes.SETTINGS) {
            link.reminis.app.ui.SettingsScreen(onBack = { nav.popBackStack() })
        }
        composable(Routes.ADD) {
            AddConsoleScreen(
                onBack = { nav.popBackStack() },
                onScan = { nav.navigate(Routes.SCAN) },
                onLink = { nav.navigate(Routes.pair(links.put(it, external = false))) },
            )
        }
        composable(Routes.SCAN) {
            ScanScreen(
                onBack = { nav.popBackStack() },
                onLink = { link ->
                    nav.navigate(Routes.pair(links.put(link, external = false))) { popUpTo(Routes.ADD) }
                },
            )
        }
        composable(Routes.PAIR, arguments = listOf(navArgument("token") { type = NavType.StringType })) { entry ->
            PairingScreen(
                token = entry.arguments?.getString("token").orEmpty(),
                onClose = { nav.popBackStack(Routes.CONSOLES, inclusive = false) },
                onOpenConsole = { deviceId ->
                    nav.navigate(Routes.dashboard(deviceId)) { popUpTo(Routes.CONSOLES) }
                },
            )
        }
        composable(Routes.DASHBOARD, arguments = listOf(id)) { entry ->
            val deviceId = entry.arguments?.getString("deviceId").orEmpty()
            DashboardScreen(
                deviceId = deviceId,
                onBack = { nav.popBackStack(Routes.CONSOLES, inclusive = false) },
                onTransfers = { nav.navigate(Routes.transfers(deviceId)) },
                onFiles = { nav.navigate(Routes.files(deviceId)) { tabOptions() } },
                onScreenshots = { nav.navigate(Routes.screenshots(deviceId)) },
                onTrash = { nav.navigate(Routes.trash(deviceId)) },
                onForgotten = { nav.popBackStack(Routes.CONSOLES, inclusive = false) },
            )
        }
        composable(Routes.LIBRARY, arguments = listOf(id)) { entry ->
            val deviceId = entry.arguments?.getString("deviceId").orEmpty()
            // M15 on Android: the list and the game side by side in a wide window, one at a time otherwise.
            LibraryPanes(
                deviceId = deviceId,
                onBack = { nav.popBackStack(Routes.CONSOLES, inclusive = false) },
                onAdd = { nav.navigate(Routes.addFiles(deviceId = deviceId)) },
                onImportArchive = { gameId -> nav.navigate(Routes.archiveImport(deviceId = deviceId, gameId = gameId)) },
                onSave = { gameId -> nav.navigate(Routes.save(deviceId, gameId)) },
                onStates = { gameId, name -> nav.navigate(Routes.states(deviceId, gameId, name)) },
                onCheats = { gameId -> nav.navigate(Routes.cheats(deviceId, gameId)) },
                onPatch = { gameId -> nav.navigate(Routes.patch(deviceId = deviceId, gameId = gameId)) },
                onBrowse = { nav.navigate(Routes.systems(deviceId)) },
                onSystem = { nav.navigate(Routes.folder(deviceId, it.id, null, it.label)) },
            )
        }
        composable(Routes.SYSTEMS, arguments = listOf(id)) { entry ->
            val deviceId = entry.arguments?.getString("deviceId").orEmpty()
            link.reminis.app.ui.SystemsScreen(
                deviceId = deviceId,
                onBack = { nav.popBackStack() },
                onOpen = { nav.navigate(Routes.folder(deviceId, it.id, null, it.label)) },
            )
        }
        composable(
            Routes.FOLDER,
            arguments = listOf(
                id,
                navArgument("systemId") { type = NavType.StringType },
                navArgument("folderId") { type = NavType.StringType; nullable = true; defaultValue = null },
                navArgument("title") { type = NavType.StringType; defaultValue = "" },
            ),
        ) { entry ->
            val deviceId = entry.arguments?.getString("deviceId").orEmpty()
            val systemId = entry.arguments?.getString("systemId").orEmpty()
            link.reminis.app.ui.FolderScreen(
                deviceId = deviceId,
                systemId = systemId,
                folderId = entry.arguments?.getString("folderId"),
                title = entry.arguments?.getString("title").orEmpty(),
                onBack = { nav.popBackStack() },
                onFolder = { nav.navigate(Routes.folder(deviceId, systemId, it.id, it.name)) },
                onGame = { nav.navigate(Routes.game(deviceId, it)) },
            )
        }
        val gameId = navArgument("gameId") { type = NavType.StringType }
        composable(Routes.GAME, arguments = listOf(id, gameId)) { entry ->
            val deviceId = entry.arguments?.getString("deviceId").orEmpty()
            // A rename replaces this game in place (Codex review 1): its entry, wherever it is in its
            // tab's stack, shows the new id; the screens above it and the other tabs stay as they are.
            val renamed by entry.savedStateHandle.getStateFlow<String?>(RENAMED_GAME, null).collectAsState()
            val game = renamed ?: entry.arguments?.getString("gameId").orEmpty()
            GameScreen(
                deviceId = deviceId,
                gameId = game,
                onBack = { nav.popBackStack() },
                onSave = { nav.navigate(Routes.save(deviceId, game)) },
                onStates = { name -> nav.navigate(Routes.states(deviceId, game, name)) },
                onCheats = { nav.navigate(Routes.cheats(deviceId, game)) },
                onPatch = { nav.navigate(Routes.patch(deviceId = deviceId, gameId = game)) },
                onDeleted = { nav.popBackStack() },
                onImportArchive = { nav.navigate(Routes.archiveImport(deviceId = deviceId, gameId = game)) },
                onRenamed = { newId -> entry.savedStateHandle[RENAMED_GAME] = newId },
            )
        }
        composable(
            Routes.ARCHIVE_IMPORT,
            arguments = listOf(
                navArgument("deviceId") { type = NavType.StringType; nullable = true; defaultValue = null },
                navArgument("gameId") { type = NavType.StringType; nullable = true; defaultValue = null },
                navArgument("share") { type = NavType.StringType; nullable = true; defaultValue = null },
            ),
        ) { entry ->
            val fromGame = entry.arguments?.getString("gameId")
            ArchiveImportScreen(
                deviceId = entry.arguments?.getString("deviceId"),
                gameId = fromGame,
                shareToken = entry.arguments?.getString("share"),
                onBack = { nav.popBackStack() },
                onOpenGame = { device, target ->
                    nav.popBackStack()
                    if (target != fromGame) nav.navigate(Routes.game(device, target))
                },
            )
        }
        composable(Routes.SCREENSHOTS, arguments = listOf(id)) { entry ->
            val deviceId = entry.arguments?.getString("deviceId").orEmpty()
            ScreenshotsScreen(
                deviceId = deviceId,
                onBack = { nav.popBackStack() },
                onOpen = { nav.navigate(Routes.screenshot(deviceId, it)) },
            )
        }
        composable(Routes.SCREENSHOT, arguments = listOf(id, navArgument("screenshotId") { type = NavType.StringType })) { entry ->
            ScreenshotScreen(
                deviceId = entry.arguments?.getString("deviceId").orEmpty(),
                screenshotId = entry.arguments?.getString("screenshotId").orEmpty(),
                onBack = { nav.popBackStack() },
            )
        }
        composable(Routes.CHEATS, arguments = listOf(id, gameId)) { entry ->
            CheatsScreen(
                deviceId = entry.arguments?.getString("deviceId").orEmpty(),
                gameId = entry.arguments?.getString("gameId").orEmpty(),
                onBack = { nav.popBackStack() },
            )
        }
        composable(
            Routes.PATCH,
            arguments = listOf(
                navArgument("deviceId") { type = NavType.StringType; nullable = true; defaultValue = null },
                navArgument("gameId") { type = NavType.StringType; nullable = true; defaultValue = null },
                navArgument("share") { type = NavType.StringType; nullable = true; defaultValue = null },
            ),
        ) { entry ->
            val fromGame = entry.arguments?.getString("gameId")
            PatchScreen(
                deviceId = entry.arguments?.getString("deviceId"),
                gameId = fromGame,
                shareToken = entry.arguments?.getString("share"),
                onBack = { nav.popBackStack() },
                onFinished = { device, target ->
                    // Back on the game the patch was applied to, or on the new game.
                    nav.popBackStack()
                    if (target != fromGame) nav.navigate(Routes.game(device, target))
                },
            )
        }
        composable(Routes.APPS, arguments = listOf(id)) { entry ->
            PaksScreen(deviceId = entry.arguments?.getString("deviceId").orEmpty(), onBack = { nav.popBackStack(Routes.CONSOLES, inclusive = false) })
        }
        composable(
            Routes.FILES,
            arguments = listOf(
                id,
                navArgument("folderId") { type = NavType.StringType; nullable = true; defaultValue = null },
                navArgument("title") { type = NavType.StringType; nullable = true; defaultValue = null },
            ),
        ) { entry ->
            val deviceId = entry.arguments?.getString("deviceId").orEmpty()
            val folderId = entry.arguments?.getString("folderId")
            link.reminis.app.ui.SdFolderScreen(
                deviceId = deviceId,
                folderId = folderId,
                title = entry.arguments?.getString("title"),
                // The root of the tab goes back to the list of consoles, as the other tabs.
                onBack = if (folderId == null) ({ nav.popBackStack(Routes.CONSOLES, inclusive = false) }) else ({ nav.popBackStack() }),
                onOpenFolder = { nav.navigate(Routes.files(deviceId, it.sdEntryId, it.name)) },
            )
        }
        composable(Routes.TRASH, arguments = listOf(id)) { entry ->
            val deviceId = entry.arguments?.getString("deviceId").orEmpty()
            TrashScreen(
                deviceId = deviceId,
                onBack = { nav.popBackStack() },
                onRestore = { nav.navigate(Routes.trashRestore(deviceId, it.id, it.label)) },
            )
        }
        composable(
            Routes.TRASH_RESTORE,
            arguments = listOf(
                id,
                navArgument("entryId") { type = NavType.StringType },
                navArgument("label") { type = NavType.StringType; defaultValue = "" },
            ),
        ) { entry ->
            TrashRestoreScreen(
                deviceId = entry.arguments?.getString("deviceId").orEmpty(),
                entryId = entry.arguments?.getString("entryId").orEmpty(),
                label = entry.arguments?.getString("label").orEmpty(),
                onBack = { nav.popBackStack() },
            )
        }
        composable(Routes.SAVE, arguments = listOf(id, gameId)) { entry ->
            SaveScreen(
                deviceId = entry.arguments?.getString("deviceId").orEmpty(),
                gameId = entry.arguments?.getString("gameId").orEmpty(),
                onBack = { nav.popBackStack() },
            )
        }
        composable(
            Routes.STATES,
            arguments = listOf(id, gameId, navArgument("name") { type = NavType.StringType; defaultValue = "" }),
        ) { entry ->
            StatesScreen(
                deviceId = entry.arguments?.getString("deviceId").orEmpty(),
                gameId = entry.arguments?.getString("gameId").orEmpty(),
                gameName = entry.arguments?.getString("name").orEmpty(),
                onBack = { nav.popBackStack() },
            )
        }
        composable(Routes.TRANSFERS, arguments = listOf(id)) { entry ->
            val deviceId = entry.arguments?.getString("deviceId").orEmpty()
            TransfersScreen(
                deviceId = deviceId,
                onBack = { nav.popBackStack() },
                onAdd = { nav.navigate(Routes.addFiles(deviceId = deviceId)) },
            )
        }
        composable(
            Routes.ADD_FILES,
            arguments = listOf(
                navArgument("deviceId") { type = NavType.StringType; nullable = true; defaultValue = null },
                navArgument("share") { type = NavType.StringType; nullable = true; defaultValue = null },
            ),
        ) { entry ->
            AddFilesScreen(
                deviceId = entry.arguments?.getString("deviceId"),
                shareToken = entry.arguments?.getString("share"),
                onBack = { nav.popBackStack() },
                onSent = { deviceId ->
                    nav.popBackStack()
                    // The Transfers screen of the Console tab (M23).
                    if (nav.currentBackStackEntry?.arguments?.getString("deviceId") == deviceId) {
                        nav.navigate(Routes.dashboard(deviceId)) { tabOptions() }
                    }
                    nav.navigate(Routes.transfers(deviceId)) { launchSingleTop = true }
                },
            )
        }
    }
}

@Composable
fun ReminisLinkTheme(content: @Composable () -> Unit) {
    val dark = isSystemInDarkTheme()
    val context = LocalContext.current
    val colors = when {
        Build.VERSION.SDK_INT >= Build.VERSION_CODES.S -> if (dark) dynamicDarkColorScheme(context) else dynamicLightColorScheme(context)
        dark -> darkColorScheme()
        else -> lightColorScheme()
    }
    MaterialTheme(colorScheme = colors, content = content)
}
