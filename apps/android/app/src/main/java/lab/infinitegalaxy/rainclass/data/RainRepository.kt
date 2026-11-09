package lab.infinitegalaxy.rainclass.data

import android.content.Context
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import lab.infinitegalaxy.rainclass.reminders.AndroidNotifications
import mobile.Engine
import mobile.Mobile

class RainRepository(context: Context) : ScreenDataSource {
    private val app = context.applicationContext
    private val queue = RefreshQueue()
    private val control = Any()
    @Volatile private var engine: Engine? = null
    private var activeRefresh: Pair<String?, mobile.Refresh>? = null

    private fun core(): Engine = engine ?: Mobile.newEngine(
        java.io.File(app.noBackupFilesDir, "core").absolutePath,
        SessionVault(app), AndroidNotifications(app), java.util.TimeZone.getDefault().id
    ).also { engine = it }

    override suspend fun snapshot(): Snapshot = withContext(Dispatchers.IO) {
        queue.read { Snapshot.parse(core().snapshot()) }
    }

    override suspend fun refresh(): Snapshot = refresh(null)

    suspend fun refresh(worker: String?, stopped: () -> Boolean = { false }): Snapshot = withContext(Dispatchers.IO) {
        if (stopped()) throw CancellationException("canceled")
        queue.refresh(
            snapshot = {
                if (stopped()) throw CancellationException("canceled")
                Snapshot.parse(core().snapshot())
            },
            action = {
                val current = core()
                current.setTimeZone(java.util.TimeZone.getDefault().id)
                val request = current.newRefresh()
                synchronized(control) {
                    activeRefresh = worker to request
                    if (stopped()) request.cancel()
                }
                try {
                    Snapshot.parse(request.run())
                } finally {
                    request.cancel()
                    synchronized(control) { activeRefresh = null }
                }
            },
        )
    }

    suspend fun authenticate(cookie: String) = withContext(Dispatchers.IO) {
        queue.changeSession {
            val current = try {
                core()
            } catch (error: Exception) {
                if (error.message?.contains("vault") != true) throw error
                SessionVault(app).reset()
                core()
            }
            current.authenticate(cookie)
        }
    }

    override suspend fun signOut() = withContext(Dispatchers.IO) {
        engine?.cancel()
        queue.changeSession { core().signOut() }
    }

    fun cancel() {
        engine?.cancel()
    }

    fun cancelWorker(worker: String) {
        synchronized(control) { activeRefresh?.takeIf { it.first == worker }?.second?.cancel() }
    }
}