package lab.infinitegalaxy.rainclass.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.ensureActive
import lab.infinitegalaxy.rainclass.RainApplication
import lab.infinitegalaxy.rainclass.data.Snapshot
import lab.infinitegalaxy.rainclass.data.ScreenDataSource
import lab.infinitegalaxy.rainclass.reminders.RefreshWorker

data class ScreenState(
    val snapshot: Snapshot = Snapshot(),
    val busy: Boolean = false,
    val message: String? = null,
    val background: Boolean = true,
)

class RainViewModel @JvmOverloads constructor(
    application: Application,
    private val source: ScreenDataSource = (application as RainApplication).repository,
) : AndroidViewModel(application) {
    private val app = application as RainApplication
    private val preferences = app.getSharedPreferences("settings", 0)
    private val mutable = MutableStateFlow(ScreenState(background = preferences.getBoolean("background", true)))
    val state = mutable.asStateFlow()
    private var loading: Job? = null

    fun load() {
        if (mutable.value.busy) return
        loading?.cancel()
        loading = viewModelScope.launch {
            runCatching { source.snapshot() }
                .onSuccess {
                    ensureActive()
                    if (mutable.value.busy) return@onSuccess
                    mutable.value = mutable.value.copy(snapshot = it)
                    if (it.authState != "signed_out" && mutable.value.background) RefreshWorker.schedule(app)
                }
                .onFailure {
                    if (it is CancellationException) throw it
                    mutable.value = mutable.value.copy(message = readableError(it))
                }
        }
    }

    fun loginSucceeded() = refresh(loadCache = true)

    fun refresh() = refresh(loadCache = false)

    private fun refresh(loadCache: Boolean) {
        if (mutable.value.busy) return
        loading?.cancel()
        mutable.value = mutable.value.copy(busy = true, message = null)
        viewModelScope.launch {
            try {
                if (loadCache) mutable.value = mutable.value.copy(snapshot = source.snapshot())
                mutable.value = mutable.value.copy(snapshot = source.refresh())
            } catch (error: Exception) {
                if (error is CancellationException) throw error
                val cached = runCatching { source.snapshot() }.getOrDefault(mutable.value.snapshot)
                mutable.value = mutable.value.copy(snapshot = cached, message = readableError(error))
            } finally {
                mutable.value = mutable.value.copy(busy = false)
                if (mutable.value.background && mutable.value.snapshot.authState != "signed_out") RefreshWorker.schedule(app)
            }
        }
    }

    fun background(enabled: Boolean) {
        preferences.edit().putBoolean("background", enabled).apply()
        mutable.value = mutable.value.copy(background = enabled)
        if (enabled && mutable.value.snapshot.authState != "signed_out") RefreshWorker.schedule(app)
        else RefreshWorker.cancel(app)
    }

    fun signOut() {
        if (mutable.value.busy) return
        loading?.cancel()
        mutable.value = mutable.value.copy(busy = true)
        viewModelScope.launch {
            try {
                source.signOut()
                RefreshWorker.cancel(app)
                mutable.value = mutable.value.copy(snapshot = Snapshot(), message = null)
            } catch (error: Exception) {
                if (error is CancellationException) throw error
                val cached = runCatching { source.snapshot() }.getOrDefault(mutable.value.snapshot)
                if (cached.authState == "signed_out") RefreshWorker.cancel(app)
                mutable.value = mutable.value.copy(snapshot = cached, message = readableError(error))
            } finally { mutable.value = mutable.value.copy(busy = false) }
        }
    }
}

fun readableError(error: Throwable): String {
    val code = error.message.orEmpty()
    return when {
        code.contains("auth_expired") -> "登录已失效，请重新登录。已缓存的任务仍可查看。"
        code.contains("session_required") -> "请先登录雨课堂。"
        code.contains("invalid_session") -> "尚未取得有效登录，请在官方页面完成登录。"
        code.contains("rate_limited") -> "请求较频繁，请稍后再试。"
        code.contains("storage") -> "本地数据暂时不可用，请重新打开应用。"
        code.contains("vault") -> "本机登录凭据无法读取，请重新登录。"
        code.contains("timezone") -> "无法识别设备时区，请在系统设置中选择所在地区后重试。"
        code.contains("canceled") -> "刷新已取消。"
        else -> "暂时无法连接雨课堂，已保留上次任务，请稍后重试。"
    }
}
