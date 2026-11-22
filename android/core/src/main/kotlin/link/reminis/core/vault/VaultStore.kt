package link.reminis.core.vault

import java.io.File
import java.io.FileOutputStream
import java.io.IOException
import java.io.InputStream
import java.io.OutputStream

/**
 * An item of a vault folder: a file or a folder, named [name], reached by
 * [ref] (an opaque handle of its [VaultStore]: a path, a document id).
 */
data class VaultEntry(val name: String, val isDirectory: Boolean, val sizeBytes: Long, val ref: String)

/**
 * The vault folder is gone or cannot be used any more: a folder of the
 * system picker whose permission was revoked, whose provider is missing, or
 * that cannot be listed. Nothing is written; the user picks another folder
 * or goes back to the app's internal storage.
 */
class VaultUnavailableException(message: String, cause: Throwable? = null) : IOException(message, cause)

/**
 * The file system under a vault ([SaveVault]): the app's internal storage
 * ([FileVaultStore]), a temporary folder (demo, tests) or a folder chosen
 * with the Android picker (the app's `SafVaultStore`, Storage Access
 * Framework). Every call blocks: run them off the main thread.
 *
 * Writes never replace anything: a new file is written whole, synced, then
 * becomes visible (a temporary name renamed when the store can rename,
 * [rename]); an existing name is never overwritten.
 */
interface VaultStore {
    /** The root folder of the vault; [VaultUnavailableException] when it cannot be reached. */
    fun root(): VaultEntry

    /** The items of [folder], hidden ones (a leading dot) included; empty when it does not exist. */
    fun children(folder: VaultEntry): List<VaultEntry>

    /** A new folder [name] in [parent] (the name must be free). */
    fun createFolder(parent: VaultEntry, name: String): VaultEntry

    /**
     * A new file [name] in [parent] (the name must be free), written by
     * [write] and synced to its storage before returning when the store can
     * (a provider that writes through a pipe commits the document when it
     * is closed).
     */
    fun createFile(parent: VaultEntry, name: String, write: (OutputStream) -> Unit): VaultEntry

    fun openRead(file: VaultEntry): InputStream

    /**
     * Renames [entry] in its folder to [name] (free), in one step. Null when
     * the store cannot rename (a provider without rename support): the
     * caller writes a new item instead, then deletes the old one.
     */
    fun rename(entry: VaultEntry, name: String): VaultEntry?

    /**
     * Deletes [entry]: a file, or an empty folder (never recursive: a folder
     * that is not empty stays, false). False when it could not be deleted.
     */
    fun delete(entry: VaultEntry): Boolean

    /**
     * Whether [delete] removes (empty) folders. False for a storage
     * provider (SAF), which deletes a folder with whatever arrived in it:
     * the vault then leaves an emptied folder in place.
     */
    val removesFolders: Boolean get() = true

    /** What the user sees of the root: a folder name, or null for the app's own storage. */
    val displayName: String? get() = null
}

/**
 * A [VaultStore] in a folder of the file system: the app's internal storage,
 * a temporary folder of the demo mode, or a test folder. A file is written
 * under a temporary hidden name, synced (`fsync`), then renamed.
 */
open class FileVaultStore(val rootDirectory: File) : VaultStore {
    override fun root(): VaultEntry {
        if (!rootDirectory.isDirectory && !rootDirectory.mkdirs() && !rootDirectory.isDirectory) {
            throw VaultUnavailableException("the vault folder cannot be created")
        }
        return entry(rootDirectory)
    }

    override fun children(folder: VaultEntry): List<VaultEntry> =
        File(folder.ref).listFiles().orEmpty().map(::entry).sortedBy { it.name }

    override fun createFolder(parent: VaultEntry, name: String): VaultEntry {
        val folder = File(parent.ref, name)
        if (!folder.mkdir()) throw IOException("folder not created: $name")
        return entry(folder)
    }

    override fun createFile(parent: VaultEntry, name: String, write: (OutputStream) -> Unit): VaultEntry {
        val target = File(parent.ref, name)
        if (target.exists()) throw IOException("name taken: $name")
        val temporary = File(parent.ref, ".$name.${System.nanoTime()}.tmp")
        try {
            FileOutputStream(temporary).use { out ->
                write(out)
                out.flush()
                out.fd.sync()
            }
            if (target.exists() || !temporary.renameTo(target)) throw IOException("file not published: $name")
        } finally {
            temporary.delete()
        }
        return entry(target)
    }

    override fun openRead(file: VaultEntry): InputStream = File(file.ref).inputStream()

    override fun rename(entry: VaultEntry, name: String): VaultEntry? {
        val from = File(entry.ref)
        val to = File(from.parentFile, name)
        if (to.exists()) throw IOException("name taken: $name")
        if (!from.renameTo(to)) throw IOException("not renamed: ${entry.name}")
        return entry(to)
    }

    override fun delete(entry: VaultEntry): Boolean = File(entry.ref).delete()

    private fun entry(file: File) = VaultEntry(file.name, file.isDirectory, if (file.isFile) file.length() else 0, file.absolutePath)
}
