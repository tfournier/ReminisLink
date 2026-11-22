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
            tap("sdMenu-Hacks")
            tap("sdTrashOne")
            waitFor("folder confirmation") { hasText(context.getString(R.string.sd_trash_folders)) }
            tap("sdTrashConfirm")
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
