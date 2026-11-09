package lab.infinitegalaxy.rainclass.data

import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.async
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.supervisorScope
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class RefreshQueueTest {
    @Test
    fun overlappingRefreshReusesCompletedSync() = runBlocking {
        val queue = RefreshQueue()
        val entered = CompletableDeferred<Unit>()
        val release = CompletableDeferred<Unit>()
        val actions = AtomicInteger()

        val first = async(start = CoroutineStart.UNDISPATCHED) {
            queue.refresh(
                snapshot = { "reused" },
                action = {
                    actions.incrementAndGet()
                    entered.complete(Unit)
                    release.await()
                    "fresh"
                },
            )
        }
        entered.await()
        val second = async(start = CoroutineStart.UNDISPATCHED) {
            queue.refresh(
                snapshot = { "reused" },
                action = {
                    actions.incrementAndGet()
                    "second"
                },
            )
        }

        release.complete(Unit)
        assertEquals("fresh", first.await())
        assertEquals("reused", second.await())
        assertEquals(1, actions.get())
    }

    @Test
    fun failedRefreshDoesNotCompleteQueuedRefresh() = runBlocking {
        supervisorScope {
            val queue = RefreshQueue()
            val entered = CompletableDeferred<Unit>()
            val release = CompletableDeferred<Unit>()
            val actions = AtomicInteger()

            val first = async(start = CoroutineStart.UNDISPATCHED) {
                queue.refresh(
                    snapshot = { "stale" },
                    action = {
                        actions.incrementAndGet()
                        entered.complete(Unit)
                        release.await()
                        error("synthetic failure")
                    },
                )
            }
            entered.await()
            val second = async(start = CoroutineStart.UNDISPATCHED) {
                queue.refresh(
                    snapshot = { "stale" },
                    action = {
                        actions.incrementAndGet()
                        "second"
                    },
                )
            }

            release.complete(Unit)
            val failure = runCatching { first.await() }.exceptionOrNull()
            assertTrue(failure is IllegalStateException)
            assertEquals("second", second.await())
            assertEquals(2, actions.get())
        }
    }

    @Test
    fun sessionChangeInvalidatesOlderQueuedRefresh() = runBlocking {
        supervisorScope {
            val queue = RefreshQueue()
            val entered = CompletableDeferred<Unit>()
            val release = CompletableDeferred<Unit>()
            val staleActions = AtomicInteger()

            val first = async(start = CoroutineStart.UNDISPATCHED) {
                queue.refresh(
                    snapshot = { "reused" },
                    action = {
                        entered.complete(Unit)
                        release.await()
                        "fresh"
                    },
                )
            }
            entered.await()
            val changed = async(start = CoroutineStart.UNDISPATCHED) {
                queue.changeSession { "changed" }
            }
            val stale = async(start = CoroutineStart.UNDISPATCHED) {
                queue.refresh(
                    snapshot = { "reused" },
                    action = {
                        staleActions.incrementAndGet()
                        "stale"
                    },
                )
            }

            release.complete(Unit)
            assertEquals("fresh", first.await())
            assertEquals("changed", changed.await())
            val failure = runCatching { stale.await() }.exceptionOrNull()
            assertTrue(failure is CancellationException)
            assertEquals("canceled", failure?.message)
            assertEquals(0, staleActions.get())
        }
    }
}