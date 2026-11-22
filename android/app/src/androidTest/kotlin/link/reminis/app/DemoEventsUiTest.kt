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
import androidx.compose.ui.test.printToLog
import androidx.lifecycle.Lifecycle
import androidx.test.core.app.ActivityScenario
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.async
import kotlinx.coroutines.launch
import kotlinx.coroutines.cancel
import kotlinx.coroutines.runBlocking
import link.reminis.core.CheatsUpdate
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/**
 * M14 and M16 in the app, with the demo mode's fake pak (in process, no
 * network): "Try Without a Console" opens a made-up console whose screens
 * work; changes made through the API by another client show up on the open
 * screen through the event stream; the stream stops in the background and
 * comes back in the foreground; a disconnection by the console is shown with
 * Reconnect and never reconnects by itself; `shutdown` sends the app back to
 * the list; Quit ends the demo and leaves nothing behind.
 */
@RunWith(AndroidJUnit4::class)
class DemoEventsUiTest {
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
            compose.onRoot(useUnmergedTree = true).printToLog("DemoEventsUiTest")
            throw AssertionError("timeout waiting for $what", e)
        }
    }

    private fun hasTag(tag: String) = compose.onAllNodesWithTag(tag, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()

    private fun hasText(text: String) = compose.onAllNodesWithText(text, substring = true).fetchSemanticsNodes().isNotEmpty()

    @Test
    fun demoScreensFollowTheEvents() {
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            compose.onNodeWithTag("tryDemo").performClick()
            waitFor("dashboard") { hasTag("dashboard") }
            compose.onNodeWithTag("demoBanner").assertExists()
            compose.onNodeWithText(context.getString(R.string.demo_mode)).assertExists()
            val demo = context.container.demo.value!!
            // The demo console is never written to the list of consoles.
            assertNull(context.container.consoles.get(demo.deviceId))
            waitFor("stream open") { context.container.events.isLive(demo.deviceId) }

            compose.onNodeWithTag("dashboard").performScrollToNode(hasTestTag("openLibrary"))
            compose.onNodeWithTag("openLibrary").performClick()
            waitFor("library") { hasText("Brambleway Dash") }

            // Another client deletes a game: the open list follows without any action here.
            val client = demo.client
            val dash = runBlocking { client.allGames() }.games.first { it.name == "Brambleway Dash" }
            runBlocking { client.deleteGame(dash.id, keepSave = false) }
            waitFor("the game gone from the screen") { !hasText("Brambleway Dash") }

            // Cheats changed by another client while the Cheats screen is open.
            compose.onNodeWithText("Thistledown Isles").performClick()
            waitFor("game") { hasTag("openCheats") }
            compose.onNodeWithTag("game").performScrollToNode(hasTestTag("openCheats"))
            compose.onNodeWithTag("openCheats").performClick()
            waitFor("cheats") { hasText(context.getString(R.string.demo_cheat_name, 1)) }
            val isles = runBlocking { client.allGames() }.games.first { it.name == "Thistledown Isles" }
            runBlocking {
                val list = client.cheats(isles.id)
                client.replaceCheats(isles.id, CheatsUpdate(listOf(list.cheats.first().copy(description = "Lantern of the API")) + list.cheats.drop(1)), list.etag)
            }
            waitFor("the new cheat on the screen") { hasText("Lantern of the API") }

            // Background: the stream stops; foreground: it opens again.
            scenario.moveToState(Lifecycle.State.CREATED)
            waitFor("stream closed in the background") { demo.pak.openStreams == 0 && !context.container.events.isLive(demo.deviceId) }
            scenario.moveToState(Lifecycle.State.RESUMED)
            waitFor("stream open again") { context.container.events.isLive(demo.deviceId) && demo.pak.openStreams == 1 }

            // Disconnected by the console: shown on the dashboard, never reconnected by itself.
            scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }
            scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }
            scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }
            waitFor("dashboard again") { hasTag("dashboard") }
            waitFor("stream open on the dashboard") { context.container.events.isLive(demo.deviceId) }
            val beforeDisconnection = demo.pak.eventsRequests.get()
            val disconnectedAt = System.nanoTime()
            demo.pak.disconnectClient()
            waitFor("disconnected shown") { hasText(context.getString(R.string.dashboard_disconnected)) }
            // Counted from the disconnection: the stream ended, its probe answered, exactly one
            // reconnection (after the 1 s backoff of a live stream) got `401 disconnected`; then nothing.
            Thread.sleep(4_000)
            assertEquals("GET /events attempts since the disconnection", beforeDisconnection + 1, demo.pak.eventsRequests.get())
            val delayMillis = (demo.pak.lastEventsRequestNanos.get() - disconnectedAt) / 1_000_000
            assertTrue("reconnected after $delayMillis ms (backoff of 1 s expected)", delayMillis >= 900)
            assertEquals("POST /clients/me/connect sent without the user", 0, demo.pak.connectRequests.get())
            assertEquals(0, demo.pak.openStreams)
            assertTrue(demo.pak.isClientDisconnected)
            compose.onNodeWithText(context.getString(R.string.reconnect)).performClick()
            waitFor("reconnected") { hasTag("dashboard") && demo.pak.openStreams == 1 && context.container.events.isLive(demo.deviceId) }
            assertEquals(1, demo.pak.connectRequests.get())
            assertTrue(!demo.pak.isClientDisconnected)

            // A disconnection seen by the quiet GET /device after a new `hello` is not hidden.
            demo.pak.failNextDeviceWithDisconnected = true
            demo.pak.dropStreams()
            waitFor("disconnected shown after the quiet refresh") { hasText(context.getString(R.string.dashboard_disconnected)) }
            waitFor("stream stopped") { demo.pak.openStreams == 0 && !context.container.events.isLive(demo.deviceId) }
            val afterQuiet = demo.pak.eventsRequests.get()
            Thread.sleep(3_000)
            assertEquals("GET /events sent again without the user", afterQuiet, demo.pak.eventsRequests.get())
            compose.onNodeWithText(context.getString(R.string.reconnect)).performClick()
            waitFor("reconnected again") { demo.pak.openStreams == 1 }
            // GET /device answered: POST /clients/me/connect is only sent after a 401.
            assertEquals(1, demo.pak.connectRequests.get())

            // The console closes ReminisLink: back to the list.
            demo.pak.sendShutdown()
            waitFor("back on the list") { hasTag("demoConsole") && !hasTag("dashboard") }
            assertEquals(0, demo.pak.openStreams)

            // Quit: the demo ends, the welcome screen is back.
            compose.onNodeWithTag("demoQuit").performClick()
            waitFor("welcome") { hasTag("addConsole") && !hasTag("demoBanner") }
            assertNull(context.container.demo.value)
        }
    }
}

/**
 * M16 with a real console registered: the demo never contacts it (no probe,
 * no request, no address written) and the list shows the demo console only.
 */
@RunWith(AndroidJUnit4::class)
class DemoIsolationUiTest {
    @get:Rule
    val compose = createEmptyComposeRule()

    private val context: Context get() = ApplicationProvider.getApplicationContext()
    private val realId = "0c5b1e7a-2d3f-4a6b-9c8d-7e6f5a4b3c2d"
    private lateinit var socket: java.net.ServerSocket
    private val accepted = java.util.concurrent.atomic.AtomicInteger()

    @Before
    fun setUp() {
        val container = context.container
        InstrumentationHelper.onMain { container.quitDemo() }
        container.consoles.all().forEach { container.forget(it.deviceId) }
        // Stands for the real console: counts every connection it gets.
        socket = java.net.ServerSocket(0, 8, java.net.InetAddress.getByName("127.0.0.1"))
        Thread {
            while (!socket.isClosed) runCatching { socket.accept().use { accepted.incrementAndGet() } }
        }.apply { isDaemon = true }.start()
        container.replacePairing(
            link.reminis.core.PairedConsole(
                deviceId = realId, deviceName = "Fern Handheld", model = "brick",
                pin = link.reminis.core.Base64Url.encode(ByteArray(32) { 5 }),
                lastEndpoint = link.reminis.core.Endpoint("127.0.0.1", socket.localPort), pairedAt = 1L,
            ),
        )
    }

    @After
    fun tearDown() {
        InstrumentationHelper.onMain { context.container.quitDemo() }
        context.container.forget(realId)
        socket.close()
    }

    private fun hasTag(tag: String) = compose.onAllNodesWithTag(tag, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()

    private fun hasText(text: String) = compose.onAllNodesWithText(text, substring = true).fetchSemanticsNodes().isNotEmpty()

    @Test
    fun theDemoNeverContactsARealConsole() {
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            compose.waitUntil(10_000) { hasText("Fern Handheld") }
            compose.onNodeWithTag("consoles").performScrollToNode(hasTestTag("tryDemo"))
            compose.onNodeWithTag("tryDemo").performClick()
            compose.waitUntil(10_000) { hasTag("dashboard") }
            scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }
            compose.waitUntil(10_000) { hasTag("demoConsole") }
            // The list of the demo shows its console only.
            assertTrue(!hasText("Fern Handheld"))
            // Neither a probe nor a request reaches the real console, and its address stays.
            val endpoint = context.container.consoles.get(realId)!!.lastEndpoint
            assertTrue(!runBlocking { context.container.validates(realId, endpoint!!) })
            val refused = runCatching { runBlocking { context.container.client(realId, refresh = true) } }
            assertTrue(refused.exceptionOrNull() is link.reminis.core.ApiException.Network)
            Thread.sleep(2_000)
            assertEquals("a real console was contacted during the demo", 0, accepted.get())
            assertEquals(endpoint, context.container.consoles.get(realId)!!.lastEndpoint)

            compose.onNodeWithTag("demoQuit").performClick()
            compose.waitUntil(10_000) { hasText("Fern Handheld") && !hasTag("demoBanner") }
        }
    }

    @Test
    fun aResolutionInFlightStopsWhenTheDemoStarts() {
        // A transfer reconnecting to the real console: mDNS for 3 s, then its last address.
        val before = context.container.consoles.get(realId)!!.lastEndpoint
        val scope = kotlinx.coroutines.CoroutineScope(kotlinx.coroutines.SupervisorJob() + kotlinx.coroutines.Dispatchers.Default)
        val result = scope.async { runCatching { context.container.client(realId, refresh = true) } }
        Thread.sleep(1_000)
        InstrumentationHelper.onMain { context.container.startDemo() }
        val outcome = runBlocking { kotlinx.coroutines.withTimeout(10_000) { result.await() } }
        assertTrue("got ${outcome.exceptionOrNull()}", outcome.exceptionOrNull() is link.reminis.core.ApiException.Network)
        Thread.sleep(3_000)
        assertEquals("the real console was contacted after the demo started", 0, accepted.get())
        assertEquals(before, context.container.consoles.get(realId)!!.lastEndpoint)
        scope.cancel()
    }

    @Test
    fun aShareDuringTheDemoEndsItAndOpensAdd() {
        InstrumentationHelper.onMain { context.container.startDemo() }
        val values = android.content.ContentValues().apply {
            put(android.provider.MediaStore.Downloads.DISPLAY_NAME, "Fern Share ${System.currentTimeMillis()}.gba")
        }
        val resolver = context.contentResolver
        val uri = resolver.insert(android.provider.MediaStore.Downloads.EXTERNAL_CONTENT_URI, values)!!
        try {
            resolver.openOutputStream(uri, "wt")!!.use { it.write(ByteArray(64) { 7 }) }
            val intent = android.content.Intent(android.content.Intent.ACTION_SEND).setClass(context, MainActivity::class.java)
                .setType("application/octet-stream").putExtra(android.content.Intent.EXTRA_STREAM, uri)
                .addFlags(android.content.Intent.FLAG_GRANT_READ_URI_PERMISSION)
            ActivityScenario.launch<MainActivity>(intent).use {
                // As on iOS: the demo ends first, then the share goes to the real consoles' Add screen.
                compose.waitUntil(10_000) { hasText("Fern Share") }
                assertNull(context.container.demo.value)
                assertTrue(!hasTag("demoBanner"))
            }
        } finally {
            resolver.delete(uri, null, null)
        }
    }

    @Test
    fun aPairingLinkFromAnotherAppNeverPairsDuringTheDemo() {
        InstrumentationHelper.onMain { context.container.startDemo() }
        val link = link.reminis.core.PairingLink(
            deviceId = "7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10",
            deviceName = "Brick de test",
            secret = ByteArray(32) { 1 },
            endpoint = link.reminis.core.Endpoint("127.0.0.1", socket.localPort),
            pin = link.reminis.core.Base64Url.encode(ByteArray(32) { 2 }),
        ).toUri()
        val intent = android.content.Intent(android.content.Intent.ACTION_VIEW, android.net.Uri.parse(link))
            .setClass(context, MainActivity::class.java)
        ActivityScenario.launch<MainActivity>(intent).use {
            compose.waitUntil(10_000) { hasTag("demoPairCancel") }
            // No pairing screen, no connection while the demo runs.
            assertTrue(!hasTag("externalConfirm"))
            Thread.sleep(4_000)
            assertEquals(0, accepted.get())
            // Quit the demo: the usual confirmation of a link from another app, still nothing sent.
            compose.onNodeWithTag("demoPairQuit").performClick()
            compose.waitUntil(10_000) { hasTag("externalConfirm") }
            assertNull(context.container.demo.value)
            assertEquals(0, accepted.get())
            compose.onNodeWithTag("cancelPair").performClick()
            compose.waitForIdle()
            assertNull(context.container.consoles.get("7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10"))
        }
    }
}

/** A copy cancelled after it finished (quitting the demo during Send) leaves no file behind. */
@RunWith(AndroidJUnit4::class)
class CopyCancellationTest {
    private val context: Context get() = ApplicationProvider.getApplicationContext()

    @Test
    fun aCancelledCopyLeavesNothing() {
        val root = java.io.File(context.cacheDir, "copy-test-${System.nanoTime()}").apply { mkdirs() }
        val values = android.content.ContentValues().apply {
            put(android.provider.MediaStore.Downloads.DISPLAY_NAME, "Fern Big ${System.currentTimeMillis()}.bin")
        }
        val resolver = context.contentResolver
        val uri = resolver.insert(android.provider.MediaStore.Downloads.EXTERNAL_CONTENT_URI, values)!!
        try {
            resolver.openOutputStream(uri, "wt")!!.use { out -> val block = ByteArray(1 shl 20); repeat(24) { out.write(block) } }
            repeat(5) { attempt ->
                runBlocking {
                    val job = launch(kotlinx.coroutines.Dispatchers.Default) {
                        // A copy handed back belongs to the caller (it queues it): removed here as a caller would.
                        runCatching { LocalFiles.copyIn(context, uri, root) }.getOrNull()?.file?.parentFile?.deleteRecursively()
                    }
                    kotlinx.coroutines.delay(5L + attempt * 20)
                    job.cancel()
                    job.join()
                }
                assertTrue("a copy nobody received was left: ${root.list()?.toList()}", root.list().isNullOrEmpty())
            }
        } finally {
            resolver.delete(uri, null, null)
            root.deleteRecursively()
        }
    }
}

/** Runs on the main thread (the app container's event state lives there). */
object InstrumentationHelper {
    fun onMain(block: () -> Unit) = androidx.test.platform.app.InstrumentationRegistry.getInstrumentation().runOnMainSync(block)
}
