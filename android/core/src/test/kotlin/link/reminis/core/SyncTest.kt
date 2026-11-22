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
        SaveVault(store, VaultLocation.INTERNAL, max, ZoneOffset.UTC) { clock }

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
}
