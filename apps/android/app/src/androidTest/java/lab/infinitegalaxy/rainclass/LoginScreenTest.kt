package lab.infinitegalaxy.rainclass

import android.view.View
import android.view.ViewGroup
import android.webkit.WebView
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference

@RunWith(AndroidJUnit4::class)
class LoginScreenTest {
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
                    <style>body{margin:0;height:100%}.wrapper{height:100%;position:relative;width:950px;margin:auto}
                    #qr{position:absolute;width:202px;height:202px;left:374px;top:50%;transform:translateY(-50%)}</style>
                    </head><body><div class="wrapper"><div id="qr">合成二维码占位</div></div></body></html>
                """.trimIndent(), "text/html", "UTF-8", null)
            }
            var visible = false
            val until = System.currentTimeMillis() + 5000
            while (!visible && System.currentTimeMillis() < until) {
                val done = CountDownLatch(1)
                val result = AtomicReference<String>()
                instrumentation.runOnMainSync {
                    view.evaluateJavascript("""
                        (() => { const qr = document.getElementById('qr'); if (!qr) return false;
                            const r = qr.getBoundingClientRect();
                            return innerWidth >= 950 && r.left >= 0 && r.right <= innerWidth &&
                                r.top >= 0 && r.bottom <= innerHeight && r.height > 0;
                        })()
                    """.trimIndent()) { result.set(it); done.countDown() }
                }
                assertTrue(done.await(2, TimeUnit.SECONDS))
                visible = result.get() == "true"
                if (!visible) Thread.sleep(50)
            }
            assertTrue("desktop login QR is outside viewport", visible)
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
