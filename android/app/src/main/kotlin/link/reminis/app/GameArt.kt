package link.reminis.app

import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.util.LruCache
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.Dp
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.withContext

/**
 * The box art of the games shown (iOS: the cover in the library and on the
 * game screen, cached by game). `GET /games/{gameId}/art`, read four at a
 * time, decoded scaled down, and kept in memory (24 MiB at most); a
 * console's entries are dropped when its library changes (a `library`
 * event, an art set or removed from this phone), so a new art is read
 * again. A game without art (`404`) is remembered as such.
 */
class GameArtCache {
    private val bitmaps = object : LruCache<String, Bitmap>(24 shl 20) {
        override fun sizeOf(key: String, value: Bitmap) = value.byteCount
    }
    private val missing = java.util.Collections.synchronizedSet(mutableSetOf<String>())
    private val _revisions = kotlinx.coroutines.flow.MutableStateFlow<Map<String, Int>>(emptyMap())

    /** Bumped for a console when its arts may have changed (the art views read theirs again). */
    val revisions: kotlinx.coroutines.flow.StateFlow<Map<String, Int>> = _revisions
    private val permits = Semaphore(4)

    private fun key(deviceId: String, gameId: String, side: Int) = "$deviceId/${_revisions.value[deviceId] ?: 0}/$gameId/$side"

    fun cached(deviceId: String, gameId: String, side: Int): Bitmap? = bitmaps.get(key(deviceId, gameId, side))

    /** The console's arts may have changed: they are read again when shown. */
    fun invalidate(deviceId: String) {
        _revisions.update { it + (deviceId to (it[deviceId] ?: 0) + 1) }
    }

    /** The art of a game, decoded to at most [side] px, null when it has none or it cannot be read. */
    suspend fun load(client: suspend () -> link.reminis.core.ApiClient, deviceId: String, gameId: String, side: Int): Bitmap? {
        val key = key(deviceId, gameId, side)
        bitmaps.get(key)?.let { return it }
        if (key in missing) return null
        return permits.withPermit {
            bitmaps.get(key)?.let { return@withPermit it }
            val bytes = try {
                client().gameArt(gameId)
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (_: Exception) {
                return@withPermit null
            }
            val bitmap = bytes?.let { withContext(Dispatchers.Default) { decode(it, side) } }
            if (bitmap == null) missing += key else bitmaps.put(key, bitmap)
            bitmap
        }
    }

    private fun decode(bytes: ByteArray, side: Int): Bitmap? {
        val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
        BitmapFactory.decodeByteArray(bytes, 0, bytes.size, bounds)
        if (bounds.outWidth <= 0 || bounds.outHeight <= 0) return null
        var sample = 1
        while (maxOf(bounds.outWidth, bounds.outHeight) / (sample * 2) >= side) sample *= 2
        return BitmapFactory.decodeByteArray(bytes, 0, bytes.size, BitmapFactory.Options().apply { inSampleSize = sample })
    }
}

/** The art of a game in a rounded box of [size] (an empty box while it loads or when it has none). */
@Composable
fun GameArt(deviceId: String, gameId: String, hasArt: Boolean, size: Dp, modifier: Modifier = Modifier) {
    val context = LocalContext.current
    val container = context.container
    val revisions by container.artCache.revisions.collectAsState()
    val revision = revisions[deviceId] ?: 0
    val side = with(androidx.compose.ui.platform.LocalDensity.current) { size.roundToPx() }.coerceIn(48, 512)
    var bitmap by remember(deviceId, gameId, revision) { mutableStateOf(container.artCache.cached(deviceId, gameId, side)) }
    if (hasArt) {
        LaunchedEffect(deviceId, gameId, revision) {
            bitmap = container.artCache.load({ container.client(deviceId) }, deviceId, gameId, side)
        }
    }
    val shape = RoundedCornerShape(6)
    Box(modifier.size(size).clip(shape).background(MaterialTheme.colorScheme.surfaceVariant)) {
        val shown = bitmap.takeIf { hasArt }
        if (shown != null) {
            Image(
                shown.asImageBitmap(), contentDescription = null, contentScale = ContentScale.Crop,
                modifier = Modifier.size(size).testTag("gameArt"),
            )
        }
    }
}
