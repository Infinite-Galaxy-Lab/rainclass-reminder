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
    val publicAccount: Boolean = false,
    val image: ByteArray? = null,
    val confirmationLink: String? = null,
    val loading: Boolean = true,
    val complete: Boolean = false,
    val error: String? = null,
)

internal class LoginViewModel(
    private val createSession: () -> LoginSession,
    private val createPublicAccountSession: () -> LoginSession = createSession,
) : ViewModel() {
    private val mutable = MutableStateFlow(LoginState())
    val state = mutable.asStateFlow()
    private var active: LoginSession? = null
    private var pending: Job? = null
    private var publicAccount = false

    fun start() {
        if (pending?.isActive == true || mutable.value.complete) return
        mutable.value = LoginState(publicAccount = publicAccount)
        pending = viewModelScope.launch {
            var session: LoginSession? = null
            try {
                session = if (publicAccount) createPublicAccountSession() else createSession()
                active = session
                val image = session.start()
                ensureActive()
                val link = if (BuildConfig.DEBUG && publicAccount) session.confirmationURL() else null
                mutable.value = LoginState(publicAccount = publicAccount, image = image, confirmationLink = link, loading = false)
                session.complete()
                ensureActive()
                mutable.value = LoginState(publicAccount = publicAccount, loading = false, complete = true)
            } catch (error: Exception) {
                if (error is CancellationException) throw error
                ensureActive()
                val message = when {
                    error.message.orEmpty().contains("login_expired") -> "二维码已过期，请获取新二维码。"
                    error.message.orEmpty().contains("login_session_missing") -> "微信确认后未取得应用登录凭据，此实验暂不支持当前账号。可返回微信扫码登录。"
                    else -> "暂时无法完成登录，请重试或使用官方网页。"
                }
                mutable.value = LoginState(publicAccount = publicAccount, loading = false, error = message)
            } finally {
                session?.cancel()
                if (active === session) active = null
            }
        }
    }

    fun restart() { stop(); start() }

    fun usePublicAccount(enabled: Boolean) {
        if (!BuildConfig.DEBUG || publicAccount == enabled) return
        stop()
        publicAccount = enabled
        start()
    }

    fun stop() {
        pending?.cancel()
        pending = null
        active?.cancel()
        active = null
        mutable.value = LoginState(publicAccount = publicAccount, loading = false)
    }

    override fun onCleared() { stop(); super.onCleared() }
}
