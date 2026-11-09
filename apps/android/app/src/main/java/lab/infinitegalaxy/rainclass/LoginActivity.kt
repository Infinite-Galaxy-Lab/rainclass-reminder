package lab.infinitegalaxy.rainclass

import android.annotation.SuppressLint
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.view.ViewGroup
import android.view.WindowManager
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
import kotlinx.coroutines.launch
import lab.infinitegalaxy.rainclass.ui.RainTheme
import lab.infinitegalaxy.rainclass.ui.readableError

class LoginActivity : ComponentActivity() {
    companion object { const val BASE = "https://changjiang.yuketang.cn" }
    private var webView: WebView? = null
    private var message by mutableStateOf("正在加载官方登录页…")
    private var checking by mutableStateOf(false)

    @OptIn(ExperimentalMaterial3Api::class)
    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        WebView.setWebContentsDebuggingEnabled(BuildConfig.DEBUG)
        window.addFlags(WindowManager.LayoutParams.FLAG_SECURE)
        CookieManager.getInstance().setAcceptCookie(true)
        setContent {
            RainTheme {
                Scaffold(topBar = {
                    TopAppBar(title = { Text("登录雨课堂") }, navigationIcon = {
                        TextButton(onClick = { finish() }) { Text("返回") }
                    }, actions = {
                        TextButton(onClick = { webView?.reload() }, enabled = !checking) { Text("重载") }
                        TextButton(onClick = ::checkSession, enabled = !checking) { Text("已登录") }
                    })
                }) { insets ->
                    Column(Modifier.fillMaxSize().padding(insets)) {
                        Text(message, Modifier.padding(horizontal = 20.dp, vertical = 10.dp), style = MaterialTheme.typography.bodySmall)
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
                                            checkSession()
                                        }
                                    }
                                    override fun onReceivedError(view: WebView, request: WebResourceRequest, error: WebResourceError) {
                                        if (request.isForMainFrame) message = "官方登录页加载失败，请检查网络后点击重载。"
                                    }
                                }
                                CookieManager.getInstance().removeAllCookies { loadUrl("$BASE/v2/web/index") }
                            }
                        })
                    }
                }
            }
        }
    }

    private fun fitLoginPage(view: WebView) {
        // 官方登录页固定宽度且禁止缩放，窄屏中二维码会落在可见区域外。
        view.evaluateJavascript("""
            (() => {
                const viewport = document.querySelector('meta[name="viewport"]');
                if (viewport) viewport.content = 'width=1100, user-scalable=yes';
                window.dispatchEvent(new Event('resize'));
            })();
        """.trimIndent(), null)
    }

    private fun checkSession() {
        if (checking) return
        val cookie = CookieManager.getInstance().getCookie(BASE).orEmpty()
        if (!cookie.split(';').any { it.trim().startsWith("sessionid=") && it.substringAfter('=').isNotBlank() }) {
            message = "尚未取得登录状态，请先在下方官方页面完成登录。"
            return
        }
        checking = true
        lifecycleScope.launch {
            try {
                (application as RainApplication).repository.authenticate(cookie)
                setResult(RESULT_OK)
                finish()
            } catch (error: Exception) {
                message = readableError(error)
            } finally { checking = false }
        }
    }

    override fun onDestroy() {
        webView?.apply { stopLoading(); loadUrl("about:blank"); clearCache(true); clearHistory(); destroy() }
        webView = null
        CookieManager.getInstance().removeAllCookies(null)
        WebStorage.getInstance().deleteAllData()
        super.onDestroy()
    }
}
