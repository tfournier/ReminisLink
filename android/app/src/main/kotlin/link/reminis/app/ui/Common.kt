package link.reminis.app.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import link.reminis.app.R

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AppScaffold(
    title: String,
    onBack: (() -> Unit)?,
    actions: @Composable () -> Unit = {},
    floatingActionButton: @Composable () -> Unit = {},
    bottomBar: @Composable () -> Unit = {},
    /**
     * Lists and forms keep a readable width in a wide window (M15 on
     * Android: tablets, foldables, desktop windows), centred; a grid
     * (screenshots) or a two-pane layout takes the whole width.
     */
    readable: Boolean = true,
    content: @Composable (Modifier) -> Unit,
) {
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(title) },
                navigationIcon = {
                    if (onBack != null) {
                        IconButton(onClick = onBack) {
                            Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = stringResource(R.string.action_back))
                        }
                    }
                },
                actions = { actions() },
            )
        },
        floatingActionButton = floatingActionButton,
        bottomBar = bottomBar,
    ) { padding ->
        if (readable) {
            Box(Modifier.fillMaxSize().padding(padding), contentAlignment = Alignment.TopCenter) {
                content(Modifier.widthIn(max = READABLE_WIDTH))
            }
        } else {
            content(Modifier.padding(padding))
        }
    }
}

/** The widest a list or a form gets (like iOS M15: 700 pt). */
val READABLE_WIDTH = 720.dp

@Composable
fun LoadingView(modifier: Modifier = Modifier) {
    Box(modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        CircularProgressIndicator()
    }
}

@Composable
fun ErrorView(
    error: Throwable,
    onRetry: (() -> Unit)?,
    modifier: Modifier = Modifier,
    message: String? = null,
    @androidx.annotation.StringRes retryLabel: Int = R.string.action_retry,
) {
    val context = LocalContext.current
    Box(modifier.fillMaxSize().padding(24.dp), contentAlignment = Alignment.Center) {
        Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(16.dp)) {
            Text(
                message ?: errorMessage(context, error),
                textAlign = TextAlign.Center,
                style = MaterialTheme.typography.bodyLarge,
                modifier = Modifier.testTag("error"),
            )
            if (onRetry != null) Button(onClick = onRetry, modifier = Modifier.testTag("retry")) { Text(stringResource(retryLabel)) }
        }
    }
}

@Composable
fun <T> LoadContent(
    load: Load<T>,
    onRetry: () -> Unit,
    modifier: Modifier = Modifier,
    content: @Composable (T) -> Unit,
) {
    when (load) {
        Load.Loading -> LoadingView(modifier)
        is Load.Failed -> ErrorView(load.error, onRetry, modifier)
        is Load.Loaded -> content(load.value)
    }
}
