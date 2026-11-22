package link.reminis.app.ui

import android.content.Context
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import link.reminis.app.ClosedNotice
import link.reminis.app.R
import link.reminis.core.DeviceModels
import link.reminis.core.demo.FakePak

/** The model shown for a console: product names stay as they are; the demo console is "Demo mode" (M16). */
fun modelName(context: Context, model: String): String =
    if (model == FakePak.MODEL) context.getString(R.string.demo_mode) else DeviceModels.displayName(model)

/** The banner of the demo mode (M16), above every screen while it runs, with Quit (as on iOS). */
@Composable
fun DemoBanner(onQuit: () -> Unit, modifier: Modifier = Modifier) {
    Row(
        modifier.fillMaxWidth().background(MaterialTheme.colorScheme.tertiaryContainer).padding(horizontal = 16.dp, vertical = 8.dp)
            .testTag("demoBanner"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Icon(Icons.Filled.PlayArrow, contentDescription = null, tint = MaterialTheme.colorScheme.onTertiaryContainer)
        Column(Modifier.weight(1f)) {
            Text(
                stringResource(R.string.demo_banner_title),
                style = MaterialTheme.typography.titleSmall,
                color = MaterialTheme.colorScheme.onTertiaryContainer,
            )
            Text(
                stringResource(R.string.demo_banner_message),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onTertiaryContainer,
            )
        }
        val description = stringResource(R.string.demo_quit_description)
        Button(onClick = onQuit, modifier = Modifier.semantics { contentDescription = description }.testTag("demoQuit")) {
            Text(stringResource(R.string.demo_quit))
        }
    }
}

/** "The console closed ReminisLink" (M11): the app came back to the list when the pak quit; its uploads wait, paused. */
@Composable
fun ClosedNoticeCard(notice: ClosedNotice, onDismiss: () -> Unit, modifier: Modifier = Modifier) {
    Card(modifier.fillMaxWidth().padding(16.dp).testTag("closedNotice")) {
        Row(Modifier.padding(16.dp), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text(stringResource(R.string.closed_title), style = MaterialTheme.typography.titleMedium)
                if (notice.name.isNotEmpty()) Text(notice.name, style = MaterialTheme.typography.bodyMedium)
                Text(stringResource(R.string.closed_message), style = MaterialTheme.typography.bodySmall)
            }
            IconButton(onClick = onDismiss, modifier = Modifier.testTag("closedNoticeDismiss")) {
                Icon(Icons.Filled.Close, contentDescription = stringResource(R.string.action_dismiss))
            }
        }
    }
}
