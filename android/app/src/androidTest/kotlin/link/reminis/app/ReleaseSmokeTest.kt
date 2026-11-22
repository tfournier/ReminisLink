package link.reminis.app

import android.content.Context
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import androidx.test.core.app.ActivityScenario
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Smoke test of the app as R8 shrinks it (build type `minified`: the
 * release build signed with the debug key; run with
 * `-Preminislink.testBuildType=minified`, also passes on debug): the demo
 * opens (Tink signs, kotlinx.serialization decodes the dashboard), the
 * library is read, and the event stream receives its `hello` (OkHttp,
 * SSE parser, typed events).
 */
@RunWith(AndroidJUnit4::class)
class ReleaseSmokeTest {
    @get:Rule
    val compose = createEmptyComposeRule()

    private val context: Context get() = ApplicationProvider.getApplicationContext()

    private fun has(tag: String) = compose.onAllNodesWithTag(tag, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()

    @Test
    fun demoLibraryAndHello() {
        InstrumentationRegistry.getInstrumentation().runOnMainSync { context.container.quitDemo() }
        ActivityScenario.launch(MainActivity::class.java).use {
            compose.waitUntil(10_000) { has("tryDemo") }
            compose.onNodeWithTag("tryDemo").performClick()
            compose.waitUntil(10_000) { has("dashboard") }
            val demo = context.container.demo.value!!
            compose.waitUntil(10_000) { context.container.events.isLive(demo.deviceId) }
            compose.onNodeWithTag("dashboard").assertExists()
            compose.onNodeWithTag("tabLibrary").performClick()
            compose.waitUntil(10_000) { compose.onAllNodesWithText("Thistledown Isles").fetchSemanticsNodes().isNotEmpty() }
            InstrumentationRegistry.getInstrumentation().runOnMainSync { context.container.quitDemo() }
        }
    }
}
