package link.reminis.app

import android.content.Context
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.performTextReplacement
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.swipeLeft
import androidx.compose.ui.test.printToLog
import androidx.test.core.app.ActivityScenario
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.runBlocking
import link.reminis.core.OnConflict
import link.reminis.core.UploadTarget
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/**
 * The sixth Android increment in the demo mode (fake pak in process, no
 * network, made-up names only): the console's tabs, the Files tab and its
 * writes (M23: folder made, file added and moved to the trash, protections,
 * Keep Both only), the rename of a game and its cheat count, the resume
 * history, a pak installed from the SD card whose replacement installed
 * another pak, the error observer (`401 disconnected`, `503
 * shutting_down` from any screen), Unpair, and the version in Settings.
 */
@RunWith(AndroidJUnit4::class)
class FilesRenameUiTest {
    @get:Rule
    val compose = createEmptyComposeRule()

    private val context: Context get() = ApplicationProvider.getApplicationContext()

    @Before
    fun setUp() {
        val container = context.container
        InstrumentationHelper.onMain { container.quitDemo() }
        container.consoles.all().forEach { container.forget(it.deviceId) }
    }

    @After
    fun tearDown() {
        InstrumentationHelper.onMain { context.container.quitDemo() }
    }

    private fun waitFor(what: String, timeout: Long = 15_000, condition: () -> Boolean) {
        try {
            compose.waitUntil(timeout, condition)
        } catch (e: Throwable) {
            compose.onRoot(useUnmergedTree = true).printToLog("FilesRenameUiTest")
            throw AssertionError("timeout waiting for $what", e)
        }
    }

    /** Waits without a screen (no compose hierarchy). */
    private fun poll(what: String, condition: () -> Boolean) {
        val end = System.currentTimeMillis() + 10_000
        while (!condition()) {
            if (System.currentTimeMillis() > end) throw AssertionError("timeout waiting for $what")
            Thread.sleep(50)
        }
    }

    private fun hasTag(tag: String) = compose.onAllNodesWithTag(tag, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()

    private fun hasText(text: String) = compose.onAllNodesWithText(text, substring = true).fetchSemanticsNodes().isNotEmpty()

    private fun openDemo(): link.reminis.core.demo.DemoConsole {
        compose.onNodeWithTag("tryDemo").performClick()
        waitFor("dashboard") { hasTag("dashboard") }
        return context.container.demo.value!!
    }

    private fun tap(tag: String) {
        waitFor(tag) { hasTag(tag) }
        compose.onNodeWithTag(tag, useUnmergedTree = true).performClick()
    }

    @Test
    fun filesTabWritesAndProtections() {
        ActivityScenario.launch(MainActivity::class.java).use {
            val demo = openDemo()
            // The Console tab: Transfers and Trash rows, with the count of the trash.
            waitFor("trash count") { hasTag("openTrash-count") }
            compose.onNodeWithTag("openTransfers").assertExists()

            tap("tabFiles")
            waitFor("root of the card") { hasTag("sd-Downloads") && hasTag("sd-.userdata") }
            compose.onNodeWithText(context.getString(R.string.sd_root_footer)).assertExists()
            tap("sd-Downloads")
            waitFor("Downloads") { hasTag("sd-README.txt") }

            // A new folder, then a name already taken.
            tap("sdAdd")
            tap("sdNewFolder")
            compose.onNodeWithTag("sdNewFolderName").performTextReplacement("Hacks")
            tap("sdNewFolderCreate")
            waitFor("folder made") { hasTag("sd-Hacks") }
            tap("sdAdd")
            tap("sdNewFolder")
            compose.onNodeWithTag("sdNewFolderName").performTextReplacement("hacks")
            tap("sdNewFolderCreate")
            waitFor("name taken") { hasText(context.getString(R.string.sd_name_taken)) }

            // A file added to the folder (as Add Files queues it), then the same name: Keep Both only.
            tap("sd-Hacks")
            waitFor("empty folder") { hasTag("sdEmpty") }
            val hacks = runBlocking { demo.client.browseSd(null) }.entries.first { it.name == "Downloads" }
                .let { runBlocking { demo.client.browseSd(it.sdEntryId) } }.entries.first { it.name == "Hacks" }
            fun queue(name: String): String {
                val dir = java.io.File(context.container.transferDirectory, java.util.UUID.randomUUID().toString()).apply { mkdirs() }
                val file = java.io.File(dir, "file").apply { writeBytes(ByteArray(2_048) { it.toByte() }) }
                return context.container.transfers.enqueueUpload(
                    demo.deviceId, file, name, UploadTarget.SdFile(hacks.sdEntryId, name), "Hacks", OnConflict.FAIL, ownsFile = true,
                )
            }
            queue("notes.txt")
            waitFor("file listed by the event") { hasTag("sd-notes.txt") }
            val second = queue("Notes.txt")
            waitFor("conflict") { context.container.transfers.items.value.first { it.id == second }.isNameConflict }
            tap("tabConsole")
            tap("openTransfers")
            waitFor("Keep Both") { hasTag("keepBoth") }
            compose.onNodeWithText(context.getString(R.string.transfer_replace)).assertDoesNotExist()
            tap("keepBoth")
            waitFor("kept both") { context.container.transfers.items.value.first { it.id == second }.phase == link.reminis.core.TransferPhase.DONE }

            // Back to the Files tab, where the folder kept its place; both files are there.
            tap("tabFiles")
            waitFor("both files") { hasTag("sd-Notes (2).txt") && hasTag("sd-notes.txt") }

            // Selection: one file to the trash after a confirmation that names it.
            tap("sdSelect")
            assertTrue("the tab bar hides during the selection", !hasTag("consoleTabs"))
            tap("sd-Notes (2).txt")
            tap("sdSelectTrash")
            waitFor("confirmation") { hasTag("sdTrashMessage") }
            compose.onNodeWithTag("sdTrashMessage").assertExists()
            assertTrue(hasText("Notes (2).txt"))
            tap("sdTrashConfirm")
            waitFor("moved") { !hasTag("sd-Notes (2).txt") }
            assertTrue(hasTag("consoleTabs"))

            // The folder itself, from its parent, with what it holds.
            compose.onNodeWithTag("tabFiles").assertExists()
            it.onActivity { a -> a.onBackPressedDispatcher.onBackPressed() }
            waitFor("Downloads") { hasTag("sd-Hacks") }
            // The listing kept in the back stack may still show the folder as it was before the
            // file left it: the console then refuses (`changed`) and the folder is read again.
            repeat(3) {
                if (!hasTag("sd-Hacks")) return@repeat
                tap("sdMenu-Hacks")
                tap("sdTrashOne")
                waitFor("folder confirmation") { hasText(context.getString(R.string.sd_trash_folders)) }
                tap("sdTrashConfirm")
                waitFor("folder moved or refused") { !hasTag("sd-Hacks") || hasText(context.getString(R.string.sd_entry_changed)) }
                compose.waitForIdle()
                Thread.sleep(500)
            }
            waitFor("folder moved") { !hasTag("sd-Hacks") }
            val entry = runBlocking { demo.client.trash() }.entries.first { it.label == "Hacks" }
            assertEquals("delete-file", entry.reason)
            assertEquals(listOf("Downloads/Hacks/notes.txt"), entry.files)

            // A protected folder is read only.
            it.onActivity { a -> a.onBackPressedDispatcher.onBackPressed() }
            tap("sd-.userdata")
            waitFor("read only") { hasTag("sdReadOnly") }
            assertTrue(!hasTag("sdAdd"))
        }
    }

    @Test
    fun renameCheatCountAndResumeHistory() {
        ActivityScenario.launch(MainActivity::class.java).use {
            val demo = openDemo()
            tap("tabLibrary")
            waitFor("library") { hasText("Thistledown Isles") }
            compose.onNodeWithText("Thistledown Isles").performClick()
            waitFor("game") { hasTag("cheatCount") }
            compose.onNodeWithTag("cheatCount", useUnmergedTree = true).assertExists()
            assertTrue(hasText(context.resources.getQuantityString(R.plurals.cheat_count, 12, 12)))

            // A name the pak refuses is said before anything is sent.
            tap("gameMenu")
            tap("renameGame")
            compose.onNodeWithTag("renameName").performTextReplacement("Thistledown: Isles")
            tap("renameConfirm")
            waitFor("refused") { hasText(context.getString(R.string.name_forbidden, ":")) }

            tap("gameMenu")
            tap("renameGame")
            assertTrue(hasText(context.getString(R.string.game_rename_extension, ".hab")))
            compose.onNodeWithTag("renameName").performTextReplacement("Thistledown Isles (Rev 1)")
            tap("renameConfirm")
            waitFor("renamed") { hasText("Thistledown Isles (Rev 1)") && hasTag("cheatCount") }
            val renamed = runBlocking { demo.client.allGames() }.games.first { it.name == "Thistledown Isles (Rev 1)" }
            assertTrue(runBlocking { demo.client.game(renamed.id) }.hasSave)

            // The resume history of the States screen.
            compose.onNodeWithTag("game").performScrollToNode(hasTestTag("openStates"))
            tap("openStates")
            waitFor("history") { hasTag("resumeEntry") }
            compose.onNodeWithText(context.getString(R.string.resume_footer_demo)).assertExists()
            val before = runBlocking { demo.client.resumeHistory(renamed.id) }.entries.size
            compose.onAllNodesWithTag("resumeEntry")[before - 1].performClick()
            tap("confirmOk")
            waitFor("restored") { hasText(context.getString(R.string.resume_restored)) }
            assertEquals(before + 1, runBlocking { demo.client.resumeHistory(renamed.id) }.entries.size)
        }
    }

    @Test
    fun pakFromTheSdCardAndAnotherPakInstalled() {
        ActivityScenario.launch(MainActivity::class.java).use {
            val demo = openDemo()
            tap("tabApps")
            waitFor("apps") { hasTag("pak-Clock") }
            tap("paksInstall")
            tap("paksFromSd")
            waitFor("picker") { hasTag("sdPicker") && hasTag("sd-Downloads") }
            tap("sd-Downloads")
            waitFor("zips") { hasTag("sd-Clock.pak.zip") }
            tap("sd-Clock.pak.zip")
            tap("pakInstallConfirm")
            waitFor("replace?") { hasTag("pakReplaceConfirm") }
            // The SD file changes between the conflict and the replacement.
            demo.pak.nextPakSourceName = "Weather.pak.zip"
            tap("pakReplaceConfirm")
            waitFor("mismatch said", timeout = 20_000) { hasTag("pakInstallMismatch") }
            assertTrue(hasText(context.getString(R.string.pak_mismatch_installed, "Weather", "Clock")))
            waitFor("Weather listed") { hasTag("pak-Weather") }
        }
    }

    /**
     * Codex review 1: Weather already installed, the source of a confirmed
     * replacement of Clock becomes Weather, and the trash cannot be read:
     * the row says "Installed, to check", never plain "Installed"; the
     * conflict is answered from Transfers.
     */
    @Test
    fun replacementWhoseCheckFailsSaysToCheck() {
        ActivityScenario.launch(MainActivity::class.java).use {
            val demo = openDemo()
            runBlocking {
                val downloads = demo.client.browseSd(demo.client.browseSd(null).entries.first { e -> e.name == "Downloads" }.sdEntryId)
                val job = demo.client.installPakFromSd(link.reminis.core.PakInstallRequest(downloads.entries.first { e -> e.name == "Weather.pak.zip" }.sdEntryId, link.reminis.core.PakInstallKind.AUTO, false))
                kotlinx.coroutines.withTimeout(10_000) { while (!demo.client.job(job.jobId).isFinished) kotlinx.coroutines.delay(100) }
            }
            tap("tabApps")
            waitFor("Weather installed") { hasTag("pak-Weather") }
            tap("paksInstall")
            tap("paksFromSd")
            tap("sd-Downloads")
            tap("sd-Clock.pak.zip")
            tap("pakInstallConfirm")
            waitFor("replace?") { hasTag("pakReplaceConfirm") }
            // Answered from Transfers instead (as iOS offers it there too).
            tap("pakKeepInstalled")
            tap("paksInstall")
            tap("paksFromSd")
            tap("sd-Downloads")
            tap("sd-Clock.pak.zip")
            tap("pakInstallConfirm")
            waitFor("conflict again") { hasTag("pakReplaceConfirm") }
            it.onActivity { a -> a.onBackPressedDispatcher.onBackPressed() }
            tap("tabConsole")
            tap("openTransfers")
            waitFor("the conflict in Transfers") { hasTag("pakInstallReplace") }
            demo.pak.nextPakSourceName = "Weather.pak.zip"
            demo.pak.failTrashList = true
            tap("pakInstallReplace")
            tap("pakReplaceConfirm")
            waitFor("to check", timeout = 20_000) { hasTag("pakInstallUnverified") }
            assertTrue(hasText(context.getString(R.string.pak_installed_unverified)))
        }
    }

    @Test
    fun errorAnswersCloseTheConnection() {
        ActivityScenario.launch(MainActivity::class.java).use {
            val demo = openDemo()
            tap("tabFiles")
            waitFor("root") { hasTag("sd-Downloads") }
            // Any request of a screen: `401 disconnected` → the dashboard says it, with Reconnect.
            demo.pak.disconnectClient()
            tap("sd-Downloads")
            waitFor("disconnected on the dashboard") { hasTag("dashboard") || hasText(context.getString(R.string.dashboard_disconnected)) }
            waitFor("disconnected shown") { hasText(context.getString(R.string.dashboard_disconnected)) }
            assertEquals(0, demo.pak.connectRequests.get())
            compose.onNodeWithText(context.getString(R.string.reconnect)).performClick()
            waitFor("reconnected") { hasTag("dashboard") && !demo.pak.isClientDisconnected }

            // `503 shutting_down` from a screen: back to the list with the notice.
            tap("tabLibrary")
            waitFor("library") { hasText("Thistledown Isles") }
            demo.pak.answerShuttingDown = true
            compose.onNodeWithText("Thistledown Isles").performClick()
            waitFor("back on the list") { hasTag("demoConsole") && !hasTag("dashboard") }
        }
    }

    @Test
    fun libraryFiltersBrowseSwipeAndEmptyTrash() {
        ActivityScenario.launch(MainActivity::class.java).use {
            val demo = openDemo()
            tap("tabLibrary")
            waitFor("library") { hasTag("game-Brambleway Dash") }
            // Filter and Sort: with a save only.
            tap("libraryFilterButton")
            tap("filterSave")
            tap("filterDone")
            waitFor("filtered") { hasTag("filterSummary") && !hasTag("game-Brambleway Dash") }
            assertTrue(hasTag("game-Thistledown Isles"))
            tap("libraryClearFilters")
            waitFor("all again") { hasTag("game-Brambleway Dash") }

            // Browse by Console: a system, a folder, its games.
            tap("libraryFilterButton")
            tap("libraryBySystem")
            waitFor("systems") { hasTag("system-HA") }
            tap("system-HA")
            waitFor("folder") { hasTag("folder-Homebrew") }
            tap("folder-Homebrew")
            waitFor("games of the folder") { hasText("Quillwork Tactics") }
            repeat(3) { _ -> it.onActivity { a -> a.onBackPressedDispatcher.onBackPressed() }; compose.waitForIdle() }
            waitFor("library again") { hasTag("game-Brambleway Dash") }

            // Swipe to delete, then Empty Trash Now.
            compose.onNodeWithTag("game-Brambleway Dash").performTouchInput { swipeLeft() }
            waitFor("confirmation") { hasTag("deleteGameConfirm") && hasTag("deleteMoved") }
            tap("deleteGameConfirm")
            waitFor("deleted") { !hasTag("game-Brambleway Dash") && hasTag("emptyTrashNow") }
            tap("emptyTrashNow")
            tap("confirmOk")
            waitFor("trash emptied") { runBlocking { demo.client.trash() }.entries.isEmpty() }
        }
    }

    /**
     * Codex review 1: a game opened from Browse by Console and renamed is
     * replaced in place; back leads to its folder, never to the old id.
     */
    @Test
    fun renameReplacesTheGameInPlace() {
        ActivityScenario.launch(MainActivity::class.java).use {
            val demo = openDemo()
            tap("tabLibrary")
            waitFor("library") { hasTag("libraryFilterButton") }
            tap("libraryMenu")
            tap("libraryMenuBySystem")
            tap("system-HA")
            waitFor("folder") { hasText("Brambleway Dash") }
            compose.onNodeWithText("Brambleway Dash").performClick()
            waitFor("game") { hasTag("gameMenu") }
            tap("gameMenu")
            tap("renameGame")
            compose.onNodeWithTag("renameName").performTextReplacement("Brambleway Dash II")
            tap("renameConfirm")
            waitFor("renamed in place") { hasText("Brambleway Dash II.hab") }
            assertTrue(runBlocking { demo.client.allGames() }.games.any { g -> g.name == "Brambleway Dash II" })
            it.onActivity { a -> a.onBackPressedDispatcher.onBackPressed() }
            waitFor("the folder") { hasTag("folder") && !hasTag("gameMenu") }
            assertTrue(hasText("Brambleway Dash II"))
        }
    }

    @Test
    fun largerLibraryIsBrowsedByConsole() {
        ActivityScenario.launch(MainActivity::class.java).use {
            val demo = openDemo()
            demo.pak.libraryTruncated = true
            tap("tabLibrary")
            waitFor("systems instead of the list") { hasTag("libraryTruncated") && hasTag("system-HA") }
            assertTrue(!hasTag("libraryFilterButton"))
        }
    }

    @Test
    fun aConsoleRenamedOnThisPhone() {
        val id = "5d2c1b0a-3e4f-4a5b-8c7d-6e5f4a3b2c1d"
        context.container.replacePairing(
            link.reminis.core.PairedConsole(
                deviceId = id, deviceName = "Fern Handheld", model = "brick",
                pin = link.reminis.core.Base64Url.encode(ByteArray(32) { 3 }), pairedAt = 2L, consoleName = "Fern Handheld",
            ),
        )
        try {
            ActivityScenario.launch(MainActivity::class.java).use {
                waitFor("list") { hasText("Fern Handheld") }
                tap("renameConsole")
                compose.onNodeWithTag("consoleNameField").performTextReplacement("Living Room")
                tap("consoleNameSave")
                waitFor("renamed") { hasText("Living Room") }
                assertEquals("Living Room", context.container.consoles.get(id)!!.deviceName)
            }
        } finally {
            context.container.forget(id)
        }
    }

    /**
     * Codex review 1: an error answer read by a client of a previous pairing
     * of the console (paired again since) closes nothing; the same answer
     * for the current pairing closes the connection.
     */
    @Test
    fun anAnswerOfAPreviousPairingClosesNothing() {
        val id = "6e3d2c1b-4f5a-4b6c-9d8e-7f6a5b4c3d2e"
        val pin = link.reminis.core.Base64Url.encode(ByteArray(32) { 4 })
        val container = context.container
        container.replacePairing(link.reminis.core.PairedConsole(id, "Fern Handheld", "brick", pin, pairedAt = 1L, consoleName = "Fern Handheld"))
        val previous = pin to 1L
        container.replacePairing(link.reminis.core.PairedConsole(id, "Fern Handheld", "brick", pin, pairedAt = 2L, consoleName = "Fern Handheld"))
        val current = pin to 2L
        fun error(status: Int, code: String) = link.reminis.core.ApiException.Server(status, code, "late")
        try {
            InstrumentationHelper.onMain {
                container.events.dismissClosed()
                container.serverError(id, previous, error(503, link.reminis.core.ErrorCodes.SHUTTING_DOWN))
                container.serverError(id, previous, error(401, link.reminis.core.ErrorCodes.DISCONNECTED))
            }
            Thread.sleep(500)
            InstrumentationHelper.onMain {
                assertNull("closed by an old pairing", container.events.closed.value)
                assertTrue("disconnected by an old pairing", id !in container.events.disconnected.value)
                container.serverError(id, current, error(401, link.reminis.core.ErrorCodes.DISCONNECTED))
            }
            poll("disconnected by the current pairing") { id in container.events.disconnected.value }
            InstrumentationHelper.onMain { container.serverError(id, current, error(503, link.reminis.core.ErrorCodes.SHUTTING_DOWN)) }
            poll("closed by the current pairing") { container.events.closed.value?.deviceId == id }
        } finally {
            InstrumentationHelper.onMain { container.events.dismissClosed() }
            container.forget(id)
        }
    }

    /** A picture of the QR code (iOS M15): the link it holds, read by ZXing; a picture without one gives none. */
    @Test
    fun pairingLinkReadFromAPicture() {
        val link = "reminislink://pair?v=2&id=5d2c1b0a-3e4f-4a5b-8c7d-6e5f4a3b2c1d&n=Fern&h=192.168.1.2&p=48100&k=abc&s=def"
        val matrix = com.google.zxing.qrcode.QRCodeWriter().encode(link, com.google.zxing.BarcodeFormat.QR_CODE, 600, 600)
        val bitmap = android.graphics.Bitmap.createBitmap(800, 800, android.graphics.Bitmap.Config.ARGB_8888)
        bitmap.eraseColor(android.graphics.Color.WHITE)
        for (x in 0 until 600) for (y in 0 until 600) if (matrix[x, y]) bitmap.setPixel(x + 100, y + 100, android.graphics.Color.BLACK)
        val file = java.io.File(context.cacheDir, "qr-test.png")
        file.outputStream().use { bitmap.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it) }
        val blank = java.io.File(context.cacheDir, "blank-test.png")
        blank.outputStream().use { android.graphics.Bitmap.createBitmap(100, 100, android.graphics.Bitmap.Config.ARGB_8888).compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it) }
        try {
            assertEquals(link, QrImage.link(context.contentResolver, android.net.Uri.fromFile(file), java.io.File(context.cacheDir, "qr")))
            assertNull(QrImage.link(context.contentResolver, android.net.Uri.fromFile(blank), java.io.File(context.cacheDir, "qr")))
        } finally {
            file.delete()
            blank.delete()
        }
        assertTrue("the copies are deleted", java.io.File(context.cacheDir, "qr").listFiles().orEmpty().isEmpty())
    }

    @Test
    fun unpairAndTheVersionInSettings() {
        ActivityScenario.launch(MainActivity::class.java).use {
            openDemo()
            compose.onNodeWithTag("dashboard").performScrollToNode(hasTestTag("unpair"))
            tap("unpair")
            waitFor("confirmation") { hasText(context.getString(R.string.unpair_message)) }
            tap("confirmOk")
            // The demo console forgotten: the demo ends.
            waitFor("welcome") { hasTag("addConsole") && !hasTag("demoBanner") }
            assertNull(context.container.demo.value)
            tap("openSettings")
            waitFor("settings") { hasTag("appVersion") }
            val version = context.packageManager.getPackageInfo(context.packageName, 0).versionName!!
            assertTrue(version.isNotEmpty())
            compose.onNodeWithText(version).assertExists()
        }
    }
}
