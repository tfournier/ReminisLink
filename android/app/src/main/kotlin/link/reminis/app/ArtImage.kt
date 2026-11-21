package link.reminis.app

import android.content.ContentResolver
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.net.Uri
import java.io.ByteArrayOutputStream
import java.io.FileNotFoundException

/** A picture that cannot be decoded. */
class UnreadablePictureException : Exception("unreadable picture")

/**
 * Box art for `PUT /games/{gameId}/art` (M8): the picture scaled down to
 * 1024 px at most on its longer side, as PNG of at most 4 MiB.
 */
object ArtImage {
    const val MAX_SIDE = 1024
    const val MAX_BYTES = 4 shl 20

    fun png(resolver: ContentResolver, uri: Uri): ByteArray {
        val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
        resolver.openInputStream(uri)?.use { BitmapFactory.decodeStream(it, null, bounds) } ?: throw FileNotFoundException("picture")
        if (bounds.outWidth <= 0 || bounds.outHeight <= 0) throw UnreadablePictureException()
        // Decoded at a power-of-two reduction close to the target, never the full picture.
        var sample = 1
        while (maxOf(bounds.outWidth, bounds.outHeight) / (sample * 2) >= MAX_SIDE) sample *= 2
        val decoded = resolver.openInputStream(uri)?.use {
            BitmapFactory.decodeStream(it, null, BitmapFactory.Options().apply { inSampleSize = sample })
        } ?: throw UnreadablePictureException()
        var side = minOf(MAX_SIDE, maxOf(decoded.width, decoded.height))
        while (true) {
            val scaled = scale(decoded, side)
            val out = ByteArrayOutputStream()
            scaled.compress(Bitmap.CompressFormat.PNG, 100, out)
            if (scaled !== decoded) scaled.recycle()
            if (out.size() <= MAX_BYTES || side <= 64) {
                decoded.recycle()
                return out.toByteArray()
            }
            side = side * 3 / 4
        }
    }

    private fun scale(bitmap: Bitmap, side: Int): Bitmap {
        val longer = maxOf(bitmap.width, bitmap.height)
        if (longer <= side) return bitmap
        val ratio = side.toFloat() / longer
        return Bitmap.createScaledBitmap(
            bitmap, (bitmap.width * ratio).toInt().coerceAtLeast(1), (bitmap.height * ratio).toInt().coerceAtLeast(1), true,
        )
    }
}
