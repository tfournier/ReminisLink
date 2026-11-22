package link.reminis.app

import android.content.Context
import android.content.Intent
import android.provider.DocumentsContract
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.printToLog
import androidx.test.core.app.ActivityScenario
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import java.io.ByteArrayOutputStream
import java.security.MessageDigest
import kotlinx.coroutines.runBlocking
import link.reminis.core.DownloadSource
import link.reminis.core.Endpoint
import link.reminis.core.PairedConsole
import link.reminis.core.TlsPin
import link.reminis.core.demo.FakePak
import link.reminis.core.vault.VaultLocation
import link.reminis.core.vault.VaultUnavailableException
import okhttp3.Headers.Companion.toHeaders
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import okhttp3.tls.HandshakeCertificates
import okhttp3.tls.HeldCertificate
import okio.Buffer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Increment 7 in the app: the sync of saves and states. Against a console
 * paired like a real one (a local TLS server with a pinned certificate)
 * whose routes are answered by the fake pak of the demo: a real folder of
 * the Android picker (SAF, chosen in the system's DocumentsUI), a second
 * version after a change on the console, a version restored, the folder's
 * permission revoked then the app's storage chosen; and the demo, which
 * syncs to a temporary folder erased when it ends. Invented names only.
 */
@RunWith(AndroidJUnit4::class)
class SyncUiTest {
    @get:Rule
    val compose = createEmptyComposeRule()

    private val context: Context get() = ApplicationProvider.getApplicationContext()
    private lateinit var pak: FakePak
    private lateinit var server: MockWebServer
    private val folderName = "RLSyncTest${System.currentTimeMillis() % 1_000_000}"

    @Before
    fun setUp() {
        grantNotifications()
        val container = context.container
        InstrumentationHelper.onMain { container.quitDemo() }
        container.consoles.all().forEach { container.forget(it.deviceId) }
        container.sync.useInternalStorage()
        container.sync.setSyncOnConnect(false)
        pak = FakePak(deviceName = "Hazel Test Console", cheatName = { "Test cheat $it" })
        val certificate = HeldCertificate.Builder().ecdsa256().commonName(pak.deviceId).build()
        server = MockWebServer()
        server.useHttps(HandshakeCertificates.Builder().heldCertificate(certificate).build().sslSocketFactory(), false)
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse = proxy(request)
        }
        server.start(java.net.InetAddress.getByName("127.0.0.1"), 0)
        container.replacePairing(
            PairedConsole(
                deviceId = pak.deviceId, deviceName = "Hazel Test Console", model = "desktop",
                pin = TlsPin.pin(certificate.certificate), lastEndpoint = Endpoint("127.0.0.1", server.port),
                pairedAt = System.currentTimeMillis(),
            ),
        )
    }

    @After
    fun tearDown() {
        val container = context.container
        InstrumentationHelper.onMain { container.quitDemo() }
        container.forget(pak.deviceId)
        container.sync.useInternalStorage()
        container.sync.setSyncOnConnect(true)
        container.sync.initialFolderForTests = null
        server.shutdown()
        pak.close()
        shell("rm -rf /sdcard/Documents/$folderName")
        java.io.File(context.filesDir, "SyncedSaves").deleteRecursively()
    }

    /** The fake pak behind the pinned TLS server; no event stream (an older pak: the screens poll). */
    private fun proxy(recorded: RecordedRequest): MockResponse {
        if (recorded.path.orEmpty().startsWith("/api/v1/events")) {
            return MockResponse().setResponseCode(404).setHeader("Content-Type", "application/json")
                .setBody("""{"error":{"code":"not_found","message":"No route","details":{}}}""")
        }
        val bytes = recorded.body.readByteArray()
        val body = if (recorded.method == "GET" || recorded.method == "DELETE") null else bytes.toRequestBody(recorded.getHeader("Content-Type")?.toMediaTypeOrNull())
        val request = Request.Builder()
            .url("https://127.0.0.1:${server.port}${recorded.path}")
            .headers(recorded.headers.toMultimap().mapValues { it.value.first() }.toHeaders())
            .method(recorded.method!!, body)
            .build()
        val response = pak.handle(request)
        val out = MockResponse().setResponseCode(response.code)
        response.headers.forEach { (name, value) -> out.addHeader(name, value) }
        response.body?.let { b ->
            b.contentType()?.let { out.setHeader("Content-Type", it.toString()) }
            out.setBody(Buffer().write(b.bytes()))
        }
        return out
    }

    private fun shell(command: String): String {
        val automation = InstrumentationRegistry.getInstrumentation().uiAutomation
        return automation.executeShellCommand(command).use { pfd ->
            android.os.ParcelFileDescriptor.AutoCloseInputStream(pfd).bufferedReader().readText()
        }
    }

    private fun waitFor(what: String, timeout: Long = 30_000, condition: () -> Boolean) {
        try {
            compose.waitUntil(timeout, condition)
        } catch (e: Throwable) {
            compose.onRoot(useUnmergedTree = true).printToLog("SyncUiTest")
            throw AssertionError("timeout waiting for $what", e)
        }
    }

    private fun hasTag(tag: String) = compose.onAllNodesWithTag(tag, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()
    private fun hasText(text: String) = compose.onAllNodesWithText(text, substring = true).fetchSemanticsNodes().isNotEmpty()

    private fun sha(bytes: ByteArray) = MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") { "%02x".format(it) }

    private val client get() = runBlocking { context.container.client(pak.deviceId) }

    /** Taps a button of the system's DocumentsUI, found by its text (accessibility). */
    private fun tapInPicker(vararg labels: String) {
        val automation = InstrumentationRegistry.getInstrumentation().uiAutomation
        automation.serviceInfo = automation.serviceInfo.apply { flags = flags or android.accessibilityservice.AccessibilityServiceInfo.FLAG_RETRIEVE_INTERACTIVE_WINDOWS }
        var tapped = false
        repeat(30) {
            if (tapped) return@repeat
            // The confirmation is a dialog window of its own: every window of the picker is searched.
            val roots = (automation.windows.mapNotNull { it.root } + listOfNotNull(automation.rootInActiveWindow))
                .filter { it.packageName?.contains("documentsui") == true }
            for (root in roots) {
                if (tapped) break
                for (label in labels) {
                    val node = (if (label.startsWith("id:")) root.findAccessibilityNodeInfosByViewId(label.removePrefix("id:")) else root.findAccessibilityNodeInfosByText(label))
                        .firstOrNull { it.isEnabled && (label.startsWith("id:") || it.text?.toString()?.equals(label, ignoreCase = true) == true) }
                    var target = node
                    while (target != null && !target.isClickable) target = target.parent
                    if (target != null && target.performAction(android.view.accessibility.AccessibilityNodeInfo.ACTION_CLICK)) {
                        tapped = true
                        break
                    }
                }
            }
            if (!tapped) Thread.sleep(1_000)
        }
        assertTrue("the picker's ${labels.first()} button was not found", tapped)
    }

    /**
     * Taps the button showing [text] by its bounds on screen (`uiautomator
     * dump`, then `input tap`): the picker's confirmation is a dialog window
     * that accessibility actions did not reach.
     */
    private fun tapOnScreen(text: String) {
        val automation = InstrumentationRegistry.getInstrumentation().uiAutomation
        automation.serviceInfo = automation.serviceInfo.apply { flags = flags or android.accessibilityservice.AccessibilityServiceInfo.FLAG_RETRIEVE_INTERACTIVE_WINDOWS }
        repeat(20) {
            val node = automation.windows.mapNotNull { it.root }.flatMap { it.findAccessibilityNodeInfosByText(text) }
                .firstOrNull { it.text?.toString()?.equals(text, ignoreCase = true) == true }
            if (node != null) {
                val bounds = android.graphics.Rect().also(node::getBoundsInScreen)
                shell("input tap ${bounds.centerX()} ${bounds.centerY()}")
                return
            }
            Thread.sleep(1_000)
        }
        throw AssertionError("the button $text was not found on screen")
    }

    private fun openConsoleAndSync() {
        waitFor("consoles") { hasText("Hazel Test Console") }
        compose.onAllNodesWithText("Hazel Test Console")[0].performClick()
        waitFor("dashboard") { hasTag("dashboard") }
        compose.onNodeWithTag("dashboard").performScrollToNode(hasTestTag("syncNow"))
        compose.onNodeWithTag("syncNow").performClick()
        waitFor("sync report") { hasTag("syncReport") }
    }

    @Test
    fun syncToAFolderOfThePickerRestoreAndRevoke() {
        shell("mkdir -p /sdcard/Documents/$folderName")
        context.container.sync.initialFolderForTests =
            DocumentsContract.buildDocumentUri("com.android.externalstorage.documents", "primary:Documents/$folderName")
        val game = runBlocking { client.saves() }.saves.first { it.rtc != null }
        val firstSave = game.sha256
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            // Settings: the folder chosen with the system's picker.
            waitFor("consoles") { hasTag("openSettings") }
            compose.onNodeWithTag("openSettings").performClick()
            waitFor("settings") { hasTag("syncChooseFolder") }
            assertTrue(hasText(context.getString(R.string.sync_place_internal)))
            compose.onNodeWithTag("syncChooseFolder").performClick()
            tapInPicker("USE THIS FOLDER", "Use this folder")
            tapOnScreen("ALLOW")
            var chosen = false
            repeat(30) { if (!chosen) { chosen = context.container.sync.place.value.location == VaultLocation.FOLDER; if (!chosen) Thread.sleep(1_000) } }
            assertTrue("the folder was not taken", chosen)
            waitFor("folder shown") { hasText(folderName) }
            scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }

            // The first sync: every save and state copied into the folder.
            openConsoleAndSync()
            val listing = shell("ls -R /sdcard/Documents/$folderName")
            assertTrue(listing, listing.contains("Hazel Test Console"))
            assertTrue(listing, listing.contains(".version.json") || shell("ls -aR /sdcard/Documents/$folderName").contains(".version.json"))
            val vault = context.container.sync.vault(pak.deviceId)
            assertEquals(VaultLocation.FOLDER, vault.location)
            assertEquals(1, vault.versions(pak.deviceId, game.gameId).size)
            assertNotNull(vault.versions(pak.deviceId, game.gameId).single().info.rtc)

            // The save changes on the console: a second version.
            runBlocking {
                client.replaceSave(game.gameId, java.io.File(context.cacheDir, "hazel.sav").apply { writeText("HAZEL CHANGED") })
            }
            compose.onNodeWithTag("syncNow").performClick()
            waitFor("second version") { context.container.sync.vault(pak.deviceId).versions(pak.deviceId, game.gameId).size == 2 }
            waitFor("sync done") { hasTag("syncNow") }

            // The first version restored from the synced copies.
            compose.onNodeWithTag("dashboard").performScrollToNode(hasTestTag("openSyncedSaves"))
            compose.onNodeWithTag("openSyncedSaves").performClick()
            waitFor("synced games") { hasTag("syncedGames") }
            compose.onNodeWithTag("syncedGames").performScrollToNode(androidx.compose.ui.test.hasText(game.gameName, substring = true))
            compose.onAllNodesWithText(game.gameName, substring = true)[0].performClick()
            waitFor("versions") { hasTag("syncedVersion1") }
            assertTrue(hasText(context.getString(R.string.synced_with_clock)))
            compose.onNodeWithTag("syncedVersion1").performClick()
            compose.onNodeWithTag("action0").performClick()
            compose.onNodeWithTag("confirmOk").performClick()
            waitFor("restored") { hasText(context.getString(R.string.synced_restored)) }
            val restored = ByteArrayOutputStream()
            runBlocking { client.download(DownloadSource.Save(game.gameId), restored) }
            assertEquals(firstSave, sha(restored.toByteArray()))
            scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }
            scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }

            // The folder's permission revoked: the sync says it, nothing is lost, and the app's storage can be chosen.
            val tree = context.container.sync.folderUri!!
            context.contentResolver.releasePersistableUriPermission(tree, Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION)
            waitFor("dashboard") { hasTag("dashboard") }
            compose.onNodeWithTag("dashboard").performScrollToNode(hasTestTag("syncNow"))
            compose.onNodeWithTag("syncNow").performClick()
            waitFor("unavailable") { hasTag("syncUnavailable") }
            assertTrue(context.container.sync.state(pak.deviceId).phase.let { it is SyncPhase.Failed && it.error is VaultUnavailableException })
            assertTrue("the copies stay in the folder", shell("ls -R /sdcard/Documents/$folderName").contains("Hazel Test Console"))
            compose.onNodeWithTag("dashboard").performScrollToNode(hasTestTag("syncUseInternal"))
            compose.onNodeWithTag("syncUseInternal").performClick()
            waitFor("internal storage") { context.container.sync.place.value.location == VaultLocation.INTERNAL }
            compose.onNodeWithTag("dashboard").performScrollToNode(hasTestTag("syncNow"))
            compose.onNodeWithTag("syncNow").performClick()
            waitFor("internal sync") { context.container.sync.state(pak.deviceId).phase is SyncPhase.Finished }
            assertEquals(1, context.container.sync.vault(pak.deviceId).versions(pak.deviceId, game.gameId).size)
        }
    }

    @Test
    fun syncOnConnectAndAStateRestored() {
        context.container.sync.setSyncOnConnect(true)
        val state = runBlocking { client.allStates() }.states.first { it.thumbnail != null }
        // Unlocked, so that the console accepts its replacement.
        runCatching { runBlocking { client.unlockState(state.gameId, state.slot) } }
        ActivityScenario.launch(MainActivity::class.java).use {
            waitFor("consoles") { hasText("Hazel Test Console") }
            compose.onAllNodesWithText("Hazel Test Console")[0].performClick()
            waitFor("dashboard") { hasTag("dashboard") }
            // No tap: the connection syncs.
            waitFor("automatic sync") { context.container.sync.state(pak.deviceId).lastReport != null }
            val report = context.container.sync.state(pak.deviceId).lastReport!!
            assertTrue(report.states!!.stored.contains(state.id))
            compose.onNodeWithTag("dashboard").performScrollToNode(hasTestTag("openSyncedSaves"))
            compose.onNodeWithTag("openSyncedSaves").performClick()
            waitFor("synced games") { hasTag("syncedGames") }
            compose.onNodeWithTag("syncedGames").performScrollToNode(androidx.compose.ui.test.hasText(state.gameName, substring = true))
            compose.onAllNodesWithText(state.gameName, substring = true)[0].performClick()
            waitFor("state versions") { hasTag("syncedState${state.slot}-0") }
            compose.onNodeWithTag("syncedVersions").performScrollToNode(hasTestTag("syncedState${state.slot}-0"))
            compose.onNodeWithTag("syncedState${state.slot}-0").performClick()
            compose.onNodeWithTag("action0").performClick()
            compose.onNodeWithTag("confirmOk").performClick()
            waitFor("queued") { hasText(context.getString(R.string.synced_state_queued)) }
            waitFor("state sent") {
                context.container.transfers.items.value.any { it.fileName == state.path.substringAfterLast('/') && it.phase == link.reminis.core.TransferPhase.DONE }
            }
            val sent = ByteArrayOutputStream()
            runBlocking { client.download(DownloadSource.State(state.gameId, state.slot), sent) }
            assertEquals(state.sha256, sha(sent.toByteArray()))
        }
    }

    @Test
    fun theDemoSyncsToATemporaryFolderErasedOnQuit() {
        InstrumentationHelper.onMain { context.container.forget(pak.deviceId) }
        ActivityScenario.launch(MainActivity::class.java).use {
            waitFor("welcome") { hasTag("tryDemo") }
            compose.onNodeWithTag("tryDemo").performClick()
            waitFor("dashboard") { hasTag("dashboard") }
            val demo = context.container.demo.value!!
            compose.onNodeWithTag("dashboard").performScrollToNode(hasTestTag("syncNow"))
            assertTrue(hasTag("syncLocation-demo"))
            compose.onNodeWithTag("syncNow").performClick()
            waitFor("sync report") { hasTag("syncReport") }
            val folder = context.container.sync.demoVaultForTests!!
            assertTrue(folder.walkTopDown().any { it.name == ".version.json" })
            assertFalse("nothing in the app's storage", java.io.File(context.filesDir, "SyncedSaves").walkTopDown().any { it.name == ".version.json" })
            // Share in the demo: nothing leaves the app.
            compose.onNodeWithTag("dashboard").performScrollToNode(hasTestTag("openSyncedSaves"))
            compose.onNodeWithTag("openSyncedSaves").performClick()
            waitFor("synced games") { hasTag("syncedGames") }
            compose.onAllNodesWithText("Thistledown Isles", substring = true)[0].performClick()
            waitFor("versions") { hasTag("syncedVersion0") }
            compose.onNodeWithTag("syncedVersion0").performClick()
            compose.onNodeWithTag("action1").performClick()
            waitFor("demo note") { hasTag("demoNoteOk") }
            compose.onNodeWithTag("demoNoteOk").performClick()
            compose.onNodeWithTag("demoQuit").performClick()
            waitFor("demo ended") { context.container.demo.value == null }
            waitFor("demo vault erased") { !folder.exists() }
            assertNull(context.container.sync.demoVaultForTests)
            assertEquals(VaultLocation.INTERNAL, context.container.sync.place.value.location)
            assertTrue(demo.deviceId !in context.container.sync.states.value)
        }
    }
}
