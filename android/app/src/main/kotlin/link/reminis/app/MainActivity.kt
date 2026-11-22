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
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.ui.Modifier
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
import link.reminis.app.ui.LibraryScreen
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
        if (single != null && link.reminis.core.PatchNaming.isPatchFileName(LocalFiles.displayName(contentResolver, single))) {
            incomingPatch.value = token
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
) {
    val nav = rememberNavController()
    val links = LocalContext.current.container.pendingLinks
    LaunchedEffect(incomingLink) {
        if (incomingLink != null) {
            // From another app: the pairing screen asks before any network access.
            nav.navigate(Routes.pair(links.put(incomingLink, external = true)))
            onLinkHandled()
        }
    }
    LaunchedEffect(incomingShare) {
        if (incomingShare != null) {
            nav.navigate(Routes.addFiles(share = incomingShare))
            onShareHandled()
        }
    }
    LaunchedEffect(incomingPatch) {
        if (incomingPatch != null) {
            nav.navigate(Routes.patch(share = incomingPatch))
            onPatchHandled()
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
    Column(Modifier.fillMaxSize()) {
        if (demo != null) DemoBanner(onQuit = { quitDemo() }, modifier = Modifier.windowInsetsPadding(WindowInsets.statusBars))
        val insets = if (demo != null) Modifier.consumeWindowInsets(WindowInsets.statusBars) else Modifier
        Box(Modifier.weight(1f).then(insets)) {
            AppNavHost(nav, links)
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
            )
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
                onBack = { nav.popBackStack() },
                onLibrary = { nav.navigate(Routes.library(deviceId)) },
                onTransfers = { nav.navigate(Routes.transfers(deviceId)) },
                onApps = { nav.navigate(Routes.apps(deviceId)) },
                onTrash = { nav.navigate(Routes.trash(deviceId)) },
                onForgotten = { nav.popBackStack(Routes.CONSOLES, inclusive = false) },
            )
        }
        composable(Routes.LIBRARY, arguments = listOf(id)) { entry ->
            val deviceId = entry.arguments?.getString("deviceId").orEmpty()
            LibraryScreen(
                deviceId = deviceId,
                onBack = { nav.popBackStack() },
                onGame = { nav.navigate(Routes.game(deviceId, it)) },
                onAdd = { nav.navigate(Routes.addFiles(deviceId = deviceId)) },
            )
        }
        val gameId = navArgument("gameId") { type = NavType.StringType }
        composable(Routes.GAME, arguments = listOf(id, gameId)) { entry ->
            val deviceId = entry.arguments?.getString("deviceId").orEmpty()
            val game = entry.arguments?.getString("gameId").orEmpty()
            GameScreen(
                deviceId = deviceId,
                gameId = game,
                onBack = { nav.popBackStack() },
                onSave = { nav.navigate(Routes.save(deviceId, game)) },
                onStates = { name -> nav.navigate(Routes.states(deviceId, game, name)) },
                onCheats = { nav.navigate(Routes.cheats(deviceId, game)) },
                onPatch = { nav.navigate(Routes.patch(deviceId = deviceId, gameId = game)) },
                onDeleted = { nav.popBackStack() },
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
            PaksScreen(deviceId = entry.arguments?.getString("deviceId").orEmpty(), onBack = { nav.popBackStack() })
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
