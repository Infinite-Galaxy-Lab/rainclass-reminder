package lab.infinitegalaxy.rainclass

import android.app.Instrumentation.ActivityMonitor
import android.content.Intent
import android.content.ClipData
import android.content.ClipboardManager
import android.view.WindowManager
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.ViewModelStore
import androidx.test.core.app.ActivityScenario
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.withContext
import lab.infinitegalaxy.rainclass.data.LoginSession
import lab.infinitegalaxy.rainclass.ui.LoginViewModel
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import java.util.concurrent.atomic.AtomicInteger

@RunWith(AndroidJUnit4::class)
class NativeLoginTest {
    @get:Rule val compose = createEmptyComposeRule()
    private val instrumentation get() = InstrumentationRegistry.getInstrumentation()
    private val app: RainApplication get() = ApplicationProvider.getApplicationContext()

    @Test fun homeAndLauncherReturnKeepScanAndDeliverResult() {
        val session = FakeLogin()
        val factories = AtomicInteger()
        val previous = app.loginSessionFactory
        app.loginSessionFactory = { factories.incrementAndGet(); session }
        val monitor = ActivityMonitor(LoginActivity::class.java.name, null, false)
        instrumentation.addMonitor(monitor)
        val launch = requireNotNull(app.packageManager.getLaunchIntentForPackage(app.packageName))
        try {
            ActivityScenario.launch<MainActivity>(launch).use {
                compose.onNodeWithText("登录雨课堂").performClick()
                val login = instrumentation.waitForMonitorWithTimeout(monitor, 5000) as LoginActivity
                await { session.waiting }
                compose.onNodeWithContentDescription("微信登录二维码").assertIsDisplayed()
                instrumentation.uiAutomation.executeShellCommand("input keyevent KEYCODE_HOME").use { }
                await {
                    var stopped = false
                    main { stopped = !login.lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED) }
                    stopped
                }
                session.confirmed.complete(Unit)
                await { session.completed }
                main { app.startActivity(Intent(launch)) }
                await { login.isFinishing || login.isDestroyed }
                compose.onNodeWithText("连接你的课堂").assertIsDisplayed()
                assertEquals("launcher created a second QR session", 1, factories.get())
                assertEquals(1, session.starts)
            }
        } finally {
            instrumentation.removeMonitor(monitor)
            app.loginSessionFactory = previous
        }
    }

    @Test fun recreationKeepsNativeAttemptAndAllowsScreenshot() {
        val session = FakeLogin()
        val factories = AtomicInteger()
        val previous = app.loginSessionFactory
        app.loginSessionFactory = { factories.incrementAndGet(); session }
        try {
            ActivityScenario.launch(LoginActivity::class.java).use { scenario ->
                await { session.waiting }
                scenario.recreate()
                compose.onNodeWithContentDescription("微信登录二维码").assertIsDisplayed()
                scenario.onActivity {
                    assertEquals(0, it.window.attributes.flags and WindowManager.LayoutParams.FLAG_SECURE)
                }
                assertEquals(1, factories.get())
                assertEquals(1, session.starts)
                assertFalse(session.cancelled)
            }
            assertTrue("leaving login did not cancel its network request", session.cancelled)
        } finally { app.loginSessionFactory = previous }
    }

    @Test fun restartingRejectsLateResultFromPreviousAttempt() {
        val old = FakeLogin(ignoreCancellation = true)
        val fresh = FakeLogin()
        var factories = 0
        withModel({ if (factories++ == 0) old else fresh }) { model ->
            main { model.start(); model.start() }
            await { old.waiting }
            assertEquals(1, factories)
            main { model.restart() }
            await { fresh.waiting }
            assertTrue(old.cancelled)
            old.confirmed.complete(Unit)
            await { old.completed }
            instrumentation.waitForIdleSync()
            main { assertFalse("a cancelled QR completed the replacement login", model.state.value.complete) }
            fresh.confirmed.complete(Unit)
            await { model.state.value.complete }
            main { assertNull(model.state.value.image) }
        }
    }

    @Test fun switchingLoginMethodRejectsLatePreviousResult() {
        val old = FakeLogin(ignoreCancellation = true)
        val fresh = FakeLogin()
        withModel({ old }, { fresh }) { model ->
            main { model.start() }
            await { old.waiting }
            main { model.usePublicAccount(true) }
            await { fresh.waiting }
            main {
                assertTrue(old.cancelled)
                assertEquals(1, fresh.starts)
                assertTrue(model.state.value.publicAccount)
                assertEquals("http://weixin.qq.com/q/synthetic-scene", model.state.value.confirmationLink)
            }
            old.confirmed.complete(Unit)
            await { old.completed }
            instrumentation.waitForIdleSync()
            main { assertFalse("a cancelled method switched the replacement login", model.state.value.complete) }
            fresh.confirmed.complete(Unit)
            await { model.state.value.complete }
        }
    }

    @Test fun missingPublicAccountSessionShowsExplanation() {
        val old = FakeLogin()
        val failing = FakeLogin(failure = "login_session_missing")
        withModel({ old }, { failing }) { model ->
            main { model.start() }
            await { old.waiting }
            main { model.usePublicAccount(true) }
            await { failing.waiting }
            failing.confirmed.complete(Unit)
            await { model.state.value.error != null }
            main {
                assertFalse(model.state.value.complete)
                assertTrue(model.state.value.error.orEmpty().contains("未取得应用登录凭据"))
            }
        }
    }

    @Test fun validationFailureShowsRetryAndNeverCompletes() {
        val rejected = FakeLogin(failure = "invalid_session")
        val fresh = FakeLogin()
        var factories = 0
        withModel({ if (factories++ == 0) rejected else fresh }) { model ->
            main { model.start() }
            await { rejected.waiting }
            rejected.confirmed.complete(Unit)
            await { model.state.value.error != null }
            main {
                assertFalse(model.state.value.complete)
                assertNull(model.state.value.image)
                model.restart()
            }
            await { fresh.waiting }
            fresh.confirmed.complete(Unit)
            await { model.state.value.complete }
        }
    }

    @Test fun clearingModelCancelsPendingNativeRequest() {
        val session = FakeLogin(ignoreCancellation = true)
        try {
            withModel({ session }) { model ->
                main { model.start() }
                await { session.waiting }
                assertFalse(model.state.value.complete)
            }
            assertTrue("ViewModel release left the native request running", session.cancelled)
        } finally { session.confirmed.complete(Unit) }
    }

    @Test fun confirmationLinkCopiesOnlyOnRequestAndClearsOnRetry() {
        val previous = app.loginSessionFactory
        val previousPublic = app.publicAccountLoginSessionFactory
        val clipboard = app.getSystemService(ClipboardManager::class.java)
        app.loginSessionFactory = { FakeLogin() }
        app.publicAccountLoginSessionFactory = { FakeLogin() }
        try {
            ActivityScenario.launch(LoginActivity::class.java).use {
                compose.onNodeWithText("尝试公众号链接登录").performScrollTo().assertIsDisplayed()
                main { clipboard.setPrimaryClip(ClipData.newPlainText("test", "unrelated")) }
                compose.onNodeWithText("尝试公众号链接登录").performClick()
                compose.onNodeWithText("复制链接到微信").performScrollTo().assertIsDisplayed()
                main { assertEquals("unrelated", clipboard.primaryClip?.getItemAt(0)?.text?.toString()) }
                compose.onNodeWithText("复制链接到微信").performClick()
                compose.onNodeWithText("复制公众号链接").assertIsDisplayed()
                main { assertEquals("unrelated", clipboard.primaryClip?.getItemAt(0)?.text?.toString()) }
                compose.onNodeWithText("复制公众号链接").performClick()
                main {
                    val clip = requireNotNull(clipboard.primaryClip)
                    assertEquals("http://weixin.qq.com/q/synthetic-scene", clip.getItemAt(0).text.toString())
                    assertTrue(clip.description.extras?.getBoolean("android.content.extra.IS_SENSITIVE") == true)
                }
                compose.onNodeWithText("获取新二维码").performScrollTo().performClick()
                main { assertTrue(clipboard.primaryClip?.getItemAt(0)?.text.isNullOrEmpty()) }
            }
        } finally {
            app.publicAccountLoginSessionFactory = previousPublic
            app.loginSessionFactory = previous
        }
    }

    @Test fun confirmationLinkCleanupPreservesNewUserContent() {
        val previous = app.loginSessionFactory
        val previousPublic = app.publicAccountLoginSessionFactory
        val clipboard = app.getSystemService(ClipboardManager::class.java)
        app.loginSessionFactory = { FakeLogin() }
        app.publicAccountLoginSessionFactory = { FakeLogin() }
        try {
            ActivityScenario.launch(LoginActivity::class.java).use {
                compose.onNodeWithText("尝试公众号链接登录").performScrollTo().assertIsDisplayed()
                compose.onNodeWithText("尝试公众号链接登录").performClick()
                compose.onNodeWithText("复制链接到微信").performScrollTo().assertIsDisplayed()
                compose.onNodeWithText("复制链接到微信").performClick()
                compose.onNodeWithText("复制公众号链接").performClick()
                main {
                    assertEquals(
                        "http://weixin.qq.com/q/synthetic-scene",
                        clipboard.primaryClip?.getItemAt(0)?.text?.toString(),
                    )
                    clipboard.setPrimaryClip(ClipData.newPlainText("test", "new user content"))
                }
                compose.onNodeWithText("获取新二维码").performScrollTo().performClick()
                main { assertEquals("new user content", clipboard.primaryClip?.getItemAt(0)?.text?.toString()) }
            }
        } finally {
            main { clipboard.clearPrimaryClip() }
            app.publicAccountLoginSessionFactory = previousPublic
            app.loginSessionFactory = previous
        }
    }

    private fun withModel(
        factory: () -> LoginSession,
        sceneFactory: (() -> LoginSession)? = null,
        test: (LoginViewModel) -> Unit,
    ) {
        val store = ViewModelStore()
        lateinit var model: LoginViewModel
        main {
            model = ViewModelProvider(store, object : ViewModelProvider.Factory {
                @Suppress("UNCHECKED_CAST")
                override fun <T : ViewModel> create(modelClass: Class<T>): T =
                    LoginViewModel(factory, sceneFactory ?: factory) as T
            })[LoginViewModel::class.java]
        }
        try { test(model) } finally { main { store.clear() } }
    }

    private fun main(action: () -> Unit) = instrumentation.runOnMainSync(action)

    private fun await(condition: () -> Boolean) {
        val until = System.currentTimeMillis() + 5000
        while (!condition() && System.currentTimeMillis() < until) Thread.sleep(20)
        assertTrue("login condition timed out", condition())
    }

    private class FakeLogin(private val ignoreCancellation: Boolean = false, private val failure: String? = null) : LoginSession {
        val confirmed = CompletableDeferred<Unit>()
        @Volatile var starts = 0
        @Volatile var waiting = false
        @Volatile var completed = false
        @Volatile var cancelled = false
        override suspend fun start(): ByteArray {
            starts++
            val bitmap = android.graphics.Bitmap.createBitmap(8, 8, android.graphics.Bitmap.Config.ARGB_8888)
            return java.io.ByteArrayOutputStream().use {
                bitmap.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it)
                bitmap.recycle()
                it.toByteArray()
            }
        }
        override fun confirmationURL(): String = "http://weixin.qq.com/q/synthetic-scene"
        override suspend fun complete() {
            waiting = true
            if (ignoreCancellation) withContext(NonCancellable) { confirmed.await() } else confirmed.await()
            if (failure != null) error(failure)
            completed = true
        }
        override fun cancel() { cancelled = true }
    }
}
