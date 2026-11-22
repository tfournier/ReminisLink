package link.reminis.app

import android.content.Context
import android.net.Uri
import android.provider.DocumentsContract
import android.provider.DocumentsContract.Document
import java.io.FileNotFoundException
import java.io.FileOutputStream
import java.io.IOException
import java.io.InputStream
import java.io.OutputStream
import link.reminis.core.vault.VaultEntry
import link.reminis.core.vault.VaultStore
import link.reminis.core.vault.VaultUnavailableException

/**
 * A vault in a folder chosen with the Android picker (Storage Access
 * Framework, `ACTION_OPEN_DOCUMENT_TREE`, persisted permission): on the
 * device or at a cloud provider of the picker, through `DocumentsContract`
 * only (no `androidx.documentfile`). Refs are document ids of the tree.
 *
 * - Every call checks nothing more than what it uses; a revoked
 *   permission, a provider gone (uninstalled, signed out) or a folder
 *   removed ends as [VaultUnavailableException]: the sync stops, nothing is
 *   lost, and the app offers another folder or its internal storage.
 * - A new file is a new document, written whole, synced when the
 *   provider gives a regular file (a provider that writes through a pipe
 *   commits the document when it is closed), and checked to carry the name
 *   asked (a provider may pick another one: the document is then deleted).
 * - Rename only when the document says it can ([rename] answers null
 *   otherwise: the vault writes a new folder, then deletes the old one).
 */
class SafVaultStore(context: Context, val treeUri: Uri) : VaultStore {
    private val resolver = context.applicationContext.contentResolver

    private fun uri(ref: String): Uri = DocumentsContract.buildDocumentUriUsingTree(treeUri, ref)

    override val displayName: String?
        get() = runCatching { query(uri(DocumentsContract.getTreeDocumentId(treeUri)), arrayOf(Document.COLUMN_DISPLAY_NAME)) { it.getString(0) } }.getOrNull()

    override fun root(): VaultEntry = unavailableOnFailure {
        val held = resolver.persistedUriPermissions.any { it.uri == treeUri && it.isReadPermission && it.isWritePermission }
        if (!held) throw VaultUnavailableException("the permission on the folder was removed")
        val id = DocumentsContract.getTreeDocumentId(treeUri)
        val entry = query(uri(id), COLUMNS) { entry(it) } ?: throw VaultUnavailableException("the folder cannot be read")
        if (!entry.isDirectory) throw VaultUnavailableException("not a folder")
        entry
    }

    override fun children(folder: VaultEntry): List<VaultEntry> = unavailableOnFailure {
        val result = mutableListOf<VaultEntry>()
        val cursor = resolver.query(DocumentsContract.buildChildDocumentsUriUsingTree(treeUri, folder.ref), COLUMNS, null, null, null)
            ?: throw VaultUnavailableException("the folder cannot be listed")
        cursor.use { while (it.moveToNext()) result += entry(it) }
        result.sortedBy { it.name }
    }

    override fun createFolder(parent: VaultEntry, name: String): VaultEntry = unavailableOnFailure {
        val created = DocumentsContract.createDocument(resolver, uri(parent.ref), Document.MIME_TYPE_DIR, name)
            ?: throw IOException("folder not created")
        checkedName(created, name, isDirectory = true)
    }

    override fun createFile(parent: VaultEntry, name: String, write: (OutputStream) -> Unit): VaultEntry = unavailableOnFailure {
        val created = DocumentsContract.createDocument(resolver, uri(parent.ref), MIME_TYPE, name) ?: throw IOException("file not created")
        try {
            val descriptor = resolver.openFileDescriptor(created, "w") ?: throw IOException("file not opened")
            descriptor.use { pfd ->
                FileOutputStream(pfd.fileDescriptor).use { out ->
                    write(out)
                    out.flush()
                    // Synced when the provider gives a regular file; a pipe is committed on close.
                    val regular = runCatching { android.system.OsConstants.S_ISREG(android.system.Os.fstat(pfd.fileDescriptor).st_mode) }.getOrDefault(false)
                    if (regular) out.fd.sync()
                }
            }
            checkedName(created, name, isDirectory = false)
        } catch (e: Exception) {
            runCatching { DocumentsContract.deleteDocument(resolver, created) }
            throw e
        }
    }

    override fun openRead(file: VaultEntry): InputStream = unavailableOnFailure {
        try {
            resolver.openInputStream(uri(file.ref)) ?: throw IOException("file not opened")
        } catch (e: FileNotFoundException) {
            // One file gone (deleted meanwhile) is not the folder gone: a revoked permission is a SecurityException.
            throw IOException("file not found", e)
        }
    }

    override fun rename(entry: VaultEntry, name: String): VaultEntry? = unavailableOnFailure {
        val document = uri(entry.ref)
        val flags = query(document, arrayOf(Document.COLUMN_FLAGS)) { it.getInt(0) } ?: 0
        if (flags and Document.FLAG_SUPPORTS_RENAME == 0) return@unavailableOnFailure null
        val renamed = DocumentsContract.renameDocument(resolver, document, name) ?: document
        val result = query(renamed, COLUMNS) { entry(it) } ?: throw IOException("renamed document not found")
        // A provider that picked another name: the vault writes a new folder instead.
        if (result.name != name) {
            runCatching { DocumentsContract.renameDocument(resolver, renamed, entry.name) }
            return@unavailableOnFailure null
        }
        result
    }

    /**
     * Never a folder: a provider deletes a folder with whatever is in it at
     * that moment (a file arrived from another device included), and SAF has
     * no conditional deletion. Folders are left (an empty one has no
     * manifest: ignored by every read); only files are deleted.
     */
    override val removesFolders: Boolean get() = false

    override fun delete(entry: VaultEntry): Boolean = unavailableOnFailure {
        if (entry.isDirectory) return@unavailableOnFailure false
        DocumentsContract.deleteDocument(resolver, uri(entry.ref))
    }

    private fun checkedName(document: Uri, name: String, isDirectory: Boolean): VaultEntry {
        val entry = query(document, COLUMNS) { entry(it) } ?: throw IOException("created document not found")
        if (entry.name != name || entry.isDirectory != isDirectory) {
            runCatching { DocumentsContract.deleteDocument(resolver, document) }
            throw IOException("the provider changed the name")
        }
        return entry
    }

    private fun <T> query(document: Uri, columns: Array<String>, read: (android.database.Cursor) -> T): T? =
        resolver.query(document, columns, null, null, null)?.use { if (it.moveToFirst()) read(it) else null }

    private fun entry(cursor: android.database.Cursor): VaultEntry {
        val id = cursor.getString(0)
        val name = cursor.getString(1) ?: id
        val mime = cursor.getString(2)
        val size = if (cursor.isNull(3)) 0 else cursor.getLong(3)
        return VaultEntry(name, mime == Document.MIME_TYPE_DIR, size, id)
    }

    /** A revoked permission, a provider gone or a folder removed: the vault is unavailable (never a silent loss). */
    private inline fun <T> unavailableOnFailure(block: () -> T): T = try {
        block()
    } catch (e: VaultUnavailableException) {
        throw e
    } catch (e: SecurityException) {
        throw VaultUnavailableException("no access to the folder", e)
    } catch (e: FileNotFoundException) {
        throw VaultUnavailableException("the folder is gone", e)
    } catch (e: IllegalArgumentException) {
        // An unknown authority (provider uninstalled) or a document id it no longer knows.
        throw VaultUnavailableException("the folder's provider is missing", e)
    } catch (e: IllegalStateException) {
        throw VaultUnavailableException("the folder's provider failed", e)
    }

    companion object {
        private const val MIME_TYPE = "application/octet-stream"
        private val COLUMNS = arrayOf(Document.COLUMN_DOCUMENT_ID, Document.COLUMN_DISPLAY_NAME, Document.COLUMN_MIME_TYPE, Document.COLUMN_SIZE)
    }
}
