package lab.infinitegalaxy.rainclass.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import lab.infinitegalaxy.rainclass.data.LoginSession
import lab.infinitegalaxy.rainclass.BuildConfig

internal data class LoginState(
    val image: ByteArray? = null,
    val confirmationLink: String? = null,
    val loading: Boolean = true,
    val complete: Boolean = false,
    val error: String? = null,
)

internal class LoginViewModel(private val createSession: () -> LoginSession) : ViewModel() {
    private val mutable = MutableStateFlow(LoginState())
    val state = mutable.asStateFlow()
    private var active: LoginSession? = null
    private var pending: Job? = null

    fun start() {
        if (pending?.isActive == true || mutable.value.complete) return
        mutable.value = LoginState()
        pending = viewModelScope.launch {
            var session: LoginSession? = null
            try {
                session = createSession()
                active = session
                val image = session.start()
                ensureActive()
                val link = if (BuildConfig.DEBUG) session.confirmationURL() else null
                mutable.value = LoginState(image = image, confirmationLink = link, loading = false)
                session.complete()
                ensureActive()
                mutable.value = LoginState(loading = false, complete = true)
            } catch (error: Exception) {
                if (error is CancellationException) throw error
                ensureActive()
                val message = if (error.message.orEmpty().contains("login_expired"))
                    "二维码已过期，请获取新二维码。" else "暂时无法完成登录，请重试或使用官方网页。"
                mutable.value = LoginState(loading = false, error = message)
            } finally {
                session?.cancel()
                if (active === session) active = null
            }
        }
    }

    fun restart() { stop(); start() }

    fun stop() {
        active?.cancel()
        active = null
        pending?.cancel()
        pending = null
        mutable.value = LoginState(loading = false)
    }

    override fun onCleared() { stop(); super.onCleared() }
}
