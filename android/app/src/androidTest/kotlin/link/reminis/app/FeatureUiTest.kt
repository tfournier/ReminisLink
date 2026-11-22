package link.reminis.app

import android.content.ContentValues
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.provider.MediaStore
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.printToLog
import androidx.test.core.app.ActivityScenario
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import java.time.Instant
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.atomic.AtomicInteger
import link.reminis.core.Endpoint
import link.reminis.core.PairedConsole
import link.reminis.core.TlsPin
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import okhttp3.tls.HandshakeCertificates
import okhttp3.tls.HeldCertificate
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Increment 3 in the app against a local TLS server pinned like a paired
 * console (a small fake pak in memory): a cheat turned on and saved with
 * `If-Match`, a shared patch applied to a game chosen on the Patch screen
 * (which closes by itself on the new game), an app deleted while
 * ReminisLink itself is protected, and trash entries restored under a free
 * name or replacing the current save. Every name is invented.
 */
@RunWith(AndroidJUnit4::class)
class FeatureUiTest {
    @get:Rule
    val compose = createEmptyComposeRule()

    private val context: Context get() = ApplicationProvider.getApplicationContext()
    private val deviceId = "5d3f9b2a-7c4e-4a81-8f32-8b9cad0e1f30"
    private val gameId = "R0JBL01vc3MgTGFudGVybi5nYmE"
    private val patchedId = "R0JBL01vc3MgTGFudGVybiAocGF0Y2hlZCkuZ2Jh"
    private lateinit var server: MockWebServer
    private val requests = CopyOnWriteArrayList<RecordedRequest>()
    private val created = mutableListOf<Uri>()
    private val jobPolls = AtomicInteger()

    @Volatile private var cheatEnabled = false

    @Volatile private var trashPreview = ""

    private val falsePreviews = AtomicInteger()
    private val falseGate = java.util.concurrent.CountDownLatch(1)
    private val trueGate = java.util.concurrent.CountDownLatch(1)
    private val trueAnswered = java.util.concurrent.CountDownLatch(1)

    @Before
    fun setUp() {
        grantNotifications()
        val container = context.container
        container.consoles.all().forEach { container.forget(it.deviceId) }
        container.transfers.clearFinished()
        container.jobs.clearFinished()
        val certificate = HeldCertificate.Builder().ecdsa256().commonName(deviceId).build()
        server = MockWebServer()
        server.useHttps(HandshakeCertificates.Builder().heldCertificate(certificate).build().sslSocketFactory(), false)
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                requests += request
                return answer(request)
            }
        }
        server.start(java.net.InetAddress.getByName("127.0.0.1"), 0)
        container.replacePairing(
            PairedConsole(
                deviceId = deviceId, deviceName = "Juniper Test Console", model = "desktop",
                pin = TlsPin.pin(certificate.certificate), lastEndpoint = Endpoint("127.0.0.1", server.port),
                pairedAt = System.currentTimeMillis(),
            ),
        )
    }

    @After
    fun tearDown() {
        server.shutdown()
        created.forEach { runCatching { context.contentResolver.delete(it, null, null) } }
        context.container.forget(deviceId)
    }

    private fun json(body: String, code: Int = 200) =
        MockResponse().setResponseCode(code).setHeader("Content-Type", "application/json").setBody(body)

    private fun game(id: String, name: String, file: String) =
        """{"id":"$id","systemId":"R0JB","name":"$name","fileName":"$file","kind":"file","totalBytes":4,
        "files":[{"fileId":"cm9t","role":"rom","path":"Roms/Pocket Advance (GBA)/$file","sizeBytes":4,"modifiedAt":"2026-11-21T10:00:00Z"}]}"""

    private val cheats get() =
        """{"cheats":[{"index":0,"description":"Endless lantern oil","code":"82025490+FFFF","enabled":$cheatEnabled,"extra":{"handler":"0"}}],
        "etag":"\"sha256-${if (cheatEnabled) "on" else "off"}\""}"""

    private fun job(state: String) =
        """{"id":"job_p","kind":"patch","state":"$state","progress":{"done":1,"total":2},"createdAt":"2026-11-22T10:00:00Z",
        "updatedAt":"2026-11-22T10:00:01Z","result":${if (state == "succeeded") "{\"gameId\":\"$patchedId\",\"verified\":true}" else "null"},"error":null}"""

    /** The fake pak. */
    private fun answer(request: RecordedRequest): MockResponse {
        val path = request.path.orEmpty()
        val method = request.method
        return when {
            path == "/api/v1/hello" -> json(
                """{"deviceId":"$deviceId","deviceName":"Juniper Test Console","model":"desktop","platform":"tg5040","os":"nextui",
                "pakVersion":"1.1.0","apiVersion":2,"minClientVersion":"1.0.0","serverTime":"${Instant.now()}","pairingOpen":false}""",
            )
            path == "/api/v1/device" -> json(
                """{"deviceId":"$deviceId","deviceName":"Juniper Test Console","model":"desktop","platform":"tg5040","os":"nextui",
                "pakVersion":"1.1.0","network":{"ipv4":"127.0.0.1"},"storage":[{"id":"sd","label":"SD","totalBytes":1000,"freeBytes":500}],
                "uptimeSeconds":1,"connectedClients":[]}""",
            )
            path == "/api/v1/systems" -> json("""{"systems":[{"id":"R0JB","label":"Pocket Advance","folder":"Pocket Advance (GBA)","tag":"GBA","gameCount":1}]}""")
            path == "/api/v1/games" -> json(
                """{"games":[{"id":"$gameId","name":"Moss Lantern","fileName":"Moss Lantern.gba","kind":"file","sizeBytes":4,
                "modifiedAt":"2026-11-21T10:00:00Z","hasSave":false,"stateCount":0,"cheatCount":1,"systemId":"R0JB"},
                {"id":"R0JBL0Rpc2tz","name":"Two Discs","fileName":"Two Discs.m3u","kind":"m3u","sizeBytes":4,
                "modifiedAt":"2026-11-21T10:00:00Z","hasSave":false,"stateCount":0,"cheatCount":0,"systemId":"R0JB"}],
                "truncated":false,"generatedAt":"2026-11-21T10:00:00Z"}""",
            )
            path == "/api/v1/games/$gameId" -> json(game(gameId, "Moss Lantern", "Moss Lantern.gba"))
            path == "/api/v1/games/$patchedId" -> json(game(patchedId, "Moss Lantern (patched)", "Moss Lantern (patched).gba"))
            path == "/api/v1/games/$gameId/deletion?keepSave=false" -> {
                // Barriers driven by the test: every answer after the first waits for its gate.
                val n = falsePreviews.incrementAndGet()
                if (n > 1) falseGate.await(20, java.util.concurrent.TimeUnit.SECONDS)
                json(
                // A folder game: more than 8 files, two of them with the same name in different folders.
                """{"moved":[${(1..9).joinToString(",") { "{\"name\":\"Roms/Pocket Advance (GBA)/Moss Lantern/track$it.bin\",\"role\":\"rom\",\"size\":1}" }},
                {"name":"Roms/Pocket Advance (GBA)/Moss Lantern/disc1/data.bin","role":"other","size":1},
                {"name":"Roms/Pocket Advance (GBA)/Moss Lantern/disc2/data.bin","role":"other","size":1},
                {"name":"Saves/GBA/Moss Lantern.gba.sav","role":"save","size":1}],"kept":[],"movedBytes":12}""",
                )
            }
            // Answered late: it must never stand for the option shown after it.
            path == "/api/v1/games/$gameId/deletion?keepSave=true" -> {
                trueGate.await(20, java.util.concurrent.TimeUnit.SECONDS)
                json(
                    """{"moved":[{"name":"Roms/Pocket Advance (GBA)/Moss Lantern/track1.bin","role":"rom","size":1}],
                    "kept":[{"name":"Saves/GBA/Moss Lantern.gba.sav","role":"save","size":1}],"movedBytes":1}""",
                ).also { trueAnswered.countDown() }
            }
            path == "/api/v1/games/$gameId?keepSave=false" && method == "DELETE" ->
                json("""{"trashEntryId":"tr_9","moved":["Saves/GBA/Moss Lantern.gba.sav"],"movedBytes":12}""")
            path == "/api/v1/games/$gameId/cheats" && method == "GET" -> json(cheats)
            path == "/api/v1/games/$gameId/cheats" && method == "PUT" -> {
                if (request.getHeader("If-Match") != "\"sha256-off\"") {
                    json("""{"error":{"code":"conflict","message":"changed","details":{}}}""", 412)
                } else {
                    cheatEnabled = request.body.clone().readUtf8().contains("\"enabled\":true")
                    json(cheats)
                }
            }
            path == "/api/v1/uploads" -> json("""{"uploadId":"up_p","chunkSize":64,"offset":0,"expiresAt":"2026-11-23T10:00:00Z"}""", 201)
            path == "/api/v1/uploads/up_p" && method == "PUT" ->
                MockResponse().setResponseCode(204).setHeader("Upload-Offset", request.bodySize)
            path == "/api/v1/uploads/up_p/complete" -> json("""{"stagingId":"st_p"}""")
            path == "/api/v1/games/$gameId/patch" -> json("""{"jobId":"job_p"}""", 202)
            path == "/api/v1/jobs/job_p" -> json(job(if (jobPolls.incrementAndGet() < 2) "running" else "succeeded"))
            path == "/api/v1/paks" -> json(
                """{"paks":[{"pakId":"pk_self","name":"ReminisLink","kind":"tool","path":"Tools/tg5040/ReminisLink.pak","sizeBytes":9,"modifiedAt":"2026-11-20T08:00:00Z","version":"v1.1.0","self":true},
                {"pakId":"pk_clock","name":"Thistle Clock","kind":"tool","path":"Tools/tg5040/Thistle Clock.pak","sizeBytes":2048,"modifiedAt":"2026-11-20T08:00:00Z","version":null,"self":false}]}""",
            )
            path == "/api/v1/paks/pk_clock" && method == "DELETE" ->
                json("""{"trashEntryId":"tr_pak","moved":["Tools/tg5040/Thistle Clock.pak/launch.sh"],"movedBytes":2048}""")
            path == "/api/v1/trash" && method == "GET" -> json(
                """{"entries":[{"id":"tr_1","trashedAt":"2026-11-21T10:00:00Z","reason":"delete-game","label":"Moss Lantern",
                "files":["Roms/Pocket Advance (GBA)/Moss Lantern.gba"],"sizeBytes":4,"expiresAt":"2026-12-21T10:00:00Z"}],"totalBytes":4}""",
            )
            path == "/api/v1/trash/tr_1/restore" && method == "GET" -> json(trashPreview)
            path == "/api/v1/trash/tr_1/restore" && method == "POST" -> {
                val body = request.body.clone().readUtf8()
                when {
                    body.contains("rename") -> json(
                        """{"restored":["Roms/Pocket Advance (GBA)/Moss Lantern (2).gba"],"restoredBytes":4,"renamed":true,
                        "replacedEntryId":null,"replaced":[],"replacedKeptAs":null}""",
                    )
                    body.contains("replace") -> json(
                        """{"restored":["Saves/GBA/Moss Lantern.gba.sav"],"restoredBytes":3,"renamed":false,
                        "replacedEntryId":null,"replaced":["Saves/GBA/Moss Lantern.gba.sav"],"replacedKeptAs":"backup"}""",
                    )
                    else -> json("""{"error":{"code":"conflict","message":"taken","details":{"conflicts":["Roms/Pocket Advance (GBA)/Moss Lantern.gba"]}}}""", 409)
                }
            }
            else -> json("""{"error":{"code":"not_found","message":"Not found","details":{}}}""", 404)
        }
    }

    private fun document(name: String, bytes: ByteArray): Uri {
        val values = ContentValues().apply { put(MediaStore.Downloads.DISPLAY_NAME, name) }
        val uri = context.contentResolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values)!!
        created += uri
        context.contentResolver.openOutputStream(uri, "wt")!!.use { it.write(bytes) }
        return uri
    }

    private fun waitFor(what: String, timeout: Long = 20_000, condition: () -> Boolean) {
        try {
            compose.waitUntil(timeout, condition)
        } catch (e: Throwable) {
            compose.onRoot(useUnmergedTree = true).printToLog("FeatureUiTest")
            throw AssertionError("timeout waiting for $what", e)
        }
    }

    private fun waitForTag(tag: String, timeout: Long = 20_000) =
        waitFor(tag, timeout) { compose.onAllNodesWithTag(tag, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty() }

    private fun waitForText(text: String, timeout: Long = 20_000) =
        waitFor(text, timeout) { compose.onAllNodesWithText(text, substring = true).fetchSemanticsNodes().isNotEmpty() }

    private fun openDashboard() {
        compose.onNodeWithText("Juniper Test Console").performClick()
        waitForTag("dashboard", 30_000)
    }

    private fun openFromDashboard(tag: String) {
        compose.onNodeWithTag("dashboard").performScrollToNode(hasTestTag(tag))
        compose.onNodeWithTag(tag).performClick()
    }

    private fun openGame() {
        openDashboard()
        openFromDashboard("openLibrary")
        waitForTag("games")
        compose.onNodeWithText("Moss Lantern").performClick()
        waitForTag("game")
    }

    @Test
    fun aCheatIsTurnedOnAndSavedWithIfMatch() {
        ActivityScenario.launch(MainActivity::class.java).use {
            openGame()
            compose.onNodeWithTag("game").performScrollToNode(hasTestTag("openCheats"))
            compose.onNodeWithTag("openCheats").performClick()
            waitForText("Endless lantern oil")
            compose.onNodeWithTag("cheatToggle0").performClick()
            waitForTag("cheatsUnsaved")
            compose.onNodeWithTag("cheatsSave").performClick()
            waitForText(context.getString(R.string.cheats_saved))
        }
        val put = requests.first { it.method == "PUT" && it.path == "/api/v1/games/$gameId/cheats" }
        assertEquals("\"sha256-off\"", put.getHeader("If-Match"))
        assertTrue(put.getHeader("X-RL-Signature") != null)
        val body = put.body.clone().readUtf8()
        assertTrue("the unknown key travels back", body.contains("\"handler\":\"0\""))
        assertTrue(cheatEnabled)
    }

    @Test
    fun aSharedPatchIsAppliedAndTheScreenClosesOnTheNewGame() {
        val patch = "PATCH-invented-bytes".toByteArray()
        val uri = document("Moss Fix ${System.currentTimeMillis()}.ips", patch)
        val intent = Intent(Intent.ACTION_SEND).setClass(context, MainActivity::class.java)
            .setType("application/octet-stream").putExtra(Intent.EXTRA_STREAM, uri)
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
        ActivityScenario.launch<MainActivity>(intent).use {
            waitForTag("patchScreen")
            // The game is asked at once; a multi-disc game is not offered.
            waitForTag("patchGameItem")
            assertTrue(compose.onAllNodesWithText("Two Discs").fetchSemanticsNodes().isEmpty())
            compose.onNodeWithText("Moss Lantern").performClick()
            waitForTag("patchFile")
            waitForText("Moss Lantern (patched).gba")
            compose.onNodeWithTag("patchScreen").performScrollToNode(hasTestTag("patchApply"))
            compose.onNodeWithTag("patchApply").performClick()
            // Applied: the screen closed by itself on the new game, which says so.
            waitForText(context.getString(R.string.patch_applied_new), 30_000)
            waitForText("Moss Lantern (patched)")
        }
        val create = requests.first { it.path == "/api/v1/uploads" }.body.clone().readUtf8()
        assertTrue(create.contains("\"kind\":\"staging\""))
        val apply = requests.first { it.path == "/api/v1/games/$gameId/patch" }.body.clone().readUtf8()
        assertTrue(apply.contains("\"stagingId\":\"st_p\""))
        assertTrue(apply.contains("\"mode\":\"new-file\""))
        assertTrue(apply.contains("\"fileName\":\"Moss Lantern (patched).gba\""))
    }

    @Test
    fun anAppIsDeletedAndReminisLinkIsProtected() {
        ActivityScenario.launch(MainActivity::class.java).use {
            openDashboard()
            openFromDashboard("openApps")
            waitForTag("pak-Thistle Clock")
            compose.onNodeWithTag("pakSelf", useUnmergedTree = true).assertExists()
            assertTrue(compose.onAllNodesWithTag("pakDelete-ReminisLink").fetchSemanticsNodes().isEmpty())
            compose.onNodeWithTag("pakDelete-Thistle Clock").performClick()
            compose.onNodeWithTag("confirmOk").performClick()
            waitForText(context.getString(R.string.pak_deleted, "Thistle Clock"))
        }
        assertTrue(requests.any { it.method == "DELETE" && it.path == "/api/v1/paks/pk_clock" })
        assertTrue(requests.none { it.method == "DELETE" && it.path == "/api/v1/paks/pk_self" })
    }

    @Test
    fun aTrashEntryIsRestoredUnderAFreeName() {
        trashPreview = """{"entryId":"tr_1","reason":"delete-game","label":"Moss Lantern","conflict":true,
            "items":[{"name":"Roms/Pocket Advance (GBA)/Moss Lantern.gba","size":4,"conflict":true,"freeName":"Roms/Pocket Advance (GBA)/Moss Lantern (2).gba"}],
            "restoredBytes":4,"freeNameUnavailable":null,"replaceable":false,"replaceDisplaced":[],"replaceKeptAs":null,"replaceUnavailable":"not-a-save"}"""
        ActivityScenario.launch(MainActivity::class.java).use {
            openDashboard()
            openFromDashboard("openTrash")
            waitForTag("trashEntry")
            compose.onNodeWithTag("trashEntry").performClick()
            waitForTag("trashRestoreConflict")
            compose.onNodeWithText(context.getString(R.string.trash_item_comes_back_as, "Roms/Pocket Advance (GBA)/Moss Lantern (2).gba")).assertExists()
            compose.onNodeWithTag("trashRestore").performScrollToNode(hasTestTag("trashRestoreRename"))
            compose.onNodeWithTag("trashRestoreRename").performClick()
            waitForTag("trashRestoreDone")
            compose.onNodeWithText(context.getString(R.string.trash_done_renamed)).assertExists()
        }
        val post = requests.first { it.method == "POST" && it.path == "/api/v1/trash/tr_1/restore" }
        assertEquals("""{"onConflict":"rename"}""", post.body.clone().readUtf8())
    }

    @Test
    fun aDeletedBackupCopyReplacesTheCurrentSaveAfterAConfirmation() {
        trashPreview = """{"entryId":"tr_1","reason":"delete-backup","label":"Moss Lantern","conflict":false,
            "items":[{"name":"ReminisLink/backups/moss.sav","size":3,"conflict":false,"freeName":"ReminisLink/backups/moss.sav","replaceName":"Saves/GBA/Moss Lantern.gba.sav"},
            {"name":"ReminisLink/backups/moss.rtc","size":1,"conflict":false,"freeName":"ReminisLink/backups/moss.rtc","replaceName":"Saves/GBA/Moss Lantern.gba.rtc"}],
            "restoredBytes":4,"freeNameUnavailable":null,"replaceable":true,"replaceDisplaced":["Saves/GBA/Moss Lantern.gba.sav"],"replaceKeptAs":"backup","replaceUnavailable":null}"""
        ActivityScenario.launch(MainActivity::class.java).use {
            openDashboard()
            openFromDashboard("openTrash")
            waitForTag("trashEntry")
            compose.onNodeWithTag("trashEntry").performClick()
            waitForTag("trashRestoreConfirm")
            compose.onNodeWithTag("trashRestore").performScrollToNode(hasTestTag("trashRestoreReplace"))
            compose.onNodeWithText(context.getString(R.string.trash_restore_backup_help)).assertExists()
            compose.onNodeWithTag("trashRestoreReplace").performClick()
            // The confirmation says where the current save goes, clock file included.
            waitForTag("confirmOk")
            assertTrue(compose.onAllNodesWithText(context.getString(R.string.trash_replace_backup_clock)).fetchSemanticsNodes().size >= 2)
            compose.onNodeWithTag("confirmOk").performClick()
            waitForTag("trashRestoreDone")
            compose.onNodeWithText(context.getString(R.string.trash_done_backup)).assertExists()
        }
        val post = requests.first { it.method == "POST" && it.path == "/api/v1/trash/tr_1/restore" }
        assertEquals("""{"onConflict":"replace"}""", post.body.clone().readUtf8())
    }

    @Test
    fun aDeletionPreviewOfTheOtherOptionNeverStandsForTheOptionShown() {
        ActivityScenario.launch(MainActivity::class.java).use {
            openGame()
            compose.onNodeWithTag("gameMenu").performClick()
            compose.onNodeWithTag("deleteGame").performClick()
            waitForText("Saves/GBA/Moss Lantern.gba.sav")
            // On: its preview is held by the test; the confirmation waits for it.
            compose.onNodeWithTag("deleteKeepSave").performScrollTo().performClick()
            waitFor("the keep-save request") { requests.any { r -> r.path == "/api/v1/games/$gameId/deletion?keepSave=true" } }
            compose.onNodeWithTag("deleteGameConfirm").assertIsNotEnabled()
            // Off again: its preview is held too, the confirmation still waits.
            compose.onNodeWithTag("deleteKeepSave").performScrollTo().performClick()
            waitFor("the second preview request") { falsePreviews.get() >= 2 }
            compose.onNodeWithTag("deleteGameConfirm").assertIsNotEnabled()
            // The preview of the option shown answers first...
            falseGate.countDown()
            waitFor("the preview shown") {
                compose.onAllNodesWithTag("deleteGameConfirm").fetchSemanticsNodes().any {
                    !it.config.contains(androidx.compose.ui.semantics.SemanticsProperties.Disabled)
                }
            }
            // ...then the late answer for "keep": it changes nothing.
            trueGate.countDown()
            assertTrue(trueAnswered.await(20, java.util.concurrent.TimeUnit.SECONDS))
            waitFor("the late answer read") { requests.count { r -> r.path?.contains("/deletion?") == true } >= 3 }
            Thread.sleep(500)
            compose.waitForIdle()
            assertTrue(compose.onAllNodesWithText(context.getString(R.string.game_delete_kept)).fetchSemanticsNodes().isEmpty())
            // The exact list, with relative paths: 12 files, the two data.bin told apart.
            assertEquals(12, compose.onAllNodesWithTag("deleteMoved").fetchSemanticsNodes().size)
            compose.onNodeWithText("Roms/Pocket Advance (GBA)/Moss Lantern/disc2/data.bin").assertExists()
            compose.onNodeWithTag("deleteGameConfirm").assertIsEnabled().performClick()
            waitForTag("search")
        }
        assertTrue(requests.any { it.method == "GET" && it.path == "/api/v1/games/$gameId/deletion?keepSave=true" })
        assertTrue(requests.any { it.method == "DELETE" && it.path == "/api/v1/games/$gameId?keepSave=false" })
        assertTrue(requests.none { it.method == "DELETE" && it.path == "/api/v1/games/$gameId?keepSave=true" })
    }
}
