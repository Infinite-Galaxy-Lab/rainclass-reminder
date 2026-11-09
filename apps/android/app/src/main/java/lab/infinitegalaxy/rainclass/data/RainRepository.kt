package lab.infinitegalaxy.rainclass.data

import android.content.Context
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import lab.infinitegalaxy.rainclass.reminders.AndroidNotifications
import mobile.Engine
import mobile.Mobile

class RainRepository(context: Context) {
    private val app = context.applicationContext
    private val lock = Mutex()
    private val control = Any()
    @Volatile private var engine: Engine? = null
    private var activeRefresh: Pair<String?, mobile.Refresh>? = null

    private fun core(): Engine = engine ?: Mobile.newEngine(
        java.io.File(app.noBackupFilesDir, "core").absolutePath,
        SessionVault(app), AndroidNotifications(app), java.util.TimeZone.getDefault().id
    ).also { engine = it }

    suspend fun snapshot(): Snapshot = withContext(Dispatchers.IO) {
        lock.withLock { Snapshot.parse(core().snapshot()) }
    }

    suspend fun refresh(worker: String? = null, stopped: () -> Boolean = { false }): Snapshot = withContext(Dispatchers.IO) {
        lock.withLock {
            val current = core()
            current.setTimeZone(java.util.TimeZone.getDefault().id)
            val request = current.newRefresh()
            synchronized(control) {
                activeRefresh = worker to request
                if (stopped()) request.cancel()
            }
            try { Snapshot.parse(request.run()) }
            finally {
                request.cancel()
                synchronized(control) { activeRefresh = null }
            }
        }
    }

    suspend fun authenticate(cookie: String) = withContext(Dispatchers.IO) {
        lock.withLock {
            val current = try { core() } catch (error: Exception) {
                if (error.message?.contains("vault") != true) throw error
                SessionVault(app).reset()
                core()
            }
            current.authenticate(cookie)
        }
    }

    suspend fun signOut() = withContext(Dispatchers.IO) {
        engine?.cancel()
        lock.withLock { core().signOut() }
    }

    fun cancel() { engine?.cancel() }

    fun cancelWorker(worker: String) {
        synchronized(control) { activeRefresh?.takeIf { it.first == worker }?.second?.cancel() }
    }
}
