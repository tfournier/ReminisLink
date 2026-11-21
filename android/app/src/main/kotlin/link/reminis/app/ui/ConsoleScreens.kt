package link.reminis.app.ui

import android.app.Application
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Check
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
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
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.viewmodel.compose.viewModel
import link.reminis.app.R
import link.reminis.app.container
import link.reminis.core.DeviceModels
import link.reminis.core.PairingLink
import link.reminis.core.PairingLinkException

// MARK: consoles list

@Composable
fun ConsolesScreen(onAdd: () -> Unit, onOpen: (String) -> Unit) {
    val vm: ConsolesViewModel = viewModel()
    val consoles by vm.consoles.collectAsState()
    val online by vm.online.collectAsState()
    var forgetting by remember { mutableStateOf<String?>(null) }

    LifecycleResumeEffect(Unit) {
        vm.reload()
        vm.startBrowsing()
        onPauseOrDispose { vm.stopBrowsing() }
    }

    AppScaffold(
        title = stringResource(R.string.consoles_title),
        onBack = null,
        floatingActionButton = {
            if (consoles.isNotEmpty()) {
                ExtendedFloatingActionButton(
                    onClick = onAdd,
                    icon = { Icon(Icons.Filled.Add, contentDescription = null) },
                    text = { Text(stringResource(R.string.add_console)) },
                )
            }
        },
    ) { modifier ->
        if (consoles.isEmpty()) {
            Welcome(onAdd, modifier)
        } else {
            LazyColumn(modifier.fillMaxSize().testTag("consoles")) {
                items(consoles, key = { it.deviceId }) { console ->
                    ListItem(
                        headlineContent = { Text(console.deviceName) },
                        supportingContent = {
                            val model = console.model?.let(DeviceModels::displayName).orEmpty()
                            val status = stringResource(if (console.deviceId in online) R.string.status_online else R.string.status_offline)
                            Text(listOf(model, status).filter { it.isNotEmpty() }.joinToString(" · "))
                        },
                        trailingContent = {
                            TextButton(onClick = { forgetting = console.deviceId }) { Text(stringResource(R.string.forget)) }
                        },
                        modifier = Modifier.clickable { onOpen(console.deviceId) },
                    )
                    HorizontalDivider()
                }
            }
        }
    }

    forgetting?.let { id ->
        val name = consoles.firstOrNull { it.deviceId == id }?.deviceName.orEmpty()
        ForgetDialog(name, onConfirm = { vm.forget(id); forgetting = null }, onDismiss = { forgetting = null })
    }
}

@Composable
fun ForgetDialog(name: String, onConfirm: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.forget_title)) },
        text = { Text(stringResource(R.string.forget_message, name)) },
        confirmButton = { TextButton(onClick = onConfirm) { Text(stringResource(R.string.forget)) } },
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_cancel)) } },
    )
}

@Composable
private fun Welcome(onAdd: () -> Unit, modifier: Modifier) {
    Column(
        modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        Text(stringResource(R.string.welcome_title), style = MaterialTheme.typography.headlineSmall)
        listOf(R.string.welcome_step_install, R.string.welcome_step_open, R.string.welcome_step_scan)
            .forEachIndexed { i, step ->
                Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    Text("${i + 1}.", style = MaterialTheme.typography.titleMedium)
                    Text(stringResource(step), style = MaterialTheme.typography.bodyLarge)
                }
            }
        Button(onClick = onAdd, modifier = Modifier.fillMaxWidth().testTag("addConsole")) {
            Text(stringResource(R.string.add_console))
        }
    }
}

// MARK: add a console

@Composable
fun AddConsoleScreen(onBack: () -> Unit, onScan: () -> Unit, onLink: (String) -> Unit) {
    val context = LocalContext.current
    val container = context.container
    var name by rememberSaveable { mutableStateOf(container.clientName) }
    var link by rememberSaveable { mutableStateOf("") }
    var linkError by remember { mutableStateOf<String?>(null) }

    fun saveName() {
        container.clientName = name.trim().ifEmpty { container.defaultClientName }
    }

    AppScaffold(title = stringResource(R.string.add_console), onBack = onBack) { modifier ->
        Column(
            modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(24.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            OutlinedTextField(
                value = name,
                onValueChange = { name = it },
                label = { Text(stringResource(R.string.client_name_label)) },
                supportingText = { Text(stringResource(R.string.client_name_help)) },
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
            )
            Text(stringResource(R.string.add_console_help), style = MaterialTheme.typography.bodyLarge)
            Button(onClick = { saveName(); onScan() }, modifier = Modifier.fillMaxWidth()) {
                Text(stringResource(R.string.scan_qr))
            }
            HorizontalDivider()
            OutlinedTextField(
                value = link,
                onValueChange = { link = it; linkError = null },
                label = { Text(stringResource(R.string.paste_link_label)) },
                isError = linkError != null,
                supportingText = linkError?.let { { Text(it) } },
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Go),
                modifier = Modifier.fillMaxWidth().testTag("pairingLink"),
            )
            OutlinedButton(
                onClick = {
                    try {
                        PairingLink.parse(link)
                        saveName()
                        onLink(link.trim())
                    } catch (e: PairingLinkException) {
                        linkError = errorMessage(context, e)
                    }
                },
                enabled = link.isNotBlank(),
                modifier = Modifier.fillMaxWidth().testTag("pairWithLink"),
            ) { Text(stringResource(R.string.paste_link_action)) }
        }
    }
}

// MARK: pairing

@Composable
fun PairingScreen(link: String, onClose: () -> Unit, onOpenConsole: (String) -> Unit) {
    val context = LocalContext.current
    val parsed = remember(link) { runCatching { PairingLink.parse(link) } }
    AppScaffold(title = stringResource(R.string.pairing_title), onBack = onClose) { modifier ->
        parsed.fold(
            onSuccess = { pairingLink ->
                val app = context.applicationContext as Application
                val vm: PairingViewModel = viewModel(key = link) { PairingViewModel(app, pairingLink) }
                val state by vm.state.collectAsState()
                PairingProgress(state, modifier, onRetry = vm::start, onClose = onClose, onOpenConsole = onOpenConsole)
            },
            onFailure = { ErrorView(it, null, modifier) },
        )
    }
}

@Composable
private fun PairingProgress(
    state: PairingState,
    modifier: Modifier,
    onRetry: () -> Unit,
    onClose: () -> Unit,
    onOpenConsole: (String) -> Unit,
) {
    if (state.step == PairingStep.CONFIRM) {
        AlertDialog(
            onDismissRequest = onClose,
            title = { Text(stringResource(R.string.already_paired_title)) },
            text = { Text(stringResource(R.string.already_paired_message)) },
            confirmButton = { TextButton(onClick = onRetry) { Text(stringResource(R.string.pair_again)) } },
            dismissButton = { TextButton(onClick = onClose) { Text(stringResource(R.string.action_cancel)) } },
        )
        return
    }
    if (state.error != null) {
        ErrorView(state.error, onRetry, modifier)
        return
    }
    Column(
        modifier.fillMaxSize().padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(20.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        state.deviceName?.let { Text(it, style = MaterialTheme.typography.headlineSmall, textAlign = TextAlign.Center) }
        val steps = listOf(
            PairingStep.SEARCHING to R.string.pairing_step_search,
            PairingStep.PAIRING to R.string.pairing_step_pair,
            PairingStep.DONE to R.string.pairing_step_done,
        )
        for ((step, label) in steps) {
            Row(
                Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                when {
                    state.step.ordinal > step.ordinal || state.step == PairingStep.DONE ->
                        Icon(Icons.Filled.Check, contentDescription = null)
                    state.step == step -> CircularProgressIndicator(Modifier.padding(2.dp))
                    else -> Text("·", style = MaterialTheme.typography.titleLarge)
                }
                Text(stringResource(label), style = MaterialTheme.typography.titleMedium)
            }
        }
        if (state.step == PairingStep.DONE && state.deviceId != null) {
            Text(stringResource(R.string.pairing_done, state.deviceName.orEmpty()), modifier = Modifier.testTag("paired"))
            Button(onClick = { onOpenConsole(state.deviceId) }, modifier = Modifier.fillMaxWidth().testTag("openConsole")) {
                Text(stringResource(R.string.pairing_open_console))
            }
        }
    }
}
