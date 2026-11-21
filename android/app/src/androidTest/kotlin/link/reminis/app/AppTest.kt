package link.reminis.app

import android.content.Context
import android.util.Log
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
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
