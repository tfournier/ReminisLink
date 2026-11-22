package link.reminis.app

import android.content.ContentResolver
import android.graphics.BitmapFactory
import android.net.Uri
import com.google.zxing.BarcodeFormat
import com.google.zxing.BinaryBitmap
import com.google.zxing.DecodeHintType
import com.google.zxing.MultiFormatReader
import com.google.zxing.NotFoundException
import com.google.zxing.RGBLuminanceSource
import com.google.zxing.common.GlobalHistogramBinarizer
import com.google.zxing.common.HybridBinarizer

/**
 * The pairing link of a picture of the QR code (iOS M15 "Import a
 * Picture"), chosen with the system photo picker (`PickVisualMedia`, no
 * permission) and decoded with ZXing core. The picture is never decoded at
 * full size: 50 MB and 200 megapixels at most, scaled down to 3000 px.
 * Whatever is read goes through the same check as the scanner
 * (`PairingLink.parse`, by the caller): no other way to a pairing.
 */
object QrImage {
    const val MAX_BYTES = 50L * 1000 * 1000
    const val MAX_PIXELS = 200L * 1000 * 1000
    const val MAX_SIDE = 3000

    /**
     * The first `reminislink:` text of the picture, null when there is none
     * (or it cannot be read). The picture is first copied into [scratch]
     * (a new file, deleted at the end), [MAX_BYTES] at most whatever the
     * provider declares; its size and its pixels are then read from the
     * same bytes (Codex review 1).
     */
    fun link(resolver: ContentResolver, uri: Uri, scratch: java.io.File): String? {
        scratch.mkdirs()
        val copy = java.io.File.createTempFile("rl-qr-", ".img", scratch)
        try {
            val input = resolver.openInputStream(uri) ?: return null
            val complete = input.use { inp ->
                copy.outputStream().use { out ->
                    val buffer = ByteArray(64 * 1024)
                    var total = 0L
                    while (true) {
                        val n = inp.read(buffer)
                        if (n < 0) break
                        total += n
                        // More than allowed: never read further.
                        if (total > MAX_BYTES) return@use false
                        out.write(buffer, 0, n)
                    }
                    true
                }
            }
            if (!complete) return null
            return linkInFile(copy)
        } finally {
            copy.delete()
        }
    }

    /** The link of a picture file whose size is checked before it is decoded scaled down. */
    fun linkInFile(file: java.io.File): String? {
        if (file.length() > MAX_BYTES) return null
        val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
        // With inJustDecodeBounds, decodeFile returns null by design: the size is in the options.
        BitmapFactory.decodeFile(file.path, bounds)
        if (bounds.outWidth <= 0 || bounds.outHeight <= 0) return null
        if (bounds.outWidth.toLong() * bounds.outHeight > MAX_PIXELS) return null
        var sample = 1
        while (maxOf(bounds.outWidth, bounds.outHeight) / sample > MAX_SIDE) sample *= 2
        val bitmap = BitmapFactory.decodeFile(file.path, BitmapFactory.Options().apply { inSampleSize = sample }) ?: return null
        val pixels = IntArray(bitmap.width * bitmap.height)
        bitmap.getPixels(pixels, 0, bitmap.width, 0, 0, bitmap.width, bitmap.height)
        val source = RGBLuminanceSource(bitmap.width, bitmap.height, pixels)
        bitmap.recycle()
        return decode(source)
    }

    private fun decode(source: com.google.zxing.LuminanceSource): String? {
        val reader = MultiFormatReader().apply {
            setHints(mapOf(DecodeHintType.POSSIBLE_FORMATS to listOf(BarcodeFormat.QR_CODE), DecodeHintType.TRY_HARDER to true))
        }
        // A photo of a screen: both binarizers, and the inverted picture (light code on a dark screen).
        for (candidate in listOf(source, source.invert())) {
            for (bitmap in listOf(BinaryBitmap(HybridBinarizer(candidate)), BinaryBitmap(GlobalHistogramBinarizer(candidate)))) {
                val text = try {
                    reader.decodeWithState(bitmap).text
                } catch (_: NotFoundException) {
                    null
                } catch (_: com.google.zxing.ReaderException) {
                    null
                } finally {
                    reader.reset()
                }
                if (text != null && text.startsWith("reminislink:", ignoreCase = true)) return text
            }
        }
        return null
    }
}
