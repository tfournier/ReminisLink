package link.reminis.core.demo

import java.io.ByteArrayOutputStream
import java.io.DataOutputStream
import java.util.zip.CRC32
import java.util.zip.DeflaterOutputStream

/**
 * Original pictures of the demo mode (box arts and state thumbnails): soft
 * gradients with a few bands, drawn here and encoded as PNG with the JDK
 * only (no game image, no product logo). Counterpart of `DemoArtwork.swift`.
 */
object DemoArtwork {
    /** A [width] x [height] PNG whose colours depend on [seed]. */
    fun png(width: Int, height: Int, seed: Int): ByteArray {
        val hue = ((seed * 47) % 360 + 360) % 360
        val (r1, g1, b1) = rgb(hue.toFloat(), 0.45f, 0.75f)
        val (r2, g2, b2) = rgb(((hue + 40) % 360).toFloat(), 0.55f, 0.35f)
        val raw = ByteArrayOutputStream()
        for (y in 0 until height) {
            raw.write(0) // no filter
            for (x in 0 until width) {
                val t = (x.toFloat() / width + y.toFloat() / height) / 2
                // Gentle hills: a darker band under a wave.
                val wave = height * (0.62f + 0.08f * kotlin.math.sin((x + seed * 13) / (width / 6f)))
                val shade = if (y > wave) 0.8f else 1f
                raw.write(((r1 + (r2 - r1) * t) * shade).toInt())
                raw.write(((g1 + (g2 - g1) * t) * shade).toInt())
                raw.write(((b1 + (b2 - b1) * t) * shade).toInt())
            }
        }
        val deflated = ByteArrayOutputStream().also { out -> DeflaterOutputStream(out).use { it.write(raw.toByteArray()) } }
        val png = ByteArrayOutputStream()
        png.write(byteArrayOf(0x89.toByte(), 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A))
        val header = ByteArrayOutputStream().also {
            DataOutputStream(it).apply {
                writeInt(width)
                writeInt(height)
                writeByte(8) // bit depth
                writeByte(2) // RGB
                writeByte(0)
                writeByte(0)
                writeByte(0)
            }
        }
        chunk(png, "IHDR", header.toByteArray())
        chunk(png, "IDAT", deflated.toByteArray())
        chunk(png, "IEND", ByteArray(0))
        return png.toByteArray()
    }

    private fun chunk(out: ByteArrayOutputStream, type: String, data: ByteArray) {
        val d = DataOutputStream(out)
        d.writeInt(data.size)
        val typeBytes = type.toByteArray(Charsets.US_ASCII)
        d.write(typeBytes)
        d.write(data)
        val crc = CRC32().apply { update(typeBytes); update(data) }
        d.writeInt(crc.value.toInt())
    }

    private fun rgb(h: Float, s: Float, v: Float): Triple<Float, Float, Float> {
        val c = v * s
        val x = c * (1 - kotlin.math.abs((h / 60) % 2 - 1))
        val m = v - c
        val (r, g, b) = when {
            h < 60 -> Triple(c, x, 0f)
            h < 120 -> Triple(x, c, 0f)
            h < 180 -> Triple(0f, c, x)
            h < 240 -> Triple(0f, x, c)
            h < 300 -> Triple(x, 0f, c)
            else -> Triple(c, 0f, x)
        }
        return Triple((r + m) * 255, (g + m) * 255, (b + m) * 255)
    }
}
