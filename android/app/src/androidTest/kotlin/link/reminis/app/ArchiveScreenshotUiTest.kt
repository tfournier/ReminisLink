package link.reminis.app

import android.content.ContentValues
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.provider.MediaStore
import androidx.compose.foundation.layout.Box
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.DeviceConfigurationOverride
import androidx.compose.ui.test.ForcedSize
import androidx.compose.ui.test.hasTestTag
import androidx.activity.compose.setContent
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onFirst
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsNotDisplayed
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.longClick
import androidx.compose.ui.test.printToLog
import androidx.compose.ui.unit.DpSize
import androidx.compose.ui.unit.dp
import androidx.test.core.app.ActivityScenario
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import java.io.ByteArrayOutputStream
import kotlinx.coroutines.runBlocking
import link.reminis.core.BackupReasons
import link.reminis.core.DownloadSource
import link.reminis.core.Endpoint
import link.reminis.core.PairedConsole
import link.reminis.core.TlsPin
import link.reminis.core.TransferPhase
import link.reminis.core.demo.DemoZip
import link.reminis.core.demo.FakePak
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
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Increment 5 in the app. The game archives (M19) and the screenshots (M22)
 * against a console paired like a real one (a local TLS server with a
 * pinned certificate) whose routes are answered by the fake pak of the demo
 * mode (made-up games, real zips); the screenshots grid in the demo mode;
 * and the large-screen layout (M15 on Android) with a forced window size.
 */
@RunWith(AndroidJUnit4::class)
class ArchiveScreenshotUiTest {
    @get:Rule
    val compose = createEmptyComposeRule()

    private val context: Context get() = ApplicationProvider.getApplicationContext()
    private lateinit var pak: FakePak
    private lateinit var server: MockWebServer
    private val created = mutableListOf<Uri>()

    @Before
    fun setUp() {
        grantNotifications()
        val container = context.container
        InstrumentationHelper.onMain { container.quitDemo() }
        container.consoles.all().forEach { container.forget(it.deviceId) }
        container.transfers.clearFinished()
        container.jobs.clearFinished()
        pak = FakePak(deviceName = "Juniper Test Console", cheatName = { "Test cheat $it" })
        val certificate = HeldCertificate.Builder().ecdsa256().commonName(pak.deviceId).build()
        server = MockWebServer()
        server.useHttps(HandshakeCertificates.Builder().heldCertificate(certificate).build().sslSocketFactory(), false)
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse = proxy(request)
        }
        server.start(java.net.InetAddress.getByName("127.0.0.1"), 0)
        container.replacePairing(
            PairedConsole(
                deviceId = pak.deviceId, deviceName = "Juniper Test Console", model = "desktop",
                pin = TlsPin.pin(certificate.certificate), lastEndpoint = Endpoint("127.0.0.1", server.port),
                pairedAt = System.currentTimeMillis(),
            ),
        )
    }

    @After
    fun tearDown() {
        InstrumentationHelper.onMain { context.container.quitDemo() }
        context.container.forget(pak.deviceId)
        server.shutdown()
        pak.close()
        created.forEach { runCatching { context.contentResolver.delete(it, null, null) } }
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

    private fun waitFor(what: String, timeout: Long = 20_000, condition: () -> Boolean) {
        try {
            compose.waitUntil(timeout, condition)
        } catch (e: Throwable) {
            compose.onRoot(useUnmergedTree = true).printToLog("ArchiveScreenshotUiTest")
            throw AssertionError("timeout waiting for $what", e)
        }
    }

    private fun hasTag(tag: String) = compose.onAllNodesWithTag(tag, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()

    private fun hasText(text: String) = compose.onAllNodesWithText(text, substring = true).fetchSemanticsNodes().isNotEmpty()

    private fun count(tag: String) = compose.onAllNodesWithTag(tag, useUnmergedTree = true).fetchSemanticsNodes().size

    private val client get() = runBlocking { context.container.client(pak.deviceId) }

    private fun gameId(name: String) = runBlocking { client.allGames() }.games.first { it.name == name }.id

    private fun archiveOf(name: String, rom: Boolean): ByteArray {
        val out = ByteArrayOutputStream()
        runBlocking { client.download(DownloadSource.GameArchive(gameId(name), rom), out) }
        return out.toByteArray()
    }

    /** A document owned by the app (MediaStore Downloads), as another app would share it. */
    private fun document(name: String, bytes: ByteArray): Uri {
        val values = ContentValues().apply { put(MediaStore.Downloads.DISPLAY_NAME, name) }
        val uri = context.contentResolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values)!!
        created += uri
        context.contentResolver.openOutputStream(uri, "wt")!!.use { it.write(bytes) }
        return uri
    }

    /**
     * Add, type App (pak) (06 §4.6): a zip shared to the app and sent as an
     * app goes through the installation of the Apps screen (target `pak`,
     * a job), never offered by default; the pak is installed.
     */
    @Test
    fun sharedZipSentAsAnAppIsInstalled() {
        val zip = java.io.ByteArrayOutputStream().also { out ->
            java.util.zip.ZipOutputStream(out).use { z ->
                z.putNextEntry(java.util.zip.ZipEntry("Juniper Tool.pak/launch.sh"))
                z.write("#!/bin/sh\n".toByteArray())
                z.closeEntry()
            }
        }.toByteArray()
        val uri = document("Juniper Tool.pak.zip", zip)
        ActivityScenario.launch<MainActivity>(share(uri)).use {
            waitFor("Add") { hasTag("addFile") }
            // A zip is a game by default.
            compose.onNodeWithTag("addKindPak").performClick()
            compose.onNodeWithTag("sendFiles").performClick()
            waitFor("installed", timeout = 30_000) {
                runBlocking { runCatching { context.container.jobs.refresh(pak.deviceId) } }
                runBlocking { runCatching { context.container.client(pak.deviceId).paks() }.getOrNull() }?.any { it.name == "Juniper Tool" } == true
            }
        }
    }

    private fun share(uri: Uri) = Intent(Intent.ACTION_SEND).setClass(context, MainActivity::class.java)
        .setType("application/zip").putExtra(Intent.EXTRA_STREAM, uri).addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)

    private fun openGame(name: String) {
        waitFor("consoles") { hasText("Juniper Test Console") }
        compose.onNodeWithText("Juniper Test Console").performClick()
        waitFor("dashboard") { hasTag("dashboard") }
        compose.onNodeWithTag("dashboard").assertExists()
        compose.onNodeWithTag("tabLibrary").performClick()
        waitFor("library") { hasText(name) }
        compose.onNodeWithText(name).performClick()
        waitFor("game") { hasTag("game") }
    }

    /** Taps Save in the system's save-as picker (found through accessibility). */
    private fun tapSaveInPicker() {
        val automation = androidx.test.platform.app.InstrumentationRegistry.getInstrumentation().uiAutomation
        var tapped = false
        repeat(20) {
            if (tapped) return@repeat
            val save = automation.rootInActiveWindow
                ?.takeIf { it.packageName?.contains("documentsui") == true }
                ?.findAccessibilityNodeInfosByViewId("android:id/button1")?.firstOrNull()
            if (save != null) tapped = save.performAction(android.view.accessibility.AccessibilityNodeInfo.ACTION_CLICK)
            if (!tapped) Thread.sleep(1_000)
        }
        assertTrue("the picker's Save button was not found", tapped)
    }

    @Test
    fun exportFromTheGameScreenIntoADocument() {
        context.container.lastDownloadDocumentForTests = null
        var document: Uri? = null
        try {
            ActivityScenario.launch(MainActivity::class.java).use {
                openGame("Quillwork Tactics")
                compose.onNodeWithTag("game").performScrollToNode(hasTestTag("archiveExport"))
                compose.onNodeWithTag("archiveExport").performClick()
                compose.onNodeWithTag("archiveExportRom").assertExists()
                compose.onNodeWithTag("archiveExportData").performClick()
                tapSaveInPicker()
                waitFor("export done", 30_000) { hasTag("archiveExported") }
                document = context.container.lastDownloadDocumentForTests
                val bytes = context.contentResolver.openInputStream(document!!)!!.use { it.readBytes() }
                val files = DemoZip.read(bytes)!!
                assertTrue(files.containsKey("manifest.json"))
                assertTrue("a save is exported", files.keys.any { it.startsWith("save/") })
                assertTrue("no ROM asked", files.keys.none { it.startsWith("rom/") })
                val item = context.container.transfers.items.value.first { it.fileName == "Quillwork Tactics.reminislink.zip" }
                assertEquals(TransferPhase.DONE, item.phase)
            }
        } finally {
            document?.let { DocumentSink.deleteDocument(context, it) }
        }
    }

    @Test
    fun sharedArchiveIsImportedIntoTheSuggestedGame() {
        val isles = gameId("Thistledown Isles")
        val uri = document("Thistledown Isles ${System.nanoTime()}.reminislink.zip", archiveOf("Thistledown Isles", rom = false))
        ActivityScenario.launch<MainActivity>(share(uri)).use { scenario ->
            waitFor("plan") { hasTag("archiveTarget") }
            compose.onNodeWithTag("archiveTarget").assertExists()
            assertTrue(hasText(context.getString(R.string.archive_suggested)))
            assertTrue("the locked state is skipped", hasText(context.getString(R.string.archive_skip_locked)))
            // A configuration change keeps the preview (the run lives in the ViewModel).
            scenario.recreate()
            waitFor("plan after recreation") { hasTag("archiveImportAction") }
            assertEquals("the archive is sent once", 1, pak.stagedCount)
            compose.onNodeWithTag("archiveImportAction").performClick()
            // Replacements: the confirmation promises the save copy (the save is replaced).
            waitFor("confirmation") { hasTag("confirmOk") }
            assertTrue(hasText("Thistledown Isles"))
            compose.onNodeWithTag("confirmOk").performClick()
            waitFor("result", 30_000) { hasTag("archiveResult") }
            assertTrue(hasText(context.getString(R.string.archive_previous_save)))
            assertEquals(BackupReasons.BEFORE_IMPORT, runBlocking { client.saveBackups(isles) }.first().reason)
            assertEquals(0, pak.stagedCount)
            // The import job shows in Transfers.
            assertTrue(context.container.jobs.jobs.value.any { it.kind == "archive-import" && it.isFinished })
        }
    }

    @Test
    fun newGameConflictOffersThatGameAndClosingDropsTheArchive() {
        val uri = document("Brambleway ${System.nanoTime()}.reminislink.zip", archiveOf("Brambleway Dash", rom = true))
        ActivityScenario.launch<MainActivity>(share(uri)).use { scenario ->
            waitFor("plan") { hasTag("archiveNewGame") }
            compose.onNodeWithTag("archiveImportScreen").performScrollToNode(hasTestTag("archiveNewGame"))
            compose.onNodeWithTag("archiveNewGame").performClick()
            compose.onAllNodesWithTag("archiveNewGameSystem").onFirst().performClick()
            waitFor("409") { hasTag("archiveUseExisting") }
            assertTrue(hasText(context.getString(R.string.archive_game_exists)))
            compose.onNodeWithTag("archiveUseExisting").performClick()
            waitFor("plan of that game") { hasTag("archiveTarget") && hasText("Brambleway Dash") }
            assertEquals(1, pak.stagedCount)
            scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }
            waitFor("staging entry deleted") { pak.stagedCount == 0 }
        }
    }

    @Test
    fun olderPakSaysUpdateBeforeSending() {
        pak.archivesSupported = false
        val uri = document("Old ${System.nanoTime()}.reminislink.zip", "zip".toByteArray())
        ActivityScenario.launch<MainActivity>(share(uri)).use {
            waitFor("message") { hasText(context.getString(R.string.archive_needs_newer)) }
            assertEquals(0, pak.stagedCount)
        }
    }

    @Test
    fun screenshotsShareAndGalleryWithARealConsole() = runBlocking {
        val manager = context.container.screenshots(pak.deviceId)
        manager.load()
        waitFor("list") { manager.screenshots.isNotEmpty() }
        val shots = manager.screenshots.take(2)
        // The share sheet gets copies served by the app's FileProvider.
        val files = manager.exportForSharing(shots)
        assertEquals(2, files.size)
        // Its hour of retention starts once the export is complete (Codex review round 2).
        val folder = files[0].parentFile!!
        assertTrue(java.io.File(folder, ".ready").exists())
        assertTrue(folder !in ScreenshotManager.staleShares(context))
        assertTrue("an hour after the end it goes", folder in ScreenshotManager.staleShares(context, System.currentTimeMillis() + ScreenshotManager.SHARE_KEPT_MILLIS + 60_000))
        // An export still in progress (no marker) stays, even if its folder is old.
        val running = java.io.File(ScreenshotManager.sharedDirectory(context), "in-progress").apply { mkdirs(); setLastModified(System.currentTimeMillis() - 2 * ScreenshotManager.SHARE_KEPT_MILLIS) }
        assertTrue(running !in ScreenshotManager.staleShares(context))
        running.deleteRecursively()
        val shared = androidx.core.content.FileProvider.getUriForFile(context, "${context.packageName}.files", files[0])
        val bytes = context.contentResolver.openInputStream(shared)!!.use { it.readBytes() }
        assertTrue(bytes.size > 8 && bytes[1] == 'P'.code.toByte())
        // The gallery (MediaStore, no permission on API 29+).
        assertEquals(1, manager.saveToGallery(shots.take(1)))
        val saved = context.contentResolver.query(
            MediaStore.Images.Media.EXTERNAL_CONTENT_URI, arrayOf(MediaStore.Images.Media._ID),
            "${MediaStore.Images.Media.DISPLAY_NAME} = ?", arrayOf(shots[0].name), null,
        )!!.use { c -> List(c.count) { c.moveToNext(); android.content.ContentUris.withAppendedId(MediaStore.Images.Media.EXTERNAL_CONTENT_URI, c.getLong(0)) } }
        assertTrue("saved to the gallery", saved.isNotEmpty())
        created += saved
        // A large screenshot is refused before it is shown (64 MiB at most).
        val decoded = manager.displayImage(shots[0], 512)
        assertTrue(decoded.width in 1..640)
    }

    @Test
    fun demoScreenshotsGridSelectionTrashAndRestore() {
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            compose.onNodeWithTag("tryDemo").performClick()
            waitFor("dashboard") { hasTag("dashboard") }
            val demo = context.container.demo.value!!
            compose.onNodeWithTag("dashboard").performScrollToNode(hasTestTag("openScreenshots"))
            compose.onNodeWithTag("openScreenshots").performClick()
            waitFor("grid") { count("screenshotCell") >= 6 }
            // Open one: the image is downloaded and shown.
            compose.onAllNodesWithTag("screenshotCell").onFirst().performClick()
            waitFor("image") { hasTag("screenshotImage") }
            assertTrue(hasText("Thistledown Isles"))
            // The demo never lets a file leave the app.
            compose.onNodeWithTag("screenshotShare").performClick()
            waitFor("demo note") { hasTag("demoScreenshotsOk") }
            compose.onNodeWithTag("demoScreenshotsOk").performClick()
            scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }
            waitFor("grid again") { hasTag("screenshotsGrid") }

            // Select two (a long press starts the selection), then move them to the trash.
            compose.onAllNodesWithTag("screenshotCell")[0].performTouchInput { longClick() }
            compose.onAllNodesWithTag("screenshotCell")[1].performClick()
            waitFor("2 selected") { hasText(context.resources.getQuantityString(R.plurals.screenshots_selected, 2, 2)) }
            // Kept across a configuration change.
            scenario.recreate()
            waitFor("selection after recreation") { hasText(context.resources.getQuantityString(R.plurals.screenshots_selected, 2, 2)) }
            val before = demo.pak.screenshotNames.size
            compose.onNodeWithTag("screenshotsTrash").performClick()
            compose.onNodeWithTag("confirmOk").performClick()
            waitFor("moved") { demo.pak.screenshotNames.size == before - 2 && !hasTag("screenshotsBar") }

            // Another client moves one: the `screenshots` event reloads the grid.
            val cells = count("screenshotCell")
            demo.pak.trashScreenshotsFromAnotherClient(listOf(demo.pak.screenshotNames.first()))
            waitFor("event") { count("screenshotCell") == cells - 1 }

            // A partial move says how many went.
            compose.onNodeWithTag("screenshotsSelect").performClick()
            compose.onAllNodesWithTag("screenshotCell")[0].performClick()
            demo.pak.failNextScreenshotTrash = true
            compose.onNodeWithTag("screenshotsTrash").performClick()
            compose.onNodeWithTag("confirmOk").performClick()
            waitFor("partial") { hasText(context.resources.getQuantityString(R.plurals.screenshots_partly_moved, 1, 1)) }

            // The trash shows the two in one entry, counted in the user's language, and restores them.
            scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }
            waitFor("dashboard") { hasTag("dashboard") }
            compose.onNodeWithTag("dashboard").performScrollToNode(hasTestTag("openTrash"))
            compose.onNodeWithTag("openTrash").performClick()
            val two = context.resources.getQuantityString(R.plurals.trash_screenshots, 2, 2)
            waitFor("trash entry") { hasText(two) }
            compose.onNodeWithText(two).performClick()
            waitFor("restore preview") { hasTag("trashRestoreConfirm") }
            compose.onNodeWithTag("trashRestore").performScrollToNode(hasTestTag("trashRestoreConfirm"))
            compose.onNodeWithTag("trashRestoreConfirm").performClick()
            // The two come back (the partial move and the other client's stay in the trash).
            waitFor("restored") { demo.pak.screenshotNames.size == before - 2 }
        }
    }

    @Test
    fun wideWindowShowsTheListAndTheGameSideBySide() {
        val rule = compose
        var wide by mutableStateOf(true)
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            scenario.onActivity { activity ->
                activity.setContent {
                    // Low enough that the list scrolls, tall enough for two panes.
                    val size = if (wide) DpSize(1100.dp, 760.dp) else DpSize(400.dp, 760.dp)
                    DeviceConfigurationOverride(DeviceConfigurationOverride.ForcedSize(size)) {
                        ReminisLinkTheme { Box { AppNavigation(incomingLink = null, onLinkHandled = {}) } }
                    }
                }
            }
            rule.onNodeWithTag("tryDemo").performClick()
            waitFor("dashboard") { hasTag("dashboard") }
            rule.onNodeWithTag("dashboard").assertExists()
            rule.onNodeWithTag("tabLibrary").performClick()
            waitFor("library") { hasText("Brambleway Dash") }
            assertTrue("placeholder of the detail pane", hasTag("gamePanePlaceholder"))
            // The last game, scrolled to: the first one leaves the screen.
            rule.onNodeWithTag("games").performScrollToNode(androidx.compose.ui.test.hasText("Thistledown Isles"))
            rule.onNodeWithText("Thistledown Isles").performClick()
            waitFor("two panes") { hasTag("game") && hasTag("games") }
            rule.onNodeWithText("Brambleway Dash").assertIsNotDisplayed()
            // Narrow window: the game alone, the same one.
            wide = false
            waitFor("one pane") { hasTag("game") && !hasTag("games") }
            // Wide again: both, the game still chosen, the list where it was (Codex review round 1).
            wide = true
            waitFor("two panes again") { hasTag("game") && hasTag("games") }
            rule.onNodeWithText("Brambleway Dash").assertIsNotDisplayed()
            rule.onAllNodesWithText("Thistledown Isles").onFirst().assertIsDisplayed()
            // Narrow, then back: the list.
            wide = false
            waitFor("one pane again") { hasTag("game") && !hasTag("games") }
            scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }
            waitFor("the list") { hasTag("games") && !hasTag("game") }
        }
    }

    /**
     * A screenshot replaced on the console by another file of the same name
     * (another listed version) while its confirmation is open (Codex review
     * round 1): nothing is moved, the list is read again and the error says
     * so. This console sends no events (the confirmation stays open).
     */
    @Test
    fun screenshotReplacedDuringTheConfirmationIsNotMoved() {
        val name = pak.screenshotNames.first()
        ActivityScenario.launch(MainActivity::class.java).use {
            waitFor("consoles") { hasText("Juniper Test Console") }
            compose.onNodeWithText("Juniper Test Console").performClick()
            waitFor("dashboard") { hasTag("dashboard") }
            compose.onNodeWithTag("dashboard").performScrollToNode(hasTestTag("openScreenshots"))
            compose.onNodeWithTag("openScreenshots").performClick()
            waitFor("grid") { count("screenshotCell") >= 2 }
            compose.onAllNodesWithTag("screenshotCell")[0].performTouchInput { longClick() }
            compose.onNodeWithTag("screenshotsTrash").performClick()
            waitFor("confirmation") { hasTag("confirmOk") }
            pak.replaceScreenshot(name)
            compose.onNodeWithTag("confirmOk").performClick()
            waitFor("refused") { hasText(context.getString(R.string.screenshot_changed)) }
            assertTrue("the replacing screenshot was moved", name in pak.screenshotNames)
            // The selection dropped the version that left.
            waitFor("selection emptied") { hasText(context.getString(R.string.screenshots_select_prompt)) }
        }
    }

    /** The same in the demo, with the `screenshots` event: the open confirmation closes, the image is read again. */
    @Test
    fun demoScreenshotReplacedWhileShownClosesItsConfirmation() {
        ActivityScenario.launch(MainActivity::class.java).use {
            compose.onNodeWithTag("tryDemo").performClick()
            waitFor("dashboard") { hasTag("dashboard") }
            val demo = context.container.demo.value!!
            compose.onNodeWithTag("dashboard").performScrollToNode(hasTestTag("openScreenshots"))
            compose.onNodeWithTag("openScreenshots").performClick()
            waitFor("grid") { count("screenshotCell") >= 6 }
            val name = demo.pak.screenshotNames.first()
            compose.onAllNodesWithTag("screenshotCell").onFirst().performClick()
            waitFor("image") { hasTag("screenshotImage") }
            compose.onNodeWithTag("screenshotMore").performClick()
            compose.onNodeWithTag("screenshotTrash").performClick()
            waitFor("confirmation") { hasTag("confirmOk") }
            demo.pak.replaceScreenshot(name, notify = true)
            waitFor("confirmation closed") { !hasTag("confirmOk") && hasText(context.getString(R.string.screenshot_changed)) }
            waitFor("image of the new version") { hasTag("screenshotImage") }
            assertTrue(name in demo.pak.screenshotNames)
        }
    }
}
