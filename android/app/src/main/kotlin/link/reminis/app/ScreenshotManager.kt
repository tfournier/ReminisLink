package link.reminis.app

import android.content.ContentValues
import android.content.Context
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.net.Uri
import android.os.Build
import android.provider.DocumentsContract
import android.provider.MediaStore
import android.util.LruCache
import java.io.File
import java.util.UUID
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Deferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.async
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit
import kotlinx.coroutines.withContext
import link.reminis.app.ui.Load
import link.reminis.app.ui.ScreenshotTooLargeException
import link.reminis.app.ui.screenshotsError
import link.reminis.core.ApiClient
import link.reminis.core.DownloadLimitExceededException
import link.reminis.core.DownloadSource
import link.reminis.core.Screenshot
import link.reminis.core.ScreenshotFormats
import link.reminis.core.ScreenshotList
import link.reminis.core.freeFileName
import link.reminis.core.trashScreenshotsInBatches

/**
 * The screenshots of one console (M22, mirror of `ScreenshotManager` of the
 * iOS app): the list, the previews (kept in memory, bounded), the image of
 * one screenshot (streamed to a temporary file, then decoded subsampled),
 * copies for the share sheet and the gallery, and moves to the trash of the
 * console. One per console, kept by [AppContainer] (shared by the grid and
 * the screen of one screenshot); dropped when the console is forgotten.
 */
class ScreenshotManager(
    private val context: Context,
    val deviceId: String,
    private val scope: CoroutineScope,
    /** A connected client for the console, asked again for every request. */
    private val client: suspend () -> ApiClient,
    /** The trash changed from here: its screen reloads. */
    private val onTrashChanged: () -> Unit = {},
) {
    private val _list = MutableStateFlow<Load<ScreenshotList>>(Load.Loading)
    val list: StateFlow<Load<ScreenshotList>> = _list.asStateFlow()

    /** Bumped when a screenshot left or came back: the screens drop what they selected and is gone. */
    private val _revision = MutableStateFlow(0)
    val revision: StateFlow<Int> = _revision.asStateFlow()

    /** The list was asked for once: a `screenshots` event (or a new stream) reloads it. */
    @Volatile
    private var wanted = false
    private var generation = 0

    val screenshots: List<Screenshot> get() = (_list.value as? Load.Loaded)?.value?.screenshots.orEmpty()

    fun find(id: String): Screenshot? = screenshots.firstOrNull { it.screenshotId == id }

    // MARK: list

    /** `GET /screenshots`; only the last load started publishes. [quiet]: a failure keeps the list shown. */
    fun load(quiet: Boolean = false) {
        wanted = true
        val current = ++generation
        if (!quiet && _list.value !is Load.Loaded) _list.value = Load.Loading
        scope.launch {
            val result = try {
                Load.Loaded(client().screenshots())
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                Load.Failed(screenshotsError(e, list = true))
            }
            if (current != generation) return@launch
            if (result is Load.Failed && quiet && _list.value is Load.Loaded) return@launch
            _list.value = result
            if (result is Load.Loaded) _revision.value++
        }
    }

    /** A `screenshots` event or a new stream (M14, M22): reloaded once the list was asked for. */
    fun changed() {
        if (wanted) load(quiet = true)
    }

    // MARK: previews

    /** Previews by [Screenshot.thumbnailKey], bounded by their size in memory. */
    private val thumbnails = object : LruCache<String, Bitmap>(THUMBNAIL_CACHE_BYTES) {
        override fun sizeOf(key: String, value: Bitmap) = value.allocationByteCount
    }

    /** Screenshots the pak makes no preview for (a BMP, or a `404`). */
    private val noPreview = java.util.Collections.synchronizedSet(mutableSetOf<String>())
    private val loads = mutableMapOf<String, Deferred<Bitmap?>>()
    private val previewPermits = Semaphore(4)

    fun cachedThumbnail(shot: Screenshot): Bitmap? = thumbnails.get(shot.thumbnailKey)

    /** The pak makes no preview for this screenshot (a BMP, or a `404` already received). */
    fun hasNoPreview(shot: Screenshot): Boolean = !shot.hasThumbnail || shot.thumbnailKey in noPreview

    /** The preview of a screenshot, from memory or the console; null when the pak makes none (a pictogram is shown). */
    suspend fun thumbnail(shot: Screenshot): Bitmap? {
        val key = shot.thumbnailKey
        thumbnails.get(key)?.let { return it }
        if (!shot.hasThumbnail || key in noPreview) return null
        // One request per preview, a few at a time.
        val load = synchronized(loads) {
            loads.getOrPut(key) {
                scope.async {
                    previewPermits.withPermit {
                        // A failure (network, busy console) is not remembered: the preview is asked again later.
                        val answer = runCatching { client().screenshotThumbnail(shot.screenshotId) }
                        val bytes = answer.getOrNull()
                        val bitmap = bytes?.let { withContext(Dispatchers.Default) { decodeBytes(it, THUMBNAIL_MAX_PIXELS) } }
                        when {
                            bitmap != null -> thumbnails.put(key, bitmap)
                            // `404` (the pak makes none) or an unreadable image: the pictogram stays.
                            answer.isSuccess -> noPreview += key
                        }
                        bitmap
                    }
                }.also { it.invokeOnCompletion { synchronized(loads) { loads.remove(key) } } }
            }
        }
        return load.await()
    }

    // MARK: the image of one screenshot

    /**
     * The image of a screenshot for its screen (Codex review of M22 on
     * iOS): downloaded to a temporary file (streamed, never in memory, at
     * most [MAX_PREVIEW_BYTES]), decoded subsampled (`inSampleSize`) to at
     * most [maxPixels] on its longest side, then the file deleted. Larger,
     * or no room left on the phone: [ScreenshotTooLargeException].
     */
    suspend fun displayImage(shot: Screenshot, maxPixels: Int): Bitmap {
        if (shot.sizeBytes > MAX_PREVIEW_BYTES) throw ScreenshotTooLargeException()
        val directory = File(context.cacheDir, "rl-shot-${UUID.randomUUID()}")
        try {
            val file = File(directory, "image")
            withContext(Dispatchers.IO) { directory.mkdirs() }
            try {
                withContext(Dispatchers.IO) {
                    file.outputStream().use { out -> client().download(DownloadSource.Screenshot(shot.screenshotId), out, MAX_PREVIEW_BYTES) }
                }
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                if (meansTooLarge(e)) throw ScreenshotTooLargeException(e)
                throw failure(e)
            }
            return withContext(Dispatchers.Default) { decodeFile(file, maxPixels) } ?: throw link.reminis.app.UnreadablePictureException()
        } finally {
            withContext(NonCancellable + Dispatchers.IO) { directory.deleteRecursively() }
        }
    }

    // MARK: copies (share sheet, gallery)

    /**
     * Downloads [shots] into one new directory of the share area (cache,
     * served by the FileProvider); two names the phone cannot keep apart
     * (another case only) get " (2)". Earlier copies are deleted first.
     */
    suspend fun exportForSharing(shots: List<Screenshot>): List<File> = withContext(Dispatchers.IO) {
        val root = sharedDirectory(context)
        root.listFiles().orEmpty().forEach { it.deleteRecursively() }
        val directory = File(root, UUID.randomUUID().toString()).apply { mkdirs() }
        val taken = mutableSetOf<String>()
        try {
            shots.map { shot ->
                val file = File(directory, freeFileName(safeName(shot.name), taken))
                try {
                    file.outputStream().use { out -> client().download(DownloadSource.Screenshot(shot.screenshotId), out) }
                } catch (e: kotlinx.coroutines.CancellationException) {
                    throw e
                } catch (e: Exception) {
                    throw failure(e)
                }
                file
            }
        } catch (e: Throwable) {
            directory.deleteRecursively()
            throw e
        }
    }

    /**
     * Saves [shots] to the gallery. Android 10 and later: MediaStore
     * (Pictures/ReminisLink), no permission; each image is written pending,
     * then published, and removed if its download fails. Android 8 and 9:
     * into [folder], a directory the user chose (`ACTION_OPEN_DOCUMENT_TREE`),
     * so no storage permission is ever asked. Returns how many were saved.
     */
    suspend fun saveToGallery(shots: List<Screenshot>, folder: Uri? = null): Int = withContext(Dispatchers.IO) {
        val resolver = context.contentResolver
        var saved = 0
        for (shot in shots) {
            val mime = mimeType(shot)
            val uri: Uri = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                val values = ContentValues().apply {
                    put(MediaStore.Images.Media.DISPLAY_NAME, safeName(shot.name))
                    put(MediaStore.Images.Media.MIME_TYPE, mime)
                    put(MediaStore.Images.Media.RELATIVE_PATH, "Pictures/ReminisLink")
                    put(MediaStore.Images.Media.IS_PENDING, 1)
                }
                resolver.insert(MediaStore.Images.Media.getContentUri(MediaStore.VOLUME_EXTERNAL_PRIMARY), values)
                    ?: throw LocalFileException(java.io.IOException("gallery refused the image"))
            } else {
                val tree = folder ?: throw LocalFileException(java.io.IOException("no folder"))
                val parent = DocumentsContract.buildDocumentUriUsingTree(tree, DocumentsContract.getTreeDocumentId(tree))
                DocumentsContract.createDocument(resolver, parent, mime, safeName(shot.name))
                    ?: throw LocalFileException(java.io.IOException("folder refused the image"))
            }
            try {
                val out = resolver.openOutputStream(uri, "w") ?: throw LocalFileException(java.io.FileNotFoundException("image"))
                try {
                    out.use { client().download(DownloadSource.Screenshot(shot.screenshotId), it) }
                } catch (e: kotlinx.coroutines.CancellationException) {
                    throw e
                } catch (e: LocalFileException) {
                    throw e
                } catch (e: Exception) {
                    throw failure(e)
                }
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                    resolver.update(uri, ContentValues().apply { put(MediaStore.Images.Media.IS_PENDING, 0) }, null, null)
                }
                saved++
            } catch (e: Throwable) {
                withContext(NonCancellable) { runCatching { DocumentSink.deleteDocument(context, uri) } }
                throw e
            }
        }
        saved
    }

    // MARK: trash

    /**
     * `POST /screenshots/trash` in batches of 500. The list reloads, and the
     * trash too, also after a failure (earlier batches, or screenshots the
     * pak says stayed in the trash, may have moved: a partial move says so).
     */
    suspend fun trash(shots: List<Screenshot>) {
        try {
            trashScreenshotsInBatches(shots.map { it.screenshotId }) { client().trashScreenshots(it) }
        } catch (e: kotlinx.coroutines.CancellationException) {
            throw e
        } catch (e: Exception) {
            load(quiet = true)
            onTrashChanged()
            throw screenshotsError(e)
        }
        val gone = shots.map { it.screenshotId }.toSet()
        (_list.value as? Load.Loaded)?.value?.let { current ->
            _list.value = Load.Loaded(current.copy(screenshots = current.screenshots.filterNot { it.screenshotId in gone }))
            _revision.value++
        }
        load(quiet = true)
        onTrashChanged()
    }

    /** A screenshot gone meanwhile reloads the list. */
    private fun failure(e: Exception): Throwable {
        val error = screenshotsError(e)
        if ((error as? link.reminis.app.ui.UserMessage)?.id == R.string.screenshot_gone) load(quiet = true)
        return error
    }

    companion object {
        /** The largest screenshot its screen shows (a larger one can still be shared or saved). */
        const val MAX_PREVIEW_BYTES = 64L shl 20

        /** Memory for the previews of one console (a few tens of KB each). */
        const val THUMBNAIL_CACHE_BYTES = 24 shl 20

        /** The previews are decoded at most this large (the pak makes 320 × 320 at most). */
        private const val THUMBNAIL_MAX_PIXELS = 640

        /** The share area (cache, served by the FileProvider of the manifest). */
        fun sharedDirectory(context: Context) = File(context.cacheDir, "shared")

        private fun mimeType(shot: Screenshot) = when (shot.format) {
            ScreenshotFormats.JPEG -> "image/jpeg"
            ScreenshotFormats.BMP -> "image/bmp"
            else -> "image/png"
        }

        /** A local file name from a console name: never a path (the name is the pak's, informative). */
        fun safeName(name: String): String =
            name.substringAfterLast('/').replace(Regex("[\\\\\u0000-\u001f]"), "_").trim().ifEmpty { "screenshot.png" }

        /** The download stopped at its limit, or the phone ran out of room. */
        fun meansTooLarge(e: Throwable): Boolean = generateSequence(e) { it.cause }.any {
            it is DownloadLimitExceededException ||
                (it is android.system.ErrnoException && it.errno == android.system.OsConstants.ENOSPC) ||
                (it is java.io.IOException && it.message?.contains("ENOSPC") == true)
        }

        /** The power of two that brings [width] × [height] to at most [maxPixels] on the longest side. */
        fun sampleSize(width: Int, height: Int, maxPixels: Int): Int {
            var sample = 1
            val longest = maxOf(width, height)
            while (longest / (sample * 2) >= maxPixels.coerceAtLeast(1)) sample *= 2
            return sample
        }

        private fun decodeBytes(bytes: ByteArray, maxPixels: Int): Bitmap? {
            val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
            BitmapFactory.decodeByteArray(bytes, 0, bytes.size, bounds)
            if (bounds.outWidth <= 0 || bounds.outHeight <= 0) return null
            val options = BitmapFactory.Options().apply { inSampleSize = sampleSize(bounds.outWidth, bounds.outHeight, maxPixels) }
            return BitmapFactory.decodeByteArray(bytes, 0, bytes.size, options)
        }

        /** Decodes an image file at most about [maxPixels] on its longest side (subsampled while it is read). */
        fun decodeFile(file: File, maxPixels: Int): Bitmap? {
            val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
            BitmapFactory.decodeFile(file.path, bounds)
            if (bounds.outWidth <= 0 || bounds.outHeight <= 0) return null
            val options = BitmapFactory.Options().apply { inSampleSize = sampleSize(bounds.outWidth, bounds.outHeight, maxPixels) }
            return BitmapFactory.decodeFile(file.path, options)
        }
    }
}
