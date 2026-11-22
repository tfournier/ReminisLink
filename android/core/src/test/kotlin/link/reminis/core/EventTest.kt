package link.reminis.core

import java.util.Collections
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.SocketPolicy
import okhttp3.tls.HandshakeCertificates
import okhttp3.tls.HeldCertificate
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test

/** M14: the SSE parser, the typed events, the stream's reconnection and liveness, and `GET /events` over pinned TLS. */
class EventTest {
    // MARK: parser

    private fun parse(text: String, chunk: Int = Int.MAX_VALUE): List<SseItem> {
        val parser = SseParser()
        val bytes = text.toByteArray()
        val out = mutableListOf<SseItem>()
        var i = 0
        while (i < bytes.size) {
            val n = minOf(chunk, bytes.size - i)
            out += parser.feed(bytes, i, n)
            i += n
        }
        return out
    }

    private fun events(text: String, chunk: Int = Int.MAX_VALUE) = parse(text, chunk).filterIsInstance<SseItem.Event>().map { it.event }

    @Test
    fun parsesTheExampleOfTheSpec() {
        val text = "id: 1\nevent: hello\ndata: {\"serverTime\":\"2026-11-20T08:00:00Z\",\"keepAliveSeconds\":15}\n\n" +
            ": keep-alive\n\nid: 2\nevent: library\ndata: {\"systemIds\":[\"R2FtZQ\"]}\n\nid: 3\nevent: shutdown\ndata: {\"reason\":\"quit\"}\n\n"
        for (chunk in listOf(1, 2, 7, Int.MAX_VALUE)) {
            val items = parse(text, chunk)
            assertEquals(SseItem.Comment("keep-alive"), items[1])
            val list = items.filterIsInstance<SseItem.Event>().map { it.event }
            assertEquals(listOf("hello", "library", "shutdown"), list.map { it.type })
            assertEquals(listOf("1", "2", "3"), list.map { it.id })
            val typed = list.map { PakEvent.parse(it) }
            assertEquals(PakEvent.Hello(EventHello("2026-11-20T08:00:00Z", 15)), typed[0])
            assertEquals(setOf("R2FtZQ"), (typed[1] as PakEvent.Library).library.affectedSystems)
            assertEquals("quit", (typed[2] as PakEvent.Shutdown).shutdown.reason)
        }
    }

    @Test
    fun lineEndingsBomDataLinesAndFields() {
        // CRLF split between chunks counts once; CR alone ends a line; a BOM is dropped.
        val text = "﻿event: a\r\ndata: one\r\ndata:two\r\rdata\n\nretry: 10\nfoo: bar\ndata: x\nid: has\u0000nul\n\n"
        for (chunk in listOf(1, 3, Int.MAX_VALUE)) {
            val list = events(text, chunk)
            assertEquals(3, list.size)
            assertEquals(SseEvent(null, "a", "one\ntwo"), list[0])
            // `data` without a colon: an empty value; no `event`: "message".
            assertEquals(SseEvent(null, "message", ""), list[1])
            // An id holding NUL is ignored.
            assertEquals(SseEvent(null, "message", "x"), list[2])
        }
    }

    @Test
    fun noDataNoEventAndCutEventsAreDropped() {
        assertTrue(events("event: library\n\n").isEmpty())
        assertTrue(events("event: library\ndata: {}").isEmpty())
        // The id is kept from one event to the next.
        val list = events("id: 7\ndata: a\n\ndata: b\n\n")
        assertEquals(listOf("7", "7"), list.map { it.id })
    }

    @Test
    fun oversizedLinesAndEventsAreDroppedWhole() {
        val long = "x".repeat(SseParser.MAX_LINE_BYTES + 10)
        val list = events("data: $long\ndata: kept\n\ndata: after\n\n")
        assertEquals(listOf("kept", "after"), list.map { it.data })
        val half = "y".repeat(SseParser.MAX_EVENT_BYTES / 2 + 10)
        val big = events("data: $half\ndata: $half\ndata: tail\n\ndata: next\n\n")
        assertEquals(listOf("next"), big.map { it.data })
    }

    @Test
    fun unknownTypesAndUnreadableDataAreIgnored() {
        assertEquals(PakEvent.Unknown("future"), PakEvent.parse(SseEvent("1", "future", "{}")))
        assertNull(PakEvent.parse(SseEvent("1", "library", "not json")))
        assertNull(PakEvent.parse(SseEvent("1", "device", "{}")))
        assertEquals(PakEvent.Paks, PakEvent.parse(SseEvent("1", "paks", "{}")))
        val device = PakEvent.parse(
            SseEvent(
                "4", "device",
                """{"battery":{"percent":41,"charging":true},"connectedClients":[{"clientName":"Pixel","lastSeenAt":"2026-11-20T08:00:00Z"}],
                   "storage":[{"id":"sd","label":"SD card","totalBytes":100,"freeBytes":40}],"future":1}""",
            ),
        ) as PakEvent.Device
        assertEquals(41, device.device.battery?.percent)
        val job = PakEvent.parse(SseEvent("5", "job", """{"id":"j1","kind":"patch","state":"running","progress":{"done":1,"total":2}}""")) as PakEvent.JobChanged
        assertEquals("j1", job.job.id)
        assertEquals(EventLibrary(emptyList()).affectedSystems, null)
    }

    @Test
    fun deviceStatusTakesTheEventFields() {
        val status = DeviceStatus(
            "d", "Brick", "brick", "tg5040", "nextui", null, "1.0.0", DeviceStatus.Network("1.2.3.4"),
            DeviceStatus.Battery(80, false), listOf(DeviceStatus.Storage("sd", "SD", 10, 5)), 3, emptyList(),
        )
        val updated = status.updated(EventDevice(DeviceStatus.Battery(12, true), listOf(ConnectedClient("Pixel", "t")), emptyList()))
        assertEquals(12, updated.battery?.percent)
        assertEquals("Pixel", updated.connectedClients.single().clientName)
        assertEquals("Brick", updated.deviceName)
    }

    @Test
    fun probeMeansClosed() {
        assertTrue(PakClosure.probeMeansClosed(ApiException.Server(503, ErrorCodes.SHUTTING_DOWN, "Shutting down")))
        assertTrue(PakClosure.probeMeansClosed(ApiException.Network(java.io.IOException("refused"))))
        assertTrue(!PakClosure.probeMeansClosed(ApiException.Server(401, ErrorCodes.DISCONNECTED, "Disconnected")))
        assertTrue(!PakClosure.probeMeansClosed(ApiException.UnexpectedStatus(500)))
    }

    // MARK: stream

    @Test
    fun backoffDoublesUpToTheMaximum() {
        val timing = EventStreamTiming()
        assertEquals(listOf(1_000L, 2_000L, 4_000L, 8_000L, 15_000L, 15_000L), (0..5).map { timing.reconnectDelay(it) })
        assertEquals(3_000L, timing.reconnectDelay(4, retryAfterMillis = 3_000))
    }

    /** A connection whose bytes the test sends. */
    private class FakeSource : EventByteSource {
        val chunks = Channel<ByteArray?>(Channel.UNLIMITED)
        @Volatile
        var cancelled = false

        fun send(text: String) {
            chunks.trySend(text.toByteArray())
        }

        fun end() {
            chunks.trySend(null)
        }

        override suspend fun read(buffer: ByteArray): Int {
            val chunk = chunks.receiveCatching().getOrNull() ?: return -1
            if (cancelled) throw ApiException.Network(java.io.IOException("cancelled"))
            chunk.copyInto(buffer)
            return chunk.size
        }

        override fun cancel() {
            cancelled = true
            chunks.close()
        }
    }

    private class Recorder {
        val events: MutableList<PakEvent> = Collections.synchronizedList(mutableListOf())
        val ends: MutableList<EventStream.End> = Collections.synchronizedList(mutableListOf())
        val sleeps: MutableList<Long> = Collections.synchronizedList(mutableListOf())
    }

    private fun timing(recorder: Recorder, liveness: Long = 60_000) = EventStreamTiming(
        livenessTimeoutMillis = liveness,
        sleep = { ms ->
            // The reconnection waits (at most 15 s) are recorded and shortened; the watchdog's sleeps are real.
            if (ms < liveness / 2) {
                recorder.sleeps += ms
                delay(1)
            } else {
                delay(ms)
            }
        },
    )

    private suspend fun waitUntil(what: String, condition: () -> Boolean) {
        try {
            withTimeout(5_000) { while (!condition()) delay(5) }
        } catch (e: Exception) {
            throw AssertionError("timeout waiting for $what", e)
        }
    }

    private val hello = "id: 1\nevent: hello\ndata: {\"serverTime\":\"2026-11-20T08:00:00Z\",\"keepAliveSeconds\":15}\n\n"

    @Test
    fun eventsThenShutdownStopsWithoutReconnecting() = runBlocking {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        val recorder = Recorder()
        val source = FakeSource()
        var connects = 0
        val stream = EventStream(
            scope, timing(recorder),
            connect = { connects++; source },
            onEvent = { recorder.events += it },
            onEnd = { recorder.ends += it; if (it is EventStream.End.Shutdown) EventStream.Decision.STOP else EventStream.Decision.RECONNECT },
        )
        stream.start()
        source.send(hello)
        source.send("event: future\ndata: {}\n\nevent: library\ndata: {\"systemIds\":[]}\n\n")
        waitUntil("library") { recorder.events.any { it is PakEvent.Library } }
        assertTrue(stream.isLive)
        source.send("event: shutdown\ndata: {\"reason\":\"quit\"}\n\nevent: library\ndata: {}\n\n")
        waitUntil("end") { recorder.ends.isNotEmpty() }
        assertEquals(EventStream.End.Shutdown(EventShutdown("quit")), recorder.ends.single())
        // Nothing read after shutdown; the connection is closed.
        assertEquals(1, recorder.events.count { it is PakEvent.Library })
        assertTrue(source.cancelled)
        assertEquals(PakEvent.Unknown("future"), recorder.events[1])
        stream.stop()
        scope.cancel()
    }

    @Test
    fun failuresBackOffAndALiveStreamResetsTheBackoff() = runBlocking {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        val recorder = Recorder()
        val sources = mutableListOf<FakeSource>()
        val stream = EventStream(
            scope, timing(recorder),
            connect = {
                when (sources.size) {
                    0, 1, 2 -> { sources += FakeSource(); throw ApiException.Network(java.io.IOException("refused")) }
                    3 -> { sources += FakeSource(); throw ApiException.Server(429, ErrorCodes.TOO_MANY_ATTEMPTS, "Too many", retryAfter = 3) }
                    4 -> { sources += FakeSource(); throw ApiException.Server(429, ErrorCodes.TOO_MANY_ATTEMPTS, "Too many") }
                    else -> FakeSource().also { sources += it; it.send(hello); it.end() }
                }
            },
            onEvent = { recorder.events += it },
            onEnd = { recorder.ends += it; if (recorder.ends.size < 7) EventStream.Decision.RECONNECT else EventStream.Decision.STOP },
        )
        stream.start()
        waitUntil("7 ends") { recorder.ends.size == 7 }
        waitUntil("stopped") { !stream.isRunning }
        // 2 s, 4 s, 8 s after network failures (doubled from the first failure, as on iOS), then Retry-After (3 s), the default 5 s,
        // then 1 s after each live stream.
        assertEquals(listOf(2_000L, 4_000L, 8_000L, 3_000L, 5_000L, 1_000L), recorder.sleeps.toList())
        assertEquals(EventStream.End.EndOfStream, recorder.ends.last())
        assertEquals(7, stream.attempts)
        scope.cancel()
    }

    @Test
    fun silenceEndsTheStreamOnceAndStopCancelsABlockedRead() = runBlocking {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        val recorder = Recorder()
        val source = FakeSource()
        val stream = EventStream(
            scope, timing(recorder, liveness = 300),
            connect = { source },
            onEvent = { recorder.events += it },
            onEnd = { recorder.ends += it; EventStream.Decision.RECONNECT },
        )
        stream.start()
        source.send(hello)
        waitUntil("liveness lost") { recorder.ends.isNotEmpty() }
        assertEquals(EventStream.End.LivenessLost, recorder.ends.single())
        assertTrue(!stream.isRunning)
        assertTrue("the blocked read was not cancelled", source.cancelled)
        delay(400)
        assertEquals(1, recorder.ends.size)

        // stop() during a blocked read: the source is cancelled and nothing is reported.
        val second = FakeSource()
        val other = EventStream(scope, timing(recorder), connect = { second }, onEvent = {}, onEnd = { recorder.ends += it; EventStream.Decision.RECONNECT })
        other.start()
        second.send(hello)
        waitUntil("live") { other.isLive }
        other.stop()
        waitUntil("cancelled") { second.cancelled }
        delay(50)
        assertEquals(1, recorder.ends.size)
        scope.cancel()
    }

    // MARK: GET /events over pinned TLS

    private val deviceId = "7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10"
    private lateinit var server: MockWebServer
    private lateinit var pin: String

    @Before
    fun setUp() {
        val certificate = HeldCertificate.Builder().ecdsa256().commonName(deviceId).build()
        pin = TlsPin.pin(certificate.certificate)
        server = MockWebServer()
        server.useHttps(HandshakeCertificates.Builder().heldCertificate(certificate).build().sslSocketFactory(), false)
        server.start()
    }

    @After
    fun tearDown() {
        server.shutdown()
    }

    private fun client(pin: String = this.pin) = ApiClient(
        endpoint = Endpoint(server.hostName, server.port),
        expectedDeviceId = deviceId,
        credentials = DeviceCredentials(deviceId, "3b1c8d2a-5e6f-4a7b-8c9d-0e1f2a3b4c5d", SeedEd25519Key(ByteArray(32) { it.toByte() }), this.pin),
        pin = pin,
    )

    @Test
    fun openEventsIsSignedAndStreams() = runBlocking {
        server.enqueue(
            MockResponse().setHeader("Content-Type", "text/event-stream").setBody(hello + "event: paks\ndata: {}\n\n")
                .setSocketPolicy(SocketPolicy.KEEP_OPEN),
        )
        val source = client().openEvents()
        val parser = SseParser()
        val buffer = ByteArray(64)
        val got = mutableListOf<PakEvent>()
        withTimeout(5_000) {
            while (got.size < 2) {
                val n = source.read(buffer)
                assertTrue(n >= 0)
                parser.feed(buffer, 0, n).filterIsInstance<SseItem.Event>().mapNotNull { PakEvent.parse(it.event) }.forEach { got += it }
            }
        }
        assertEquals(PakEvent.Paks, got[1])
        source.cancel()
        val request = server.takeRequest()
        assertEquals("/api/v1/events", request.path)
        assertNotNull(request.getHeader("X-RL-Signature"))
        assertEquals("text/event-stream", request.getHeader("Accept"))
        // No Last-Event-ID: the pak ignores it, the app reloads after hello.
        assertNull(request.getHeader("Last-Event-ID"))
    }

    @Test
    fun openEventsErrorsAndWrongPin() = runBlocking {
        server.enqueue(MockResponse().setResponseCode(401).setBody("""{"error":{"code":"disconnected","message":"Disconnected"}}"""))
        try {
            client().openEvents()
            fail("401 accepted")
        } catch (e: ApiException.Server) {
            assertEquals(ErrorCodes.DISCONNECTED, e.code)
        }
        server.enqueue(MockResponse().setResponseCode(429).setHeader("Retry-After", "5").setBody("""{"error":{"code":"too_many_attempts","message":"Too many"}}"""))
        try {
            client().openEvents()
            fail("429 accepted")
        } catch (e: ApiException.Server) {
            assertEquals(5, e.retryAfter)
        }
        val before = server.requestCount
        try {
            client(pin = Base64Url.encode(ByteArray(32) { 3 })).openEvents()
            fail("wrong pin accepted")
        } catch (_: ApiException.IdentityChanged) {
        }
        assertEquals(before, server.requestCount)
    }

    /** An error answer whose body stalls (headers sent, then a byte now and then). */
    private fun stalledError(status: Int = 429, code: String = "too_many_attempts") = MockResponse().setResponseCode(status).setHeader("Retry-After", "5")
        .setBody("{\"error\":{\"code\":\"$code\",\"message\":\"" + "x".repeat(200) + "\"}}")
        .throttleBody(1, 1, java.util.concurrent.TimeUnit.SECONDS)

    @Test
    fun aStalled401IsBoundedInTimeToo() = runBlocking {
        // The 401 body is read by the clock-skew check of signedResponse, before openEvents sees it.
        server.enqueue(stalledError(401, "disconnected"))
        val started = System.nanoTime()
        try {
            client().openEvents()
            fail("stalled 401 accepted")
        } catch (_: ApiException.Network) {
        }
        val seconds = (System.nanoTime() - started) / 1e9
        assertTrue("took $seconds s", seconds < ApiClient.ERROR_BODY_TIMEOUT_MILLIS / 1000.0 + 3)
    }

    @Test
    fun aStalledErrorBodyIsBoundedInTime() = runBlocking {
        server.enqueue(stalledError())
        val started = System.nanoTime()
        try {
            client().openEvents()
            fail("stalled error accepted")
        } catch (_: ApiException.Network) {
        }
        val seconds = (System.nanoTime() - started) / 1e9
        assertTrue("took $seconds s", seconds < ApiClient.ERROR_BODY_TIMEOUT_MILLIS / 1000.0 + 3)
    }

    @Test
    fun cancellingTheOpeningClosesAStalledErrorAtOnce() = runBlocking {
        server.enqueue(stalledError())
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        val job = scope.launch { runCatching { client().openEvents() } }
        delay(800)
        val started = System.nanoTime()
        job.cancel()
        withTimeout(2_000) { job.join() }
        assertTrue((System.nanoTime() - started) / 1e9 < 2)
        scope.cancel()
    }

    @Test
    fun connectClientIsSigned() = runBlocking {
        server.enqueue(MockResponse().setResponseCode(204))
        client().connectClient()
        val request = server.takeRequest()
        assertEquals("POST", request.method)
        assertEquals("/api/v1/clients/me/connect", request.path)
        assertNotNull(request.getHeader("X-RL-Signature"))
    }

    @Test
    fun jobEventsUpdateFollowedJobsOfTheSamePairingOnly() {
        val tracker = JobTracker({ "pairing-1" }) { throw IllegalStateException("no poll") }
        tracker.track("d", "j1", "Game", JobKinds.PATCH)
        tracker.apply("d", Job("j1", JobKinds.PATCH, JobStates.RUNNING, Job.Progress(1, 2)), "pairing-2")
        assertNull(tracker.get("d", "j1")?.job)
        tracker.apply("d", Job("j1", JobKinds.PATCH, JobStates.SUCCEEDED), "pairing-1")
        assertEquals(JobStates.SUCCEEDED, tracker.get("d", "j1")?.job?.state)
        tracker.apply("d", Job("other", JobKinds.PATCH, JobStates.RUNNING), "pairing-1")
        assertNull(tracker.get("d", "other"))
    }
}
