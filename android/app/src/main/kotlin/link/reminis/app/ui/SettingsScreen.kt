package link.reminis.app.ui

import android.content.Context
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.material3.ListItem
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import link.reminis.app.R

/**
 * The settings of the app (docs/specs/06-app-ios.md §4.9), opened from the
 * list of consoles. For now the About section: the version of the app
 * (`versionName`), so that a bug report can say it ("Settings > About" on
 * iOS and Android alike).
 */
@Composable
fun SettingsScreen(onBack: () -> Unit) {
    val context = LocalContext.current
    val version = remember { appVersion(context) }
    AppScaffold(title = stringResource(R.string.settings_title), onBack = onBack) { modifier ->
        LazyColumn(modifier.fillMaxSize().testTag("settings")) {
            item { SectionTitle(stringResource(R.string.settings_about)) }
            item {
                ListItem(
                    headlineContent = { Text(stringResource(R.string.settings_version)) },
                    trailingContent = { Text(version, modifier = Modifier.testTag("appVersion")) },
                )
            }
        }
    }
}

/** The `versionName` of the app (`android/version.properties`). */
fun appVersion(context: Context): String = runCatching {
    context.packageManager.getPackageInfo(context.packageName, 0).versionName
}.getOrNull().orEmpty()
