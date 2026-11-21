package link.reminis.app

/**
 * A generation counter per key, guarded by one lock. An operation started at
 * generation `g` (see [next]) publishes its result only through [ifCurrent],
 * which checks `g` is still the key's generation and publishes under the
 * same lock: a [next] or [invalidate] from another thread can never slip
 * between the check and the publication.
 */
class Generations<K> {
    private val generations = HashMap<K, Long>()
    private val lock = Any()

    /** Starts a new generation for [key], superseding every earlier one. */
    fun next(key: K): Long = synchronized(lock) { bump(key) }

    /** Supersedes every generation of [key], then runs [then] under the lock (e.g. to emit `Lost` in order). */
    fun invalidate(key: K, then: () -> Unit = {}) = synchronized(lock) {
        bump(key)
        then()
    }

    /** Supersedes every generation of every key. */
    fun invalidateAll() = synchronized(lock) { generations.keys.toList().forEach(::bump) }

    fun isCurrent(key: K, generation: Long): Boolean = synchronized(lock) { generations[key] == generation }

    /** Runs [block] under the lock if [generation] is still the key's current one; null otherwise. */
    fun <T> ifCurrent(key: K, generation: Long, block: () -> T): T? = synchronized(lock) {
        if (generations[key] == generation) block() else null
    }

    private fun bump(key: K): Long {
        val next = (generations[key] ?: 0) + 1
        generations[key] = next
        return next
    }
}
