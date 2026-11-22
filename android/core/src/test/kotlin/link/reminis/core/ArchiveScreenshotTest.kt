package link.reminis.core

import java.io.ByteArrayOutputStream
import java.io.File
import java.time.LocalDate
import java.time.ZoneId
import java.util.zip.ZipInputStream
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.json.JsonArray
import link.reminis.core.demo.DemoConsole
import link.reminis.core.demo.DemoZip
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

/**
 * M19 game archives and M22 screenshots on Android: the models, the import
 * run and the screenshot logic, through the real signed client against the
 * fake pak of the demo mode (made-up games, no network).
 */
class ArchiveScreenshotTest {
    private val demo = DemoConsole("Demo Handheld") { "Demo cheat $it" }
    private val client get() = demo.client
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private val jobs = JobTracker { client }
    private val temp = File(System.getProperty("java.io.tmpdir"), "rl-archive-test-${System.nanoTime()}").apply { mkdirs() }

    @After
    fun tearDown() {
        scope.cancel()
        demo.close()
        temp.deleteRecursively()
    }

    private suspend fun game(name: String) = client.allGames().games.first { it.name == name }

    private suspend fun export(gameId: String, rom: Boolean): ByteArray {
        val out = ByteArrayOutputStream()
        client.download(DownloadSource.GameArchive(gameId, rom), out)
        return out.toByteArray()
    }

    private fun copy(bytes: ByteArray, name: String = "archive"): File =
        File(temp, "$name-${System.nanoTime()}").apply { writeBytes(bytes) }

    private fun run(target: ArchiveTarget?, deleted: MutableList<File> = mutableListOf()) =
        ArchiveImportRun(demo.deviceId, scope, jobs, { client }, target, onLocalCopyDone = { deleted += it; it.delete() })

    private suspend fun ArchiveImportRun.awaitStep(predicate: (ArchiveStep) -> Boolean): ArchiveStep =
        withTimeout(10_000) { step.first(predicate) }

    private suspend fun ArchiveImportRun.finish(): ArchiveStep = withTimeout(15_000) {
        while (step.value == ArchiveStep.Importing || step.value == ArchiveStep.Starting) {
            tick()
            delay(100)
        }
        step.value
    }

    @Test
    fun exportIsARealZipWithTheManifest() = runBlocking {
        val isles = game("Thistledown Isles")
        val bytes = export(isles.id, rom = false)
        val names = mutableListOf<String>()
        ZipInputStream(bytes.inputStream()).use { zip -> while (true) names += (zip.nextEntry ?: break).name }
        assertEquals("manifest.json", names.last())
        val files = DemoZip.read(bytes)!!
        val manifest = ProtocolJson.decodeFromString(GameArchiveManifest.serializer(), files.getValue("manifest.json").toString(Charsets.UTF_8))
        assertEquals(GameArchiveManifest.FORMAT, manifest.format)
        assertEquals("Thistledown Isles", manifest.source.gameName)
        assertFalse(manifest.hasRom)
        assertTrue(manifest.files.any { it.role == ArchiveRoles.SAVE })
        assertTrue(manifest.files.any { it.role == ArchiveRoles.STATE_THUMBNAIL && it.slot == 0 })
        assertTrue(manifest.files.any { it.role == ArchiveRoles.CHEATS })
        assertTrue(export(isles.id, rom = true).let { DemoZip.read(it)!!.keys.any { k -> k.startsWith("rom/") } })
        // A folder game has no single ROM to export.
        val orrery = game("Orrery of Nine Tides")
        try {
            export(orrery.id, rom = true); fail()
        } catch (e: ApiException.Server) {
            assertEquals(ErrorCodes.UNSUPPORTED_FORMAT, e.code)
        }
    }

    @Test
    fun importIntoTheSameGameReplacesAndKeepsTheSave() = runBlocking {
        val isles = game("Thistledown Isles")
        val bytes = export(isles.id, rom = false)
        val deleted = mutableListOf<File>()
        val run = run(null, deleted)
        run.start(copy(bytes), "Thistledown Isles.reminislink.zip")
        assertEquals(ArchiveStep.Ready, run.awaitStep { it == ArchiveStep.Ready || it is ArchiveStep.Failed })
        assertEquals("the local copy goes once the console has it", 1, deleted.size)
        val plan = run.plan.value!!
        assertTrue(plan.suggested)
        assertEquals(ArchiveTarget.Game(isles.id), plan.target)
        assertEquals("Thistledown Isles", plan.targetName)
        assertEquals(ArchivePlan.SavePromise.COPY, plan.savePromise)
        // The locked state 0 is skipped.
        assertEquals(ArchiveSkipReasons.LOCKED, plan.entries.first { it.role == ArchiveRoles.STATE && it.slot == 0 }.reason)
        assertTrue(plan.importsAnything)
        run.confirm("Import: Thistledown Isles")
        val end = run.finish()
        val result = (end as ArchiveStep.Succeeded).result
        assertEquals(isles.id, result.gameId)
        assertNotNull(result.saveBackupId)
        assertNotNull(result.trashEntryId)
        assertTrue(result.skipped.any { it.reason == ArchiveSkipReasons.LOCKED })
        assertEquals(BackupReasons.BEFORE_IMPORT, client.saveBackups(isles.id).first().reason)
        assertEquals("import-archive", client.trash().entries.first().reason)
        assertEquals("the console deleted the staging entry", 0, demo.pak.stagedCount)
        assertEquals(JobKinds.ARCHIVE_IMPORT, jobs.get(demo.deviceId, run.jobId!!)!!.kind)
    }

    @Test
    fun newGameConflictOffersTheExistingGameThenImportsAsANewGame() = runBlocking {
        val dash = game("Brambleway Dash")
        val bytes = export(dash.id, rom = true)
        val run = run(null)
        run.start(copy(bytes), "Brambleway Dash.reminislink.zip")
        run.awaitStep { it == ArchiveStep.Ready || it is ArchiveStep.Failed }
        val system = client.systems().first { it.label == "Handheld Advance" }
        run.retarget(ArchiveTarget.NewGame(system.id))
        val failed = run.awaitStep { it is ArchiveStep.Failed } as ArchiveStep.Failed
        assertEquals(409, (failed.error as ApiException.Server).status)
        assertEquals(dash.id, run.existingGameId.value)
        assertEquals("kept to choose another target", 1, demo.pak.stagedCount)
        // The game is deleted meanwhile: the archive makes it again.
        client.deleteGame(dash.id, keepSave = false)
        run.retarget(ArchiveTarget.NewGame(system.id))
        run.awaitStep { it == ArchiveStep.Ready }
        assertEquals(ArchiveActions.CREATE, run.plan.value!!.entries.first { it.role == ArchiveRoles.ROM }.action)
        run.confirm("Import")
        val result = (run.finish() as ArchiveStep.Succeeded).result
        assertTrue(client.allGames().games.any { it.id == result.gameId && it.name == "Brambleway Dash" })
    }

    @Test
    fun olderPakSaysSoBeforeSending() = runBlocking {
        demo.pak.archivesSupported = false
        assertFalse(client.supportsGameArchives())
        val deleted = mutableListOf<File>()
        val run = run(null, deleted)
        run.start(copy(ByteArray(10)), "x.reminislink.zip")
        val failed = run.awaitStep { it is ArchiveStep.Failed } as ArchiveStep.Failed
        assertTrue(failed.error is ArchivesNeedNewerPakException)
        assertEquals(1, deleted.size)
        assertEquals(0, demo.pak.stagedCount)
        demo.pak.archivesSupported = true
        assertTrue(client.supportsGameArchives())
    }

    @Test
    fun refusedArchiveLeavesTheStagingAreaAndAbandonDeletesIt() = runBlocking {
        val run = run(null)
        run.start(copy("not a zip".toByteArray()), "bad.reminislink.zip")
        val failed = run.awaitStep { it is ArchiveStep.Failed } as ArchiveStep.Failed
        assertEquals(ErrorCodes.UNSUPPORTED_FORMAT, (failed.error as ApiException.Server).code)
        withTimeout(5_000) { while (demo.pak.stagedCount != 0) delay(20) }

        val isles = game("Thistledown Isles")
        val good = run(ArchiveTarget.Game(isles.id))
        good.start(copy(export(isles.id, false)), "a.reminislink.zip")
        good.awaitStep { it == ArchiveStep.Ready }
        assertFalse("given, not suggested", good.plan.value!!.suggested)
        assertEquals(1, demo.pak.stagedCount)
        good.abandon()
        withTimeout(5_000) { while (demo.pak.stagedCount != 0) delay(20) }
    }

    private suspend fun waitUntil(what: String, condition: () -> Boolean) = withTimeout(15_000) {
        while (!condition()) delay(20)
    }.also { assertTrue(what, condition()) }

    @Test
    fun screenClosedWhileCompleteIsAnsweredDeletesTheStagingEntry() = runBlocking {
        val hold = java.util.concurrent.CountDownLatch(1)
        demo.pak.holdCompleteAnswer = hold
        val run = run(null)
        run.start(copy(export(game("Thistledown Isles").id, false)), "a.reminislink.zip")
        // The console wrote the entry; its answer is held.
        waitUntil("complete done") { demo.pak.completesAnswered.get() == 1 && demo.pak.stagedCount == 1 }
        run.abandon()
        demo.pak.holdCompleteAnswer = null
        hold.countDown()
        waitUntil("entry deleted once the answer came") { demo.pak.stagedCount == 0 }
        assertTrue(run.step.value !is ArchiveStep.Ready)
    }

    @Test
    fun jobFailingWithAConflictKeepsThatGameThenClosingDeletes() = runBlocking {
        val isles = game("Thistledown Isles")
        val run = run(null)
        run.start(copy(export(isles.id, false)), "a.reminislink.zip")
        run.awaitStep { it == ArchiveStep.Ready }
        demo.pak.failNextArchiveImport = "conflict" to kotlinx.serialization.json.buildJsonObject {
            put("gameId", kotlinx.serialization.json.JsonPrimitive(isles.id))
        }
        run.confirm("Import")
        val failed = run.finish() as ArchiveStep.Failed
        assertTrue(failed.error is ArchiveJobFailure)
        assertEquals("Import into That Game is offered", isles.id, run.existingGameId.value)
        assertEquals("kept while the screen can act on it", 1, demo.pak.stagedCount)
        run.abandon()
        waitUntil("deleted when the screen goes") { demo.pak.stagedCount == 0 }
    }

    @Test
    fun closedDuringStartingThenRefusedDeletesTheStagingEntry() = runBlocking {
        val dash = game("Quillwork Tactics")
        val run = run(ArchiveTarget.Game(dash.id))
        run.start(copy(export(dash.id, false)), "a.reminislink.zip")
        run.awaitStep { it == ArchiveStep.Ready }
        // The target is gone: POST /import answers 404 after the screen closed.
        client.deleteGame(dash.id, keepSave = false)
        run.confirm("Import")
        run.abandon()
        waitUntil("deleted after the refusal") { demo.pak.stagedCount == 0 }
    }

    @Test
    fun screenClosedDuringAFailingImportDeletesTheStagingEntry() = runBlocking {
        val isles = game("Thistledown Isles")
        val run = run(null)
        run.start(copy(export(isles.id, false)), "a.reminislink.zip")
        run.awaitStep { it == ArchiveStep.Ready }
        demo.pak.failNextArchiveImport = "internal" to null
        run.confirm("Import")
        run.awaitStep { it == ArchiveStep.Importing }
        run.abandon()
        waitUntil("deleted once the job failed") { demo.pak.stagedCount == 0 }
    }

    @Test
    fun pairingReplacedBetweenChunksStopsTheUpload() = runBlocking {
        val calls = java.util.concurrent.atomic.AtomicInteger()
        val bytes = export(game("Thistledown Isles").id, rom = true)
        assertTrue("several chunks", bytes.size > 2 * link.reminis.core.demo.FakePak.CHUNK_SIZE)
        val run = ArchiveImportRun(
            demo.deviceId, scope, jobs, { client }, null,
            // identity, two checks, the loop, chunk 1; from chunk 2 on another pairing.
            pairing = { if (calls.incrementAndGet() >= 6) "pairing B" else "pairing A" },
        )
        run.start(copy(bytes), "big.reminislink.zip")
        val failed = run.awaitStep { it is ArchiveStep.Failed } as ArchiveStep.Failed
        assertTrue("${failed.error}", failed.error is ConsoleForgottenException)
        assertEquals("no complete through another pairing", 0, demo.pak.completesAnswered.get())
        assertEquals(0, demo.pak.stagedCount)
    }

    @Test
    fun importResultAndPlanModels() {
        val plan = ProtocolJson.decodeFromString(
            ArchivePlan.serializer(),
            """{"manifest":{"format":"reminislink-game-archive","version":1,"createdAt":"2026-11-21T10:00:00Z","future":1,
               "source":{"os":"nextui","systemTag":"GBA","systemLabel":"Game Boy Advance","gameName":"Fern","fileName":"Fern.gba","romName":"Fern.gba","kind":"file"},
               "files":[{"path":"rtc/Fern.gba.rtc","role":"rtc","sizeBytes":8,"sha256":"00"},{"path":"x/y","role":"future-role","sizeBytes":1,"sha256":"00"}]},
               "target":{"systemId":"c3lz","folderId":null},"targetName":"Game Boy Advance","suggested":true,
               "entries":[{"role":"rtc","slot":null,"sizeBytes":8,"action":"replace","reason":null},{"role":"future-role","slot":null,"sizeBytes":1,"action":"skip","reason":"future-reason"}],
               "importBytes":8}""",
        )
        assertEquals(ArchiveTarget.NewGame("c3lz", null), plan.target)
        assertEquals(ArchivePlan.SavePromise.COPY_IF_ANY, plan.savePromise)
        assertEquals(1, plan.skipped.size)
        assertEquals(listOf("systemId" to "c3lz"), plan.target!!.query)
        val none = plan.copy(targetJson = null)
        assertFalse(none.importsAnything)
        assertTrue(GameArchiveManifest.isArchiveName("Fern.ReminisLink.ZIP"))
        assertFalse(GameArchiveManifest.isArchiveName("Fern.zip"))
        val result = ArchiveImportResult.from(
            ProtocolJson.parseToJsonElement("""{"gameId":"g","imported":3,"skipped":[{"role":"cheats","reason":"unsupported-format"},{"role":"state","slot":2,"reason":"locked"}],"trashEntryId":null,"saveBackupId":"bk"}""")
                as kotlinx.serialization.json.JsonObject,
        )!!
        assertEquals(3, result.imported)
        assertEquals(2, result.skipped[1].slot)
        assertNull(result.trashEntryId)
        assertEquals("bk", result.saveBackupId)
        assertNull(ArchiveImportResult.from(null))
        assertEquals("""{"target":{"gameId":"g"}}""", ProtocolJson.encodeToString(ArchiveImportRequest.serializer(), ArchiveImportRequest(ArchiveTarget.Game("g").toJson())))
    }

    // MARK: screenshots

    @Test
    fun screenshotsListPreviewsDownloadAndTrash() = runBlocking {
        val list = client.screenshots()
        assertEquals(8, list.screenshots.size)
        assertEquals("Thistledown Isles", list.screenshots.first().title)
        val bmp = list.screenshots.first { it.format == ScreenshotFormats.BMP }
        assertFalse(bmp.hasThumbnail)
        assertEquals("capture-0042", bmp.title)
        assertNull("a BMP has no preview", client.screenshotThumbnail(bmp.screenshotId))
        val first = list.screenshots.first()
        assertNotNull(client.screenshotThumbnail(first.screenshotId))
        val out = ByteArrayOutputStream()
        assertTrue(client.download(DownloadSource.Screenshot(first.screenshotId), out, maxBytes = 64L shl 20) > 0)
        try {
            client.download(DownloadSource.Screenshot(first.screenshotId), ByteArrayOutputStream(), maxBytes = 16)
            fail("larger than the limit")
        } catch (_: DownloadLimitExceededException) {
        }
        // One entry, then restored by the trash route (M18).
        val moved = trashScreenshotsInBatches(listOf(first.screenshotId, first.screenshotId)) { client.trashScreenshots(it) }
        assertEquals(1, moved)
        val entry = client.trash().entries.first()
        assertEquals(TrashEntry.SCREENSHOT_REASON, entry.reason)
        assertEquals(1, entry.screenshotCount)
        assertEquals(7, client.screenshots().screenshots.size)
        client.restoreTrashEntry(entry.id, TrashRestoreConflict.FAIL)
        assertEquals(8, client.screenshots().screenshots.size)
        try {
            client.trashScreenshots(listOf("sc_" + Base64Url.encode("gone.png".toByteArray())))
            fail()
        } catch (e: ApiException.Server) {
            assertEquals(404, e.status)
            assertTrue(e.details["missing"] is JsonArray)
        }
        demo.pak.screenshotsSupported = false
        try {
            client.screenshots(); fail()
        } catch (e: ApiException.Server) {
            assertEquals(404, e.status)
        }
    }

    @Test
    fun partialTrashCountsKeptScreenshots() = runBlocking {
        val ids = client.screenshots().screenshots.map { it.screenshotId }
        demo.pak.failNextScreenshotTrash = true
        try {
            trashScreenshotsInBatches(ids.take(3)) { client.trashScreenshots(it) }
            fail()
        } catch (e: ScreenshotsPartlyMovedException) {
            assertEquals(3, e.moved)
        }
        // A failure before anything moved is thrown as is.
        val busy = ApiException.Server(429, ErrorCodes.DEVICE_BUSY, "busy")
        try {
            trashScreenshotsInBatches(listOf("a")) { throw busy }
            fail()
        } catch (e: ApiException.Server) {
            assertEquals(busy, e)
        }
        // Batches of 500: the second fails after the first moved.
        val many = (1..700).map { "sc_$it" }
        val sent = mutableListOf<Int>()
        try {
            trashScreenshotsInBatches(many + many.take(5)) { batch ->
                sent += batch.size
                if (sent.size == 2) throw ApiException.Network(java.io.IOException("cut"))
                TrashMoveResult("tr")
            }
            fail()
        } catch (e: ScreenshotsPartlyMovedException) {
            assertEquals(500, e.moved)
        }
        assertEquals(listOf(500, 200), sent)
    }

    @Test
    fun screenshotModels() {
        fun shot(id: String, at: String, name: String = id) = Screenshot(id, name, at)
        val shots = listOf(
            shot("a", "2026-11-21T23:30:00Z"), shot("b", "2026-11-22T00:30:00Z"), shot("c", "2026-11-21T10:00:00Z", "z"),
            shot("d", "2026-11-21T10:00:00Z", "y"),
        )
        val utc = ScreenshotList.byDay(shots, ZoneId.of("UTC"))
        assertEquals(listOf(LocalDate.of(2026, 11, 22), LocalDate.of(2026, 11, 21)), utc.map { it.date })
        assertEquals(listOf("a", "d", "c"), utc[1].screenshots.map { it.screenshotId })
        // Paris (UTC+1): 23:30 UTC is the next day.
        val paris = ScreenshotList.byDay(shots, ZoneId.of("Europe/Paris"))
        assertEquals(listOf("b", "a"), paris[0].screenshots.map { it.screenshotId })
        val lenient = ProtocolJson.decodeFromString(ScreenshotList.serializer(), """{"screenshots":[{"screenshotId":"sc_x","name":"x.png","takenAt":"2026-11-21T10:00:00Z","new":1}]}""")
        assertEquals(0L, lenient.screenshots.first().sizeBytes)
        assertFalse(lenient.screenshots.first().hasThumbnail)
        assertFalse(lenient.truncated)
        assertEquals(2, ScreenshotGridLayout.columns(200f))
        assertEquals(3, ScreenshotGridLayout.columns(360f))
        assertEquals(8, ScreenshotGridLayout.columns(2000f))
        assertEquals(2, ScreenshotGridLayout.columns(Float.NaN))
        var selection = ScreenshotSelection().toggled(shots[0]).toggled(shots[1]).toggled(shots[0])
        assertEquals(listOf("b"), selection.selected(shots).map { it.screenshotId })
        selection = selection.toggledAll(shots)
        assertEquals(4, selection.count)
        assertTrue(selection.toggledAll(shots).isEmpty)
        assertEquals(listOf("a"), selection.kept(shots.take(1)).selected(shots).map { it.screenshotId })
        // The same name replaced on the console (another size): a new version leaves the selection.
        val replaced = shots[0].copy(sizeBytes = 99)
        assertTrue(shots[0] in selection)
        assertFalse(replaced in selection)
        assertTrue(selection.kept(listOf(replaced) + shots.drop(1)).selected(listOf(replaced)).isEmpty())
        assertEquals(listOf("a"), exactScreenshots(setOf(shots[0].thumbnailKey), shots).map { it.screenshotId })
        try {
            exactScreenshots(setOf(shots[0].thumbnailKey), listOf(replaced))
            fail("a replaced screenshot was accepted")
        } catch (_: ScreenshotChangedException) {
        }
        assertEquals(listOf(500, 1), ScreenshotTrashRequest.batches((0..500).map { "$it" } + "1").map { it.size })
        val taken = mutableSetOf<String>()
        assertEquals("a.png", freeFileName("a.png", taken))
        assertEquals("A (2).png", freeFileName("A.png", taken))
        assertEquals("noext", freeFileName("noext", taken))
        val held = ProtocolJson.decodeFromString(
            TrashEntry.serializer(),
            """{"id":"t","trashedAt":"2026-11-21T10:00:00Z","reason":"delete-screenshot","label":"3 screenshots","files":["a","b","c"],"sizeBytes":3,"held":true,"expiresAt":"2026-12-21T10:00:00Z"}""",
        )
        assertTrue(held.held)
        assertEquals(3, held.screenshotCount)
    }

    @Test
    fun screenshotsEventParsed() {
        assertEquals(PakEvent.Screenshots, PakEvent.parse(SseEvent(id = null, type = "screenshots", data = "{}")))
        assertNull(PakEvent.parse(SseEvent(id = null, type = "screenshots", data = "[]")))
    }

    @Test
    fun screenshotEventReachesTheStream() = runBlocking {
        val source = client.openEvents()
        val parser = SseParser()
        val buffer = ByteArray(4096)
        demo.pak.trashScreenshotsFromAnotherClient(listOf(demo.pak.screenshotNames.first()))
        val seen = withTimeout(5_000) {
            var found = false
            while (!found) {
                val n = source.read(buffer)
                if (n < 0) break
                found = parser.feed(buffer, 0, n).any { (it as? SseItem.Event)?.event?.type == "screenshots" }
            }
            found
        }
        source.cancel()
        assertTrue(seen)
        assertTrue(client.trash().entries.first().files.first().startsWith("Screenshots/"))
    }
}
