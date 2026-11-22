package link.reminis.app.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import kotlinx.coroutines.delay

/**
 * Runs [action] every [everyMillis] while the screen is visible and the app
 * in the foreground (docs/specs/03-protocole.md §8: the jobs are polled
 * every second while a screen that shows them is visible, never otherwise).
 */
@Composable
fun PollWhileVisible(key: Any?, everyMillis: Long = 1_000, action: suspend () -> Unit) {
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    LaunchedEffect(key, lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.RESUMED) {
            while (true) {
                runCatching { action() }.onFailure { if (it is kotlinx.coroutines.CancellationException) throw it }
                delay(everyMillis)
            }
        }
    }
}
