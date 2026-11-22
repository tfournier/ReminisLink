package link.reminis.app

import android.content.ContentResolver
import android.content.Context
import android.net.Uri
import android.provider.DocumentsContract
import android.provider.OpenableColumns
import java.io.File
import java.io.FileNotFoundException
import java.io.OutputStream
import java.text.Normalizer
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.withContext
import link.reminis.core.DownloadSink

/** A document of the phone could not be read or written (picker, share sheet, download destination). */
class LocalFileException(cause: Throwable) : Exception("local file: ${cause.javaClass.simpleName}", cause)

/** A file chosen with the system picker or shared by another app, copied into the app. */
data class PickedFile(val file: File, val name: String, val sizeBytes: Long) {
    /** Deletes the copy and its directory. */
    fun remove() {
        file.parentFile?.deleteRecursively()
    }
}

/**
 * Files read through the Storage Access Framework (no storage permission):
 * the document picker, the share sheet, and the documents a download writes.
 */
object LocalFiles {
    /**
     * The display name of a document, NFC (the console compares names in
     * NFC). Only shown and sent as `fileName` (the console validates it);
     * never used as a local path.
     */
    fun displayName(resolver: ContentResolver, uri: Uri): String {
        val name = runCatching {
            resolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { c ->
                if (c.moveToFirst()) c.getString(0) else null
            }
        }.getOrNull() ?: uri.lastPathSegment.orEmpty()
        val last = name.substringAfterLast('/').trim().ifEmpty { "file" }
        return Normalizer.normalize(last, Normalizer.Form.NFC)
    }

    /** The tree chosen is not a folder whose name ends with `.pak`, or it is larger than a pak may be. */
    class NotAPakFolderException(message: String) : Exception(message)

    /**
     * Zips a folder whose name ends with `.pak` (`ACTION_OPEN_DOCUMENT_TREE`;
     * iOS zips it with `NSFileCoordinator .forUploading`) into a new
     * directory of [root], named `<Name>.pak.zip`, its entries under
     * `<Name>.pak/` (a layout the pak accepts). Read through the Storage
     * Access Framework only: no storage permission, no extra library.
     */
    suspend fun zipPakFolder(context: Context, tree: Uri, root: File): PickedFile {
        val directory = File(root, UUID.randomUUID().toString())
        var handed = false
        try {
            val picked = withContext(Dispatchers.IO) {
                val resolver = context.contentResolver
                val rootId = DocumentsContract.getTreeDocumentId(tree)
                val folderName = displayName(resolver, DocumentsContract.buildDocumentUriUsingTree(tree, rootId))
                if (!folderName.lowercase().endsWith(".pak") || folderName.length <= 4) throw NotAPakFolderException("not a .pak folder")
                if (!directory.mkdirs()) throw LocalFileException(FileNotFoundException("transfer directory"))
                val destination = File(directory, "file")
                var entries = 0
                var bytes = 0L
                try {
                    java.util.zip.ZipOutputStream(destination.outputStream().buffered()).use { zip ->
                        fun add(parentId: String, prefix: String) {
                            val columns = arrayOf(
                                DocumentsContract.Document.COLUMN_DOCUMENT_ID,
                                DocumentsContract.Document.COLUMN_DISPLAY_NAME,
                                DocumentsContract.Document.COLUMN_MIME_TYPE,
                            )
                            val rows = mutableListOf<Triple<String, String, String>>()
                            resolver.query(DocumentsContract.buildChildDocumentsUriUsingTree(tree, parentId), columns, null, null, null)?.use { c ->
                                while (c.moveToNext()) rows += Triple(c.getString(0), c.getString(1).orEmpty(), c.getString(2).orEmpty())
                            }
                            for ((id, rawName, mime) in rows.sortedBy { it.second.lowercase() }) {
                                val name = Normalizer.normalize(rawName, Normalizer.Form.NFC)
                                // Never a path: a name with a separator, or a dot name, is skipped.
                                if (name.isEmpty() || name == "." || name == ".." || name.contains('/') || name.contains('\\')) continue
                                if (++entries > PakFolderZip.MAX_ENTRIES) throw NotAPakFolderException("too many entries")
                                if (mime == DocumentsContract.Document.MIME_TYPE_DIR) {
                                    zip.putNextEntry(java.util.zip.ZipEntry("$prefix$name/"))
                                    zip.closeEntry()
                                    add(id, "$prefix$name/")
                                    continue
                                }
                                zip.putNextEntry(java.util.zip.ZipEntry("$prefix$name"))
                                val input = resolver.openInputStream(DocumentsContract.buildDocumentUriUsingTree(tree, id))
                                    ?: throw FileNotFoundException("document")
                                input.use { inp ->
                                    val buffer = ByteArray(256 * 1024)
                                    while (true) {
                                        val n = inp.read(buffer)
                                        if (n < 0) break
                                        bytes += n
                                        if (bytes > PakFolderZip.MAX_BYTES) throw NotAPakFolderException("too large")
                                        zip.write(buffer, 0, n)
                                    }
                                }
                                zip.closeEntry()
                            }
                        }
                        add(rootId, "$folderName/")
                    }
                    PickedFile(destination, "$folderName.zip", destination.length())
                } catch (e: kotlinx.coroutines.CancellationException) {
                    throw e
                } catch (e: NotAPakFolderException) {
                    throw e
                } catch (e: LocalFileException) {
                    throw e
                } catch (e: Exception) {
                    throw LocalFileException(e)
                }
            }
            kotlinx.coroutines.currentCoroutineContext().ensureActive()
            handed = true
            return picked
        } finally {
            if (!handed) withContext(kotlinx.coroutines.NonCancellable + Dispatchers.IO) { directory.deleteRecursively() }
        }
    }

    /**
     * Copies a document into a new directory of [root] (`<uuid>/file`), so
     * an upload can be resumed after the picker's access ends. The local
     * name never comes from the document.
     */
    suspend fun copyIn(context: Context, uri: Uri, root: File): PickedFile {
        // The directory is known before the copy starts: whatever ends this call (a failure, or a
        // cancellation even after the copy finished, which drops withContext's result), it is
        // deleted unless the copy is handed back to a caller still running.
        val directory = File(root, UUID.randomUUID().toString())
        var handed = false
        try {
            val picked = withContext(Dispatchers.IO) {
                val resolver = context.contentResolver
                val name = displayName(resolver, uri)
                if (!directory.mkdirs()) throw LocalFileException(FileNotFoundException("transfer directory"))
                val destination = File(directory, "file")
                try {
                    val input = resolver.openInputStream(uri) ?: throw FileNotFoundException("document")
                    input.use { inp -> destination.outputStream().use { out -> inp.copyTo(out, 256 * 1024) } }
                    PickedFile(destination, name, destination.length())
                } catch (e: kotlinx.coroutines.CancellationException) {
                    throw e
                } catch (e: LocalFileException) {
                    throw e
                } catch (e: Exception) {
                    throw LocalFileException(e)
                }
            }
            kotlinx.coroutines.currentCoroutineContext().ensureActive()
            handed = true
            return picked
        } finally {
            if (!handed) withContext(kotlinx.coroutines.NonCancellable + Dispatchers.IO) { directory.deleteRecursively() }
        }
    }
}

/** The pak's own limits (1 GiB, 10 000 entries) for a zipped `.pak` folder: beyond, the console refuses it anyway. */
internal object PakFolderZip {
    const val MAX_BYTES = 1L shl 30
    const val MAX_ENTRIES = 10_000
}

/** A download written into a document created by the user (`ACTION_CREATE_DOCUMENT`). */
class DocumentSink(private val context: Context, private val uri: Uri) : DownloadSink {
    override fun open(): OutputStream = try {
        context.contentResolver.openOutputStream(uri, "wt") ?: throw FileNotFoundException("document")
    } catch (e: Exception) {
        throw LocalFileException(e)
    }

    /**
     * Flushed, synced to storage when the provider gives a regular file (a
     * provider writing through a pipe cannot be synced: its own close
     * commits the document), then closed. A write, sync or close error
     * fails the download.
     */
    override fun finish(out: OutputStream) = try {
        out.flush()
        val fd = (out as? java.io.FileOutputStream)?.fd
        if (fd != null && isRegularFile(fd)) fd.sync()
        out.close()
    } catch (e: Exception) {
        throw LocalFileException(e)
    }

    private fun isRegularFile(fd: java.io.FileDescriptor): Boolean = try {
        android.system.OsConstants.S_ISREG(android.system.Os.fstat(fd).st_mode)
    } catch (_: android.system.ErrnoException) {
        false
    }

    override fun discard(): Boolean = deleteDocument(context, uri)

    companion object {
        /** Deletes a document; true when it is gone (deleted, or already missing). */
        fun deleteDocument(context: Context, uri: Uri): Boolean = try {
            DocumentsContract.deleteDocument(context.contentResolver, uri)
        } catch (_: FileNotFoundException) {
            true
        } catch (_: Exception) {
            // Not a document URI (e.g. MediaStore): delete through the resolver.
            runCatching { context.contentResolver.delete(uri, null, null) > 0 }.getOrDefault(false)
        }
    }
}

/**
 * Files shared by another app (`ACTION_SEND`), kept in memory behind an
 * opaque token until the Add screen copies them. Like the pairing links,
 * nothing goes into navigation arguments or saved state; after a process
 * restart, share the files again.
 */
class ShareInbox {
    private val requests = ConcurrentHashMap<String, List<Uri>>()

    fun put(uris: List<Uri>): String {
        val token = UUID.randomUUID().toString()
        requests[token] = uris
        return token
    }

    fun get(token: String): List<Uri>? = requests[token]

    fun remove(token: String) {
        requests.remove(token)
    }
}
