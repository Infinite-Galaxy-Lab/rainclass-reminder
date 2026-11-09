package lab.infinitegalaxy.rainclass.data

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

internal class RefreshQueue {
    private val lock = Mutex()
    @Volatile private var completed = 0L
    @Volatile private var generation = 0L

    suspend fun <T> read(block: suspend () -> T): T = lock.withLock { block() }

    suspend fun <T> changeSession(block: suspend () -> T): T = lock.withLock {
        try {
            block()
        } finally {
            generation++
        }
    }

    suspend fun <T> refresh(snapshot: suspend () -> T, action: suspend () -> T): T {
        val observedGeneration = generation
        val observedCompleted = completed
        return lock.withLock {
            if (generation != observedGeneration) throw CancellationException("canceled")
            if (completed != observedCompleted) return@withLock snapshot()
            val result = action()
            completed++
            result
        }
    }
}