package link.reminis.app

import android.content.Context
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import androidx.test.core.app.ActivityScenario
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import java.time.Instant
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import link.reminis.core.Base64Url
import link.reminis.core.Endpoint
import link.reminis.core.Pairing
import link.reminis.core.PairingLink
import link.reminis.core.PairRequest
import link.reminis.core.ProtocolJson
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
 * The commit point of pairing: a local TLS server with a P-256 certificate
 * pinned by the link stands for the console. System back before `POST /pair`
 * is written sends nothing and stores nothing; once the console has received
 * `POST /pair`, leaving the screen still completes and stores the pairing.
 * A failed attempt can be retried, and the link never outlives the screen.
 */
@RunWith(AndroidJUnit4::class)
class PairingBoundaryTest {
    @get:Rule
    val compose = createEmptyComposeRule()

    private val context: Context get() = ApplicationProvider.getApplicationContext()
    private val deviceId = "0b7a3c9e-2d4f-4e61-8a72-9c0d1e2f3a4b"
    private val secret = ByteArray(32) { (it * 7).toByte() }
    private lateinit var server: MockWebServer
    private lateinit var pin: String
    private val pairRequests = AtomicInteger(0)
    private val pairReceived = CountDownLatch(1)

    /** Released by a test to let the console answer `POST /pair`. */
    private val answerPair = CountDownLatch(1)

    /** How many of the next `POST /pair` the console rejects with 429 (after receiving them). */
    private val rejectPairs = AtomicInteger(0)

    /** Links left by other tests of the same process. */
    private var linksBefore = 0

    @Before
    fun setUp() {
        val container = context.container
        container.consoles.all().forEach { container.forget(it.deviceId) }
        linksBefore = container.pendingLinks.size
        val certificate = HeldCertificate.Builder().ecdsa256().commonName(deviceId).build()
        pin = TlsPin.pin(certificate.certificate)
        server = MockWebServer()
        server.useHttps(HandshakeCertificates.Builder().heldCertificate(certificate).build().sslSocketFactory(), false)
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse = when (request.path) {
                "/api/v1/hello" -> json(
                    """{"deviceId":"$deviceId","deviceName":"Boundary","model":"desktop","platform":"tg5040",
                    "os":"nextui","osVersion":null,"pakVersion":"1.0.1","apiVersion":2,"minClientVersion":"1.0.0",
                    "serverTime":"${Instant.now()}","pairingOpen":true}""",
                )
                "/api/v1/pair" -> {
                    pairRequests.incrementAndGet()
                    pairReceived.countDown()
                    answerPair.await(10, TimeUnit.SECONDS)
                    if (rejectPairs.getAndDecrement() > 0) MockResponse().setResponseCode(429) else pairAnswer(request)
                }
                else -> MockResponse().setResponseCode(404)
            }
        }
        server.start(java.net.InetAddress.getByName("127.0.0.1"), 0)
    }

    @After
    fun tearDown() {
        context.container.pairingGateForTests = null
        answerPair.countDown()
        server.shutdown()
    }

    /** The console's valid answer to `POST /pair`, with its proof. */
    private fun pairAnswer(request: RecordedRequest): MockResponse {
        val body = ProtocolJson.decodeFromString(PairRequest.serializer(), request.body.readUtf8())
        val deviceNonce = Base64Url.encode(ByteArray(32) { 5 })
        val proof = Pairing.proof(
            secret,
            Pairing.deviceProofMessage(deviceId, body.clientId, body.clientPublicKey, body.clientNonce, deviceNonce),
        )
        return json("""{"deviceId":"$deviceId","deviceName":"Boundary","deviceNonce":"$deviceNonce","proof":"$proof"}""")
    }

    private fun json(body: String) = MockResponse().setHeader("Content-Type", "application/json").setBody(body)

    private fun link() = PairingLink(deviceId, "Boundary", secret, Endpoint("127.0.0.1", server.port), pin).toUri()

    /** Waits for [latch]; fails with the pairing screen's error message if one shows up first. */
    private fun await(latch: CountDownLatch, what: String) {
        val deadline = System.currentTimeMillis() + 20_000
        while (!latch.await(200, TimeUnit.MILLISECONDS)) {
            val errors = compose.onAllNodesWithTag("error").fetchSemanticsNodes()
            if (errors.isNotEmpty()) {
                val text = errors.first().config.getOrElseNullable(androidx.compose.ui.semantics.SemanticsProperties.Text) { null }
                throw AssertionError("$what: ${text?.joinToString()}")
            }
            if (System.currentTimeMillis() > deadline) throw AssertionError("$what: timeout")
        }
    }

    private fun startPairing() {
        compose.onNodeWithTag("addConsole").performClick()
        compose.onNodeWithTag("pairingLink").performTextInput(link())
        compose.onNodeWithTag("pairWithLink").performClick()
    }

    /** The pairing link of the test is gone from memory (it may be dropped right after the pairing is stored). */
    private fun assertNoPendingLink() {
        val deadline = System.currentTimeMillis() + 5_000
        while (context.container.pendingLinks.size > linksBefore && System.currentTimeMillis() < deadline) Thread.sleep(50)
        assertEquals(linksBefore, context.container.pendingLinks.size)
    }

    private fun waitForScanScreen() = compose.waitUntil(5_000) {
        compose.onAllNodesWithText(context.getString(R.string.welcome_step_scan)).fetchSemanticsNodes().isNotEmpty()
    }

    @Test
    fun backRightAfterHelloSendsNoPair() {
        val gateReached = CountDownLatch(1)
        val release = CountDownLatch(1)
        // Runs in OkHttp's network interceptor, on the pinned connection,
        // right before the WAITING → SENT commit point.
        context.container.pairingGateForTests = {
            gateReached.countDown()
            release.await(10, TimeUnit.SECONDS)
        }
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            startPairing()
            // The request is at the commit point, not written yet.
            await(gateReached, "pairing never reached the commit point of POST /pair")
            scenario.onActivity {
                it.onBackPressedDispatcher.onBackPressed()
                // Released at once, before Navigation clears the destination:
                // only the system back's own cancellation can stop the
                // request, not a ViewModel cleared later.
                release.countDown()
            }
            waitForScanScreen()
            Thread.sleep(2_000)
            assertEquals(0, pairRequests.get())
            assertTrue(context.container.consoles.all().isEmpty())
            assertNoPendingLink()
        }
    }

    @Test
    fun retryAfterAFailedPairStartsAFreshAttempt() {
        rejectPairs.set(1)
        answerPair.countDown()
        ActivityScenario.launch(MainActivity::class.java).use {
            startPairing()
            // POST /pair was sent (state SENT) and the console answered 429.
            compose.waitUntil(20_000) { compose.onAllNodesWithTag("error").fetchSemanticsNodes().isNotEmpty() }
            assertEquals(1, pairRequests.get())
            compose.onNodeWithText(context.getString(R.string.action_retry)).performClick()
            compose.waitUntil(20_000) { compose.onAllNodesWithTag("paired").fetchSemanticsNodes().isNotEmpty() }
            assertEquals(2, pairRequests.get())
            assertEquals(pin, context.container.consoles.get(deviceId)?.pin)
            assertNoPendingLink()
        }
    }

    @Test
    fun leavingAfterAFailedPairDropsTheLink() {
        rejectPairs.set(1)
        answerPair.countDown()
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            startPairing()
            compose.waitUntil(20_000) { compose.onAllNodesWithTag("error").fetchSemanticsNodes().isNotEmpty() }
            assertEquals(linksBefore + 1, context.container.pendingLinks.size)
            scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }
            waitForScanScreen()
            assertNoPendingLink()
            assertTrue(context.container.consoles.all().isEmpty())
        }
    }

    @Test
    fun backAfterPairReceivedStillStoresThePairing() {
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            startPairing()
            await(pairReceived, "POST /pair never received")
            scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }
            waitForScanScreen()
            // The console answers after the user left: the app still stores it.
            answerPair.countDown()
            val deadline = System.currentTimeMillis() + 10_000
            while (context.container.consoles.get(deviceId) == null && System.currentTimeMillis() < deadline) Thread.sleep(100)
            assertEquals(pin, context.container.consoles.get(deviceId)?.pin)
            assertEquals(1, pairRequests.get())
            assertNoPendingLink()
        }
    }
}
