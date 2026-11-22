package link.reminis.app.ui

import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import link.reminis.app.R

/** The notice or the error of the last action, above a screen's content. */
@Composable
fun MessageCard(notice: Notice?, error: Throwable?, onDismiss: () -> Unit) {
    val context = LocalContext.current
    val text = when {
        error != null -> errorMessage(context, error)
        notice != null -> context.getString(notice.id, *notice.args.toTypedArray())
        else -> return
    }
    Card(
        colors = if (error != null) {
            CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.errorContainer)
        } else {
            CardDefaults.cardColors()
        },
        modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp)
            .testTag(if (error != null) "actionError" else "notice"),
        onClick = onDismiss,
    ) {
        Text(text, modifier = Modifier.padding(16.dp))
    }
}

/** A confirmation before an action that changes the console. */
@Composable
fun ConfirmDialog(
    title: String,
    message: String,
    confirm: String,
    onConfirm: () -> Unit,
    onDismiss: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = { Text(message) },
        confirmButton = {
            TextButton(onClick = { onConfirm(); onDismiss() }, modifier = Modifier.testTag("confirmOk")) { Text(confirm) }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_cancel)) } },
    )
}

/** A choice among actions (the actions of a backup copy or of a state slot). */
@Composable
fun ActionsDialog(title: String, subtitle: String?, actions: List<Pair<String, () -> Unit>>, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                subtitle?.let { Text(it, style = MaterialTheme.typography.bodyMedium) }
                actions.forEachIndexed { index, (label, action) ->
                    TextButton(
                        onClick = { onDismiss(); action() },
                        modifier = Modifier.fillMaxWidth().testTag("action$index"),
                    ) { Text(label) }
                }
            }
        },
        confirmButton = {},
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_cancel)) } },
    )
}

/**
 * Asks the user where to save a download (`ACTION_CREATE_DOCUMENT`, no
 * storage permission), then runs the action with the document chosen.
 */
class DocumentCreator(private val launch: (String) -> Unit, private val setPending: ((Uri) -> Unit) -> Unit) {
    fun create(suggestedName: String, action: (Uri) -> Unit) {
        setPending(action)
        launch(suggestedName)
    }
}

/**
 * Actions waiting for a system picker's result, by an opaque key. Kept in a
 * ViewModel: an activity recreated while the picker is open (rotation,
 * dark mode...) still runs the action. When the action is gone (the process
 * died meanwhile), the document the picker just created is deleted instead
 * of being left empty.
 */
class PendingActions<T> {
    private val actions = mutableMapOf<String, (T) -> Unit>()

    fun put(key: String, action: (T) -> Unit) {
        actions[key] = action
    }

    /** Runs the action waiting for [key] with [result]; an orphan result goes to [orphan]. */
    fun complete(key: String, result: T?, orphan: (T) -> Unit) {
        val action = actions.remove(key)
        if (result == null) return
        if (action != null) action(result) else orphan(result)
    }
}

class DocumentRequests : androidx.lifecycle.ViewModel() {
    val pending = PendingActions<Uri>()
}

@Composable
fun rememberDocumentCreator(): DocumentCreator {
    val context = LocalContext.current.applicationContext
    // Scoped to the screen's back stack entry: it survives an activity recreation.
    val requests: DocumentRequests = androidx.lifecycle.viewmodel.compose.viewModel()
    val key = androidx.compose.runtime.saveable.rememberSaveable { java.util.UUID.randomUUID().toString() }
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument("application/octet-stream")) { uri ->
        requests.pending.complete(key, uri) { orphan ->
            Thread { link.reminis.app.DocumentSink.deleteDocument(context, orphan) }.start()
        }
    }
    return remember(launcher, key) { DocumentCreator({ launcher.launch(it) }, { requests.pending.put(key, it) }) }
}

/** A date of the console (RFC 3339), in the phone's time zone and locale. */
fun formatDate(text: String): String = try {
    OffsetDateTime.parse(text).atZoneSameInstant(ZoneId.systemDefault())
        .format(DateTimeFormatter.ofLocalizedDateTime(FormatStyle.MEDIUM, FormatStyle.SHORT))
} catch (_: Exception) {
    text
}
