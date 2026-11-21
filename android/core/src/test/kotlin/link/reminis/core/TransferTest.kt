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

    private class MemorySink : DownloadSink {
        val out = ByteArrayOutputStream()
        var discarded = false

        override fun open(): OutputStream {
            out.reset()
            return out
        }

        override fun discard() {
            discarded = true
        }
    }

    private fun queue(scope: CoroutineScope) = TransferQueue(scope, RetryPolicy.immediate) { _, _ -> client() }

    private suspend fun TransferQueue.waitFor(id: String, predicate: (TransferItem) -> Boolean): TransferItem =
        withTimeout(10_000) { items.first { list -> list.firstOrNull { it.id == id }?.let(predicate) == true } }.first { it.id == id }

    @Test
    fun queueUploadsAndDeletesItsCopy() = runBlocking {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        try {
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
            assertTrue(!q.isBusy.value)
        } finally {
            scope.cancel()
        }
    }

    @Test
    fun queuePausesOnLostConnectionAndResumes() = runBlocking {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        try {
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
        } finally {
            scope.cancel()
        }
    }

    @Test
    fun queueRetriesANameConflictWithAnotherRule() = runBlocking {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        try {
            val bytes = "abcd".toByteArray()
            server.enqueue(created(id = "up_1", chunk = 4))
            server.enqueue(chunkStored(4))
            server.enqueue(error("conflict", 409, """{"existing":"a.gba"}"""))
            val q = queue(scope)
            val id = q.enqueueUpload(deviceId, file(bytes), "a.gba", UploadTarget.Rom("s", "a.gba"), "Pocket", ownsFile = false)
            assertTrue(q.waitFor(id) { it.phase == TransferPhase.FAILED }.isNameConflict)
            server.enqueue(MockResponse().setResponseCode(204))
            server.enqueue(created(id = "up_2", chunk = 4))
            server.enqueue(chunkStored(4))
            server.enqueue(json("""{"gameId":"g2"}"""))
            q.retry(id, OnConflict.RENAME)
            assertEquals("g2", q.waitFor(id) { it.phase == TransferPhase.DONE }.result?.gameId)
            repeat(3) { server.takeRequest() }
            assertEquals("DELETE /api/v1/uploads/up_1", server.takeRequest().let { "${it.method} ${it.path}" })
            val create = Json.parseToJsonElement(server.takeRequest().body.readUtf8()).jsonObject
            assertEquals("rename", create["onConflict"]!!.jsonPrimitive.content)
        } finally {
            scope.cancel()
        }
    }

    @Test
    fun queueDownloadsAndCancelDiscards() = runBlocking {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        try {
            val bytes = ByteArray(1000) { it.toByte() }
            server.enqueue(MockResponse().setResponseCode(200).setBody(okio.Buffer().write(bytes)))
            val q = queue(scope)
            val sink = MemorySink()
            val id = q.enqueueDownload(deviceId, DownloadSource.Save("g1"), "Moss Lantern.sav", "Moss Lantern", sink)
            assertEquals(1000L, q.waitFor(id) { it.phase == TransferPhase.DONE }.sizeBytes)
            assertArrayEquals(bytes, sink.out.toByteArray())

            // A download that never ends, cancelled: its document is removed.
            server.enqueue(MockResponse().setResponseCode(200).setHeader("Content-Length", "100000").setBody("ab").throttleBody(1, 10, java.util.concurrent.TimeUnit.SECONDS))
            val slow = MemorySink()
            val id2 = q.enqueueDownload(deviceId, DownloadSource.State("g1", 0), "s.st0", "Moss Lantern", slow)
            q.waitFor(id2) { it.phase == TransferPhase.RUNNING }
            q.cancel(id2)
            assertEquals(TransferPhase.CANCELLED, q.waitFor(id2) { it.phase == TransferPhase.CANCELLED }.phase)
            withTimeout(5_000) { while (!slow.discarded) kotlinx.coroutines.delay(20) }
        } finally {
            scope.cancel()
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
