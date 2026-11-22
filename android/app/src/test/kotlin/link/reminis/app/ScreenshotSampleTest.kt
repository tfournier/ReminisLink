package link.reminis.app

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** The subsampling of a screenshot shown whole (Codex review round 1 of the Android increment 5). */
class ScreenshotSampleTest {
    private fun bytes(w: Int, h: Int, sample: Int) = ((w + sample - 1) / sample).toLong() * ((h + sample - 1) / sample) * 4

    @Test
    fun bothSidesAndTheMemoryStayUnderTheirLimits() {
        // 8000 × 8000 with a 4096 px limit: 2 (4000 × 4000), never 1 (244 MiB).
        assertEquals(2, ScreenshotManager.sampleSize(8000, 8000, 4096))
        assertEquals(1, ScreenshotManager.sampleSize(1024, 768, 2400))
        assertEquals(1, ScreenshotManager.sampleSize(2400, 1080, 2400))
        assertEquals(2, ScreenshotManager.sampleSize(2401, 1080, 2400))
        // A very long strip: the long side decides.
        assertEquals(8, ScreenshotManager.sampleSize(30000, 100, 4096))
        for ((w, h, limit) in listOf(Triple(8000, 8000, 4096), Triple(16384, 16384, 4096), Triple(5000, 4000, 4096), Triple(640, 480, 320))) {
            val s = ScreenshotManager.sampleSize(w, h, limit)
            assertTrue("$w x $h / $s", (w + s - 1) / s <= limit && (h + s - 1) / s <= limit)
            assertTrue("$w x $h / $s", bytes(w, h, s) <= ScreenshotManager.MAX_BITMAP_BYTES)
            assertTrue("the first such factor", s == 1 || (w + s / 2 - 1) / (s / 2) > limit || (h + s / 2 - 1) / (s / 2) > limit || bytes(w, h, s / 2) > ScreenshotManager.MAX_BITMAP_BYTES)
        }
    }
}
