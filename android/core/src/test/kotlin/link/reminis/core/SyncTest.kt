package link.reminis.core

import java.io.File
import java.io.OutputStream
import java.nio.file.Files
import java.time.Instant
import java.time.ZoneOffset
import kotlinx.coroutines.runBlocking
import link.reminis.core.demo.DemoConsole
import link.reminis.core.vault.ApiSyncSource
import link.reminis.core.vault.FileVaultStore
import link.reminis.core.vault.NewVersionFile
import link.reminis.core.vault.SaveSync
import link.reminis.core.vault.SaveSyncSource
import link.reminis.core.vault.SaveVault
import link.reminis.core.vault.SaveVaultException
import link.reminis.core.vault.SyncChecksumException
import link.reminis.core.vault.VaultConsoleInfo
import link.reminis.core.vault.VaultEntry
import link.reminis.core.vault.VaultGameInfo
import link.reminis.core.vault.VaultLocation
import link.reminis.core.vault.VaultUnavailableException
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

/**
 * Android increment 7: the synced vault (port of `FileSaveVault.swift`,
 * same layout and manifests as iOS) and the sync of saves and states,
 * against the fake pak through the real signed client. Invented names only.
 */
class SyncTest {
    private val work: File = Files.createTempDirectory("rl-sync").toFile()
    private val root = File(work, "vault")
    private var clock = Instant.parse("2026-11-19T15:42:10Z")
    private fun vault(store: FileVaultStore = FileVaultStore(root), max: Int = 10) =
        SaveVault(store, VaultLocation.INTERNAL, max, ZoneOffset.UTC, now = { clock })

    private val console = VaultConsoleInfo("device-fern", "Fern Handheld")
    private val game = VaultGameInfo("game-1", "sys-1", "Pocket Classic (PCL)", "Juniper Trail")

    @After
    fun tearDown() {
        work.deleteRecursively()
    }

    private fun file(name: String, text: String): NewVersionFile {
        val f = File(work, "in-${System.nanoTime()}-$name").apply { writeText(text) }
        return NewVersionFile(f, name, f.inputStream().use { sha(it.readBytes()) })
    }

    private fun sha(bytes: ByteArray) = java.security.MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") { "%02x".format(it) }

    @Test
    fun layoutAndManifestsLikeIos() {
        val v = vault()
        val version = v.addVersion(console, game, file("Juniper Trail.pcr.sav", "SAVE-1"), file("Juniper Trail.pcr.rtc", "RTC"), "2026-11-18T16:55:00Z")
        val gameFolder = File(root, "Fern Handheld/Pocket Classic (PCL)/Juniper Trail")
        assertEquals("2026-11-19 15.42.10", version.id)
        val versionFolder = File(gameFolder, "2026-11-19 15.42.10")
        assertEquals(setOf(".version.json", "Juniper Trail.pcr.sav", "Juniper Trail.pcr.rtc"), versionFolder.list()!!.toSet())
        val consoleJson = ProtocolJson.parseToJsonElement(File(root, "Fern Handheld/.console.json").readText()).toString()
        assertEquals("""{"deviceId":"device-fern","name":"Fern Handheld"}""", consoleJson)
        val gameJson = ProtocolJson.parseToJsonElement(File(gameFolder, ".game.json").readText()).toString()
        assertEquals("""{"gameId":"game-1","systemId":"sys-1","systemName":"Pocket Classic (PCL)","name":"Juniper Trail"}""", gameJson)
        val manifest = ProtocolJson.parseToJsonElement(File(versionFolder, ".version.json").readText()) as kotlinx.serialization.json.JsonObject
        assertEquals(setOf("gameId", "sequence", "syncedAt", "modifiedAt", "save", "rtc"), manifest.keys)
        assertEquals("\"2026-11-19T15:42:10Z\"", manifest["syncedAt"].toString())
        assertEquals("1", manifest["sequence"].toString())
        // No hidden assembly folder is left.
        assertTrue(gameFolder.list()!!.none { it.startsWith(".rl-partial") })

        clock = clock.plusSeconds(61)
        val state = v.addStateVersion(console, game, 3, file("Juniper Trail.pcr.st3", "STATE"), file("Juniper Trail.pcr.st3.png", "PNG"), null)
        assertEquals("2026-11-19 15.43.11 state 3", state.id)
        assertTrue(File(gameFolder, "${state.id}/.state-version.json").isFile)
        val games = v.games(console.deviceId)
        assertEquals(1, games.size)
        assertEquals(listOf(3), games[0].stateSlots)
        assertEquals(1, games[0].versionCount)
        assertEquals("Pocket Classic (PCL)/Juniper Trail", games[0].folderPath)
    }

    @Test
    fun readsAVaultWrittenByIos() {
        // As FileSaveVault.swift writes it (sorted keys, pretty printed), with a Finder file.
        val gameFolder = File(root, "Fern Handheld/Handheld Advance (HA)/Brambleway Dash").apply { mkdirs() }
        File(root, "Fern Handheld/.console.json").writeText("{\n  \"deviceId\" : \"device-fern\",\n  \"name\" : \"Fern Handheld\"\n}")
        File(gameFolder, ".game.json").writeText(
            "{\n  \"gameId\" : \"game-2\",\n  \"name\" : \"Brambleway Dash\",\n  \"systemId\" : \"sys-2\",\n  \"systemName\" : \"Handheld Advance (HA)\"\n}",
        )
        val sav = "IOS-SAVE".toByteArray()
        val one = File(gameFolder, "2026-11-10 08.00.00").apply { mkdirs() }
        File(one, "Brambleway Dash.hab.sav").writeBytes(sav)
        File(one, ".DS_Store").writeText("finder")
        File(one, ".version.json").writeText(
            """{"gameId":"game-2","modifiedAt":"2026-11-09T20:00:00Z","save":{"name":"Brambleway Dash.hab.sav","sha256":"${sha(sav)}","sizeBytes":${sav.size}},"sequence":4,"syncedAt":"2026-11-10T08:00:00.123Z"}""",
        )
        val st = "IOS-STATE".toByteArray()
        val stateFolder = File(gameFolder, "2026-11-10 08.00.00 state 0").apply { mkdirs() }
        File(stateFolder, "Brambleway Dash.hab.st0").writeBytes(st)
        File(stateFolder, ".state-version.json").writeText(
            """{"gameId":"game-2","slot":0,"sequence":1,"syncedAt":"2026-11-10T08:00:00Z","state":{"name":"Brambleway Dash.hab.st0","sha256":"${sha(st)}","sizeBytes":${st.size}}}""",
        )
        // A folder added by the user (no manifest): never a version, never deleted.
        File(gameFolder, "My notes").mkdirs()

        val v = vault()
        assertEquals(listOf("Fern Handheld"), v.consoles().map { it.folderName })
        val versions = v.versions("device-fern", "game-2")
        assertEquals(1, versions.size)
        assertEquals(4, versions[0].info.sequence)
        assertEquals(1, v.stateVersions("device-fern", "game-2").size)
        val exported = v.export("device-fern", "game-2", versions[0], File(work, "out"))
        assertTrue(exported.main.readBytes().contentEquals(sav))
        // The next version continues the numbering of the iOS ones.
        val next = v.addVersion(
            console, VaultGameInfo("game-2", "sys-2", "Handheld Advance (HA)", "Brambleway Dash"), file("Brambleway Dash.hab.sav", "ANDROID"), null, null,
        )
        assertEquals(5, next.info.sequence)
        assertTrue(next.id in gameFolder.list()!!)
        assertTrue(File(gameFolder, "My notes").isDirectory)
    }

    @Test
    fun keepsTenVersionsAndNeverDeletesWhatChangedByHand() {
        val v = vault()
        val first = v.addVersion(console, game, file("Juniper Trail.pcr.sav", "S0"), null, null)
        // A file added by hand to the oldest version: kept beyond the limit.
        File(root, "Fern Handheld/Pocket Classic (PCL)/Juniper Trail/${first.id}/note.txt").writeText("mine")
        (1..11).forEach { v.addVersion(console, game, file("Juniper Trail.pcr.sav", "S$it"), null, null) }
        val versions = v.versions(console.deviceId, game.gameId)
        // 10 counted after the newest, plus the oldest kept for its extra file.
        assertEquals(11, versions.size)
        assertEquals(12, versions.first().info.sequence)
        assertTrue(versions.any { it.id == first.id })
        assertEquals((12 downTo 3).toList() + 1, versions.map { it.info.sequence })
        // States: one series per slot, each limited.
        repeat(12) { v.addStateVersion(console, game, 0, file("Juniper Trail.pcr.st0", "ST$it"), null, null) }
        v.addStateVersion(console, game, 1, file("Juniper Trail.pcr.st1", "OTHER"), null, null)
        val states = v.stateVersions(console.deviceId, game.gameId)
        assertEquals(10, states.count { it.info.slot == 0 })
        assertEquals(1, states.count { it.info.slot == 1 })
    }

    @Test
    fun identicalVersionsShowOnceAndDuplicateFoldersAreRead() {
        val v = vault()
        v.addVersion(console, game, file("Juniper Trail.pcr.sav", "SAME"), null, null)
        // A copy of the console folder from another device ("Fern Handheld 2"): read as the same console.
        val copy = File(root, "Fern Handheld 2")
        File(root, "Fern Handheld").copyRecursively(copy)
        assertEquals(1, v.consoles().size)
        // Identical versions published one after the other are shown once (both kept).
        assertEquals(1, v.versions(console.deviceId, game.gameId).size)
        val next = vault().addVersion(console, game, file("Juniper Trail.pcr.sav", "NEW"), null, null)
        assertEquals(2, next.info.sequence)
        // Written to the canonical folder (no suffix).
        assertTrue(File(root, "Fern Handheld/Pocket Classic (PCL)/Juniper Trail/${next.id}").isDirectory)
        assertEquals(2, vault().versions(console.deviceId, game.gameId).size)
        assertEquals("Fern" to 1, SaveVault.duplicateRank("Fern"))
        assertEquals("Fern" to 2, SaveVault.duplicateRank("Fern 2"))
        assertEquals("Fern" to 3, SaveVault.duplicateRank("Fern (3)"))
        assertTrue(SaveVault.precedes("Fern", "Fern 2"))
    }

    /** A provider that cannot rename (SAF without FLAG_SUPPORTS_RENAME): a new folder first, the hidden one deleted after. */
    private class NoRenameStore(root: File) : FileVaultStore(root) {
        var renames = 0
        override fun rename(entry: VaultEntry, name: String): VaultEntry? {
            renames++
            return null
        }
    }

    @Test
    fun aStoreThatCannotRenameStillPublishesWhole() {
        val store = NoRenameStore(root)
        val v = vault(store)
        val version = v.addVersion(console, game, file("Juniper Trail.pcr.sav", "S"), file("Juniper Trail.pcr.rtc", "R"), null)
        assertTrue(store.renames > 0)
        val gameFolder = File(root, "Fern Handheld/Pocket Classic (PCL)/Juniper Trail")
        assertTrue(gameFolder.list()!!.none { it.startsWith(".") && it != ".game.json" })
        assertTrue(File(root, "Fern Handheld").list()!!.none { it.startsWith(".rl-partial") })
        assertEquals(version, vault().versions(console.deviceId, game.gameId).single())
    }

    @Test
    fun exportRefusesAVersionChangedByHand() {
        val v = vault()
        val version = v.addVersion(console, game, file("Juniper Trail.pcr.sav", "GOOD"), null, null)
        File(root, "Fern Handheld/Pocket Classic (PCL)/Juniper Trail/${version.id}/Juniper Trail.pcr.sav").writeText("EVIL")
        try {
            vault().export(console.deviceId, game.gameId, version, File(work, "out"))
            fail("exported a changed version")
        } catch (e: SaveVaultException.VersionMismatch) {
            // Nothing left behind.
            assertTrue(File(work, "out").list().orEmpty().isEmpty())
        }
        assertEquals("_", SaveVault.folderName("..."))
        assertEquals("A_B_C", SaveVault.folderName("A/B:C"))
        try {
            SaveVault.checkedFileName("../x")
            fail("a path was accepted")
        } catch (_: SaveVaultException.InvalidName) {
        }
    }

    // MARK: sync against the fake pak

    private val demo = DemoConsole("Demo Handheld") { "Demo cheat $it" }

    @After
    fun closeDemo() = demo.close()

    private fun sync(source: SaveSyncSource = ApiSyncSource { demo.client }, store: FileVaultStore = FileVaultStore(root)) =
        SaveSync(VaultConsoleInfo(demo.deviceId, "Demo Handheld"), source, vault(store), File(work, "scratch").apply { mkdirs() })

    @Test
    fun syncCopiesChangedSavesAndStatesThenRestores() = runBlocking {
        val saves = demo.client.saves()
        val states = demo.client.allStates()
        assertTrue(saves.saves.isNotEmpty())
        assertTrue("a clock file is listed", saves.saves.any { it.rtc != null })
        val report = sync().run()
        assertEquals(saves.saves.size, report.stored.size)
        assertTrue(report.failures.isEmpty())
        assertEquals(states.states.size, report.states!!.stored.size)

        val v = vault()
        val withClock = saves.saves.first { it.rtc != null }
        val version = v.latestVersion(demo.deviceId, withClock.gameId)!!
        assertEquals(withClock.sha256, version.info.save.sha256)
        assertEquals(withClock.rtc!!.sha256, version.info.rtc!!.sha256)
        assertEquals(withClock.path.substringAfterLast('/'), version.info.save.name)
        val withThumb = states.states.first { it.thumbnail != null }
        val stateVersion = v.latestStateVersions(demo.deviceId, withThumb.gameId)[withThumb.slot]!!
        assertEquals(withThumb.path.substringAfterLast('/') + ".png", stateVersion.info.thumbnail!!.name)

        // Nothing changed: nothing new.
        val again = sync().run()
        assertTrue(again.stored.isEmpty())
        assertEquals(saves.saves.size, again.unchanged)
        assertTrue(again.states!!.stored.isEmpty())

        // The save changes on the console: a second version, then the first one restored.
        demo.client.replaceSave(withClock.gameId, File(work, "new.sav").apply { writeText("CHANGED ON THE CONSOLE") })
        val third = sync().run()
        assertEquals(listOf(withClock.gameId), third.stored)
        val versions = vault().versions(demo.deviceId, withClock.gameId)
        assertEquals(2, versions.size)
        // The clock file stayed on the console: kept in the new version too.
        assertEquals(withClock.rtc.sha256, versions[0].info.rtc!!.sha256)
        val exported = vault().export(demo.deviceId, withClock.gameId, versions[1], File(work, "restore"))
        demo.client.replaceSave(withClock.gameId, exported.main)
        val back = java.io.ByteArrayOutputStream()
        demo.client.download(DownloadSource.Save(withClock.gameId), back)
        assertEquals(withClock.sha256, sha(back.toByteArray()))
    }

    @Test
    fun aDamagedFileIsAFailureAndTheSyncGoesOn() = runBlocking {
        val real = ApiSyncSource { demo.client }
        val damaged = real.saves().saves.first().gameId
        val source = object : SaveSyncSource by real {
            override suspend fun saves() = real.saves().let { list ->
                list.copy(saves = list.saves.map { if (it.gameId == damaged) it.copy(sha256 = "0".repeat(64)) else it })
            }
        }
        val report = sync(source).run()
        assertEquals(1, report.failures.size)
        assertTrue(report.failures[0].error is SyncChecksumException)
        assertEquals(report.total - 1, report.stored.size)
        assertNull(vault().latestVersion(demo.deviceId, damaged))
    }

    @Test
    fun anOlderPakSyncsTheSavesOnly() = runBlocking {
        demo.pak.statesUnsupported = true
        val report = sync().run()
        assertNull(report.states)
        assertFalse(report.stored.isEmpty())
    }

    @Test
    fun losingTheConsoleStopsTheSync() = runBlocking {
        val real = ApiSyncSource { demo.client }
        var downloads = 0
        val source = object : SaveSyncSource by real {
            override suspend fun download(source: DownloadSource, out: OutputStream) {
                if (++downloads == 3) throw ApiException.Network(java.io.IOException("gone"))
                real.download(source, out)
            }
        }
        try {
            sync(source).run()
            fail("the sync went on without its console")
        } catch (e: ApiException.Network) {
            // The version stored before stays.
            assertNotNull(vault().consoles().singleOrNull())
        }
    }

    // MARK: Codex review 1 of increment 7

    @Test
    fun aVersionOfAnotherGameIsNeverShownForThisOne() {
        val v = vault()
        val other = VaultGameInfo("game-other", "sys-1", "Pocket Classic (PCL)", "Willow Run")
        v.addVersion(console, game, file("Juniper Trail.pcr.sav", "JUNIPER"), null, null)
        val foreign = v.addVersion(console, other, file("Willow Run.pcr.sav", "WILLOW"), null, null)
        val foreignState = v.addStateVersion(console, other, 0, file("Willow Run.pcr.st0", "WILLOW-STATE"), null, null)
        // Copied by hand into the folder of the other game.
        val system = File(root, "Fern Handheld/Pocket Classic (PCL)")
        File(system, "Willow Run/${foreign.id}").copyRecursively(File(system, "Juniper Trail/copied"))
        File(system, "Willow Run/${foreignState.id}").copyRecursively(File(system, "Juniper Trail/copied state"))
        val fresh = vault()
        assertEquals(listOf("game-1"), fresh.versions(console.deviceId, game.gameId).map { it.info.gameId })
        assertTrue(fresh.stateVersions(console.deviceId, game.gameId).isEmpty())
        assertEquals(1, fresh.games(console.deviceId).first { it.id == game.gameId }.versionCount)
    }

    @Test
    fun theSameSecondIsOrderedByTheFullDateLikeIos() {
        val gameFolder = File(root, "Fern Handheld/Pocket Classic (PCL)/Juniper Trail").apply { mkdirs() }
        File(root, "Fern Handheld/.console.json").writeText("""{"deviceId":"device-fern","name":"Fern Handheld"}""")
        File(gameFolder, ".game.json").writeText("""{"gameId":"game-1","systemId":"sys-1","systemName":"Pocket Classic (PCL)","name":"Juniper Trail"}""")
        fun version(folder: String, syncedAt: String, text: String) {
            val bytes = text.toByteArray()
            val dir = File(gameFolder, folder).apply { mkdirs() }
            File(dir, "J.sav").writeBytes(bytes)
            File(dir, ".version.json").writeText(
                """{"gameId":"game-1","sequence":3,"syncedAt":"$syncedAt","save":{"name":"J.sav","sha256":"${sha(bytes)}","sizeBytes":${bytes.size}}}""",
            )
        }
        // Same sequence and second: the folder names would put "b" first, the full date says "a".
        version("a", "2026-11-19T12:00:00.900Z", "LATER")
        version("b", "2026-11-19T12:00:00.100Z", "EARLIER")
        assertEquals(listOf("a", "b"), vault().versions(console.deviceId, game.gameId).map { it.id })
    }

    @Test
    fun aVersionWithASubFolderIsNeverPurged() {
        val v = vault(max = 1)
        val first = v.addVersion(console, game, file("Juniper Trail.pcr.sav", "ONE"), null, null)
        val folder = File(root, "Fern Handheld/Pocket Classic (PCL)/Juniper Trail/${first.id}")
        // A folder named like a system file, with something in it.
        File(folder, ".DS_Store").mkdirs()
        File(folder, ".DS_Store/kept.txt").writeText("mine")
        v.addVersion(console, game, file("Juniper Trail.pcr.sav", "TWO"), null, null)
        v.addVersion(console, game, file("Juniper Trail.pcr.sav", "THREE"), null, null)
        assertTrue(File(folder, ".DS_Store/kept.txt").isFile)
        assertTrue(File(folder, "Juniper Trail.pcr.sav").isFile)
        // The second, exact, was purged file by file (its folder is gone).
        assertEquals(listOf(3, 1), vault().versions(console.deviceId, game.gameId).map { it.info.sequence })
    }

    /** A provider without rename that fails while writing the clock file in the final folder. */
    private class FailingRtcStore(root: File) : FileVaultStore(root) {
        var reached = false
        override fun rename(entry: VaultEntry, name: String): VaultEntry? = null
        override fun createFile(parent: VaultEntry, name: String, write: (OutputStream) -> Unit): VaultEntry {
            if (name.endsWith(".rtc") && !parent.name.startsWith(".rl-partial")) {
                // Reached only in the final folder, after the save was written there.
                reached = File(parent.ref, name.removeSuffix(".rtc") + ".sav").isFile
                throw java.io.IOException("provider failed")
            }
            return super.createFile(parent, name, write)
        }
    }

    @Test
    fun aFailedFinalCopyLeavesNothingBehind() {
        val store = FailingRtcStore(root)
        // The game folder first (written whole: no clock file in it).
        vault(store).addVersion(console, game, file("Juniper Trail.pcr.sav", "FIRST"), null, null)
        try {
            vault(store).addVersion(console, game, file("Juniper Trail.pcr.sav", "SAVE"), file("Juniper Trail.pcr.rtc", "RTC"), null)
            fail("the failure was hidden")
        } catch (_: java.io.IOException) {
        }
        assertTrue("the failure was not injected at the clock file of the final folder", store.reached)
        val gameFolder = File(root, "Fern Handheld/Pocket Classic (PCL)/Juniper Trail")
        // Only the first version and the identity file: no final folder with the save alone, no assembly folder.
        assertEquals(setOf(".game.json", "2026-11-19 15.42.10"), gameFolder.list()!!.toSet())
        assertEquals(1, vault().versions(console.deviceId, game.gameId).size)
    }

    @Test
    fun aMissingFileIsCopiedAgainFromTheConsole() = runBlocking {
        sync().run()
        val save = demo.client.saves().saves.first()
        val latest = vault().latestVersion(demo.deviceId, save.gameId)!!
        // The save file of the newest version disappeared from the folder (by hand, or a provider).
        val versionFolder = root.walkTopDown().first { it.isDirectory && File(it, ".version.json").isFile && File(it, latest.info.save.name).isFile && it.name == latest.id }
        File(versionFolder, latest.info.save.name).delete()
        val again = sync().run()
        assertTrue("the save was said up to date", save.gameId in again.stored)
        assertNotNull(vault().export(demo.deviceId, save.gameId, vault().latestVersion(demo.deviceId, save.gameId)!!, File(work, "out")))
    }

    /** A folder of the picker whose permission is removed once [armed]. */
    private class RevocableStore(root: File) : FileVaultStore(root) {
        @Volatile var armed: (String) -> Boolean = { false }
        private fun check(name: String) {
            if (armed(name)) throw VaultUnavailableException("permission removed")
        }
        override fun root(): VaultEntry = super.root().also { check("") }
        override fun createFile(parent: VaultEntry, name: String, write: (OutputStream) -> Unit): VaultEntry {
            check(name)
            return super.createFile(parent, name, write)
        }
    }

    @Test
    fun aRevokedFolderFailsTheSyncEvenDuringTheStatesOrWithNothingToCopy() = runBlocking {
        val store = RevocableStore(root)
        store.armed = { name -> ".st" in name }
        try {
            sync(store = store).run()
            fail("a revocation during the states passed for an incomplete sync")
        } catch (_: VaultUnavailableException) {
        }
        // Nothing to copy any more (everything synced), and the folder unavailable: said at once.
        store.armed = { false }
        sync(store = store).run()
        store.armed = { true }
        try {
            sync(store = store).run()
            fail("an unavailable folder passed for an up-to-date sync")
        } catch (_: VaultUnavailableException) {
        }
    }

    // MARK: Codex review 2 of increment 7

    @Test
    fun namesEqualAsSwiftSeesThemGoOnToTheManifestKey() {
        File(root, "Fern Handheld/.console.json").apply { parentFile.mkdirs() }.writeText("""{"deviceId":"device-fern","name":"Fern Handheld"}""")
        val gameJson = """{"gameId":"game-1","systemId":"sys-1","systemName":"Pocket Classic (PCL)","name":"Juniper Trail"}"""
        // Two folders of the same game (two devices), each with a version of the same sequence and date,
        // in folders named "Café" in NFC and in NFD.
        val nfc = "2026-11-19 12.00.00 Café"
        val nfd = "2026-11-19 12.00.00 Café"
        fun version(gameFolder: String, folder: String, text: String) {
            val game = File(root, "Fern Handheld/Pocket Classic (PCL)/$gameFolder").apply { mkdirs() }
            File(game, ".game.json").writeText(gameJson)
            val bytes = text.toByteArray()
            val dir = File(game, folder).apply { mkdirs() }
            File(dir, "J.sav").writeBytes(bytes)
            File(dir, ".version.json").writeText(
                """{"gameId":"game-1","sequence":2,"syncedAt":"2026-11-19T12:00:00Z","save":{"name":"J.sav","sha256":"${sha(bytes)}","sizeBytes":${bytes.size}}}""",
            )
        }
        version("Juniper Trail", nfc, "ALPHA")
        version("Juniper Trail 2", nfd, "OMEGA")
        val versions = vault().versions(console.deviceId, game.gameId)
        assertEquals(2, versions.size)
        // Equal names: the larger manifest key (code points of "sha/size/name|-|0.0") comes first, as on iOS.
        val expectedFirst = listOf("ALPHA", "OMEGA").maxByOrNull { "${sha(it.toByteArray())}/5/J.sav|-|0.0" }!!
        assertEquals(sha(expectedFirst.toByteArray()), versions.first().info.save.sha256)
    }

    @Test
    fun aSameSizeReplacementIsCopiedAgainFromTheConsole() = runBlocking {
        sync().run()
        val save = demo.client.saves().saves.first()
        val latest = vault().latestVersion(demo.deviceId, save.gameId)!!
        val versionFolder = root.walkTopDown().first { it.isDirectory && it.name == latest.id && File(it, latest.info.save.name).isFile }
        // Same size, other bytes: only the SHA-256 tells it.
        val target = File(versionFolder, latest.info.save.name)
        target.writeBytes(ByteArray(target.length().toInt()) { 0x5A })
        val again = sync().run()
        assertTrue("a damaged copy was said up to date", save.gameId in again.stored)
        assertNotNull(vault().export(demo.deviceId, save.gameId, vault().latestVersion(demo.deviceId, save.gameId)!!, File(work, "out")))
    }

    /** A storage provider (SAF): an empty folder is deleted only when untouched for an hour. */
    private open class ProviderStore(root: File) : FileVaultStore(root) {
        override val foldersNeedAge: Boolean get() = true
    }

    @Test
    fun aProviderHasAnEmptiedFolderDeletedOnlyAnHourLater() {
        val store = ProviderStore(root)
        // The clock of the vault follows the real one here (folder dates are real).
        clock = Instant.now()
        val v = vault(store, max = 1)
        val first = v.addVersion(console, game, file("Juniper Trail.pcr.sav", "ONE"), null, null)
        clock = clock.plusSeconds(61)
        v.addVersion(console, game, file("Juniper Trail.pcr.sav", "TWO"), null, null)
        val folder = File(root, "Fern Handheld/Pocket Classic (PCL)/Juniper Trail/${first.id}")
        // Emptied file by file (manifest first), left in place for now, and no longer a version.
        assertTrue(folder.isDirectory)
        assertTrue(folder.list()!!.isEmpty())
        assertEquals(listOf(2), vault(store).versions(console.deviceId, game.gameId).map { it.info.sequence })
        // An hour later, the next publication of the game removes it.
        folder.setLastModified(System.currentTimeMillis() - 2 * 3_600_000)
        clock = clock.plusSeconds(61)
        vault(store, max = 1).addVersion(console, game, file("Juniper Trail.pcr.sav", "THREE"), null, null)
        assertFalse(folder.exists())
    }

    @Test
    fun aForeignFileInAnAssemblyFolderStays() {
        val v = vault()
        v.addVersion(console, game, file("Juniper Trail.pcr.sav", "FIRST"), null, null)
        val gameFolder = File(root, "Fern Handheld/Pocket Classic (PCL)/Juniper Trail")
        // An assembly folder left two hours ago, its list naming two files; a third one put there by another device.
        val partial = File(gameFolder, ".rl-partial-${clock.toEpochMilli() - 2 * 3_600_000}-left").apply { mkdirs() }
        File(partial, "ours.sav").writeText("OURS")
        File(partial, "changed.sav").writeText("CHANGED SINCE")
        File(partial, "theirs.sav").writeText("FROM ANOTHER DEVICE")
        File(partial, ".rl-partial.json").writeText(
            """{"files":[{"name":"ours.sav","sha256":"${sha("OURS".toByteArray())}"},{"name":"changed.sav","sha256":"${sha("ORIGINAL".toByteArray())}"}]}""",
        )
        clock = clock.plusSeconds(61)
        vault().addVersion(console, game, file("Juniper Trail.pcr.sav", "SECOND"), null, null)
        // Only our own file with its SHA-256 went; the foreign and the changed ones stay, with the list.
        assertEquals(setOf("changed.sav", "theirs.sav", ".rl-partial.json"), partial.list()!!.toSet())
    }

    /** A provider that refuses to delete the clock file of a version (and records the order of deletions). */
    private class StubbornStore(root: File) : FileVaultStore(root) {
        val deleted = mutableListOf<String>()
        override fun delete(entry: VaultEntry): Boolean {
            if (entry.name.endsWith(".rtc")) return false
            return super.delete(entry).also { if (it) deleted += entry.name }
        }
    }

    @Test
    fun anInterruptedPurgeLeavesOrphansNeverAListedVersion() {
        val store = StubbornStore(root)
        val v = vault(store, max = 1)
        val first = v.addVersion(console, game, file("Juniper Trail.pcr.sav", "ONE"), file("Juniper Trail.pcr.rtc", "R1"), null)
        v.addVersion(console, game, file("Juniper Trail.pcr.sav", "TWO"), file("Juniper Trail.pcr.rtc", "R2"), null)
        // The manifest went first, then the save; the clock file could not be deleted.
        val purged = store.deleted.dropWhile { it != ".version.json" }
        assertEquals(listOf(".version.json", "Juniper Trail.pcr.sav"), purged.take(2))
        val folder = File(root, "Fern Handheld/Pocket Classic (PCL)/Juniper Trail/${first.id}")
        assertEquals(listOf("Juniper Trail.pcr.rtc"), folder.list()!!.toList())
        // An orphan file, never listed nor counted.
        assertEquals(listOf(2), vault().versions(console.deviceId, game.gameId).map { it.info.sequence })
    }

    @Test
    fun identicalVersionsWithNfcAndNfdNamesCountOnce() {
        val v = vault()
        val bytes = "SAME"
        v.addVersion(console, game, file("Café.sav", bytes), null, null)
        clock = clock.plusSeconds(61)
        v.addVersion(console, game, file("Café.sav", bytes), null, null)
        // As Swift: the same file under two spellings of one name is one version (both kept).
        assertEquals(1, vault().versions(console.deviceId, game.gameId).size)
        assertEquals(2, File(root, "Fern Handheld/Pocket Classic (PCL)/Juniper Trail").list()!!.count { !it.startsWith(".") })
    }

    /** Counts the reads of the vault's files by name. */
    private class CountingStore(root: File) : FileVaultStore(root) {
        val reads = java.util.concurrent.ConcurrentHashMap<String, Int>()
        override fun openRead(file: VaultEntry): java.io.InputStream {
            reads.merge(file.name, 1, Int::plus)
            return super.openRead(file)
        }
    }

    @Test
    fun aSlotGoneFromTheConsoleIsNeitherReadNorHashed() = runBlocking {
        val store = CountingStore(root)
        sync(store = store).run()
        val all = demo.client.allStates().states
        val kept = all.first()
        // The console now lists one state only: the others stay in the vault, untouched.
        val real = ApiSyncSource { demo.client }
        val source = object : SaveSyncSource by real {
            override suspend fun allStates() = StateList(listOf(kept))
        }
        store.reads.clear()
        sync(source, store).run()
        val stateFiles = all.filter { it.id != kept.id }.map { it.path.substringAfterLast('/') }
        assertTrue("a slot gone from the console was read: ${store.reads}", stateFiles.none { (store.reads[it] ?: 0) > 0 })
        assertTrue((store.reads[kept.path.substringAfterLast('/')] ?: 0) > 0)
    }

    @Test
    fun aFolderLostDuringTheLastListingFailsTheSync() = runBlocking {
        val store = RevocableStore(root)
        sync(store = store).run()
        // Everything is copied; the permission goes while `GET /states` answers (an empty list): nothing
        // writes to the vault after that, and yet the sync says the folder is gone.
        val real = ApiSyncSource { demo.client }
        val source = object : SaveSyncSource by real {
            override suspend fun allStates(): StateList {
                store.armed = { true }
                return StateList(emptyList())
            }
        }
        try {
            sync(source, store).run()
            fail("a folder lost at the end passed for a sync")
        } catch (_: VaultUnavailableException) {
        }
    }
}
