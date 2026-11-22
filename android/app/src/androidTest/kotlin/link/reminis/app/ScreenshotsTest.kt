package link.reminis.app

import android.content.Context
import android.graphics.Bitmap
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import androidx.test.core.app.ActivityScenario
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assume.assumeTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/**
 * The store screenshots (F-Droid and Google Play metadata), taken from the
 * demo mode (made-up console and games, no real product): run only by
 * android/scripts/make-screenshots.sh (instrumentation argument
 * `screenshots=true`), once per locale. The PNG files go to the app's
 * private files (`files/screenshots/`), from where the script copies them
 * into android/fastlane/metadata/android/<locale>/images/phoneScreenshots/.
 */
@RunWith(AndroidJUnit4::class)
class ScreenshotsTest {
    @get:Rule
    val compose = createEmptyComposeRule()

    private val context: Context get() = ApplicationProvider.getApplicationContext()

    private fun has(tag: String) = compose.onAllNodesWithTag(tag, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()

    private fun hasText(text: String) = compose.onAllNodesWithText(text, substring = true).fetchSemanticsNodes().isNotEmpty()

    private fun shot(name: String) {
        compose.waitForIdle()
        Thread.sleep(800)
        val bitmap = InstrumentationRegistry.getInstrumentation().uiAutomation.takeScreenshot()
        val dir = java.io.File(context.filesDir, "screenshots").apply { mkdirs() }
        java.io.File(dir, "$name.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
    }

    private fun back(scenario: ActivityScenario<MainActivity>) = scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }

    @Test
    fun storeScreenshots() {
        val arguments = InstrumentationRegistry.getArguments()
        assumeTrue("screenshots are made by make-screenshots.sh", arguments.getString("screenshots") == "true")
        InstrumentationRegistry.getInstrumentation().runOnMainSync { context.container.quitDemo() }
        java.io.File(context.filesDir, "screenshots").deleteRecursively()
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            compose.waitUntil(10_000) { has("tryDemo") }
            compose.onNodeWithTag("tryDemo").performClick()
            compose.waitUntil(10_000) { has("dashboard") }
            shot("1")
            compose.onNodeWithTag("dashboard").performScrollToNode(hasTestTag("openLibrary"))
            compose.onNodeWithTag("openLibrary").performClick()
            compose.waitUntil(10_000) { hasText("Thistledown Isles") }
            shot("2")
            compose.onNodeWithText("Thistledown Isles").performClick()
            compose.waitUntil(10_000) { has("openStates") }
            shot("3")
            compose.onNodeWithTag("openStates").performClick()
            compose.waitUntil(10_000) { has("statesScreen") && has("locked0") }
            shot("4")
            back(scenario)
            compose.waitUntil(10_000) { has("openCheats") }
            compose.onNodeWithTag("game").performScrollToNode(hasTestTag("openCheats"))
            compose.onNodeWithTag("openCheats").performClick()
            compose.waitUntil(10_000) { has("cheat0") }
            shot("5")
            back(scenario)
            compose.waitUntil(10_000) { has("openSave") }
            compose.onNodeWithTag("openSave").performClick()
            compose.waitUntil(10_000) { has("backup") }
            shot("6")
            back(scenario)
            back(scenario)
            back(scenario)
            compose.waitUntil(10_000) { has("dashboard") }
            compose.onNodeWithTag("dashboard").performScrollToNode(hasTestTag("openApps"))
            compose.onNodeWithTag("openApps").performClick()
            compose.waitUntil(10_000) { hasText("Clock") }
            shot("7")
            InstrumentationRegistry.getInstrumentation().runOnMainSync { context.container.quitDemo() }
        }
    }
}
