package link.reminis.core

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import link.reminis.core.demo.DemoConsole
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

/**
 * Sixth Android increment: the SD card browser and its writes (M23), the
 * renaming of a game, the resume history, the pak installed from the SD
 * card and the error observer, through the real signed client and the fake
 * pak of the demo (made-up names only).
 */
class FilesRenameTest {
    private val observed = java.util.Collections.synchronizedList(mutableListOf<String>())
    private val demo = DemoConsole("Demo Handheld", errorObserver = { observed += it.code }) { "Demo cheat $it" }
    private val client get() = demo.client

    @After
    fun tearDown() = demo.close()

    private suspend fun folder(vararg names: String): SdFolder {
        var current = client.browseSd(null)
        for (name in names) current = client.browseSd(current.entries.first { it.name == name }.sdEntryId)
        return current
    }

    private suspend fun upload(target: UploadTarget, bytes: ByteArray, onConflict: OnConflict = OnConflict.FAIL): UploadResult {
        val file = java.io.File.createTempFile("files", ".bin").apply { writeBytes(bytes) }
        try {
            val created = client.createUpload(CreateUploadRequest(target, bytes.size.toLong(), "00", onConflict))
            var offset = 0L
            while (offset < bytes.size) offset = client.sendChunk(created.uploadId, offset, file, minOf(created.chunkSize, bytes.size - offset))
            return client.completeUpload(created.uploadId)
        } finally {
            file.delete()
        }
    }

    private suspend inline fun <reified T : Throwable> refused(block: suspend () -> Unit): T {
        try {
            block()
        } catch (e: Throwable) {
            if (e is T) return e
            throw e
        }
        fail("not refused")
        error("unreachable")
    }

    @Test
    fun browserListsProtectionsAndVersions() = runBlocking {
        val root = client.browseSd(null)
        assertTrue(root.isRoot)
        assertTrue(root.writable)
        assertEquals(listOf(".userdata", "Bios", "Downloads", "Emus", "Roms", "Saves", "Tools"), root.entries.map { it.name })
        val hidden = root.entries.first { it.name == ".userdata" }
        assertFalse(hidden.deletable)
        assertEquals(SdProtection.SYSTEM, hidden.protection)
        assertEquals(SdProtection.ROOT, root.entries.first { it.name == "Roms" }.protection)
        assertTrue(root.entries.first { it.name == "Downloads" }.deletable)
        assertTrue(root.entries.all { !it.version.isNullOrEmpty() })
        assertFalse(client.browseSd(hidden.sdEntryId).writable)
        val tools = folder("Tools", "tg5040")
        assertEquals(SdProtection.DATA, tools.entries.first { it.name == "ReminisLink.pak" }.protection)
        assertTrue(tools.entries.first { it.name == "Clock.pak" }.isPakFolder)
        val downloads = folder("Downloads")
        assertTrue(downloads.entries.first { it.name == "Clock.pak.zip" }.isInstallableAsPak)
        assertFalse(downloads.entries.first { it.name == "README.txt" }.isInstallableAsPak)
        val readme = downloads.entries.first { it.name == "README.txt" }
        val out = java.io.ByteArrayOutputStream()
        assertEquals(512L, client.download(DownloadSource.SdFile(readme.sdEntryId), out))
    }

    @Test
    fun foldersFilesAndTrashAllOrNothing() = runBlocking {
        val downloads = folder("Downloads")
        val made = client.createSdFolder(SdFolderRequest(downloads.folderId, "Hacks"))
        assertEquals("Hacks", made.name)
        assertTrue(made.isFolder && made.deletable)
        val taken = refused<ApiException.Server> { client.createSdFolder(SdFolderRequest(downloads.folderId, "hacks")) }
        assertEquals(409, taken.status)
        assertNull(taken.sdProtection)
        val hidden = client.browseSd(null).entries.first { it.name == ".userdata" }
        val protectedParent = refused<ApiException.Server> { client.createSdFolder(SdFolderRequest(hidden.sdEntryId, "Mine")) }
        assertEquals(SdProtection.SYSTEM, protectedParent.sdProtection)

        // A file added to the new folder, never over another one: the free name comes with the conflict.
        val hacks = client.browseSd(made.sdEntryId)
        assertTrue(hacks.writable && hacks.entries.isEmpty())
        val bytes = ByteArray(3_000) { it.toByte() }
        upload(UploadTarget.SdFile(made.sdEntryId, "notes.txt"), bytes)
        val conflict = refused<ApiException.Server> { upload(UploadTarget.SdFile(made.sdEntryId, "Notes.txt"), bytes) }
        assertEquals(409, conflict.status)
        assertEquals("Notes (2).txt", conflict.sdFreeName)
        upload(UploadTarget.SdFile(made.sdEntryId, "notes.txt"), bytes, OnConflict.RENAME)
        val listed = client.browseSd(made.sdEntryId).entries
        assertEquals(listOf("notes (2).txt", "notes.txt"), listed.map { it.name })
        val out = java.io.ByteArrayOutputStream()
        client.download(DownloadSource.SdFile(listed.first { it.name == "notes.txt" }.sdEntryId), out)
        assertTrue(out.toByteArray().contentEquals(bytes))
        val noOverwrite = refused<ApiException.Server> {
            client.createUpload(CreateUploadRequest(UploadTarget.SdFile(made.sdEntryId, "x.txt"), 1, "00", OnConflict.OVERWRITE))
        }
        assertEquals(400, noOverwrite.status)

        // A file added to a system folder becomes a game.
        val system = folder("Roms").entries.first { it.name.startsWith("Handheld Advance") }
        upload(UploadTarget.SdFile(system.sdEntryId, "Lanternfish.hab"), bytes)
        assertTrue(client.allGames().games.any { it.name == "Lanternfish" })

        // The trash: the version shown is required, a change is refused, protected entries too.
        val stale = listed.first { it.name == "notes.txt" }
        client.trashSdEntries(SdTrashRequest(listOf(listed.first { it.name == "notes (2).txt" })))
        val now = client.browseSd(made.sdEntryId)
        assertEquals(listOf("notes.txt"), now.entries.map { it.name })
        val folderShown = folder("Downloads").entries.first { it.name == "Hacks" }
        val changed = refused<ApiException.Server> { client.trashSdEntries(SdTrashRequest(listOf(made))) }
        assertEquals(listOf(made.sdEntryId), changed.sdChanged)
        val roms = client.browseSd(null).entries.first { it.name == "Roms" }
        val protectedEntry = refused<ApiException.Server> { client.trashSdEntries(SdTrashRequest(listOf(roms, stale))) }
        assertEquals(SdProtection.ROOT, protectedEntry.sdProtection)
        assertEquals(listOf(roms.sdEntryId), protectedEntry.details["refused"]!!.jsonArray.map { it.jsonPrimitive.content })
        assertTrue("nothing moved", client.browseSd(made.sdEntryId).entries.isNotEmpty())

        // The folder and what it holds go in one entry, restored by M18.
        val moved = client.trashSdEntries(SdTrashRequest(listOf(folderShown, stale)))
        assertEquals(listOf("Downloads/Hacks/notes.txt"), moved.moved)
        assertTrue(folder("Downloads").entries.none { it.name == "Hacks" })
        val entry = client.trash().entries.first { it.id == moved.trashEntryId }
        assertEquals("delete-file", entry.reason)
        assertEquals("Hacks", entry.label)
        val gone = refused<ApiException.Server> { client.trashSdEntries(SdTrashRequest(listOf(folderShown))) }
        assertEquals(404, gone.status)
        assertFalse(client.trashRestorePreview(moved.trashEntryId).conflict)
        client.restoreTrashEntry(moved.trashEntryId, TrashRestoreConflict.FAIL)
        assertEquals(listOf("notes.txt"), client.browseSd(folder("Downloads").entries.first { it.name == "Hacks" }.sdEntryId).entries.map { it.name })
        assertTrue(SdTrashRequest(listOf(made.copy(version = null))).isComplete.not())
    }

    @Test
    fun pakFolderMovedToTheTrashLeavesThePaks() = runBlocking {
        val clock = folder("Tools", "tg5040").entries.first { it.name == "Clock.pak" }
        client.trashSdEntries(SdTrashRequest(listOf(clock)))
        assertTrue(client.paks().none { it.name == "Clock" })
    }

    @Test
    fun selectionKeepsTheVersionsSeen() {
        val a = SdEntry("se_a", "a.txt", "file", 1, "2026-11-18T17:00:00Z", deletable = true, version = "v1")
        val b = a.copy(sdEntryId = "se_b", name = "b.txt")
        var selection = SdSelection().toggle(a)!!.toggle(b)!!
        assertEquals(2, selection.count)
        selection = selection.prune(listOf(a, b.copy(version = "v2")))
        assertEquals(listOf("se_a"), selection.entries.map { it.sdEntryId })
        var full = SdSelection()
        repeat(SdTrashRequest.MAX_ITEMS) { full = full.toggle(a.copy(sdEntryId = "se_$it"))!! }
        assertNull("at most 100", full.toggle(a.copy(sdEntryId = "se_more")))
        val json = ProtocolJson.parseToJsonElement(SdTrashRequest(listOf(a, a, b.copy(sizeBytes = null, kind = "folder"))).toJson()).jsonObject
        val items = json["items"]!!.jsonArray
        assertEquals(2, items.size)
        assertEquals("null", items[1].jsonObject["sizeBytes"].toString())
        assertEquals("""{"parentId":null,"name":"X"}""", SdFolderRequest(null, "X").toJson())
        assertEquals("null", UploadTarget.SdFile(null, "f").toJson()["folderId"].toString())
    }

    @Test
    fun renameResultOfTheContract() {
        // `Game` of the contract: no systemId (the real pak sends none).
        val result = ProtocolJson.decodeFromString<GameRenameResult>(
            """{"game":{"id":"g2","name":"Moss","fileName":"Moss.hab","kind":"file","sizeBytes":3,"modifiedAt":"2026-11-18T17:00:00Z",""" +
                """"hasSave":true,"stateCount":0,"lockedStateCount":0,"cheatCount":0,"hasArt":false},"renamed":[{"from":"a","to":"b"}]}""",
        )
        assertEquals("g2", result.game.id)
        assertEquals(0, result.game.lockedStateCount)
        assertEquals(listOf(RenamedFile("a", "b")), result.renamed)
    }

    @Test
    fun gameNameRules() {
        assertEquals(GameNameRule.Problem.Empty, GameNameRule.problem("  "))
        assertEquals(GameNameRule.Problem.ForbiddenCharacter(':'), GameNameRule.problem("Moss: Two"))
        assertEquals(GameNameRule.Problem.InvisibleCharacter, GameNameRule.problem("Moss​Lantern"))
        assertEquals(GameNameRule.Problem.StartsWithDot, GameNameRule.problem(".Moss"))
        assertEquals(GameNameRule.Problem.TrailingSpaceOrDot, GameNameRule.problem("Moss."))
        assertEquals(GameNameRule.Problem.Reserved, GameNameRule.problem("com1"))
        assertEquals(GameNameRule.Problem.TooLong, GameNameRule.problem("é".repeat(120), ".hab"))
        assertNull(GameNameRule.problem("Moss Lantern (Rev 1)", ".hab"))
        assertEquals("Moss", GameNameRule.editableName("Moss.hab", isFolder = false))
        assertEquals(".hab", GameNameRule.keptExtension("Moss.hab", isFolder = false))
        assertEquals("Disc Tale", GameNameRule.editableName("Disc Tale", isFolder = true))
        assertEquals("", GameNameRule.keptExtension("Disc.Tale", isFolder = true))
        assertEquals("é", GameNameRule.normalized("é"))
    }

    @Test
    fun renameKeepsTheDataUnderANewId() = runBlocking {
        val games = client.allGames().games
        val isles = games.first { it.name == "Thistledown Isles" }
        val cheats = client.game(isles.id).cheatCount
        assertEquals(12, cheats)
        val result = client.renameGame(isles.id, "Thistledown Isles (Rev 1)")
        assertEquals("Thistledown Isles (Rev 1)", result.game.name)
        assertTrue(result.game.id != isles.id)
        assertEquals(1, result.renamed.size)
        val detail = client.game(result.game.id)
        assertTrue(detail.hasSave)
        assertEquals(12, detail.cheatCount)
        assertEquals(404, refused<ApiException.Server> { client.game(isles.id) }.status)
        // Same name: nothing changes.
        assertTrue(client.renameGame(result.game.id, "Thistledown Isles (Rev 1)").renamed.isEmpty())
        // A taken name, an invalid one.
        val other = games.first { it.systemId == isles.systemId && it.id != isles.id && it.kind == "file" }
        val taken = refused<ApiException.Server> { client.renameGame(other.id, "Thistledown Isles (Rev 1)") }
        assertEquals(1, taken.renameConflicts.size)
        assertEquals(400, refused<ApiException.Server> { client.renameGame(other.id, "a/b") }.status)
    }

    @Test
    fun resumeHistoryIsRestored() = runBlocking {
        val isles = client.allGames().games.first { it.name == "Thistledown Isles" }
        val history = client.resumeHistory(isles.id)
        assertEquals(3, history.entries.size)
        val restored = client.restoreResumeHistory(isles.id, history.entries.last().id)
        assertEquals("the current version is archived first", 4, restored.entries.size)
        assertEquals(404, refused<ApiException.Server> { client.restoreResumeHistory(isles.id, "rh_none") }.status)
    }

    @Test
    fun pakInstalledFromTheSdCardAndTheReplacementChecked() = runBlocking {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        try {
            val queue = TransferQueue(scope) { _, _ -> client }
            val jobs = JobTracker { client }
            val installer = PakInstaller(scope, queue, jobs, client = { client })
            val zip = folder("Downloads").entries.first { it.name == "Clock.pak.zip" }
            val id = installer.installFromSd(demo.deviceId, zip, PakInstallKind.AUTO)
            suspend fun phase(p: (PakInstall) -> Boolean): PakInstall = withTimeout(10_000) {
                while (true) {
                    jobs.refresh(demo.deviceId)
                    installer.installs.value.first { it.id == id }.takeIf(p)?.let { return@withTimeout it }
                    delay(100)
                }
                @Suppress("UNREACHABLE_CODE")
                error("unreachable")
            }
            phase { it.phase == PakInstall.Phase.NeedsReplacement("Clock") }
            // The SD file changed between the conflict and the replacement: another pak is installed.
            demo.pak.nextPakSourceName = "Weather.pak.zip"
            installer.replace(id)
            phase { it.phase == PakInstall.Phase.Installed }
            val done = withTimeout(10_000) { installer.installs.first { list -> list.first { it.id == id }.mismatch != null } }
            assertEquals(PakInstall.Mismatch("Weather", replacedOne = false), done.first { it.id == id }.mismatch)
            assertEquals("Clock", done.first { it.id == id }.confirmedName)
            assertTrue(client.paks().any { it.name == "Weather" })

            val broken = installer.installFromSd(demo.deviceId, folder("Downloads").entries.first { it.name == "broken.zip" }, PakInstallKind.AUTO)
            val failed = withTimeout(10_000) {
                while (true) {
                    jobs.refresh(demo.deviceId)
                    val phase = installer.installs.value.first { it.id == broken }.phase
                    if (phase is PakInstall.Phase.Failed) return@withTimeout phase
                    delay(100)
                }
                @Suppress("UNREACHABLE_CODE")
                error("unreachable")
            }
            assertEquals(PakRefusal.NotAnArchive, ((failed.error as PakJobFailure).server).pakRefusal)
        } finally {
            scope.cancel()
        }
    }

    @Test
    fun errorObserverSeesEveryErrorAnswer() = runBlocking {
        refused<ApiException.Server> { client.game("unknown") }
        demo.pak.disconnectClient()
        refused<ApiException.Server> { client.browseSd(null) }
        assertEquals(listOf(ErrorCodes.NOT_FOUND, ErrorCodes.DISCONNECTED), observed.toList())
        client.connectClient()
        assertNotNull(client.unpair())
    }
}
