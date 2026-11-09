package lab.infinitegalaxy.rainclass

import androidx.lifecycle.ViewModelStore
import androidx.lifecycle.ViewModelProvider
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.withContext
import lab.infinitegalaxy.rainclass.data.ScreenDataSource
import lab.infinitegalaxy.rainclass.data.Snapshot
import lab.infinitegalaxy.rainclass.ui.RainViewModel
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class RainViewModelTest {
    private val instrumentation get() = InstrumentationRegistry.getInstrumentation()
    private val app: RainApplication get() = ApplicationProvider.getApplicationContext()

    @Test fun loginRefreshesImmediatelyAndDoesNotDuplicateWhileBusy() = withModel { model, source ->
        val finished = CompletableDeferred<Snapshot>()
        source.refreshAction = { finished.await() }
        main { model.loginSucceeded(); model.loginSucceeded(); model.load() }
        await { source.refreshes == 1 }
        main {
            assertTrue(model.state.value.busy)
            assertEquals("ready", model.state.value.snapshot.authState)
        }
        finished.complete(Snapshot("ready", 200))
        await { !model.state.value.busy }
        assertEquals(200L, model.state.value.snapshot.fetchedAt)
        assertEquals(1, source.refreshes)
    }

    @Test fun lateCacheLoadCannotReplaceFreshResult() = withModel { model, source ->
        val cached = CompletableDeferred<Snapshot>()
        val started = CompletableDeferred<Unit>()
        source.snapshotAction = {
            started.complete(Unit)
            withContext(NonCancellable) { cached.await() }
        }
        main { model.load() }
        await { started.isCompleted }
        main { model.refresh() }
        await { source.refreshes == 1 && !model.state.value.busy }
        cached.complete(Snapshot("ready", 1))
        instrumentation.waitForIdleSync()
        main { assertEquals(200L, model.state.value.snapshot.fetchedAt) }
    }

    @Test fun refreshFailureKeepsCacheAndAllowsRetry() = withModel { model, source ->
        source.refreshAction = { error("network") }
        main { model.loginSucceeded() }
        await { source.refreshes == 1 && !model.state.value.busy }
        main {
            assertEquals(100L, model.state.value.snapshot.fetchedAt)
            assertNotNull(model.state.value.message)
        }
        source.refreshAction = { Snapshot("ready", 300) }
        main { model.refresh() }
        await { source.refreshes == 2 && !model.state.value.busy }
        main {
            assertEquals(300L, model.state.value.snapshot.fetchedAt)
            assertNull(model.state.value.message)
        }
    }

    @Test fun openingAppRestoresCacheWithoutLoggingInAgain() = withModel { model, source ->
        main { model.load() }
        await { model.state.value.snapshot.authState == "ready" }
        assertEquals(0, source.refreshes)
        assertEquals(100L, model.state.value.snapshot.fetchedAt)
    }

    private fun withModel(test: (RainViewModel, FakeSource) -> Unit) {
        val source = FakeSource()
        val store = ViewModelStore()
        lateinit var model: RainViewModel
        main {
            model = ViewModelProvider(store, object : ViewModelProvider.Factory {
                override fun <T : androidx.lifecycle.ViewModel> create(modelClass: Class<T>): T {
                    @Suppress("UNCHECKED_CAST")
                    return RainViewModel(app, source) as T
                }
            })[RainViewModel::class.java]
            model.background(false)
        }
        try { test(model, source) } finally { main { store.clear() } }
    }

    private fun main(action: () -> Unit) = instrumentation.runOnMainSync(action)

    private fun await(condition: () -> Boolean) {
        val until = System.currentTimeMillis() + 5000
        while (!condition() && System.currentTimeMillis() < until) Thread.sleep(10)
        assertTrue("asynchronous state did not settle", condition())
    }

    private class FakeSource : ScreenDataSource {
        @Volatile var refreshes = 0
        var snapshotAction: suspend () -> Snapshot = { Snapshot("ready", 100) }
        var refreshAction: suspend () -> Snapshot = { Snapshot("ready", 200) }
        override suspend fun snapshot() = snapshotAction()
        override suspend fun refresh(): Snapshot { refreshes++; return refreshAction() }
        override suspend fun signOut() = Unit
    }
}
