package link.reminis.app

import android.content.ContentValues
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.provider.MediaStore
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.printToLog
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.performTextInput
import androidx.test.core.app.ActivityScenario
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import java.time.Instant
import java.util.concurrent.CopyOnWriteArrayList
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import link.reminis.core.DownloadSource
import link.reminis.core.Endpoint
import link.reminis.core.PairedConsole
import link.reminis.core.TlsPin
import link.reminis.core.TransferPhase
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import okhttp3.tls.HandshakeCertificates
import okhttp3.tls.HeldCertificate
import org.junit.After
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/** The progress notification of the transfers is allowed: no system dialog over the app during a test. */
fun grantNotifications() {
    if (android.os.Build.VERSION.SDK_INT >= 33) {
        val instrumentation = androidx.test.platform.app.InstrumentationRegistry.getInstrumentation()
        instrumentation.uiAutomation.grantRuntimePermission(
            instrumentation.targetContext.packageName, android.Manifest.permission.POST_NOTIFICATIONS,
        )
    }
}

/**
 * Transfers, saves and states in the app against a local TLS server pinned
 * like a paired console (a small fake pak in memory): a file shared from
 * another app is sent as a game, a backup copy is made from the Save
 * screen, a state is locked from the States screen, and a download is
 * streamed into a document. Every name is invented.
 */
@RunWith(AndroidJUnit4::class)
class TransferUiTest {
    @get:Rule
    val compose = createEmptyComposeRule()

    private val context: Context get() = ApplicationProvider.getApplicationContext()
    private val deviceId = "4c2e8a1f-6b3d-4f70-9e21-7a8b9c0d1e2f"
    private val gameId = "R0JBL01vc3MgTGFudGVybi5nYmE"
    private lateinit var server: MockWebServer
    private val requests = CopyOnWriteArrayList<RecordedRequest>()
    private val received = java.io.ByteArrayOutputStream()
    private val backups = CopyOnWriteArrayList<String>()
    @Volatile private var slotLocked = false
    private val created = mutableListOf<Uri>()

    @Before
    fun setUp() {
        grantNotifications()
        val container = context.container
        container.consoles.all().forEach { container.forget(it.deviceId) }
        container.transfers.clearFinished()
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
                deviceId = deviceId, deviceName = "Fern Test Console", model = "desktop",
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

    private val slot get() =
        """{"slot":0,"sizeBytes":5,"modifiedAt":"2026-11-21T10:00:00Z","hasThumbnail":false,"locked":$slotLocked,"lockStatus":${if (slotLocked) "\"intact\"" else "null"}}"""

    /** The fake pak. */
    private fun answer(request: RecordedRequest): MockResponse {
        val path = request.path.orEmpty()
        return when {
            path == "/api/v1/hello" -> json(
                """{"deviceId":"$deviceId","deviceName":"Fern Test Console","model":"desktop","platform":"tg5040","os":"nextui",
                "pakVersion":"1.0.1","apiVersion":2,"minClientVersion":"1.0.0","serverTime":"${Instant.now()}","pairingOpen":false}""",
            )
            path == "/api/v1/device" -> json(
                """{"deviceId":"$deviceId","deviceName":"Fern Test Console","model":"desktop","platform":"tg5040","os":"nextui",
                "pakVersion":"1.0.1","network":{"ipv4":"127.0.0.1"},"storage":[{"id":"sd","label":"SD","totalBytes":1000,"freeBytes":500}],
                "uptimeSeconds":1,"connectedClients":[]}""",
            )
            path == "/api/v1/systems" -> json(
                """{"systems":[{"id":"R0JB","label":"Pocket Advance","folder":"Pocket Advance (GBA)","tag":"GBA","gameCount":1}]}""",
            )
            path == "/api/v1/games" -> json(
                """{"games":[{"id":"$gameId","name":"Moss Lantern","fileName":"Moss Lantern.gba","kind":"file","sizeBytes":4,
                "modifiedAt":"2026-11-21T10:00:00Z","hasSave":true,"stateCount":1,"cheatCount":0,"systemId":"R0JB"}],
                "truncated":false,"generatedAt":"2026-11-21T10:00:00Z"}""",
            )
            path == "/api/v1/games/$gameId" -> json(
                """{"id":"$gameId","systemId":"R0JB","name":"Moss Lantern","fileName":"Moss Lantern.gba","kind":"file","totalBytes":12,
                "files":[{"fileId":"cm9t","role":"rom","path":"Roms/Pocket Advance (GBA)/Moss Lantern.gba","sizeBytes":4,"modifiedAt":"2026-11-21T10:00:00Z"},
                {"fileId":"c2F2","role":"save","path":"Saves/GBA/Moss Lantern.gba.sav","sizeBytes":3,"modifiedAt":"2026-11-21T10:00:00Z"},
                {"fileId":"c3Qw","role":"state","slot":0,"path":".userdata/shared/GBA-gpsp/Moss Lantern.gba.st0","sizeBytes":5,"modifiedAt":"2026-11-21T10:00:00Z"}]}""",
            )
            path == "/api/v1/games/$gameId/save/backups" && request.method == "GET" -> json(
                """{"backups":[${backups.joinToString(",")}]}""",
            )
            path == "/api/v1/games/$gameId/save/backups" && request.method == "POST" -> {
                val label = request.body.readUtf8().substringAfter("\"label\":\"").substringBefore("\"")
                val backup = """{"id":"bk_${backups.size + 1}","label":"$label","createdAt":"2026-11-21T11:00:00Z","sizeBytes":3,"reason":"manual"}"""
                backups.add(0, backup)
                json(backup, 201)
            }
            path == "/api/v1/games/$gameId/states" -> json("""{"slots":[$slot],"resumeSlot":9}""")
            path == "/api/v1/games/$gameId/states/0/lock" -> {
                slotLocked = request.method == "PUT"
                json(slot)
            }
            path == "/api/v1/games/$gameId/save" -> MockResponse().setResponseCode(200).setBody("SAV")
            path == "/api/v1/uploads" -> json("""{"uploadId":"up_1","chunkSize":4,"offset":0,"expiresAt":"2026-11-22T10:00:00Z"}""", 201)
            path == "/api/v1/uploads/up_1" && request.method == "PUT" -> {
                synchronized(received) { received.write(request.body.readByteArray()) }
                MockResponse().setResponseCode(204).setHeader("Upload-Offset", received.size())
            }
            path == "/api/v1/uploads/up_1/complete" -> json("""{"gameId":"R0JBL05ldy5nYmE"}""")
            else -> json("""{"error":{"code":"not_found","message":"Not found","details":{}}}""", 404)
        }
    }

    /** A document owned by the app (MediaStore Downloads), as another app would share it. */
    private fun document(name: String, bytes: ByteArray): Uri {
        val values = ContentValues().apply { put(MediaStore.Downloads.DISPLAY_NAME, name) }
        val uri = context.contentResolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values)!!
        created += uri
        context.contentResolver.openOutputStream(uri, "wt")!!.use { it.write(bytes) }
        return uri
    }

    private fun waitForTag(tag: String, timeout: Long = 20_000) =
        compose.waitUntil(timeout) { compose.onAllNodesWithTag(tag, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty() }

    private fun waitForText(text: String, timeout: Long = 20_000) =
        compose.waitUntil(timeout) { compose.onAllNodesWithText(text, substring = true).fetchSemanticsNodes().isNotEmpty() }

    @Test
    fun sharedFileIsSentAsAGame() {
        val bytes = "MOSS-LANTERN-2".toByteArray()
        val uri = document("Moss Lantern II.gba", bytes)
        val intent = Intent(Intent.ACTION_SEND).setClass(context, MainActivity::class.java)
            .setType("application/octet-stream").putExtra(Intent.EXTRA_STREAM, uri)
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
        ActivityScenario.launch<MainActivity>(intent).use {
            waitForTag("addFile")
            compose.onNodeWithText("Moss Lantern II.gba").assertExists()
            // The system is suggested from the extension (tag GBA).
            waitForText("Pocket Advance (GBA)")
            compose.onNodeWithTag("sendFiles").performClick()
            try {
                waitForTag("transfers")
            } catch (e: Throwable) {
                compose.onRoot().printToLog("TransferUiTest")
                throw e
            }
            waitForText(context.getString(R.string.transfer_sent), 30_000)
        }
        assertArrayEquals(bytes, received.toByteArray())
        val chunks = requests.filter { it.method == "PUT" && it.path == "/api/v1/uploads/up_1" }
        assertEquals(listOf("0", "4", "8", "12"), chunks.map { it.getHeader("Upload-Offset") })
        assertTrue(chunks.all { it.getHeader("X-RL-Content-SHA256") == "UNSIGNED" && it.getHeader("X-RL-Signature") != null })
        val create = requests.first { it.path == "/api/v1/uploads" }
        assertEquals(link.reminis.core.Signer.contentSha256(create.body.clone().readByteArray()), create.getHeader("X-RL-Content-SHA256"))
    }

    private fun openGame() {
        compose.onNodeWithText("Fern Test Console").performClick()
        waitForTag("dashboard", 30_000)
        compose.onNodeWithTag("dashboard").performScrollToNode(hasTestTag("openLibrary"))
        compose.onNodeWithTag("openLibrary").performClick()
        waitForTag("games")
        compose.onNodeWithText("Moss Lantern").performClick()
        waitForTag("game")
    }

    @Test
    fun backupCopyFromTheSaveScreen() {
        ActivityScenario.launch(MainActivity::class.java).use {
            openGame()
            compose.onNodeWithTag("openSave").performClick()
            waitForTag("saveScreen")
            compose.onNodeWithText(context.getString(R.string.backups_none)).assertExists()
            compose.onNodeWithTag("createBackup").performClick()
            compose.onNodeWithTag("backupLabel").performTextInput("Before the bridge")
            compose.onNodeWithTag("backupCreate").performClick()
            waitForText(context.getString(R.string.backup_created))
            compose.onNodeWithTag("saveScreen").performScrollToNode(hasText("Before the bridge"))
            compose.onNodeWithText("Before the bridge").assertExists()
        }
        val post = requests.first { it.method == "POST" && it.path == "/api/v1/games/$gameId/save/backups" }
        assertTrue(post.getHeader("X-RL-Signature") != null)
    }

    @Test
    fun lockAStateFromTheStatesScreen() {
        ActivityScenario.launch(MainActivity::class.java).use {
            openGame()
            compose.onNodeWithTag("openStates").performClick()
            waitForTag("slot0")
            compose.onNodeWithTag("slot0").performClick()
            compose.onNodeWithText(context.getString(R.string.state_lock_action)).performClick()
            waitForTag("locked0")
            waitForText(context.getString(R.string.state_locked_notice, context.getString(R.string.state_slot, 1)))
        }
        assertTrue(requests.any { it.method == "PUT" && it.path == "/api/v1/games/$gameId/states/0/lock" })
    }

    @Test
    fun downloadIsStreamedIntoADocument() = runBlocking {
        val uri = document("Moss Lantern.sav", ByteArray(0))
        val queue = context.container.transfers
        val id = queue.enqueueDownload(deviceId, DownloadSource.Save(gameId), "Moss Lantern.sav", "Moss Lantern", DocumentSink(context, uri))
        withTimeout(30_000) { queue.items.first { list -> list.any { it.id == id && it.phase == TransferPhase.DONE } } }
        val bytes = context.contentResolver.openInputStream(uri)!!.use { it.readBytes() }
        assertArrayEquals("SAV".toByteArray(), bytes)
    }
}
