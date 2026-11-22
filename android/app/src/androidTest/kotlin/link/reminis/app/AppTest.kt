package link.reminis.app

import android.content.Context
import android.util.Log
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onFirst
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.onChildren
import androidx.test.core.app.ActivityScenario
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.printToLog
import kotlinx.coroutines.flow.first
import link.reminis.core.Ed25519
import link.reminis.core.PairingLink
import link.reminis.core.TransferPhase
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

private val context: Context get() = ApplicationProvider.getApplicationContext()

private fun forgetAll() {
    val container = context.container
    container.consoles.all().forEach { container.forget(it.deviceId) }
}

/** The client identity on a real device or emulator (Android Keystore). */
@RunWith(AndroidJUnit4::class)
class IdentityTest {
    @Test
    fun identitySignsAndPersists() {
        val first = IdentityStore(context).identity()
        Log.i("ReminisLinkTest", "identity storage: ${first.storage}")
        // The emulator's Keystore supports Ed25519 (API 33+): the key must live there.
        val emulator = android.os.Build.PRODUCT.startsWith("sdk_") || android.os.Build.FINGERPRINT.contains("generic")
        if (emulator && android.os.Build.VERSION.SDK_INT >= 33) assertEquals(KeyStorage.KEYSTORE, first.storage)
        assertEquals(36, first.clientId.length)
        assertEquals(first.clientId, first.clientId.lowercase())
        val message = "RL2-ED25519\nGET\n/api/v1/device".toByteArray()
        assertTrue(Ed25519.verify(first.key.publicKey, first.key.sign(message), message))
        // A new store (a new process) reads the same identity back.
        val again = IdentityStore(context).identity()
        assertEquals(first.clientId, again.clientId)
        assertTrue(first.key.publicKey.contentEquals(again.key.publicKey))
        assertTrue(Ed25519.verify(again.key.publicKey, again.key.sign(message), message))
    }
}

/**
 * A pairing link opened from another app: nothing goes on the network before
 * the user confirms, and leaving before `POST /pair` cancels everything. A
 * local TCP socket stands for the console and counts connections.
 */
@RunWith(AndroidJUnit4::class)
class ExternalLinkTest {
    @get:Rule
    val compose = createEmptyComposeRule()

    private lateinit var server: java.net.ServerSocket
    private val accepted = java.util.concurrent.LinkedBlockingQueue<java.net.Socket>()

    @Before
    fun setUp() {
        forgetAll()
        server = java.net.ServerSocket(0, 50, java.net.InetAddress.getByName("127.0.0.1"))
        Thread {
            while (!server.isClosed) {
                runCatching { accepted.put(server.accept()) }
            }
        }.apply { isDaemon = true }.start()
    }

    @org.junit.After
    fun tearDown() {
        server.close()
        accepted.forEach { runCatching { it.close() } }
    }

    private fun intent(): android.content.Intent {
        val link = PairingLink(
            deviceId = "7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10",
            deviceName = "Brick de test",
            secret = ByteArray(32) { 1 },
            endpoint = link.reminis.core.Endpoint("127.0.0.1", server.localPort),
            pin = link.reminis.core.Base64Url.encode(ByteArray(32) { 2 }),
        ).toUri()
        return android.content.Intent(android.content.Intent.ACTION_VIEW, android.net.Uri.parse(link))
            .setClass(context, MainActivity::class.java)
    }

    @Test
    fun nothingIsSentBeforeConfirmation() {
        ActivityScenario.launch<MainActivity>(intent()).use {
            compose.waitUntil(10_000) { compose.onAllNodesWithTag("externalConfirm").fetchSemanticsNodes().isNotEmpty() }
            compose.onNodeWithText(context.getString(R.string.external_link_console, "Brick de test")).assertExists()
            compose.onNodeWithText(context.getString(R.string.external_link_address, "127.0.0.1:${server.localPort}")).assertExists()
            // Longer than the 3 s mDNS wait: still no connection.
            Thread.sleep(4_500)
            assertEquals(0, accepted.size)
            compose.onNodeWithTag("cancelPair").performClick()
            compose.waitForIdle()
            Thread.sleep(4_000)
            assertEquals(0, accepted.size)
            assertTrue(context.container.consoles.all().isEmpty())
        }
    }

    @Test
    fun leavingBeforePairCancelsTheExchange() {
        ActivityScenario.launch<MainActivity>(intent()).use { scenario ->
            compose.waitUntil(10_000) { compose.onAllNodesWithTag("externalConfirm").fetchSemanticsNodes().isNotEmpty() }
            compose.onNodeWithTag("confirmPair").performClick()
            // mDNS finds nothing in 3 s, then the link's address is tried.
            val socket = accepted.poll(15, java.util.concurrent.TimeUnit.SECONDS)
            assertTrue("no connection after confirming", socket != null)
            // The TLS handshake waits for an answer that never comes: leave the screen.
            scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }
            compose.waitUntil(5_000) {
                compose.onAllNodesWithText(context.getString(R.string.welcome_step_scan)).fetchSemanticsNodes().isNotEmpty()
            }
            socket!!.soTimeout = 10_000
            val input = socket.getInputStream()
            val buffer = ByteArray(4096)
            // The client closes the connection well before OkHttp's 15 s timeout.
            try {
                while (input.read(buffer) >= 0) Unit
            } catch (e: java.net.SocketTimeoutException) {
                throw AssertionError("the exchange was not cancelled when leaving the screen")
            } catch (_: java.net.SocketException) {
            }
            assertTrue(context.container.consoles.all().isEmpty())
        }
    }
}

/** Compose UI smoke test: the empty state and the pasted-link fallback. */
@RunWith(AndroidJUnit4::class)
class SmokeTest {
    @get:Rule
    val compose = createEmptyComposeRule()

    @Before
    fun reset() = forgetAll()

    @Test
    fun emptyStateThenOutdatedLink() {
        ActivityScenario.launch(MainActivity::class.java).use {
            compose.onNodeWithText(context.getString(R.string.welcome_step_scan)).assertExists()
            compose.onNodeWithTag("addConsole").performClick()
            compose.onNodeWithTag("pairingLink").performTextInput("reminislink://pair?v=1&id=x")
            compose.onNodeWithTag("pairWithLink").performClick()
            compose.onNodeWithText(context.getString(R.string.error_outdated_pak)).assertExists()
        }
    }
}

/**
 * End to end against a real headless pak (`make headless` on the host):
 * pairs with the pasted link, opens the dashboard, the library and a game.
 * Skipped without the link file: pak-e2e.sh writes the link into the debug
 * app's private files (`run-as`, through stdin, never in a process
 * argument), and the test deletes it once read. The emulator
 * reaches the host at 10.0.2.2 (mDNS does not cross, so the locator falls
 * back to the link's address after 3 s). See android/scripts/pak-e2e.sh.
 */
@RunWith(AndroidJUnit4::class)
class PakIntegrationTest {
    @get:Rule
    val compose = createEmptyComposeRule()

    @Before
    fun reset() = forgetAll()

    @Test
    fun pairDashboardLibraryGame() {
        val file = java.io.File(InstrumentationRegistry.getInstrumentation().targetContext.filesDir, "e2e-pairing-link")
        val link = if (file.exists()) file.readText().trim().also { file.delete() } else null
        assumeTrue("no pairing link file", !link.isNullOrBlank())
        val parsed = PairingLink.parse(link!!)
        ActivityScenario.launch(MainActivity::class.java).use {
            compose.onNodeWithTag("addConsole").performClick()
            compose.onNodeWithTag("pairingLink").performTextInput(link)
            compose.onNodeWithTag("pairWithLink").performClick()
            compose.waitUntil(30_000) {
                compose.onAllNodesWithTag("paired").fetchSemanticsNodes().isNotEmpty() ||
                    compose.onAllNodesWithTag("error").fetchSemanticsNodes().isNotEmpty()
            }
            compose.onNodeWithTag("paired").assertExists()
            val stored = context.container.consoles.get(parsed.deviceId)
            assertEquals(parsed.pin, stored?.pin)

            compose.onNodeWithTag("openConsole").performClick()
            compose.waitUntil(30_000) { compose.onAllNodesWithTag("dashboard").fetchSemanticsNodes().isNotEmpty() }
            compose.onNodeWithTag("dashboard").performScrollToNode(hasTestTag("openLibrary"))
            compose.onNodeWithTag("openLibrary").performClick()
            compose.waitUntil(30_000) { compose.onAllNodesWithTag("games").fetchSemanticsNodes().isNotEmpty() }
            val games = compose.onNodeWithTag("games").onChildren()
            assertTrue("the library is empty", games.fetchSemanticsNodes().isNotEmpty())
            games.onFirst().performClick()
            compose.waitUntil(30_000) { compose.onAllNodesWithTag("game").fetchSemanticsNodes().isNotEmpty() }
            compose.onNodeWithText(context.getString(R.string.role_rom), useUnmergedTree = true).assertExists()

            transfersSavesAndStates(parsed.deviceId, it)
            cheatsTrashAndApps(parsed.deviceId, it)
        }
        sharedPatch(parsed.deviceId)
    }

    /** Waits for a condition; on a timeout, the screen goes to the log (no secret is on it). */
    private fun waitFor(what: String, condition: () -> Boolean) {
        try {
            compose.waitUntil(30_000, condition)
        } catch (e: Throwable) {
            compose.onRoot(useUnmergedTree = true).printToLog("PakIntegrationTest")
            throw AssertionError("timeout waiting for $what", e)
        }
    }

    private fun waitForTag(tag: String) =
        waitFor(tag) { compose.onAllNodesWithTag(tag, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty() }

    private fun waitForText(text: String) =
        waitFor(text) { compose.onAllNodesWithText(text, substring = true).fetchSemanticsNodes().isNotEmpty() }

    /** Sends a file through the app's queue and waits for its end. */
    private fun send(deviceId: String, name: String, bytes: ByteArray, target: link.reminis.core.UploadTarget): link.reminis.core.UploadResult =
        kotlinx.coroutines.runBlocking {
            val file = java.io.File(context.cacheDir, "e2e-${System.nanoTime()}").apply { writeBytes(bytes) }
            try {
                val queue = context.container.transfers
                val id = queue.enqueueUpload(deviceId, file, name, target, "e2e", ownsFile = false)
                val item = kotlinx.coroutines.withTimeout(60_000) {
                    queue.items.first { list -> list.any { it.id == id && (it.phase == TransferPhase.DONE || it.phase == TransferPhase.FAILED) } }
                }.first { it.id == id }
                assertEquals("upload of $name: ${item.failure}", TransferPhase.DONE, item.phase)
                item.result!!
            } finally {
                file.delete()
            }
        }

    /**
     * Increment 2 against the real pak, with files created by the test only
     * (invented names and bytes): a game file sent with a resumable upload,
     * a save sent, a backup copy made and restored from the Save screen, a
     * state sent and locked from the States screen.
     */
    private fun transfersSavesAndStates(deviceId: String, scenario: ActivityScenario<MainActivity>) {
        val client = kotlinx.coroutines.runBlocking { context.container.client(deviceId) }
        // The system whose emulator pak is on the test card (its state folder is known).
        val system = kotlinx.coroutines.runBlocking { client.systems() }.first { it.tag == "MGBA" }
        val rom = ByteArray(10_000) { (it * 31 % 251).toByte() }
        val gameId = send(deviceId, "$gameName.gba", rom, link.reminis.core.UploadTarget.Rom(system.id, "$gameName.gba")).gameId!!
        val detail = kotlinx.coroutines.runBlocking { client.game(gameId) }
        assertEquals(rom.size.toLong(), detail.files.first { it.role == link.reminis.core.GameFileRoles.ROM }.sizeBytes)

        val firstSave = "FERN-SAVE-ONE".toByteArray()
        send(deviceId, "$gameName.sav", firstSave, link.reminis.core.UploadTarget.Save(gameId))

        // Back to the library, then the new game.
        scenario.onActivity { a -> a.onBackPressedDispatcher.onBackPressed() }
        waitForTag("search")
        compose.onNodeWithTag("search").performTextInput("Fern Courier")
        waitForText(gameName)
        compose.onNodeWithText(gameName).performClick()
        waitForTag("openSave")

        // A backup copy from the Save screen, then the save changes, then the copy is restored.
        compose.onNodeWithTag("openSave").performClick()
        waitForTag("createBackup")
        compose.onNodeWithTag("createBackup").performClick()
        compose.onNodeWithTag("backupLabel").performTextInput("Before the e2e")
        compose.onNodeWithTag("backupCreate").performClick()
        waitForText(context.getString(R.string.backup_created))
        send(deviceId, "$gameName.sav", "FERN-SAVE-TWO!".toByteArray(), link.reminis.core.UploadTarget.Save(gameId))
        compose.onNodeWithTag("saveScreen").performScrollToNode(androidx.compose.ui.test.hasText("Before the e2e"))
        compose.onNodeWithText("Before the e2e").performClick()
        compose.onNodeWithTag("action0").performClick()
        compose.onNodeWithTag("confirmOk").performClick()
        waitForText(context.getString(R.string.backup_restored_kept))
        val restored = java.io.ByteArrayOutputStream()
        kotlinx.coroutines.runBlocking { client.download(link.reminis.core.DownloadSource.Save(gameId), restored) }
        assertTrue("the backup copy was not restored", restored.toByteArray().contentEquals(firstSave))

        // A state sent, then locked from the States screen.
        send(deviceId, "$gameName.st0", "FERN-STATE".toByteArray(), link.reminis.core.UploadTarget.State(gameId, 0))
        scenario.onActivity { a -> a.onBackPressedDispatcher.onBackPressed() }
        waitForTag("openStates")
        compose.onNodeWithTag("openStates").performClick()
        waitForTag("slot0")
        compose.onNodeWithTag("slot0").performClick()
        compose.onNodeWithText(context.getString(R.string.state_lock_action)).performClick()
        waitForTag("locked0")
        val slot = kotlinx.coroutines.runBlocking { client.states(gameId) }.slots.first { it.slot == 0 }
        assertTrue("the state is not locked on the console", slot.locked)
    }

    private val gameName = "Fern Courier Test"

    private fun back(scenario: ActivityScenario<MainActivity>) = scenario.onActivity { a -> a.onBackPressedDispatcher.onBackPressed() }

    private fun fernGame(client: link.reminis.core.ApiClient) =
        kotlinx.coroutines.runBlocking { client.allGames() }.games.firstOrNull { it.name == gameName }

    /**
     * Increment 3 against the real pak, with files the test creates (names
     * and bytes invented): a cheat turned on from the Cheats screen, the
     * game deleted from its screen then restored from the Trash screen, a
     * small pak zip built here installed (followed on the Apps screen),
     * then deleted from the Apps screen.
     */
    private fun cheatsTrashAndApps(deviceId: String, scenario: ActivityScenario<MainActivity>) {
        val client = kotlinx.coroutines.runBlocking { context.container.client(deviceId) }
        val gameId = fernGame(client)!!.id
        // A cheat written with the API, then turned on from the Cheats screen.
        kotlinx.coroutines.runBlocking {
            val list = client.cheats(gameId)
            client.replaceCheats(
                gameId, link.reminis.core.CheatsUpdate(listOf(link.reminis.core.Cheat(description = "Fern lantern", code = "01234567", enabled = false))),
                list.etag,
            )
        }
        back(scenario)
        waitForTag("game")
        compose.onNodeWithTag("game").performScrollToNode(hasTestTag("openCheats"))
        compose.onNodeWithTag("openCheats").performClick()
        waitForText("Fern lantern")
        compose.onNodeWithTag("cheatToggle0").performClick()
        compose.onNodeWithTag("cheatsSave").performClick()
        waitForText(context.getString(R.string.cheats_saved))
        val cheats = kotlinx.coroutines.runBlocking { client.cheats(gameId) }
        assertTrue("the cheat is not on on the console", cheats.cheats.single().enabled)

        // The game deleted from its screen, then restored from the trash.
        back(scenario)
        waitForTag("gameMenu")
        compose.onNodeWithTag("gameMenu").performClick()
        compose.onNodeWithTag("deleteGame").performClick()
        waitFor("deletion preview") {
            compose.onAllNodesWithTag("deleteGameConfirm").fetchSemanticsNodes().any {
                !it.config.contains(androidx.compose.ui.semantics.SemanticsProperties.Disabled)
            }
        }
        compose.onNodeWithTag("deleteGameConfirm").performClick()
        waitForTag("search")
        waitFor("the game gone from the console") { fernGame(client) == null }
        back(scenario)
        waitForTag("dashboard")
        compose.onNodeWithTag("dashboard").performScrollToNode(hasTestTag("openTrash"))
        compose.onNodeWithTag("openTrash").performClick()
        val entry = kotlinx.coroutines.runBlocking { client.trash() }.entries.first { it.reason == "delete-game" && it.label == gameName }
        waitForTag("trashLabel-${entry.id}")
        compose.onNodeWithTag("trashLabel-${entry.id}", useUnmergedTree = true).performClick()
        waitForTag("trashRestoreConfirm")
        compose.onNodeWithTag("trashRestoreConfirm").performClick()
        waitForTag("trashRestoreDone")
        compose.onNodeWithText(context.getString(R.string.trash_done_original)).assertExists()
        assertEquals("the game is not back with its id", gameId, fernGame(client)?.id)
        val restoredCheats = kotlinx.coroutines.runBlocking { client.cheats(gameId) }
        assertTrue("the cheats did not come back with the game", restoredCheats.cheats.single().enabled)
        compose.onNodeWithTag("trashRestoreFinished").performClick()
        waitForTag("trash")
        back(scenario)
        waitForTag("dashboard")

        // A pak zip built here, installed through the queue and followed on the Apps screen.
        val dir = java.io.File(context.container.transferDirectory, java.util.UUID.randomUUID().toString()).apply { mkdirs() }
        val zip = java.io.File(dir, "file")
        java.util.zip.ZipOutputStream(zip.outputStream()).use { out ->
            out.putNextEntry(java.util.zip.ZipEntry("Fern Tool.pak/launch.sh"))
            out.write("#!/bin/sh\necho fern\n".toByteArray())
            out.closeEntry()
            out.putNextEntry(java.util.zip.ZipEntry("Fern Tool.pak/pak.json"))
            out.write("{\"name\":\"Fern Tool\",\"version\":\"0.0.1\",\"type\":\"TOOL\"}".toByteArray())
            out.closeEntry()
        }
        val installer = context.container.pakInstaller
        val installId = installer.install(deviceId, zip, "Fern Tool.pak.zip", link.reminis.core.PakInstallKind.AUTO)
        compose.onNodeWithTag("dashboard").performScrollToNode(hasTestTag("openApps"))
        compose.onNodeWithTag("openApps").performClick()
        waitFor("the pak installed") {
            installer.installs.value.first { it.id == installId }.phase.let {
                it == link.reminis.core.PakInstall.Phase.Installed || it is link.reminis.core.PakInstall.Phase.Failed
            }
        }
        assertEquals(link.reminis.core.PakInstall.Phase.Installed, installer.installs.value.first { it.id == installId }.phase)
        assertTrue("the local copy of the zip was not deleted", !dir.exists())
        waitForTag("pakDelete-Fern Tool")
        compose.onNodeWithTag("paks").performScrollToNode(hasTestTag("pakDelete-Fern Tool"))
        compose.onNodeWithTag("pakDelete-Fern Tool").performClick()
        compose.onNodeWithTag("confirmOk").performClick()
        waitForText(context.getString(R.string.pak_deleted, "Fern Tool"))
        val paks = kotlinx.coroutines.runBlocking { client.paks() }
        assertTrue("the pak is still installed", paks.none { it.name == "Fern Tool" })
        val trash = kotlinx.coroutines.runBlocking { client.trash() }
        assertTrue("the pak is not in the trash", trash.entries.any { it.reason == "delete-pak" && it.label == "Fern Tool" })
    }

    /**
     * A small invented IPS patch shared to the app (as another app would):
     * the Patch screen opens, the game is chosen there, the patch goes to
     * the staging area and its job runs on the console; the screen closes
     * by itself on the new game, whose ROM starts with the patched bytes.
     */
    private fun sharedPatch(deviceId: String) {
        val client = kotlinx.coroutines.runBlocking { context.container.client(deviceId) }
        val ips = "PATCH".toByteArray() + byteArrayOf(0, 0, 0, 0, 4) + "FERN".toByteArray() + "EOF".toByteArray()
        val values = android.content.ContentValues().apply {
            put(android.provider.MediaStore.Downloads.DISPLAY_NAME, "Fern Fix ${System.currentTimeMillis()}.ips")
        }
        val resolver = context.contentResolver
        val uri = resolver.insert(android.provider.MediaStore.Downloads.EXTERNAL_CONTENT_URI, values)!!
        try {
            resolver.openOutputStream(uri, "wt")!!.use { it.write(ips) }
            val intent = android.content.Intent(android.content.Intent.ACTION_SEND).setClass(context, MainActivity::class.java)
                .setType("application/octet-stream").putExtra(android.content.Intent.EXTRA_STREAM, uri)
                .addFlags(android.content.Intent.FLAG_GRANT_READ_URI_PERMISSION)
            ActivityScenario.launch<MainActivity>(intent).use {
                waitForTag("patchGameSearch")
                compose.onNodeWithTag("patchGameSearch").performTextInput("Fern Courier")
                waitForText(gameName)
                compose.onNodeWithText(gameName).performClick()
                waitForTag("patchFile")
                compose.onNodeWithTag("patchScreen").performScrollToNode(hasTestTag("patchApply"))
                compose.onNodeWithTag("patchApply").performClick()
                waitFor("the patch applied") {
                    compose.onAllNodesWithText(context.getString(R.string.patch_applied_new), substring = true).fetchSemanticsNodes().isNotEmpty() ||
                        compose.onAllNodesWithTag("patchError", useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()
                }
                compose.onNodeWithText(context.getString(R.string.patch_applied_new), substring = true).assertExists()
            }
        } finally {
            resolver.delete(uri, null, null)
        }
        val patched = kotlinx.coroutines.runBlocking { client.allGames() }.games.first { it.name == "$gameName (patched)" }
        val rom = kotlinx.coroutines.runBlocking { client.game(patched.id) }.files.first { it.role == link.reminis.core.GameFileRoles.ROM }
        val bytes = java.io.ByteArrayOutputStream()
        kotlinx.coroutines.runBlocking { client.download(link.reminis.core.DownloadSource.GameFile(patched.id, rom.fileId!!), bytes) }
        val original = ByteArray(10_000) { (it * 31 % 251).toByte() }
        assertTrue("the ROM was not patched", bytes.toByteArray().copyOfRange(0, 4).contentEquals("FERN".toByteArray()))
        assertTrue("the rest of the ROM changed", bytes.toByteArray().copyOfRange(4, 10_000).contentEquals(original.copyOfRange(4, 10_000)))
    }
}
