package link.reminis.core

import java.io.File
import java.security.MessageDigest
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.asCoroutineDispatcher
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import okhttp3.tls.HandshakeCertificates
import okhttp3.tls.HeldCertificate
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

/**
 * Increment 3 against a local TLS server (pinned, signed): cheats,
 * patches and their jobs, paks and their installation, the trash and its
 * restore (M18). Names and bytes are invented.
 */
class FeatureTest {
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

    private fun error(code: String, status: Int, details: String = "{}", message: String = "x") =
        json("""{"error":{"code":"$code","message":"$message","details":$details}}""", status)

    private fun job(id: String, state: String, result: String = "null", error: String = "null", cancelRequested: Boolean = false) = json(
        """{"id":"$id","kind":"patch","state":"$state","progress":{"done":5,"total":10},"createdAt":"2026-11-22T10:00:00Z",""" +
            """"updatedAt":"2026-11-22T10:00:01Z","result":$result,"error":$error,"cancelRequested":$cancelRequested}""",
    )

    private fun body(request: RecordedRequest): JsonObject = Json.parseToJsonElement(request.body.readUtf8()).jsonObject

    private fun assertSigned(request: RecordedRequest) {
        val canonical = Signer.canonicalRequest(
            request.method!!, request.requestUrl!!.encodedPath, request.requestUrl!!.encodedQuery ?: "",
            request.getHeader("X-RL-Time")!!, request.getHeader("X-RL-Nonce")!!, request.getHeader("X-RL-Content-SHA256")!!,
        )
        assertTrue(Signer.isValidSignature(request.getHeader("X-RL-Signature")!!, key.publicKey, canonical))
    }

    private fun sha(bytes: ByteArray) = MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") { "%02x".format(it) }

    private fun file(bytes: ByteArray): File = temp.newFile().apply { writeBytes(bytes) }

    // MARK: cheats

    @Test
    fun cheatsAreReadAndSavedWithIfMatchKeepingExtraKeys() = runBlocking {
        server.enqueue(
            json("""{"cheats":[{"index":0,"description":"Lantern oil","code":"8202+FFFF","enabled":false,"extra":{"handler":"0"}}],"etag":"\"sha256-aa\""}"""),
        )
        server.enqueue(json("""{"cheats":[{"index":0,"description":"Lantern oil","code":"8202+FFFF","enabled":true,"extra":{"handler":"0"}},{"index":1,"description":"Moss","code":"01","enabled":true}],"etag":"\"sha256-bb\""}"""))
        val c = client()
        val list = c.cheats("R0FNRQ")
        assertEquals(mapOf("handler" to "0"), list.cheats[0].extra)
        val editor = CheatEditor(list)
        assertFalse(editor.hasChanges)
        editor.setEnabled(editor.entries[0].id, true)
        assertFalse("a code with only spaces is refused", editor.add("Moss", "   ", true))
        assertTrue(editor.add("  Moss\n", "01", true))
        assertTrue(editor.hasChanges)
        val saved = c.replaceCheats("R0FNRQ", editor.update(), editor.etag)
        editor.load(saved)
        assertFalse(editor.hasChanges)
        assertEquals("\"sha256-bb\"", editor.etag)

        server.takeRequest()
        val put = server.takeRequest()
        assertEquals("PUT", put.method)
        assertEquals("/api/v1/games/R0FNRQ/cheats", put.path)
        assertEquals("\"sha256-aa\"", put.getHeader("If-Match"))
        assertSigned(put)
        val cheats = body(put)["cheats"]!!.jsonArray
        assertEquals(2, cheats.size)
        assertEquals("1", cheats[1].jsonObject["index"]!!.jsonPrimitive.content)
        assertEquals("Moss", cheats[1].jsonObject["description"]!!.jsonPrimitive.content)
        assertEquals("0", cheats[0].jsonObject["extra"]!!.jsonObject["handler"]!!.jsonPrimitive.content)
    }

    @Test
    fun aCheatListChangedOnTheConsoleIsAConflict() = runBlocking {
        server.enqueue(error("conflict", 412))
        val e = runCatching { client().replaceCheats("g", CheatsUpdate(emptyList()), "\"old\"") }.exceptionOrNull()
        assertTrue(CheatEditor.isConflict(e!!))
        assertFalse(CheatEditor.isConflict(ApiException.Server(409, "conflict", "x")))
    }

    @Test
    fun cheatEditorEditsMovesRemovesAndDiscards() {
        val editor = CheatEditor(CheatList(listOf(Cheat(0, "A", "1", true), Cheat(1, "B", "2", false)), "e"))
        val (a, b) = editor.entries
        assertTrue(editor.update(b.id, "B2", "22", true))
        editor.move(b.id, -1)
        assertEquals(listOf("B2", "A"), editor.cheats.map { it.description })
        assertEquals(listOf(0, 1), editor.update().cheats.map { it.index })
        editor.remove(a.id)
        assertEquals(1, editor.cheats.size)
        assertFalse("an unknown id is not updated", editor.update("nope", "x", "y", true))
        editor.discardChanges()
        assertEquals(listOf("A", "B"), editor.cheats.map { it.description })
        assertFalse(editor.hasChanges)
        assertNull(CheatEditor.validated("", "1"))
        assertEquals("a b" to "c", CheatEditor.validated("a\nb", " c "))
    }

    @Test
    fun cheatsImportFromStagingAndSdFiles() = runBlocking {
        server.enqueue(json("""{"files":[{"id":"sd_1","path":"Cheats/GBA/Other.cht","sizeBytes":3},{"id":"sd_2","path":"Cheats/GBA/Moss Lantern.gba.cht","sizeBytes":4}]}"""))
        server.enqueue(json("""{"imported":3,"total":5}"""))
        val c = client()
        val files = SdFiles.sortedForGame(c.sdFiles(SdFileType.CHT), "Moss Lantern.gba", "Moss Lantern")
        assertEquals(listOf("sd_2", "sd_1"), files.map { it.id })
        val result = c.importCheats("g", CheatImportRequest(CheatImportSource.Staging("st_1"), CheatImportMode.REPLACE))
        assertEquals(3, result.imported)
        assertEquals("/api/v1/sd/files?type=cht", server.takeRequest().path)
        val import = body(server.takeRequest())
        assertEquals("staging", import["source"]!!.jsonObject["type"]!!.jsonPrimitive.content)
        assertEquals("st_1", import["source"]!!.jsonObject["stagingId"]!!.jsonPrimitive.content)
        assertEquals("replace", import["mode"]!!.jsonPrimitive.content)
    }

    // MARK: patches

    @Test
    fun patchNamesAndRefusals() {
        assertEquals("Moss (patched).gba", PatchNaming.suggestedFileName("Moss.gba"))
        assertEquals("Moss (patched)", PatchNaming.suggestedFileName("Moss"))
        assertEquals("Hack v1.2.gba", PatchNaming.outputName("Hack v1.2", "Moss.gba"))
        assertEquals("Hack.zip.gba", PatchNaming.outputName("Hack.zip", "Moss.gba"))
        assertEquals("Hack.GBA", PatchNaming.outputName("Hack.GBA", "Moss.gba"))
        assertNull(PatchNaming.outputName("a/b", "Moss.gba"))
        assertNull(PatchNaming.outputName(".hidden", "Moss.gba"))
        assertNull(PatchNaming.outputName("CON", "Moss.gba"))
        assertNull(PatchNaming.outputName("a​b", "Moss.gba"))
        assertEquals(PatchRefusal.MULTI_DISC, PatchNaming.refusal("m3u", "Moss.m3u"))
        assertEquals(PatchRefusal.ARCHIVE, PatchNaming.refusal("file", "Moss.zip"))
        assertNull(PatchNaming.refusal("file", "Moss.gba"))
        assertTrue(PatchNaming.isPatchFileName("fix.BPS"))
        assertFalse(PatchNaming.isPatchFileName("fix.txt"))
        val request = PatchRequest(PatchSource.Sd("sd_9"), PatchOutputMode.REPLACE, "ignored.gba")
        val output = Json.parseToJsonElement(request.toJson()).jsonObject["output"]!!.jsonObject
        assertEquals("replace", output["mode"]!!.jsonPrimitive.content)
        assertNull("no fileName for replace", output["fileName"])
    }

    private fun <T> withScope(block: suspend (CoroutineScope) -> T): T = runBlocking {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        try {
            block(scope)
        } finally {
            scope.cancel()
        }
    }

    private suspend fun PatchRun.waitFor(predicate: (PatchStep) -> Boolean): PatchStep =
        withTimeout(10_000) { step.first(predicate) }

    @Test
    fun patchFromAFileIsStagedThenAppliedAndFollowed() = withScope { scope ->
        val bytes = "PATCH-moss".toByteArray()
        server.enqueue(json("""{"uploadId":"up_1","chunkSize":64,"offset":0,"expiresAt":"2026-11-25T17:00:00Z"}""", 201))
        server.enqueue(MockResponse().setResponseCode(204).setHeader("Upload-Offset", bytes.size))
        server.enqueue(json("""{"stagingId":"st_7"}"""))
        server.enqueue(json("""{"jobId":"job_1"}""", 202))
        server.enqueue(job("job_1", "running"))
        server.enqueue(job("job_1", "succeeded", result = """{"gameId":"TmV3","verified":false}"""))
        val jobs = JobTracker { client() }
        var released: PatchInput.LocalFile? = null
        val run = PatchRun(deviceId, scope, jobs, { client() }, RetryPolicy.immediate, onLocalCopyDone = { released = it })
        val input = PatchInput.LocalFile(file(bytes), "fix.ips", "a")
        run.start("R0FNRQ", input, PatchOutputMode.NEW_FILE, "Moss (FR).gba", "Moss")
        run.waitFor { it == PatchStep.Patching }
        assertEquals(input, released)
        run.tick()
        assertEquals(PatchStep.Patching, run.step.value)
        run.tick()
        assertEquals(PatchStep.Succeeded("TmV3", late = false, verified = false), run.step.value)

        val create = body(server.takeRequest())
        assertEquals("staging", create["target"]!!.jsonObject["kind"]!!.jsonPrimitive.content)
        assertEquals("fix.ips", create["target"]!!.jsonObject["fileName"]!!.jsonPrimitive.content)
        assertEquals("rename", create["onConflict"]!!.jsonPrimitive.content)
        assertEquals(sha(bytes), create["sha256"]!!.jsonPrimitive.content)
        val chunk = server.takeRequest()
        assertEquals("UNSIGNED", chunk.getHeader("X-RL-Content-SHA256"))
        server.takeRequest()
        val patch = server.takeRequest()
        assertEquals("/api/v1/games/R0FNRQ/patch", patch.path)
        assertSigned(patch)
        val p = body(patch)
        assertEquals("st_7", p["patch"]!!.jsonObject["stagingId"]!!.jsonPrimitive.content)
        assertEquals("new-file", p["output"]!!.jsonObject["mode"]!!.jsonPrimitive.content)
        assertEquals("Moss (FR).gba", p["output"]!!.jsonObject["fileName"]!!.jsonPrimitive.content)
        assertEquals("/api/v1/jobs/job_1", server.takeRequest().path)
        assertEquals(JobKinds.PATCH, jobs.jobs.value.single().kind)
    }

    @Test
    fun aFailedPatchJobCarriesTheMismatchDetails() = withScope { scope ->
        server.enqueue(json("""{"jobId":"job_2"}""", 202))
        server.enqueue(
            job(
                "job_2", "failed",
                error = """{"code":"patch_mismatch","message":"x","details":{"reason":"source-mismatch","expectedSize":16,"actualSize":8,"expectedCrc32":"0badc0de"}}""",
            ),
        )
        val run = PatchRun(deviceId, scope, JobTracker { client() }, { client() }, RetryPolicy.immediate)
        run.start("g", PatchInput.SdCard("sd_3"), PatchOutputMode.REPLACE, null, "Moss")
        run.waitFor { it == PatchStep.Patching }
        run.tick()
        val failed = run.step.value as PatchStep.Failed
        val server = (failed.error as PatchJobFailure).server
        assertEquals("source-mismatch", server.patchMismatchReason)
        assertEquals(PatchMismatchDetails(16, 8, "0badc0de", null), failed.mismatch)
        val body = body(this.server.takeRequest())
        assertEquals("sd", body["patch"]!!.jsonObject["type"]!!.jsonPrimitive.content)
        assertEquals("sd_3", body["patch"]!!.jsonObject["sdFileId"]!!.jsonPrimitive.content)
    }

    @Test
    fun aCancelledPatchJobEndsAsTheConsoleReportsIt() = withScope { scope ->
        server.enqueue(json("""{"jobId":"job_3"}""", 202))
        server.enqueue(job("job_3", "running", cancelRequested = true))
        server.enqueue(job("job_3", "cancelled", cancelRequested = true))
        val run = PatchRun(deviceId, scope, JobTracker { client() }, { client() }, RetryPolicy.immediate, cancelPollMillis = 1)
        run.start("g", PatchInput.SdCard("sd_3"), PatchOutputMode.NEW_FILE, "x.gba", "Moss")
        run.waitFor { it == PatchStep.Patching }
        run.cancel()
        run.waitFor { it == PatchStep.Cancelled }
        server.takeRequest()
        val delete = server.takeRequest()
        assertEquals("DELETE", delete.method)
        assertEquals("/api/v1/jobs/job_3", delete.path)
    }

    @Test
    fun aJobTheConsoleForgotIsLost() = runBlocking {
        server.enqueue(error("not_found", 404))
        val jobs = JobTracker { client() }
        jobs.track(deviceId, "job_9", "Moss", JobKinds.PATCH)
        jobs.refresh(deviceId)
        assertTrue(jobs.get(deviceId, "job_9")!!.lost)
        jobs.refresh(deviceId)
        assertEquals("a lost job is not polled again", 1, server.requestCount)
        jobs.clearFinished(deviceId)
        assertTrue("cleared rows are hidden, still known to their owners", jobs.jobs.value.single().hidden)
    }

    // MARK: paks

    @Test
    fun paksAreGroupedAndRefusalsRecognized() = runBlocking {
        server.enqueue(
            json(
                """{"paks":[{"pakId":"pk_2","name":"MGBA","kind":"emu","path":"Emus/tg5040/MGBA.pak","sizeBytes":4096,"modifiedAt":"2026-11-18T17:00:00Z","version":null,"self":false},""" +
                    """{"pakId":"pk_1","name":"ReminisLink","kind":"tool","path":"Tools/tg5040/ReminisLink.pak","sizeBytes":9,"modifiedAt":"2026-11-20T08:00:00Z","version":"v1.0.0","self":true},""" +
                    """{"pakId":"pk_3","name":"clock","kind":"tool","modifiedAt":"2026-11-20T08:00:00Z"}]}""",
            ),
        )
        server.enqueue(error("conflict", 409, """{"self":true}"""))
        val c = client()
        val paks = c.paks()
        val groups = PakList.grouped(paks)
        assertEquals(listOf(PakKinds.TOOL, PakKinds.EMU), groups.map { it.first })
        assertEquals(listOf("clock", "ReminisLink"), groups[0].second.map { it.name })
        assertTrue(paks.first { it.pakId == "pk_1" }.isSelf)
        val e = runCatching { c.deletePak("pk_1") }.exceptionOrNull() as ApiException.Server
        assertTrue(e.isPakSelfConflict)
        assertNull(e.conflictingPakName)
        assertEquals("Clock", ApiException.Server(409, "conflict", "x", mapOf("existing" to kotlinx.serialization.json.JsonPrimitive("Tools/tg5040/Clock.pak"))).conflictingPakName)
        assertEquals(PakRefusal.NoLaunchScript, PakRefusal.from("Invalid pak: no launch.sh in the pak folder"))
        assertEquals(PakRefusal.UnsafeEntry, PakRefusal.from("Invalid pak: symbolic link Clock.pak/x"))
        assertEquals(PakRefusal.Other("something new"), PakRefusal.from("Invalid pak: something new"))
    }

    @Test
    fun aPakZipIsUploadedThenReplacedAfterAConflict() = withScope { scope ->
        val bytes = "PK-fake-zip".toByteArray()
        // First attempt: upload, `complete` gives a job that fails on a pak of the same name.
        server.enqueue(json("""{"uploadId":"up_1","chunkSize":64,"offset":0,"expiresAt":"2026-11-25T17:00:00Z"}""", 201))
        server.enqueue(MockResponse().setResponseCode(204).setHeader("Upload-Offset", bytes.size))
        server.enqueue(json("""{"jobId":"job_a"}"""))
        server.enqueue(job("job_a", "failed", error = """{"code":"conflict","message":"exists","details":{"existing":"Tools/tg5040/Clock.pak"}}"""))
        // Replacement: the same file with `overwrite`.
        server.enqueue(json("""{"uploadId":"up_2","chunkSize":64,"offset":0,"expiresAt":"2026-11-25T17:00:00Z"}""", 201))
        server.enqueue(MockResponse().setResponseCode(204).setHeader("Upload-Offset", bytes.size))
        server.enqueue(json("""{"jobId":"job_b"}"""))
        server.enqueue(job("job_b", "succeeded", result = """{"pakId":"pk_c"}"""))
        val queue = TransferQueue(scope, RetryPolicy.immediate) { _, _ -> client() }
        val jobs = JobTracker { client() }
        val done = mutableListOf<File>()
        val installer = PakInstaller(scope, queue, jobs) { done += it }
        val zip = file(bytes)
        val id = installer.install(deviceId, zip, "Clock.pak.zip", PakInstallKind.TOOL)
        suspend fun waitPhase(p: (PakInstall.Phase) -> Boolean) =
            withTimeout(10_000) { installer.installs.first { list -> list.first { it.id == id }.phase.let(p) } }
        waitPhase { it is PakInstall.Phase.Installing }
        jobs.refresh(deviceId)
        waitPhase { it == PakInstall.Phase.NeedsReplacement("Clock") }
        assertTrue("the copy is kept for the replacement", done.isEmpty())
        installer.replace(id)
        waitPhase { it is PakInstall.Phase.Installing }
        jobs.refresh(deviceId)
        waitPhase { it == PakInstall.Phase.Installed }
        assertEquals(listOf(zip), done)

        val first = body(server.takeRequest())
        assertEquals("pak", first["target"]!!.jsonObject["kind"]!!.jsonPrimitive.content)
        assertEquals("tool", first["target"]!!.jsonObject["pakKind"]!!.jsonPrimitive.content)
        assertEquals("Clock.pak.zip", first["target"]!!.jsonObject["fileName"]!!.jsonPrimitive.content)
        assertEquals("fail", first["onConflict"]!!.jsonPrimitive.content)
        repeat(3) { server.takeRequest() }
        val second = body(server.takeRequest())
        assertEquals("overwrite", second["onConflict"]!!.jsonPrimitive.content)
    }

    // MARK: trash

    @Test
    fun trashIsListedAndEmptiedWithItsConfirmationHeader() = runBlocking {
        server.enqueue(
            json(
                """{"entries":[{"id":"tr_1","trashedAt":"2026-11-18T17:00:00Z","reason":"delete-game","label":"Moss Lantern",""" +
                    """"files":["Roms/GBA/Moss Lantern.gba"],"sizeBytes":12,"expiresAt":"2026-12-18T17:00:00Z","futureField":1}],"totalBytes":12}""",
            ),
        )
        server.enqueue(json("""{"freedBytes":12}"""))
        server.enqueue(json("""{"freedBytes":12}"""))
        val c = client()
        assertEquals("Moss Lantern", c.trash().entries.single().label)
        assertEquals(12, c.deleteTrashEntry("tr_1").freedBytes)
        assertEquals(12, c.emptyTrash().freedBytes)
        server.takeRequest()
        val one = server.takeRequest()
        assertEquals("/api/v1/trash/tr_1", one.path)
        assertEquals("empty-trash", one.getHeader("X-RL-Confirm"))
        assertSigned(one)
        val all = server.takeRequest()
        assertEquals("DELETE", all.method)
        assertEquals("/api/v1/trash", all.path)
        assertEquals("empty-trash", all.getHeader("X-RL-Confirm"))
    }

    @Test
    fun restorePreviewConflictsAndReplace() = runBlocking {
        // A first M18 pak: no rename or replace fields.
        server.enqueue(
            json(
                """{"entryId":"tr_1","reason":"delete-game","label":"Moss","conflict":true,"items":[{"name":"Roms/GBA/Moss.gba","size":3,"conflict":true,"freeName":"Roms/GBA/Moss (2).gba"}],"restoredBytes":3}""",
            ),
        )
        server.enqueue(error("conflict", 409, """{"conflicts":["Roms/GBA/Moss.gba"]}"""))
        server.enqueue(json("""{"restored":["Roms/GBA/Moss (2).gba"],"restoredBytes":3,"renamed":true,"replacedEntryId":null,"replaced":[],"replacedKeptAs":null}"""))
        server.enqueue(error("unsupported_format", 422, """{"reason":"game-missing"}"""))
        server.enqueue(error("conflict", 409, """{"freeNameUnavailable":"all-taken"}"""))
        val c = client()
        val preview = c.trashRestorePreview("tr_1")
        assertTrue(preview.canRename)
        assertFalse(preview.replaceable)
        assertEquals("Roms/GBA/Moss (2).gba", preview.items.single().freeName)
        val conflict = runCatching { c.restoreTrashEntry("tr_1", TrashRestoreConflict.FAIL) }.exceptionOrNull() as ApiException.Server
        assertEquals(listOf("Roms/GBA/Moss.gba"), conflict.trashRestoreConflicts)
        val result = c.restoreTrashEntry("tr_1", TrashRestoreConflict.RENAME)
        assertTrue(result.renamed)
        val refused = runCatching { c.restoreTrashEntry("tr_1", TrashRestoreConflict.REPLACE) }.exceptionOrNull() as ApiException.Server
        assertEquals("game-missing", refused.trashReplaceRefusal)
        val noName = runCatching { c.restoreTrashEntry("tr_1", TrashRestoreConflict.RENAME) }.exceptionOrNull() as ApiException.Server
        assertEquals(TrashNoFreeName.ALL_TAKEN, noName.trashRestoreNoFreeName)
        assertNull(noName.trashRestoreConflicts)

        assertEquals("/api/v1/trash/tr_1/restore", server.takeRequest().path)
        assertEquals("fail", body(server.takeRequest())["onConflict"]!!.jsonPrimitive.content)
        assertEquals("rename", body(server.takeRequest())["onConflict"]!!.jsonPrimitive.content)
        assertEquals("replace", body(server.takeRequest())["onConflict"]!!.jsonPrimitive.content)
    }

    @Test
    fun restorePreviewOfABackupCopyWithItsClock() {
        val preview = ProtocolJson.decodeFromString<TrashRestorePreview>(
            """{"entryId":"tr_2","reason":"delete-backup","label":"Moss","conflict":false,"items":[{"name":"ReminisLink/backups/x.sav","size":3,"conflict":false,"freeName":"ReminisLink/backups/x.sav","replaceName":"Saves/GBA/Moss.gba.sav"},{"name":"ReminisLink/backups/x.rtc","size":1,"conflict":false,"freeName":null}],""" +
                """"restoredBytes":4,"freeNameUnavailable":null,"replaceable":true,"replaceDisplaced":["Saves/GBA/Moss.gba.sav"],"replaceKeptAs":"backup","replaceUnavailable":null}""",
        )
        assertTrue(preview.replaceable)
        assertTrue(preview.withClock)
        assertEquals(TrashKeptAs.BACKUP, preview.replaceKeptAs)
        assertFalse(preview.canRename)
        val blocked = preview.copy(replaceable = false, replaceUnavailable = "backup-taken")
        assertTrue(blocked.replaceBlocked)
    }

    @Test
    fun aGameIsDeletedToTheTrash() = runBlocking {
        server.enqueue(json("""{"moved":[{"name":"Roms/GBA/Moss.gba","role":"rom","size":3}],"kept":[],"movedBytes":3}"""))
        server.enqueue(json("""{"trashEntryId":"tr_5","moved":["Roms/GBA/Moss.gba"],"movedBytes":3}"""))
        val c = client()
        assertEquals(3, c.deletionPreview("R0FNRQ", keepSave = false).movedBytes)
        assertEquals("tr_5", c.deleteGame("R0FNRQ", keepSave = true).trashEntryId)
        assertEquals("/api/v1/games/R0FNRQ/deletion?keepSave=false", server.takeRequest().path)
        val delete = server.takeRequest()
        assertEquals("DELETE", delete.method)
        assertEquals("/api/v1/games/R0FNRQ?keepSave=true", delete.path)
        assertSigned(delete)
    }

    // MARK: Codex round 1: answers that arrive late

    @Test
    fun aCheatReadThatAnswersAfterAnEditNeverErasesIt() {
        val first = CheatList(listOf(Cheat(0, "A", "1", false)), "e1")
        val editor = CheatEditor(first)
        // A read starts, then the user turns the cheat on before it answers.
        val since = editor.revision
        editor.setEnabled(editor.entries[0].id, true)
        // Same list on the console: the edit stays and can still be saved over it.
        assertEquals(CheatEditor.ReadOutcome.UNCHANGED, editor.applyRead(first, since))
        assertTrue(editor.cheats[0].enabled)
        assertEquals("e1", editor.etag)
        // Another list on the console: held back, the edit stays.
        val changed = CheatList(listOf(Cheat(0, "A", "1", false), Cheat(1, "B", "2", true)), "e2")
        assertEquals(CheatEditor.ReadOutcome.HELD, editor.applyRead(changed, since))
        assertTrue(editor.hasChanges)
        assertTrue(editor.cheats[0].enabled)
        assertEquals("e1", editor.etag)
        // Discarding the edits shows the held list.
        editor.discardChanges()
        assertEquals(listOf("A", "B"), editor.cheats.map { it.description })
        assertEquals("e2", editor.etag)
        // No edit since the request: the read replaces the list (Reload after a 412).
        val again = editor.revision
        assertEquals(CheatEditor.ReadOutcome.APPLIED, editor.applyRead(first, again))
        assertEquals("e1", editor.etag)
    }

    @Test
    fun aPatchCancelledWhileItsRequestIsInFlightIsCancelledEvenAfterTheScreenClosed() = runBlocking {
        val held = java.util.concurrent.CountDownLatch(1)
        val posted = java.util.concurrent.CountDownLatch(1)
        val deleted = java.util.concurrent.CountDownLatch(1)
        server.dispatcher = object : okhttp3.mockwebserver.Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse = when {
                request.path == "/api/v1/games/g/patch" -> {
                    // The pak accepted the request; its answer is held.
                    posted.countDown()
                    held.await(10, java.util.concurrent.TimeUnit.SECONDS)
                    json("""{"jobId":"job_d"}""", 202)
                }
                request.method == "DELETE" && request.path == "/api/v1/jobs/job_d" -> {
                    deleted.countDown()
                    job("job_d", "cancelled", cancelRequested = true)
                }
                request.path == "/api/v1/jobs/job_d" -> job("job_d", "cancelled", cancelRequested = true)
                else -> error("not_found", 404)
            }
        }
        val screen = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        val app = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        try {
            val jobs = JobTracker { client() }
            val run = PatchRun(deviceId, screen, jobs, { client() }, RetryPolicy.immediate, cancelPollMillis = 1, durableScope = app)
            run.start("g", PatchInput.SdCard("sd_1"), PatchOutputMode.REPLACE, null, "Moss")
            assertTrue(posted.await(10, java.util.concurrent.TimeUnit.SECONDS))
            // First back: a cancel, waiting for the job id. Second back: the screen closes.
            run.cancel()
            assertTrue(run.cancelling.value)
            screen.cancel()
            held.countDown()
            assertTrue("the job was never cancelled", deleted.await(10, java.util.concurrent.TimeUnit.SECONDS))
            withTimeout(10_000) { jobs.jobs.first { list -> list.any { it.jobId == "job_d" && it.isFinished } } }
            assertEquals("the job is followed for the Transfers screen", "Moss", jobs.get(deviceId, "job_d")!!.title)
        } finally {
            app.cancel()
        }
    }

    @Test
    fun clearingFinishedTransfersNeverLosesAPakInstallation() = withScope { scope ->
        val bytes = "PK-fake-zip-2".toByteArray()
        server.enqueue(json("""{"uploadId":"up_9","chunkSize":64,"offset":0,"expiresAt":"2026-11-25T17:00:00Z"}""", 201))
        server.enqueue(MockResponse().setResponseCode(204).setHeader("Upload-Offset", bytes.size))
        server.enqueue(json("""{"jobId":"job_z"}"""))
        server.enqueue(job("job_z", "succeeded", result = """{"pakId":"pk_z"}"""))
        // The installer's collector runs on its own thread, held while the user clears the list.
        val executor = java.util.concurrent.Executors.newSingleThreadExecutor()
        val gate = java.util.concurrent.Semaphore(0)
        fun hold() = executor.execute { gate.acquire() }
        val installerScope = CoroutineScope(SupervisorJob() + executor.asCoroutineDispatcherForTest())
        try {
            val queue = TransferQueue(scope, RetryPolicy.immediate) { _, _ -> client() }
            val jobs = JobTracker { client() }
            val installer = PakInstaller(installerScope, queue, jobs)
            hold()
            val id = installer.install(deviceId, file(bytes), "Thistle.pak.zip", PakInstallKind.AUTO)
            withTimeout(10_000) { queue.items.first { list -> list.any { it.phase == TransferPhase.DONE } } }
            queue.clearFinished(deviceId)
            assertTrue("the upload is only hidden", queue.items.value.single().isHidden)
            gate.release()
            withTimeout(10_000) { installer.installs.first { it.single { i -> i.id == id }.phase is PakInstall.Phase.Installing } }
            assertTrue("released once its job id was read", queue.items.value.isEmpty())
            // The finished job cleared from Transfers before the installer reads it.
            hold()
            jobs.refresh(deviceId)
            jobs.clearFinished(deviceId)
            gate.release()
            withTimeout(10_000) { installer.installs.first { it.single { i -> i.id == id }.phase == PakInstall.Phase.Installed } }
            assertTrue(jobs.jobs.value.single().hidden)
        } finally {
            installerScope.cancel()
            executor.shutdownNow()
        }
    }

    private fun java.util.concurrent.ExecutorService.asCoroutineDispatcherForTest(): kotlinx.coroutines.CoroutineDispatcher =
        asCoroutineDispatcher()
}

