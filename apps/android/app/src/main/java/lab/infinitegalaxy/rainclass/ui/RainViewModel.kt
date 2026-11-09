package lab.infinitegalaxy.rainclass.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import lab.infinitegalaxy.rainclass.RainApplication
import lab.infinitegalaxy.rainclass.data.Snapshot
import lab.infinitegalaxy.rainclass.reminders.RefreshWorker

data class ScreenState(
    val snapshot: Snapshot = Snapshot(),
    val busy: Boolean = false,
    val message: String? = null,
    val background: Boolean = true,
)

class RainViewModel(application: Application) : AndroidViewModel(application) {
    private val app = application as RainApplication
    private val preferences = app.getSharedPreferences("settings", 0)
    private val mutable = MutableStateFlow(ScreenState(background = preferences.getBoolean("background", true)))
    val state = mutable.asStateFlow()

    fun load() {
        if (mutable.value.busy) return
        viewModelScope.launch {
            runCatching { app.repository.snapshot() }
                .onSuccess {
                    mutable.value = mutable.value.copy(snapshot = it)
                    if (it.authState != "signed_out" && mutable.value.background) RefreshWorker.schedule(app)
                }
                .onFailure { mutable.value = mutable.value.copy(message = readableError(it)) }
        }
    }

    fun refresh() {
        if (mutable.value.busy) return
        mutable.value = mutable.value.copy(busy = true, message = null)
        viewModelScope.launch {
            try {
                mutable.value = mutable.value.copy(snapshot = app.repository.refresh())
            } catch (error: Exception) {
                val cached = runCatching { app.repository.snapshot() }.getOrDefault(mutable.value.snapshot)
                mutable.value = mutable.value.copy(snapshot = cached, message = readableError(error))
            } finally {
                mutable.value = mutable.value.copy(busy = false)
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
        viewModelScope.launch {
            mutable.value = mutable.value.copy(busy = true)
            runCatching { app.repository.signOut() }
                .onSuccess {
                    RefreshWorker.cancel(app)
                    mutable.value = mutable.value.copy(snapshot = Snapshot(), message = null)
                }
                .onFailure {
                    val cached = runCatching { app.repository.snapshot() }.getOrDefault(mutable.value.snapshot)
                    if (cached.authState == "signed_out") RefreshWorker.cancel(app)
                    mutable.value = mutable.value.copy(snapshot = cached, message = readableError(it))
                }
            mutable.value = mutable.value.copy(busy = false)
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
