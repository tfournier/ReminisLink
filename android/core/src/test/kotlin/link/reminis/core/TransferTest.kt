package link.reminis.core

import java.io.ByteArrayOutputStream
import java.io.File
import java.io.OutputStream
import java.security.MessageDigest
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import okhttp3.mockwebserver.SocketPolicy
import okhttp3.tls.HandshakeCertificates
import okhttp3.tls.HeldCertificate
import org.junit.After
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

/** Uploads, downloads, saves and states against a local TLS server (pinned, signed), and the transfer queue. */
class TransferTest {
    private val deviceId = "7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10"
    private val clientId = "3b1c8d2a-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
    private val key = SeedEd25519Key(ByteArray(32) { it.toByte() })
    private val now = 1_795_539_600_000L

    @get:Rule
    val temp = TemporaryFolder()

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

    private fun client() = ApiClient(
        endpoint = Endpoint(server.hostName, server.port),
        expectedDeviceId = deviceId,
        credentials = DeviceCredentials(deviceId, clientId, key, pin),
        nowMillis = { now },
    )

    private fun json(body: String, code: Int = 200) =
        MockResponse().setResponseCode(code).setHeader("Content-Type", "application/json").setBody(body)

    private fun error(code: String, status: Int, details: String = "{}") =
        json("""{"error":{"code":"$code","message":"x","details":$details}}""", status)

    private fun created(id: String = "up_1", chunk: Long = 4, offset: Long = 0) =
        json("""{"uploadId":"$id","chunkSize":$chunk,"offset":$offset,"expiresAt":"2026-11-25T17:00:00Z"}""", 201)

    private fun chunkStored(offset: Long) = MockResponse().setResponseCode(204).setHeader("Upload-Offset", offset)

    private fun file(bytes: ByteArray): File = temp.newFile().apply { writeBytes(bytes) }

    private fun sha(bytes: ByteArray) = MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") { "%02x".format(it) }

    /** The request's signature is valid for its method, path, query and content hash. */
    private fun assertSigned(request: RecordedRequest) {
        val canonical = Signer.canonicalRequest(
            request.method!!, request.requestUrl!!.encodedPath, request.requestUrl!!.encodedQuery ?: "",
            request.getHeader("X-RL-Time")!!, request.getHeader("X-RL-Nonce")!!, request.getHeader("X-RL-Content-SHA256")!!,
        )
        assertTrue(Signer.isValidSignature(request.getHeader("X-RL-Signature")!!, key.publicKey, canonical))
    }

    // MARK: resumable upload

    @Test
    fun uploadSendsSignedCreateUnsignedChunksAndComplete() = runBlocking {
        val bytes = "0123456789".toByteArray()
        server.enqueue(created(chunk = 4))
        server.enqueue(chunkStored(4))
        server.enqueue(chunkStored(8))
        server.enqueue(chunkStored(10))
        server.enqueue(json("""{"gameId":"R0JBL05ldy5nYmE"}"""))
        val upload = ResumableUpload(
            file(bytes), bytes.size.toLong(), sha(bytes), UploadTarget.Rom("R0JB", "Moss Lantern.gba"), retry = RetryPolicy.immediate,
        )
        val offsets = mutableListOf<Long>()
        val result = upload.run(client(), progress = { offsets += it })
        assertEquals("R0JBL05ldy5nYmE", result.gameId)
        assertEquals(listOf(0L, 4L, 8L, 10L), offsets)

        val create = server.takeRequest()
        assertEquals("POST", create.method)
        assertEquals("/api/v1/uploads", create.path)
        val body = Json.parseToJsonElement(create.body.readUtf8()).jsonObject
        val target = body["target"]!!.jsonObject
        assertEquals("rom", target["kind"]!!.jsonPrimitive.content)
        assertEquals("R0JB", target["systemId"]!!.jsonPrimitive.content)
        assertEquals("Moss Lantern.gba", target["fileName"]!!.jsonPrimitive.content)
        assertEquals(sha(bytes), body["sha256"]!!.jsonPrimitive.content)
        assertEquals("10", body["sizeBytes"]!!.jsonPrimitive.content)
        assertEquals("fail", body["onConflict"]!!.jsonPrimitive.content)
        assertSigned(create)

        val received = ByteArrayOutputStream()
        for (expected in listOf(0, 4, 8)) {
            val chunk = server.takeRequest()
            assertEquals("PUT", chunk.method)
            assertEquals("/api/v1/uploads/up_1", chunk.path)
            assertEquals(expected.toString(), chunk.getHeader("Upload-Offset"))
            // The only UNSIGNED body: the whole file is checked by its SHA-256.
            assertEquals("UNSIGNED", chunk.getHeader("X-RL-Content-SHA256"))
            assertSigned(chunk)
            received.write(chunk.body.readByteArray())
        }
        assertArrayEquals(bytes, received.toByteArray())
        val complete = server.takeRequest()
        assertEquals("/api/v1/uploads/up_1/complete", complete.path)
        assertEquals(Signer.contentSha256(ByteArray(0)), complete.getHeader("X-RL-Content-SHA256"))
        assertSigned(complete)
    }

    @Test
    fun conflictOffsetIsFollowed() = runBlocking {
        val bytes = "abcdefgh".toByteArray()
        server.enqueue(created(chunk = 4))
        // The first chunk's answer was lost: the console already has 4 bytes.
        server.enqueue(error("conflict", 409, """{"offset":4}"""))
        server.enqueue(chunkStored(8))
        server.enqueue(json("""{"gameId":"g"}"""))
        val upload = ResumableUpload(file(bytes), 8, sha(bytes), UploadTarget.Rom("s", "a.gba"), retry = RetryPolicy.immediate)
        upload.run(client())
        server.takeRequest()
        assertEquals("0", server.takeRequest().getHeader("Upload-Offset"))
        assertEquals("4", server.takeRequest().getHeader("Upload-Offset"))
    }

    @Test
    fun lostConnectionResumesFromTheConfirmedOffset() = runBlocking {
        val bytes = "abcdefgh".toByteArray()
        server.enqueue(created(chunk = 4))
        server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.DISCONNECT_AFTER_REQUEST))
        server.enqueue(json("""{"uploadId":"up_1","offset":4,"sizeBytes":8}"""))
        server.enqueue(chunkStored(8))
        server.enqueue(json("""{"gameId":"g"}"""))
        val upload = ResumableUpload(file(bytes), 8, sha(bytes), UploadTarget.Rom("s", "a.gba"), retry = RetryPolicy.immediate)
        assertEquals("g", upload.run(client()).gameId)
        val paths = (1..server.requestCount).map { server.takeRequest().let { r -> "${r.method} ${r.path} ${r.getHeader("Upload-Offset") ?: ""}".trim() } }
        assertEquals(
            listOf("POST /api/v1/uploads", "PUT /api/v1/uploads/up_1 0", "GET /api/v1/uploads/up_1", "PUT /api/v1/uploads/up_1 4", "POST /api/v1/uploads/up_1/complete"),
            paths,
        )
    }

    @Test
    fun forgottenUploadIsCreatedAgain() = runBlocking {
        val bytes = "abcd".toByteArray()
        server.enqueue(created(id = "up_1", chunk = 4))
        server.enqueue(error("not_found", 404))
        server.enqueue(created(id = "up_2", chunk = 4))
        server.enqueue(chunkStored(4))
        server.enqueue(json("""{"gameId":"g"}"""))
        val upload = ResumableUpload(file(bytes), 4, sha(bytes), UploadTarget.Rom("s", "a.gba"), retry = RetryPolicy.immediate)
        upload.run(client())
        server.takeRequest(); server.takeRequest(); server.takeRequest()
        assertEquals("/api/v1/uploads/up_2", server.takeRequest().path)
    }

    @Test
    fun lostCompleteAnswerThenForgottenUploadIsAnUnknownOutcome() = runBlocking {
        val bytes = "abcd".toByteArray()
        server.enqueue(created(chunk = 4))
        server.enqueue(chunkStored(4))
        server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.DISCONNECT_AFTER_REQUEST))
        server.enqueue(error("not_found", 404))
        val upload = ResumableUpload(file(bytes), 4, sha(bytes), UploadTarget.Rom("s", "a.gba"), retry = RetryPolicy.immediate)
        try {
            upload.run(client())
            fail("no failure")
        } catch (e: UploadFailure) {
            assertEquals(UploadFailure.Reason.OUTCOME_UNKNOWN, e.reason)
        }
        // Never sent again by itself.
        try {
            upload.run(client())
            fail("sent again")
        } catch (e: UploadFailure) {
            assertEquals(UploadFailure.Reason.OUTCOME_UNKNOWN, e.reason)
        }
        assertEquals(4, server.requestCount)
    }

    @Test
    fun busyCompleteIsSentAgain() = runBlocking {
        val bytes = "abcd".toByteArray()
        server.enqueue(created(chunk = 4))
        server.enqueue(chunkStored(4))
        server.enqueue(error("device_busy", 429).setHeader("Retry-After", "1"))
        server.enqueue(json("""{"uploadId":"up_1","offset":4,"sizeBytes":4}"""))
        server.enqueue(json("""{"gameId":"g"}"""))
        val waits = mutableListOf<Long>()
        val upload = ResumableUpload(
            file(bytes), 4, sha(bytes), UploadTarget.Rom("s", "a.gba"), retry = RetryPolicy(sleep = { waits += it }),
        )
        assertEquals("g", upload.run(client()).gameId)
        assertEquals(listOf(1_000L), waits)
    }

    @Test
    fun cancelIsConfirmedByTheConsole() = runBlocking {
        val bytes = "abcdefgh".toByteArray()
        server.enqueue(created(chunk = 4))
        server.enqueue(error("insufficient_storage", 507))
        server.enqueue(MockResponse().setResponseCode(204))
        val upload = ResumableUpload(file(bytes), 8, sha(bytes), UploadTarget.Rom("s", "a.gba"), retry = RetryPolicy.immediate)
        try {
            upload.run(client()); fail()
        } catch (e: ApiException.Server) {
            assertEquals(ErrorCodes.INSUFFICIENT_STORAGE, e.code)
        }
        assertEquals(ResumableUpload.CancelOutcome.Cancelled, upload.cancel(client()))
        server.takeRequest(); server.takeRequest()
        val cancel = server.takeRequest()
        assertEquals("DELETE", cancel.method)
        assertEquals("/api/v1/uploads/up_1", cancel.path)
        assertNull(upload.uploadId)
    }

    @Test
    fun saveAndStateTargets() {
        val save = Json.parseToJsonElement(CreateUploadRequest(UploadTarget.Save("g1"), 3, "a".repeat(64)).toJson()).jsonObject
        assertEquals("""{"kind":"save","gameId":"g1"}""", save["target"].toString())
        val state = Json.parseToJsonElement(CreateUploadRequest(UploadTarget.State("g1", 2), 3, "a".repeat(64)).toJson()).jsonObject
        assertEquals("""{"kind":"state","gameId":"g1","slot":2}""", state["target"].toString())
    }

    // MARK: signing

    @Test
    fun unsignedIsRefusedOutsideChunksAndPrecomputedHashIsChecked() {
        val signer = Signer(clientId, key)
        try {
            signer.headers("PUT", "/api/v1/games/g/save", unsignedBody = true, time = 1)
            fail("UNSIGNED accepted")
        } catch (e: SignerException) {
            assertEquals(SignerException.Reason.UNSIGNED_BODY, e.reason)
        }
        for (bad in listOf("UNSIGNED", "ABC", "A".repeat(64))) {
            try {
                signer.headers("PUT", "/api/v1/games/g/save", bodySha256 = bad, time = 1)
                fail("$bad accepted")
            } catch (e: SignerException) {
                assertEquals(SignerException.Reason.CONTENT_SHA256, e.reason)
            }
        }
    }

    @Test
    fun replaceSaveSignsTheStreamedFile() = runBlocking {
        val bytes = ByteArray(300_000) { (it % 251).toByte() }
        server.enqueue(json("""{"backup":{"id":"bk_1","label":"Avant remplacement","createdAt":"2026-11-21T10:00:00Z","sizeBytes":3,"reason":"before-replace"}}"""))
        val result = client().replaceSave("g1", file(bytes))
        assertEquals(BackupReasons.BEFORE_REPLACE, result.backup?.reason)
        val request = server.takeRequest()
        assertEquals("PUT", request.method)
        assertEquals("/api/v1/games/g1/save", request.path)
        assertEquals(sha(bytes), request.getHeader("X-RL-Content-SHA256"))
        assertArrayEquals(bytes, request.body.readByteArray())
        assertSigned(request)
    }

    // MARK: saves and states

    @Test
    fun saveRoutes() = runBlocking {
        val backup = """{"id":"bk_2","label":"Before the boss","createdAt":"2026-11-21T10:00:00Z","sizeBytes":8,"reason":"manual"}"""
        server.enqueue(json("""{"backups":[$backup,{"id":"bk_1","label":null,"createdAt":"2026-11-20T10:00:00Z","sizeBytes":8,"reason":"before-future"}]}"""))
        server.enqueue(json(backup, 201))
        server.enqueue(json("""{"backup":null}"""))
        server.enqueue(MockResponse().setResponseCode(204))
        val c = client()
        val list = c.saveBackups("g1")
        assertEquals(2, list.size)
        // An unknown reason is kept, never a failure.
        assertEquals("before-future", list[1].reason)
        assertTrue(list[1].isAutomatic)
        assertEquals("bk_2", c.createSaveBackup("g1", "Before the boss").id)
        assertNull(c.restoreSaveBackup("g1", "bk_2").backup)
        c.deleteSaveBackup("g1", "bk_2")
        assertEquals("/api/v1/games/g1/save/backups", server.takeRequest().path)
        val create = server.takeRequest()
        assertEquals("""{"label":"Before the boss"}""", create.body.readUtf8())
        assertSigned(create)
        assertEquals("/api/v1/games/g1/save/backups/bk_2/restore", server.takeRequest().path)
        val delete = server.takeRequest()
        assertEquals("DELETE", delete.method)
        assertEquals("/api/v1/games/g1/save/backups/bk_2", delete.path)
    }

    @Test
    fun stateRoutes() = runBlocking {
        server.enqueue(
            json(
                """{"slots":[{"slot":0,"sizeBytes":5,"modifiedAt":"2026-11-21T10:00:00Z","hasThumbnail":true,"locked":true,"lockStatus":"intact"},
                {"slot":1,"sizeBytes":5,"modifiedAt":"2026-11-21T10:00:00Z","hasThumbnail":false,"locked":false,"lockStatus":null},
                {"slot":9,"sizeBytes":5,"modifiedAt":"2026-11-21T10:00:00Z","hasThumbnail":false,"locked":false,"lockStatus":null}],"resumeSlot":9}""",
            ),
        )
        server.enqueue(json("""{"slot":1,"sizeBytes":5,"modifiedAt":"2026-11-21T10:00:00Z","hasThumbnail":false,"locked":true,"lockStatus":"intact"}"""))
        server.enqueue(error("state_locked", 409))
        val c = client()
        val list = c.states("g1")
        assertEquals(listOf(0, 1), list.userSlots.map { it.slot })
        assertEquals(9, list.resume?.slot)
        assertTrue(list.slots[0].canRestore && !list.slots[0].canDelete && list.slots[0].matchesLockedCopy)
        val locked = c.lockState("g1", 1)
        assertTrue(locked.locked)
        assertTrue(list.replacing(locked).slots.first { it.slot == 1 }.locked)
        try {
            c.deleteState("g1", 1); fail()
        } catch (e: ApiException.Server) {
            assertEquals(ErrorCodes.STATE_LOCKED, e.code)
        }
        server.takeRequest()
        val lock = server.takeRequest()
        assertEquals("PUT", lock.method)
        assertEquals("/api/v1/games/g1/states/1/lock", lock.path)
        assertEquals("DELETE", server.takeRequest().method)
    }

    @Test
    fun downloadStreamsIntoTheSink() = runBlocking {
        val bytes = ByteArray(200_000) { (it % 7).toByte() }
        server.enqueue(MockResponse().setResponseCode(200).setBody(okio.Buffer().write(bytes)))
        server.enqueue(error("not_found", 404))
        val out = ByteArrayOutputStream()
        var last = 0L to (null as Long?)
        val written = client().download(DownloadSource.GameFile("g1", "Zl9yb20"), out) { w, t -> last = w to t }
        assertEquals(bytes.size.toLong(), written)
        assertArrayEquals(bytes, out.toByteArray())
        assertEquals(bytes.size.toLong() to bytes.size.toLong(), last)
        val request = server.takeRequest()
        assertEquals("/api/v1/games/g1/files/Zl9yb20", request.path)
        assertSigned(request)
        try {
            client().download(DownloadSource.State("g1", 3), ByteArrayOutputStream()); fail()
        } catch (e: ApiException.Server) {
            assertEquals(ErrorCodes.NOT_FOUND, e.code)
        }
        assertEquals("/api/v1/games/g1/states/3", server.takeRequest().path)
    }

    // MARK: queue

    private open class MemorySink(private val deletes: Boolean = true) : DownloadSink {
        val out = ByteArrayOutputStream()
        @Volatile var discarded = false
        @Volatile var opened = false

        override fun open(): OutputStream {
            opened = true
            out.reset()
            return out
        }

        override fun discard(): Boolean {
            discarded = true
            return deletes
        }
    }

    private fun queue(scope: CoroutineScope) = TransferQueue(scope, RetryPolicy.immediate) { _, _ -> client() }

    private suspend fun TransferQueue.waitFor(id: String, predicate: (TransferItem) -> Boolean): TransferItem =
        withTimeout(10_000) { items.first { list -> list.firstOrNull { it.id == id }?.let(predicate) == true } }.first { it.id == id }

    private fun TransferQueue.item(id: String) = items.value.first { it.id == id }

    private fun withScope(block: suspend (CoroutineScope) -> Unit) = runBlocking {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        try {
            block(scope)
        } finally {
            scope.cancel()
        }
    }

    @Test
    fun queueUploadsAndDeletesItsCopy() = withScope { scope ->
        val bytes = "abcdef".toByteArray()
        val dir = temp.newFolder()
        val copy = File(dir, "file").apply { writeBytes(bytes) }
        server.enqueue(created(chunk = 4))
        server.enqueue(chunkStored(4))
        server.enqueue(chunkStored(6))
        server.enqueue(json("""{"gameId":"g"}"""))
        val q = queue(scope)
        val id = q.enqueueUpload(deviceId, copy, "Moss Lantern.gba", UploadTarget.Rom("s", "Moss Lantern.gba"), "Pocket", ownsFile = true)
        val done = q.waitFor(id) { it.phase == TransferPhase.DONE }
        assertEquals("g", done.result?.gameId)
        assertEquals(6L, done.doneBytes)
        withTimeout(5_000) { while (dir.exists()) kotlinx.coroutines.delay(20) }
        withTimeout(5_000) { q.isBusy.first { busy -> !busy } }
    }

    @Test
    fun queuePausesOnLostConnectionAndResumes() = withScope { scope ->
        val bytes = "abcd".toByteArray()
        server.enqueue(created(chunk = 4))
        // Every retry of the run fails (4 delays, then paused).
        repeat(5) { server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.DISCONNECT_AFTER_REQUEST)) }
        val q = queue(scope)
        val id = q.enqueueUpload(deviceId, file(bytes), "a.gba", UploadTarget.Rom("s", "a.gba"), "Pocket", ownsFile = false)
        val paused = q.waitFor(id) { it.phase == TransferPhase.PAUSED }
        assertTrue(paused.failure is ApiException.Network)
        server.enqueue(json("""{"uploadId":"up_1","offset":0,"sizeBytes":4}"""))
        server.enqueue(chunkStored(4))
        server.enqueue(json("""{"gameId":"g"}"""))
        q.resumePaused(deviceId)
        assertEquals("g", q.waitFor(id) { it.phase == TransferPhase.DONE }.result?.gameId)
    }

    @Test
    fun cancelBetweenSelectionAndStartIsHonoured() = withScope { scope ->
        val q = queue(scope)
        val reached = kotlinx.coroutines.CompletableDeferred<Unit>()
        val release = kotlinx.coroutines.CompletableDeferred<Unit>()
        q.beforeStartForTests = { reached.complete(Unit); release.await() }
        val sink = MemorySink()
        val id = q.enqueueDownload(deviceId, DownloadSource.Save("g1"), "a.sav", "A", sink)
        withTimeout(5_000) { reached.await() }
        // Selected and marked started, its job not started yet.
        assertEquals(TransferPhase.RUNNING, q.item(id).phase)
        q.cancel(id)
        release.complete(Unit)
        assertEquals(TransferPhase.CANCELLED, q.waitFor(id) { it.phase == TransferPhase.CANCELLED }.phase)
        assertTrue(sink.discarded)
        assertTrue("the download started after its cancel", !sink.opened)
        assertEquals(0, server.requestCount)
    }

    @Test
    fun pauseBetweenSelectionAndStartIsHonoured() = withScope { scope ->
        val q = queue(scope)
        val reached = kotlinx.coroutines.CompletableDeferred<Unit>()
        val release = kotlinx.coroutines.CompletableDeferred<Unit>()
        q.beforeStartForTests = { reached.complete(Unit); release.await() }
        val id = q.enqueueUpload(deviceId, file("abcd".toByteArray()), "a.gba", UploadTarget.Rom("s", "a.gba"), "P", ownsFile = false)
        withTimeout(5_000) { reached.await() }
        q.pauseAll()
        release.complete(Unit)
        q.waitFor(id) { it.phase == TransferPhase.PAUSED }
        kotlinx.coroutines.delay(300)
        assertEquals(TransferPhase.PAUSED, q.item(id).phase)
        assertEquals(0, server.requestCount)
    }

    @Test
    fun conflictRuleIsReservedUntilApplied() = withScope { scope ->
        val bytes = "abcd".toByteArray()
        server.enqueue(created(id = "up_1", chunk = 4))
        server.enqueue(chunkStored(4))
        server.enqueue(error("conflict", 409, """{"existing":"a.gba"}"""))
        val q = queue(scope)
        val id = q.enqueueUpload(deviceId, file(bytes), "a.gba", UploadTarget.Rom("s", "a.gba"), "Pocket", ownsFile = false)
        assertTrue(q.waitFor(id) { it.phase == TransferPhase.FAILED }.isNameConflict)

        val reached = kotlinx.coroutines.CompletableDeferred<Unit>()
        val release = kotlinx.coroutines.CompletableDeferred<Unit>()
        q.beforeRuleAppliedForTests = { reached.complete(Unit); release.await() }
        q.retry(id, OnConflict.RENAME)
        withTimeout(5_000) { reached.await() }
        // Chosen, not applied yet: nothing else is accepted.
        val reserved = q.item(id)
        assertTrue(reserved.isReserved && !reserved.canResume && !reserved.canCancel && !reserved.isNameConflict)
        q.resume(id)
        q.resumePaused(deviceId)
        q.retry(id, OnConflict.OVERWRITE)
        q.cancel(id)
        kotlinx.coroutines.delay(200)
        assertEquals(TransferPhase.FAILED, q.item(id).phase)
        assertEquals(3, server.requestCount)

        server.enqueue(MockResponse().setResponseCode(204))
        server.enqueue(created(id = "up_2", chunk = 4))
        server.enqueue(chunkStored(4))
        server.enqueue(json("""{"gameId":"g2"}"""))
        release.complete(Unit)
        assertEquals("g2", q.waitFor(id) { it.phase == TransferPhase.DONE }.result?.gameId)
        repeat(3) { server.takeRequest() }
        assertEquals("DELETE /api/v1/uploads/up_1", server.takeRequest().let { "${it.method} ${it.path}" })
        val create = Json.parseToJsonElement(server.takeRequest().body.readUtf8()).jsonObject
        assertEquals("rename", create["onConflict"]!!.jsonPrimitive.content)
        assertEquals(7, server.requestCount)
    }

    @Test
    fun downloadSucceedsOnlyOnceTheDocumentIsCommitted() = withScope { scope ->
        val bytes = ByteArray(1000) { it.toByte() }
        server.enqueue(MockResponse().setResponseCode(200).setBody(okio.Buffer().write(bytes)))
        server.enqueue(MockResponse().setResponseCode(200).setBody(okio.Buffer().write(bytes)))
        val q = queue(scope)
        val sink = MemorySink()
        val id = q.enqueueDownload(deviceId, DownloadSource.Save("g1"), "Moss Lantern.sav", "Moss Lantern", sink)
        assertEquals(1000L, q.waitFor(id) { it.phase == TransferPhase.DONE }.sizeBytes)
        assertArrayEquals(bytes, sink.out.toByteArray())
        assertTrue(!sink.discarded)

        // The provider refuses the document when it is closed: failed, partial document deleted.
        val refusing = object : MemorySink() {
            override fun finish(out: OutputStream) = throw java.io.IOException("commit refused")
        }
        val id2 = q.enqueueDownload(deviceId, DownloadSource.Save("g1"), "Moss Lantern.sav", "Moss Lantern", refusing)
        val failed = q.waitFor(id2) { it.phase == TransferPhase.FAILED || it.phase == TransferPhase.DONE }
        assertEquals(TransferPhase.FAILED, failed.phase)
        // FAILED shows first; the partial document is deleted while the
        // queue stays busy: wait for the end of that cleanup.
        withTimeout(5_000) { q.isBusy.first { busy -> !busy } }
        assertTrue(refusing.discarded)
        assertTrue(!failed.canResume)
    }

    @Test
    fun cancelledDownloadStopsAndDeletesBeforeShowingCancelled() = withScope { scope ->
        val q = queue(scope)
        // A body that would take 100 000 s: only a cancelled call ends it.
        server.enqueue(
            MockResponse().setResponseCode(200).setBody(okio.Buffer().write(ByteArray(100_000)))
                .throttleBody(1, 1, java.util.concurrent.TimeUnit.SECONDS),
        )
        val sink = MemorySink()
        val id = q.enqueueDownload(deviceId, DownloadSource.State("g1", 0), "s.st0", "Moss Lantern", sink)
        // After the headers: the length is known.
        q.waitFor(id) { it.sizeBytes == 100_000L }
        val start = System.nanoTime()
        q.cancel(id)
        assertEquals(TransferPhase.CANCELLING, q.item(id).phase)
        assertTrue(q.isBusy.value)
        val cancelled = q.waitFor(id) { it.phase == TransferPhase.CANCELLED }
        assertTrue("deleted before shown cancelled", sink.discarded)
        assertTrue("the blocked read was not interrupted", System.nanoTime() - start < 5_000_000_000L)
        assertEquals(TransferPhase.CANCELLED, cancelled.phase)
        withTimeout(5_000) { q.isBusy.first { busy -> !busy } }

        // A provider refusing the deletion: reported, not shown cancelled.
        server.enqueue(
            MockResponse().setResponseCode(200).setBody(okio.Buffer().write(ByteArray(100_000)))
                .throttleBody(1, 1, java.util.concurrent.TimeUnit.SECONDS),
        )
        val stuck = MemorySink(deletes = false)
        val id2 = q.enqueueDownload(deviceId, DownloadSource.State("g1", 1), "s.st1", "Moss Lantern", stuck)
        q.waitFor(id2) { it.sizeBytes == 100_000L }
        q.cancel(id2)
        val kept = q.waitFor(id2) { it.phase == TransferPhase.FAILED || it.phase == TransferPhase.CANCELLED }
        assertEquals(TransferPhase.FAILED, kept.phase)
        assertTrue(kept.failure is PartialDocumentException)
    }

    @Test
    fun forgottenConsoleStopsItsTransfers() = withScope { scope ->
        val bytes = "abcdefgh".toByteArray()
        server.enqueue(created(chunk = 4))
        // The first chunk's answer takes a while: the console is forgotten meanwhile.
        server.enqueue(chunkStored(4).setHeadersDelay(2, java.util.concurrent.TimeUnit.SECONDS))
        server.enqueue(chunkStored(8))
        server.enqueue(json("""{"gameId":"g"}"""))
        val q = queue(scope)
        val id = q.enqueueUpload(deviceId, file(bytes), "a.gba", UploadTarget.Rom("s", "a.gba"), "P", ownsFile = false)
        q.waitFor(id) { it.phase == TransferPhase.RUNNING && server.requestCount >= 2 }
        q.dropConsole(deviceId)
        val dropped = q.waitFor(id) { it.phase == TransferPhase.CANCELLED }
        assertTrue(dropped.isDropped && !dropped.canResume)
        kotlinx.coroutines.delay(2_500)
        q.resume(id)
        q.resumePaused(deviceId)
        kotlinx.coroutines.delay(300)
        // Never completed, never sent again.
        assertEquals(2, server.requestCount)
        assertEquals(TransferPhase.CANCELLED, q.item(id).phase)
    }

    @Test
    fun legacyBusyCompleteIsARefusalNotAnUnknownOutcome() = runBlocking {
        val bytes = "abcd".toByteArray()
        server.enqueue(created(id = "up_1", chunk = 4))
        server.enqueue(chunkStored(4))
        // A pak from M13: too_many_attempts with the message "Device busy".
        server.enqueue(json("""{"error":{"code":"too_many_attempts","message":"Device busy","details":{}}}""", 429).setHeader("Retry-After", "1"))
        // Purged before the new attempt.
        server.enqueue(error("not_found", 404))
        server.enqueue(created(id = "up_2", chunk = 4))
        server.enqueue(chunkStored(4))
        server.enqueue(json("""{"gameId":"g"}"""))
        val upload = ResumableUpload(file(bytes), 4, sha(bytes), UploadTarget.Rom("s", "a.gba"), retry = RetryPolicy.immediate)
        assertEquals("g", upload.run(client()).gameId)
        assertEquals(7, server.requestCount)
    }

    // MARK: review round 2

    @Test
    fun demoReleasedBeforeTheChunkEndsLetsTheUploadGoOn() = withScope { scope ->
        server.enqueue(created(chunk = 4))
        server.enqueue(chunkStored(4).setHeadersDelay(800, java.util.concurrent.TimeUnit.MILLISECONDS))
        server.enqueue(json("""{"gameId":"g"}"""))
        val q = queue(scope)
        val id = q.enqueueUpload(deviceId, file("abcd".toByteArray()), "a.gba", UploadTarget.Rom("s", "a.gba"), "P", ownsFile = false)
        server.takeRequest()
        server.takeRequest() // the chunk is out, its answer delayed
        q.holdForDemo { true }
        q.releaseDemoHold()
        assertEquals("g", q.waitFor(id) { it.phase == TransferPhase.DONE || it.phase == TransferPhase.PAUSED }.result?.gameId)
    }

    @Test
    fun theDemoResumesOnlyWhatItPaused() = withScope { scope ->
        server.enqueue(created(chunk = 4))
        repeat(5) { server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.DISCONNECT_AFTER_REQUEST)) }
        val q = queue(scope)
        // Paused before the demo (lost connection, or a console that closed ReminisLink).
        val before = q.enqueueUpload(deviceId, file("abcd".toByteArray()), "a.gba", UploadTarget.Rom("s", "a.gba"), "P", ownsFile = false)
        q.waitFor(before) { it.phase == TransferPhase.PAUSED }
        val requests = server.requestCount
        q.holdForDemo { true }
        // Added during the demo: waits, nothing sent.
        val during = q.enqueueUpload(deviceId, file("efgh".toByteArray()), "b.gba", UploadTarget.Rom("s", "b.gba"), "P", ownsFile = false)
        kotlinx.coroutines.delay(300)
        assertEquals(TransferPhase.PAUSED, q.item(during).phase)
        assertEquals(requests, server.requestCount)
        server.enqueue(created(id = "up_2", chunk = 4))
        server.enqueue(chunkStored(4))
        server.enqueue(json("""{"gameId":"g2"}"""))
        q.releaseDemoHold()
        assertEquals("g2", q.waitFor(during) { it.phase == TransferPhase.DONE }.result?.gameId)
        assertEquals(TransferPhase.PAUSED, q.item(before).phase)
    }

    @Test
    fun pauseRightBeforeCompleteStopsIt() = withScope { scope ->
        val bytes = "abcd".toByteArray()
        server.enqueue(created(chunk = 4))
        server.enqueue(chunkStored(4))
        val q = queue(scope)
        // The service hits its limit after the last chunk, right before the decision to complete.
        var once = true
        q.beforeCompleteDecisionForTests = { if (once) { once = false; q.pauseAll() } }
        val id = q.enqueueUpload(deviceId, file(bytes), "a.gba", UploadTarget.Rom("s", "a.gba"), "P", ownsFile = false)
        q.waitFor(id) { it.phase == TransferPhase.PAUSED }
        kotlinx.coroutines.delay(300)
        assertEquals("complete was sent although the pause won", 2, server.requestCount)
        // Resumed later: the console is asked for the offset, then completes.
        server.enqueue(json("""{"uploadId":"up_1","offset":4,"sizeBytes":4}"""))
        server.enqueue(json("""{"gameId":"g"}"""))
        q.resume(id)
        assertEquals("g", q.waitFor(id) { it.phase == TransferPhase.DONE }.result?.gameId)
    }

    @Test
    fun forgettingAConsoleKeepsAnUnknownOutcome() = withScope { scope ->
        val bytes = "abcd".toByteArray()
        val dir = temp.newFolder()
        val copy = File(dir, "file").apply { writeBytes(bytes) }
        server.enqueue(created(chunk = 4))
        server.enqueue(chunkStored(4))
        // The console installs the file, the answer is lost, then it no longer knows the upload.
        server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.DISCONNECT_AFTER_REQUEST))
        server.enqueue(error("not_found", 404))
        val q = queue(scope)
        val id = q.enqueueUpload(deviceId, copy, "a.gba", UploadTarget.Rom("s", "a.gba"), "P", ownsFile = true)
        val unknown = q.waitFor(id) { it.phase == TransferPhase.FAILED }
        assertEquals(UploadFailure.Reason.OUTCOME_UNKNOWN, (unknown.failure as UploadFailure).reason)
        q.dropConsole(deviceId)
        kotlinx.coroutines.delay(300)
        val kept = q.item(id)
        assertEquals(TransferPhase.FAILED, kept.phase)
        assertEquals(UploadFailure.Reason.OUTCOME_UNKNOWN, (kept.failure as UploadFailure).reason)
        assertTrue("the local copy was deleted", copy.exists())
    }

    @Test
    fun forgettingAConsoleWithCompleteUnansweredIsUnknownNotCancelled() = withScope { scope ->
        val bytes = "abcd".toByteArray()
        server.enqueue(created(chunk = 4))
        server.enqueue(chunkStored(4))
        // `complete` sent, no answer, and the console unreachable: paused with the doubt.
        repeat(5) { server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.DISCONNECT_AFTER_REQUEST)) }
        val q = queue(scope)
        val id = q.enqueueUpload(deviceId, file(bytes), "a.gba", UploadTarget.Rom("s", "a.gba"), "P", ownsFile = false)
        q.waitFor(id) { it.phase == TransferPhase.PAUSED }
        q.dropConsole(deviceId)
        val dropped = q.item(id)
        assertEquals(TransferPhase.FAILED, dropped.phase)
        assertEquals(UploadFailure.Reason.OUTCOME_UNKNOWN, (dropped.failure as UploadFailure).reason)
    }

    /** A sink whose deletion waits for the test. */
    private open class BlockingDiscardSink(private val deletes: Boolean) : MemorySink(deletes) {
        val entered = java.util.concurrent.CountDownLatch(1)
        val release = java.util.concurrent.CountDownLatch(1)

        override fun discard(): Boolean {
            entered.countDown()
            release.await(10, java.util.concurrent.TimeUnit.SECONDS)
            return super.discard()
        }
    }

    @Test
    fun cancelledDownloadIsBusyUntilItsDocumentIsDeleted() = withScope { scope ->
        val q = queue(scope)
        server.enqueue(
            MockResponse().setResponseCode(200).setBody(okio.Buffer().write(ByteArray(100_000)))
                .throttleBody(1, 1, java.util.concurrent.TimeUnit.SECONDS),
        )
        val sink = BlockingDiscardSink(deletes = true)
        val id = q.enqueueDownload(deviceId, DownloadSource.State("g1", 0), "s.st0", "M", sink)
        q.waitFor(id) { it.sizeBytes == 100_000L }
        q.cancel(id)
        assertTrue(sink.entered.await(5, java.util.concurrent.TimeUnit.SECONDS))
        // Deleting: still cancelling, still busy (the service stays).
        assertEquals(TransferPhase.CANCELLING, q.item(id).phase)
        assertTrue(q.isBusy.value)
        assertTrue(!q.stopIfIdle { throw AssertionError("stopped during cleanup") })
        sink.release.countDown()
        q.waitFor(id) { it.phase == TransferPhase.CANCELLED }
        withTimeout(5_000) { q.isBusy.first { busy -> !busy } }
        var stopped = false
        assertTrue(q.stopIfIdle { stopped = true } && stopped)
    }

    @Test
    fun failedDownloadIsBusyUntilDeletedAndReportsARefusedDeletion() = withScope { scope ->
        val q = queue(scope)
        server.enqueue(MockResponse().setResponseCode(200).setBody("abc"))
        val sink = object : BlockingDiscardSink(deletes = false) {
            override fun finish(out: OutputStream) = throw java.io.IOException("commit refused")
        }
        val id = q.enqueueDownload(deviceId, DownloadSource.Save("g1"), "a.sav", "M", sink)
        assertTrue(sink.entered.await(5, java.util.concurrent.TimeUnit.SECONDS))
        assertEquals(TransferPhase.FAILED, q.item(id).phase)
        assertTrue(q.isBusy.value)
        // Not cleared while its document is being deleted.
        q.clearFinished(deviceId)
        assertTrue(q.items.value.any { it.id == id })
        sink.release.countDown()
        val failed = q.waitFor(id) { it.failure is PartialDocumentException }
        assertTrue(failed.failure?.cause is java.io.IOException)
        withTimeout(5_000) { q.isBusy.first { busy -> !busy } }
    }

    @Test
    fun cancelWhileOpeningTheDocumentClosesItsStream() = withScope { scope ->
        val q = queue(scope)
        val opening = java.util.concurrent.CountDownLatch(1)
        val release = java.util.concurrent.CountDownLatch(1)
        val closed = java.util.concurrent.atomic.AtomicBoolean(false)
        val closedBeforeDiscard = java.util.concurrent.atomic.AtomicBoolean(false)
        val sink = object : DownloadSink {
            override fun open(): OutputStream {
                opening.countDown()
                release.await(10, java.util.concurrent.TimeUnit.SECONDS)
                return object : ByteArrayOutputStream() {
                    override fun close() {
                        closed.set(true)
                    }
                }
            }

            override fun discard(): Boolean {
                closedBeforeDiscard.set(closed.get())
                return true
            }
        }
        val id = q.enqueueDownload(deviceId, DownloadSource.Save("g1"), "a.sav", "M", sink)
        assertTrue(opening.await(5, java.util.concurrent.TimeUnit.SECONDS))
        q.cancel(id)
        release.countDown()
        q.waitFor(id) { it.phase == TransferPhase.CANCELLED }
        assertTrue("the stream returned after the cancel was not closed before deletion", closedBeforeDiscard.get())
        assertEquals(0, server.requestCount)
    }

    // MARK: final review

    @Test
    fun forgettingDuringTheWaitAfterALostCompleteIsAnUnknownOutcome() = withScope { scope ->
        val bytes = "abcd".toByteArray()
        server.enqueue(created(chunk = 4))
        server.enqueue(chunkStored(4))
        // `complete` is received (the console may install the file), its answer is lost,
        // and the console stays unreachable.
        repeat(5) { server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.DISCONNECT_AFTER_REQUEST)) }
        val waiting = kotlinx.coroutines.CompletableDeferred<Unit>()
        val release = kotlinx.coroutines.CompletableDeferred<Unit>()
        var first = true
        val retry = RetryPolicy(sleep = {
            if (first) {
                first = false
                waiting.complete(Unit)
                release.await()
            }
        })
        val q = TransferQueue(scope, retry) { _, _ -> client() }
        val id = q.enqueueUpload(deviceId, file(bytes), "a.gba", UploadTarget.Rom("s", "a.gba"), "P", ownsFile = false)
        withTimeout(5_000) { waiting.await() }
        // Forgotten during the wait before the next attempt, `complete` still in doubt.
        assertTrue(q.item(id).isCompleting)
        q.dropConsole(deviceId)
        release.complete(Unit)
        val end = q.waitFor(id) { it.phase == TransferPhase.FAILED }
        assertEquals(UploadFailure.Reason.OUTCOME_UNKNOWN, (end.failure as? UploadFailure)?.reason)
    }

    @Test
    fun pauseWhileReplaceIsAppliedLandsPaused() = withScope { scope ->
        val bytes = "abcd".toByteArray()
        server.enqueue(created(id = "up_1", chunk = 4))
        server.enqueue(chunkStored(4))
        server.enqueue(error("conflict", 409, """{"existing":"a.gba"}"""))
        val q = queue(scope)
        val id = q.enqueueUpload(deviceId, file(bytes), "a.gba", UploadTarget.Rom("s", "a.gba"), "Pocket", ownsFile = false)
        q.waitFor(id) { it.phase == TransferPhase.FAILED }
        // The DELETE of the old upload is slow; the service hits its limit meanwhile.
        server.enqueue(MockResponse().setResponseCode(204).setHeadersDelay(1, java.util.concurrent.TimeUnit.SECONDS))
        q.retry(id, OnConflict.OVERWRITE)
        withTimeout(5_000) { while (server.requestCount < 4) kotlinx.coroutines.delay(10) }
        q.pauseAll()
        val paused = q.waitFor(id) { it.phase == TransferPhase.PAUSED && !it.isReserved }
        assertTrue(paused.canResume)
        kotlinx.coroutines.delay(500)
        // Nothing restarted without a new resume.
        assertEquals(4, server.requestCount)
        assertEquals(TransferPhase.PAUSED, q.item(id).phase)
        // Resumed: the new rule is used.
        server.enqueue(created(id = "up_2", chunk = 4))
        server.enqueue(chunkStored(4))
        server.enqueue(json("""{"gameId":"g2"}"""))
        q.resume(id)
        assertEquals("g2", q.waitFor(id) { it.phase == TransferPhase.DONE }.result?.gameId)
        repeat(4) { server.takeRequest() }
        val create = Json.parseToJsonElement(server.takeRequest().body.readUtf8()).jsonObject
        assertEquals("overwrite", create["onConflict"]!!.jsonPrimitive.content)
    }

    /** A document whose provider stopped reading: a write blocks until the stream is closed (or forever). */
    private class StuckSink(private val closeUnblocks: Boolean) : DownloadSink {
        val writing = java.util.concurrent.CountDownLatch(1)
        val unblock = java.util.concurrent.CountDownLatch(1)
        @Volatile var discarded = false

        override fun open(): OutputStream = object : OutputStream() {
            override fun write(b: Int) = write(byteArrayOf(b.toByte()), 0, 1)

            override fun write(b: ByteArray, off: Int, len: Int) {
                writing.countDown()
                unblock.await(30, java.util.concurrent.TimeUnit.SECONDS)
                if (closeUnblocks) throw java.io.IOException("closed")
            }

            override fun close() {
                if (closeUnblocks) unblock.countDown()
            }
        }

        override fun discard(): Boolean {
            discarded = true
            return true
        }
    }

    @Test
    fun cancelUnblocksAStuckWriteAndTheQueueGoesOn() = withScope { scope ->
        val q = queue(scope)
        server.enqueue(MockResponse().setResponseCode(200).setBody("abcdef"))
        server.enqueue(MockResponse().setResponseCode(200).setBody("next"))
        val stuck = StuckSink(closeUnblocks = true)
        val id = q.enqueueDownload(deviceId, DownloadSource.Save("g1"), "a.sav", "M", stuck)
        assertTrue(stuck.writing.await(5, java.util.concurrent.TimeUnit.SECONDS))
        val next = MemorySink()
        val id2 = q.enqueueDownload(deviceId, DownloadSource.Save("g2"), "b.sav", "M", next)
        q.cancel(id)
        q.waitFor(id) { it.phase == TransferPhase.CANCELLED }
        assertTrue(stuck.discarded)
        q.waitFor(id2) { it.phase == TransferPhase.DONE }
        assertArrayEquals("next".toByteArray(), next.out.toByteArray())
    }

    @Test
    fun aDownloadThatNeverStopsIsAbandonedAfterItsCancel() = withScope { scope ->
        val q = queue(scope)
        q.stopTimeoutMillis = 300
        server.enqueue(MockResponse().setResponseCode(200).setBody("abcdef"))
        server.enqueue(MockResponse().setResponseCode(200).setBody("next"))
        val stuck = StuckSink(closeUnblocks = false)
        try {
            val id = q.enqueueDownload(deviceId, DownloadSource.Save("g1"), "a.sav", "M", stuck)
            assertTrue(stuck.writing.await(5, java.util.concurrent.TimeUnit.SECONDS))
            val next = MemorySink()
            val id2 = q.enqueueDownload(deviceId, DownloadSource.Save("g2"), "b.sav", "M", next)
            q.cancel(id)
            q.waitFor(id) { it.phase == TransferPhase.CANCELLED }
            // The stuck one no longer holds the downloads behind it.
            q.waitFor(id2) { it.phase == TransferPhase.DONE }
        } finally {
            stuck.unblock.countDown()
        }
    }

    // MARK: small pieces

    @Test
    fun backupLabelAndSuggestions() {
        assertNull(BackupLabel.normalized("   "))
        val long = "🌱".repeat(100)
        val cut = BackupLabel.limited(long)
        assertEquals(80, cut.codePointCount(0, cut.length))
        assertEquals(AddKind.SAVE, AddKind.suggested("Moss Lantern.SAV"))
        assertEquals(AddKind.ROM, AddKind.suggested("Moss Lantern.gba"))
        val systems = listOf(
            GameSystem("a", "Handheld Advance", "Handheld Advance (MGBA)", "MGBA", 0),
            GameSystem("b", "Handheld Advance", "Handheld Advance (GBA)", "GBA", 0),
        )
        assertEquals("b", SystemSuggestion.system("x.gba", systems)?.id)
        assertNull(SystemSuggestion.system("x.zzz", systems))
    }
}
