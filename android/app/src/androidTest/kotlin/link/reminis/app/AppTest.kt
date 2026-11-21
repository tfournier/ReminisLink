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
import link.reminis.core.Ed25519
import link.reminis.core.PairingLink
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
 * Skipped without the `pairingLink` instrumentation argument; the emulator
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
        val link = InstrumentationRegistry.getArguments().getString("pairingLink")
        assumeTrue("no pairingLink argument", !link.isNullOrBlank())
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
            compose.onNodeWithText(context.getString(R.string.role_rom)).assertExists()
        }
    }
}
