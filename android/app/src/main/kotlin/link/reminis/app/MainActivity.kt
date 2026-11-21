package link.reminis.app

import android.content.Intent
import android.net.Uri
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
import androidx.compose.ui.platform.LocalContext
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import link.reminis.app.ui.AddConsoleScreen
import link.reminis.app.ui.ConsolesScreen
import link.reminis.app.ui.DashboardScreen
import link.reminis.app.ui.GameScreen
import link.reminis.app.ui.LibraryScreen
import link.reminis.app.ui.PairingScreen
import link.reminis.app.ui.ScanScreen

/** Navigation routes. Ids are base64url and links are URL-encoded in routes. */
object Routes {
    const val CONSOLES = "consoles"
    const val ADD = "add"
    const val SCAN = "scan"
    const val PAIR = "pair?link={link}"
    const val DASHBOARD = "console/{deviceId}"
    const val LIBRARY = "console/{deviceId}/library"
    const val GAME = "console/{deviceId}/game/{gameId}"

    fun pair(link: String) = "pair?link=${Uri.encode(link)}"
    fun dashboard(deviceId: String) = "console/$deviceId"
    fun library(deviceId: String) = "console/$deviceId/library"
    fun game(deviceId: String, gameId: String) = "console/$deviceId/game/$gameId"
}

class MainActivity : ComponentActivity() {
    /** A pairing link opened from another app (`reminislink://pair?...`). */
    private val incomingLink = mutableStateOf<String?>(null)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        incomingLink.value = pairingLink(intent)
        setContent {
            ReminisLinkTheme {
                AppNavigation(incomingLink.value) { incomingLink.value = null }
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        pairingLink(intent)?.let { incomingLink.value = it }
    }

    private fun pairingLink(intent: Intent?): String? =
        intent?.takeIf { it.action == Intent.ACTION_VIEW }?.dataString?.takeIf { it.startsWith("reminislink:", ignoreCase = true) }
}

@Composable
fun AppNavigation(incomingLink: String?, onLinkHandled: () -> Unit) {
    val nav = rememberNavController()
    LaunchedEffect(incomingLink) {
        if (incomingLink != null) {
            nav.navigate(Routes.pair(incomingLink))
            onLinkHandled()
        }
    }
    val id = navArgument("deviceId") { type = NavType.StringType }
    NavHost(navController = nav, startDestination = Routes.CONSOLES) {
        composable(Routes.CONSOLES) {
            ConsolesScreen(
                onAdd = { nav.navigate(Routes.ADD) },
                onOpen = { nav.navigate(Routes.dashboard(it)) },
            )
        }
        composable(Routes.ADD) {
            AddConsoleScreen(
                onBack = { nav.popBackStack() },
                onScan = { nav.navigate(Routes.SCAN) },
                onLink = { nav.navigate(Routes.pair(it)) },
            )
        }
        composable(Routes.SCAN) {
            ScanScreen(
                onBack = { nav.popBackStack() },
                onLink = { link ->
                    nav.navigate(Routes.pair(link)) { popUpTo(Routes.ADD) }
                },
            )
        }
        composable(Routes.PAIR, arguments = listOf(navArgument("link") { type = NavType.StringType; defaultValue = "" })) { entry ->
            PairingScreen(
                link = entry.arguments?.getString("link").orEmpty(),
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
                onForgotten = { nav.popBackStack(Routes.CONSOLES, inclusive = false) },
            )
        }
        composable(Routes.LIBRARY, arguments = listOf(id)) { entry ->
            val deviceId = entry.arguments?.getString("deviceId").orEmpty()
            LibraryScreen(
                deviceId = deviceId,
                onBack = { nav.popBackStack() },
                onGame = { nav.navigate(Routes.game(deviceId, it)) },
            )
        }
        composable(Routes.GAME, arguments = listOf(id, navArgument("gameId") { type = NavType.StringType })) { entry ->
            GameScreen(
                deviceId = entry.arguments?.getString("deviceId").orEmpty(),
                gameId = entry.arguments?.getString("gameId").orEmpty(),
                onBack = { nav.popBackStack() },
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
