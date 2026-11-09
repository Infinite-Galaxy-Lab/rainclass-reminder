package lab.infinitegalaxy.rainclass

import android.view.View
import android.view.ViewGroup
import android.view.WindowManager
import android.webkit.WebView
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.json.JSONObject
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference

@RunWith(AndroidJUnit4::class)
class LoginScreenTest {
    @Test fun loginAllowsScreenshotsForWechatAlbum() {
        ActivityScenario.launch(LoginActivity::class.java).use { scenario ->
            scenario.onActivity {
                assertEquals("login QR cannot be captured for WeChat", 0,
                    it.window.attributes.flags and WindowManager.LayoutParams.FLAG_SECURE)
            }
        }
    }

    @Test fun loginWebViewHasViewportAndKeepsQrVisible() {
        ActivityScenario.launch(LoginActivity::class.java).use { scenario ->
            val instrumentation = InstrumentationRegistry.getInstrumentation()
            var web: WebView? = null
            val deadline = System.currentTimeMillis() + 5000
            while (web == null && System.currentTimeMillis() < deadline) {
                scenario.onActivity { web = findWebView(it.window.decorView) }
                if (web == null) Thread.sleep(50)
            }
            val view = requireNotNull(web)
            scenario.onActivity {
                assertTrue("login view collapsed horizontally", view.width > 200 * it.resources.displayMetrics.density)
                assertTrue("login view collapsed vertically", view.height > 200 * it.resources.displayMetrics.density)
                view.stopLoading()
                view.settings.blockNetworkLoads = true
                view.loadDataWithBaseURL("https://changjiang.yuketang.cn/web/", """
                    <!doctype html><html style="height:100%"><head>
                    <meta name="viewport" content="width=device-width, initial-scale=1, user-scalable=no">
                    <style>body{margin:0;height:100%}.wrapper-inner{height:100%;position:relative;width:950px;margin:auto;
                    display:flex;flex-direction:column;justify-content:center;align-items:center}
                    .L_title{width:950px;height:40px;margin:0}.login-box{width:416px;height:448px;margin:40px auto 0}
                    .mabox{position:relative;width:416px;height:377px;padding-top:36px}#support-socket{height:362px}
                    #qrcode-box{width:204px;height:204px;margin:15px auto 16px}.logma{width:202px;height:202px;background:#123456}
                    .changeImg{position:absolute;left:340px;top:0;width:76px;height:72px;aspect-ratio:76/72}
                    .agreement-box{position:absolute;top:408px;left:50%;transform:translateX(-50%);width:416px;height:24px}
                    </style></head><body><div class="wrapper login_changjiang"><div class="wrapper-inner">
                    <h3 class="L_title">合成登录页</h3><div class="login-box"><div class="mabox"><div class="changeImg"></div><div id="support-socket">
                    <div id="qrcode-box"><div class="logma"></div></div><button>切换登录方式</button></div></div>
                    <div class="agreement-box">合成协议占位</div></div></div></div></body></html>
                """.trimIndent(), "text/html", "UTF-8", null)
            }
            var visible = false
            var dimensions = "not loaded"
            val until = System.currentTimeMillis() + 5000
            while (!visible && System.currentTimeMillis() < until) {
                val done = CountDownLatch(1)
                val result = AtomicReference<String>()
                instrumentation.runOnMainSync {
                    view.evaluateJavascript("""
                        (() => { const qr = document.querySelector('.logma'); if (!qr) return {ok: false};
                            const r = qr.getBoundingClientRect();
                            const card = document.querySelector('.login-box').getBoundingClientRect();
                            const toggle = document.querySelector('.changeImg').getBoundingClientRect();
                            const ok = innerWidth <= 600 && r.left >= 0 && r.right <= innerWidth &&
                                r.top >= 0 && r.bottom <= innerHeight && r.width >= 200 && r.height >= 200 &&
                                r.width * visualViewport.scale >= 200 && card.left >= 0 && card.right <= innerWidth &&
                                (toggle.right <= r.left || toggle.left >= r.right || toggle.bottom <= r.top || toggle.top >= r.bottom);
                            return {ok, viewport: [innerWidth, innerHeight], qr: r.toJSON(), toggle: toggle.toJSON(), card: card.toJSON()};
                        })()
                    """.trimIndent()) { result.set(it); done.countDown() }
                }
                assertTrue(done.await(2, TimeUnit.SECONDS))
                dimensions = result.get()
                visible = JSONObject(dimensions).optBoolean("ok")
                if (!visible) Thread.sleep(50)
            }
            assertTrue("login QR must be visible at readable size: $dimensions", visible)
        }
    }

    private fun findWebView(view: View): WebView? {
        if (view is WebView) return view
        if (view is ViewGroup) for (i in 0 until view.childCount) {
            findWebView(view.getChildAt(i))?.let { return it }
        }
        return null
    }
}
