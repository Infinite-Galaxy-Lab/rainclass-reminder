package lab.infinitegalaxy.rainclass.data

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.withContext
import mobile.Mobile

internal interface LoginSession {
    suspend fun start(): ByteArray
    suspend fun complete()
    fun cancel()
}

internal class NativeLoginSession(private val repository: RainRepository) : LoginSession {
    private val login = Mobile.newLogin()

    override suspend fun start(): ByteArray = withContext(Dispatchers.IO) { login.start() }

    override suspend fun complete() = withContext(Dispatchers.IO) {
        val cookie = login.await()
        currentCoroutineContext().ensureActive()
        repository.authenticate(cookie)
    }

    override fun cancel() { login.cancel() }
}
