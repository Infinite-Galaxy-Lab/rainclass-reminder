package lab.infinitegalaxy.rainclass

import android.annotation.SuppressLint
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.os.SystemClock
import android.view.ViewGroup
import android.webkit.CookieManager
import android.webkit.WebResourceRequest
import android.webkit.WebResourceError
import android.webkit.WebStorage
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.repeatOnLifecycle
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import lab.infinitegalaxy.rainclass.ui.RainTheme
import lab.infinitegalaxy.rainclass.ui.readableError
import lab.infinitegalaxy.rainclass.data.LoginChecks

class WebLoginActivity : ComponentActivity() {
    companion object { const val BASE = "https://changjiang.yuketang.cn" }
    private var webView: WebView? = null
    private var message by mutableStateOf("正在加载官方登录页…")
    private var checking by mutableStateOf(false)
    private var initialized = false
    private val sessionChecks = LoginChecks()
    private val mobileLoginScript by lazy {
        assets.open("login-mobile.js").bufferedReader().use { it.readText() }
    }

    @OptIn(ExperimentalMaterial3Api::class)
    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        WebView.setWebContentsDebuggingEnabled(BuildConfig.DEBUG)
        CookieManager.getInstance().setAcceptCookie(true)
        setContent {
            RainTheme {
                Scaffold(topBar = {
                    TopAppBar(title = { Text("登录雨课堂") }, navigationIcon = {
                        TextButton(onClick = { finish() }) { Text("返回") }
                    }, actions = {
                        TextButton(onClick = { webView?.reload() }, enabled = !checking) { Text("重载") }
                        TextButton(onClick = { checkSession() }, enabled = !checking) { Text(if (checking) "校验中…" else "已登录") }
                    })
                }) { insets ->
                    Column(Modifier.fillMaxSize().padding(insets)) {
                        Text(message, Modifier.padding(horizontal = 20.dp, vertical = 10.dp), style = MaterialTheme.typography.bodySmall)
                        Text("可截图后到微信「扫一扫 → 相册」识别，也可用另一台设备扫码。确认后返回，将自动检查登录状态。",
                            Modifier.padding(horizontal = 20.dp, vertical = 4.dp), style = MaterialTheme.typography.bodySmall)
                        if (checking) LinearProgressIndicator(Modifier.fillMaxWidth())
                        AndroidView(modifier = Modifier.fillMaxWidth().weight(1f), factory = { context ->
                            WebView(context).apply {
                                layoutParams = ViewGroup.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT)
                                webView = this
                                settings.javaScriptEnabled = true
                                settings.domStorageEnabled = true
                                settings.useWideViewPort = true
                                settings.loadWithOverviewMode = true
                                settings.builtInZoomControls = true
                                settings.displayZoomControls = false
                                settings.allowFileAccess = false
                                settings.allowContentAccess = false
                                settings.setSupportMultipleWindows(false)
                                CookieManager.getInstance().setAcceptThirdPartyCookies(this, false)
                                webViewClient = object : WebViewClient() {
                                    override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
                                        val uri = request.url
                                        if (uri.scheme == "https" && uri.host in setOf("changjiang.yuketang.cn",
                                                "open.weixin.qq.com", "lp.open.weixin.qq.com", "login.weixin.qq.com")) return false
                                        if (uri.scheme == "weixin") {
                                            runCatching { startActivity(Intent(Intent.ACTION_VIEW, uri)) }
                                                .onFailure { message = "未找到微信，可使用另一台设备扫码登录。" }
                                        }
                                        return true
                                    }
                                    override fun onPageFinished(view: WebView, url: String) {
                                        val uri = Uri.parse(url)
                                        if (uri.host == "changjiang.yuketang.cn") {
                                            if (uri.path == "/web/" || uri.path == "/web") fitLoginPage(view)
                                            sessionChecks.resume()
                                            checkSession(automatic = true)
                                        }
                                    }
                                    override fun onReceivedError(view: WebView, request: WebResourceRequest, error: WebResourceError) {
                                        if (request.isForMainFrame) message = "官方登录页加载失败，请检查网络后点击重载。"
                                    }
                                }
                                fun startLogin() {
                                    if (isDestroyed || isFinishing) return
                                    initialized = true
                                    loadUrl("$BASE/v2/web/index")
                                }
                                // 重建 Activity 时保留扫码结果；只在用户开启新登录流程时清理旧网页会话。
                                if (savedInstanceState == null) CookieManager.getInstance().removeAllCookies { startLogin() }
                                else startLogin()
                            }
                        })
                    }
                }
            }
        }
        lifecycleScope.launch {
            repeatOnLifecycle(Lifecycle.State.RESUMED) {
                while (true) {
                    checkSession(automatic = true)
                    delay(500)
                }
            }
        }
    }

    private fun fitLoginPage(view: WebView) {
        view.evaluateJavascript(mobileLoginScript, null)
    }

    private fun checkSession(automatic: Boolean = false) {
        if (!initialized || checking || isFinishing) return
        val cookie = CookieManager.getInstance().getCookie(BASE).orEmpty()
        if (!cookie.split(';').any { it.trim().startsWith("sessionid=") && it.substringAfter('=').isNotBlank() }) {
            if (!automatic) message = "尚未取得登录状态，请先在下方官方页面完成登录。"
            return
        }
        if (!sessionChecks.begin(cookie, automatic, SystemClock.elapsedRealtime())) return
        checking = true
        message = "正在校验登录状态，请稍候…"
        lifecycleScope.launch {
            try {
                (application as RainApplication).repository.authenticate(cookie)
                setResult(RESULT_OK)
                finish()
            } catch (error: Exception) {
                if (error is CancellationException) throw error
                message = readableError(error)
            } finally { checking = false }
        }
    }

    override fun onResume() {
        super.onResume()
        sessionChecks.resume()
        webView?.onResume()
    }

    override fun onStop() {
        CookieManager.getInstance().flush()
        super.onStop()
    }

    override fun onDestroy() {
        webView?.apply { stopLoading(); loadUrl("about:blank"); clearCache(true); clearHistory(); destroy() }
        webView = null
        if (isFinishing) {
            CookieManager.getInstance().removeAllCookies(null)
            WebStorage.getInstance().deleteAllData()
        }
        super.onDestroy()
    }
}
