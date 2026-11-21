package link.reminis.core

import java.nio.file.Files
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

class PairingLinkTest {
    private val good = "reminislink://pair?v=2&id=7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10&n=Brick" +
        "&s=MFYV2qsgQsnXkx1_HjfG6_kQ_eaoZSvF4sH1BjyVfWA&h=192.168.1.42&p=47821&k=dx55JGi5v9aA8sTa_6qpXzS1I0iDjz86z8UfYi-fGIw"

    private fun reason(link: String): PairingLinkException.Reason? = try {
        PairingLink.parse(link); null
    } catch (e: PairingLinkException) {
        e.reason
    }

    @Test
    fun acceptsTheGoodLinkWithSpacesAround() {
        assertEquals("Brick", PairingLink.parse("  $good\n").deviceName)
    }

    @Test
    fun versions() {
        assertEquals(PairingLinkException.Reason.OUTDATED_PAK, reason(good.replace("v=2", "v=1")))
        assertEquals(PairingLinkException.Reason.NEWER_VERSION, reason(good.replace("v=2", "v=3")))
    }

    @Test
    fun refusesMalformedLinks() {
        val invalid = PairingLinkException.Reason.INVALID
        assertEquals(invalid, reason(good.replace("reminislink://", "https://")))
        assertEquals(invalid, reason(good.replace("://pair", "://other")))
        assertEquals(invalid, reason("$good#frag"))
        assertEquals(invalid, reason("$good&v=2"))
        assertEquals(invalid, reason(good.replace("7f3a9c2e", "7F3A9C2E")))
        assertEquals(invalid, reason(good.replace("&p=47821", "")))
        assertEquals(invalid, reason(good.replace("192.168.1.42", "192.168.01.42")))
        assertEquals(invalid, reason(good.replace("47821", "70000")))
        assertEquals(invalid, reason(good.replace("&k=dx55JGi5v9aA8sTa_6qpXzS1I0iDjz86z8UfYi-fGIw", "&k=dx55")))
        assertEquals(invalid, reason(good.replace("n=Brick", "n=Br ick")))
        assertEquals(invalid, reason(good.replace("n=Brick", "n=%zz")))
        assertEquals(invalid, reason(good.replace("n=Brick", "n=%C3")))
    }

    @Test
    fun linkWithoutAddressIsAccepted() {
        val link = PairingLink.parse(good.replace("&h=192.168.1.42&p=47821", ""))
        assertNull(link.endpoint)
        assertEquals(link, PairingLink.parse(link.toUri()))
    }

    @Test
    fun clientNameIsTrimmedAndBounded() {
        assertEquals("Android", Pairing.clientName("   "))
        assertEquals("Pixel", Pairing.clientName(" Pixel "))
        val emoji = "🎮".repeat(70)
        val cut = Pairing.clientName(emoji)
        assertEquals(64, cut.codePointCount(0, cut.length))
    }
}

class ConsoleStoreTest {
    @Test
    fun putUpdateRemove() {
        val dir = Files.createTempDirectory("consoles").toFile()
        val store = ConsoleStore(dir.resolve("consoles.json"))
        assertTrue(store.all().isEmpty())
        val console = PairedConsole("id-1", "Brick", "brick", "pin", Endpoint("10.0.0.7", 47821), 1)
        store.put(console)
        store.put(console.copy(deviceName = "Brick de Zoé"))
        assertEquals(listOf("Brick de Zoé"), ConsoleStore(dir.resolve("consoles.json")).all().map { it.deviceName })
        store.update("id-1") { it.copy(lastEndpoint = Endpoint("10.0.0.8", 47822)) }
        assertEquals(47822, store.get("id-1")?.lastEndpoint?.port)
        store.remove("id-1")
        assertTrue(store.all().isEmpty())
        assertTrue(dir.listFiles()!!.none { it.name.endsWith(".tmp") })
        dir.deleteRecursively()
    }
}

class LocatorTest {
    private val fallback = Endpoint("10.0.2.2", 47821)

    @Test
    fun discoveryWins() = runBlocking {
        assertEquals(Endpoint("192.168.1.9", 47821), Locator.locate(fallback, 1_000) { Endpoint("192.168.1.9", 47821) })
    }

    @Test
    fun fallbackAfterTimeout() = runBlocking {
        val found = Locator.locate(fallback, 100) {
            delay(10_000); Endpoint("192.168.1.9", 47821)
        }
        assertEquals(fallback, found)
    }

    @Test
    fun nothingWithoutFallback() = runBlocking {
        assertNull(Locator.locate(null, 50) { null })
    }
}

class ConnectFirstTest {
    private val spoofed = Endpoint("192.168.1.66", 47821)
    private val real = Endpoint("192.168.1.42", 47821)

    @Test
    fun spoofedAnnouncementFallsBackToTheValidatedAddress() = runBlocking {
        val tried = mutableListOf<Endpoint>()
        val (endpoint, value) = Locator.connectFirst(listOf(spoofed, real)) {
            tried += it
            if (it == spoofed) throw ApiException.IdentityChanged() else "hello"
        }
        assertEquals(real, endpoint)
        assertEquals("hello", value)
        assertEquals(listOf(spoofed, real), tried)
    }

    @Test
    fun identityErrorWinsWhenNothingValidates() = runBlocking {
        try {
            Locator.connectFirst(listOf(spoofed, real)) {
                if (it == spoofed) throw ApiException.IdentityChanged() else throw ApiException.Network(java.io.IOException("down"))
            }
            fail()
        } catch (_: ApiException.IdentityChanged) {
        }
    }

    @Test
    fun consoleErrorsStop() = runBlocking {
        var calls = 0
        try {
            Locator.connectFirst(listOf(real, spoofed)) { calls++; throw ApiException.Incompatible(3, "9.0.0") }
            fail()
        } catch (_: ApiException.Incompatible) {
        }
        assertEquals(1, calls)
    }

    @Test
    fun noCandidate() = runBlocking {
        try {
            Locator.connectFirst(emptyList<Endpoint>()) { }
            fail()
        } catch (_: ConsoleNotFoundException) {
        }
    }

    @Test
    fun candidatesAreMdnsThenFallback() = runBlocking {
        assertEquals(listOf(spoofed, real), Locator.candidates(real, 1_000) { spoofed })
        assertEquals(listOf(real), Locator.candidates(real, 1_000) { real })
        assertEquals(listOf(real), Locator.candidates(real, 50) { delay(5_000); spoofed })
    }
}

class SignerRulesTest {
    private val signer = Signer("3b1c8d2a-5e6f-4a7b-8c9d-0e1f2a3b4c5d", SeedEd25519Key(ByteArray(32)))

    @Test
    fun duplicateKeysAreRefusedWhenBuilding() {
        try {
            Signer.encodeQuery(listOf("q" to "a", "q" to "b")); fail()
        } catch (e: SignerException) {
            assertEquals(SignerException.Reason.DUPLICATE_KEY, e.reason)
        }
        assertEquals("a=1&b=2", Signer.encodeQuery(listOf("b" to "2", "a" to "1")))
        // The canonicalization itself stays independent (the vectors have a duplicate key).
        assertEquals("a=10&a=2", Signer.canonicalQuery("a=2&a=10"))
        // A raw query with the same decoded key twice is not signed either.
        for (q in listOf("a=1&a=2", "a=1&%61=2", "a+b=1&a%20b=2", "flag&flag=")) {
            try {
                signer.headers("GET", "/api/v1/systems", q, time = 1); fail(q)
            } catch (e: SignerException) {
                assertEquals(SignerException.Reason.DUPLICATE_KEY, e.reason)
            }
        }
        signer.headers("GET", "/api/v1/systems", "a=1&b=1", time = 1)
    }

    @Test
    fun unsignedOnlyForUploadChunks() {
        signer.headers("PUT", "/api/v1/uploads/up_1", unsignedBody = true, time = 1)
        for ((method, path) in listOf("POST" to "/api/v1/uploads/up_1", "PUT" to "/api/v1/games/x/save", "PUT" to "/api/v1/uploads/up_1/complete", "PUT" to "/api/v1/uploads")) {
            try {
                signer.headers(method, path, unsignedBody = true, time = 1)
                fail("$method $path accepted UNSIGNED")
            } catch (e: SignerException) {
                assertEquals(SignerException.Reason.UNSIGNED_BODY, e.reason)
            }
        }
    }
}

class LibrarySearchTest {
    private fun game(name: String) = LibraryGame(
        id = name, name = name, fileName = "$name.gba", kind = "file", sizeBytes = 1, modifiedAt = "2026-11-18T16:57:40Z",
        hasSave = false, stateCount = 0, cheatCount = 0, systemId = "R0JB",
    )

    @Test
    fun accentAndCaseInsensitive() {
        assertTrue(LibrarySearch.matches("Café Rêverie", "cafe rev"))
        assertTrue(LibrarySearch.matches("Sky Garden Quest", ""))
        assertTrue(!LibrarySearch.matches("Sky Garden Quest", "dx"))
        val list = LibrarySearch.filter(listOf(game("Zelda"), game("Éclair"), game("Mario")), "")
        assertEquals(listOf("Éclair", "Mario", "Zelda"), list.map { it.name })
    }
}

class Base64UrlTest {
    @Test
    fun roundTrip() {
        for (n in 0..40) {
            val bytes = ByteArray(n) { (it * 37 + n).toByte() }
            val text = Base64Url.encode(bytes)
            assertEquals(java.util.Base64.getUrlEncoder().withoutPadding().encodeToString(bytes), text)
            assertTrue(bytes.contentEquals(Base64Url.decode(text)))
        }
        try {
            SeedEd25519Key(ByteArray(31)); fail()
        } catch (_: IllegalArgumentException) {
        }
    }
}
