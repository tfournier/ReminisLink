package link.reminis.core.demo

import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.util.zip.CRC32
import java.util.zip.ZipEntry
import java.util.zip.ZipException
import java.util.zip.ZipInputStream
import java.util.zip.ZipOutputStream

/**
 * Real zip archives for the game archives of the demo mode (M19), with the
 * JDK only (counterpart of `FakeZip` of the iOS demo): an archive exported
 * from the fake pak is a true zip with the manifest of the contract, so it
 * can be imported into the demo again (or opened on a computer).
 */
object DemoZip {
    /** The most entries, and the most bytes all entries together may hold, when reading. */
    const val MAX_ENTRIES = 512
    const val MAX_TOTAL_BYTES = FakePak.MAX_KEPT_ARCHIVE_BYTES

    /** A zip of [entries] (in order); [deflate]: compressed entries, else stored. */
    fun write(entries: List<Pair<String, ByteArray>>, deflate: Boolean = false): ByteArray {
        val out = ByteArrayOutputStream()
        ZipOutputStream(out).use { zip ->
            for ((name, data) in entries) {
                val entry = ZipEntry(name)
                if (!deflate) {
                    entry.method = ZipEntry.STORED
                    entry.size = data.size.toLong()
                    entry.compressedSize = data.size.toLong()
                    entry.crc = CRC32().apply { update(data) }.value
                }
                // A fixed date: two exports of the same data are the same bytes.
                entry.time = FIXED_TIME
                zip.putNextEntry(entry)
                zip.write(data)
                zip.closeEntry()
            }
        }
        return out.toByteArray()
    }

    /**
     * The files of a zip by entry name, null when it is not one, is damaged,
     * or holds more than [MAX_ENTRIES] entries or [MAX_TOTAL_BYTES] bytes
     * (never more than that is inflated: a hostile archive cannot fill the
     * memory). Folders are skipped; a name seen twice is refused.
     */
    fun read(bytes: ByteArray): Map<String, ByteArray>? = try {
        val files = linkedMapOf<String, ByteArray>()
        var total = 0L
        var count = 0
        ZipInputStream(ByteArrayInputStream(bytes)).use { zip ->
            val buffer = ByteArray(64 * 1024)
            while (true) {
                val entry = zip.nextEntry ?: break
                if (++count > MAX_ENTRIES) return null
                if (entry.isDirectory) continue
                if (files.containsKey(entry.name)) return null
                // Sized from the entry when it says (no growth copies).
                val data = ByteArrayOutputStream(entry.size.takeIf { it in 0..MAX_TOTAL_BYTES }?.toInt() ?: 8192)
                while (true) {
                    val n = zip.read(buffer)
                    if (n < 0) break
                    total += n
                    if (total > MAX_TOTAL_BYTES) return null
                    data.write(buffer, 0, n)
                }
                files[entry.name] = data.toByteArray()
            }
        }
        if (count == 0) null else files
    } catch (_: ZipException) {
        null
    } catch (_: java.io.IOException) {
        null
    } catch (_: IllegalArgumentException) {
        null
    }

    /** 2026-11-21 00:00 local time of the JVM: only a stable value matters. */
    private const val FIXED_TIME = 1_795_219_200_000L
}
