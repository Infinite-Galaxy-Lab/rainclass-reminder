package lab.infinitegalaxy.rainclass

import android.app.NotificationManager
import android.app.NotificationChannel
import android.Manifest
import android.content.pm.PackageManager
import android.content.Context
import android.database.sqlite.SQLiteDatabase
import android.os.ParcelFileDescriptor
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.work.WorkInfo
import androidx.work.WorkManager
import kotlinx.coroutines.runBlocking
import lab.infinitegalaxy.rainclass.data.RainRepository
import lab.infinitegalaxy.rainclass.data.SessionVault
import lab.infinitegalaxy.rainclass.data.Snapshot
import lab.infinitegalaxy.rainclass.reminders.AndroidNotifications
import lab.infinitegalaxy.rainclass.reminders.RefreshWorker
import mobile.Mobile
import mobile.Notifications
import mobile.Vault
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File
import java.util.UUID
import java.util.concurrent.TimeUnit

@RunWith(AndroidJUnit4::class)
class PlatformTest {
    private val context: Context get() = ApplicationProvider.getApplicationContext()

    @Test fun keystoreRoundTripContainsNoPlaintext() {
        val vault = SessionVault(context)
        val synthetic = "synthetic-session-${UUID.randomUUID()}"
        try {
            vault.save(synthetic)
            assertEquals(synthetic, vault.load())
            val stored = context.getSharedPreferences("session-vault", 0).all.toString()
            assertFalse(stored.contains(synthetic))
            assertTrue(stored.contains("value"))
        } finally { vault.save("") }
        assertEquals("", vault.load())
    }

    @Test fun nativeCoreReopensNullableScoresAndClearsAccountData() {
        val directory = File(context.noBackupFilesDir, "platform-test-${UUID.randomUUID()}")
        val vault = MemoryVault()
        val sink = object : Notifications { override fun notify(title: String, body: String) {} }
        var core = Mobile.newEngine(directory.absolutePath, vault, sink, "Asia/Shanghai")
        assertEquals("signed_out", Snapshot.parse(core.snapshot()).authState)
        core.close()
        try {
            SQLiteDatabase.openDatabase(File(directory, "state.db").absolutePath, null, 0).use { db ->
                db.execSQL("INSERT INTO kv(key,value) VALUES('mobile_account','synthetic-account')")
                for ((id, score) in listOf("zero" to 0.0, "decimal" to 2.75, "missing" to null)) {
                    db.execSQL("INSERT INTO tasks(id,type,title,course,status,score,total_score,fetched_at) VALUES(?,?,?,?,?,?,?,?)",
                        arrayOf<Any?>(id, "homework", "合成任务 $id", "示例课程", "pending", score, 10.0, 1794096000L))
                }
            }
            vault.value = JSONObject().put("account", "synthetic-account")
                .put("cookie", JSONObject().put("sessionid", "synthetic-only")).toString()
            core = Mobile.newEngine(directory.absolutePath, vault, sink, "Asia/Shanghai")
            val snapshot = Snapshot.parse(core.snapshot())
            assertEquals("ready", snapshot.authState)
            val tasks = snapshot.tasks.associateBy { it.id }
            assertEquals(3, tasks.size)
            assertEquals(0.0, tasks.getValue("zero").score!!, 0.0)
            assertEquals(2.75, tasks.getValue("decimal").score!!, 0.0)
            assertNull(tasks.getValue("missing").score)
            core.signOut()
            assertEquals("", vault.value)
            assertTrue(Snapshot.parse(core.snapshot()).tasks.isEmpty())
            core.close()
            core = Mobile.newEngine(directory.absolutePath, vault, sink, "Asia/Shanghai")
            assertEquals("signed_out", Snapshot.parse(core.snapshot()).authState)
            assertTrue(Snapshot.parse(core.snapshot()).tasks.isEmpty())
        } finally { core.close(); directory.deleteRecursively() }
    }

    @Test fun damagedVaultCanBeResetForNewLogin() {
        val vault = SessionVault(context)
        try {
            context.getSharedPreferences("session-vault", 0).edit().putString("value", "synthetic-corruption").commit()
            try { vault.load(); fail("corrupted vault was accepted") } catch (_: IllegalStateException) {}
            vault.reset()
            assertFalse(context.getSharedPreferences("session-vault", 0).contains("value"))
            assertEquals("", vault.load())
            vault.save("synthetic-recovered")
            assertEquals("synthetic-recovered", vault.load())
        } finally { vault.save("") }
    }

    @Test fun refreshCanBeCanceledBeforeNativeOperationStarts() {
        val directory = File(context.noBackupFilesDir, "cancel-test-${UUID.randomUUID()}")
        val sink = object : Notifications { override fun notify(title: String, body: String) {} }
        val core = Mobile.newEngine(directory.absolutePath, MemoryVault(), sink, "Pacific/Chatham")
        try {
            core.setTimeZone("America/New_York")
            try { core.setTimeZone("Invalid/Zone"); fail("invalid zone accepted") } catch (_: Exception) {}
            val request = core.newRefresh()
            request.cancel()
            try { request.run(); fail("canceled operation ran") }
            catch (error: Exception) { assertTrue(error.message.orEmpty().contains("canceled")) }
            assertEquals("signed_out", Snapshot.parse(core.snapshot()).authState)
        } finally { core.close(); directory.deleteRecursively() }
    }

    @Test fun stoppedWorkerCannotStartRequestOrCancelLaterRefresh() = runBlocking {
        val repository = RainRepository(context)
        for (worker in listOf("already-stopped", "stopped-during-registration")) {
            try {
                repository.refresh(worker) {
                    if (worker == "already-stopped") true
                    else { repository.cancelWorker(worker); false }
                }
                fail("stopped worker reached native refresh")
            } catch (error: Exception) { assertTrue(error.message.orEmpty().contains("canceled")) }
        }
        try {
            repository.refresh("another-worker") { repository.cancelWorker("unrelated"); false }
            fail("signed-out refresh succeeded")
        } catch (error: Exception) { assertTrue(error.message.orEmpty().contains("session_required")) }
    }

    @Test fun notificationsUsePrivateVisibilityAndCanBeDisabled() {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        fun shell(command: String) {
            ParcelFileDescriptor.AutoCloseInputStream(instrumentation.uiAutomation.executeShellCommand(command)).use { it.readBytes() }
        }
        val channel = "synthetic-${UUID.randomUUID()}"
        val notifier = AndroidNotifications(context, channel)
        if (context.checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            try { notifier.notify("合成提醒", "不应发送"); fail("notification permission denial was ignored") }
            catch (_: IllegalStateException) {}
        }
        shell("pm grant ${context.packageName} android.permission.POST_NOTIFICATIONS")
        val manager = context.getSystemService(NotificationManager::class.java)
        try {
            notifier.notify("合成提醒", "仅用于设备测试")
            val until = System.currentTimeMillis() + 3000
            while (manager.activeNotifications.isEmpty() && System.currentTimeMillis() < until) Thread.sleep(50)
            val notification = manager.activeNotifications.first { it.notification.extras.getString("android.title") == "合成提醒" }
            assertEquals(android.app.Notification.VISIBILITY_PRIVATE, notification.notification.visibility)
            manager.createNotificationChannel(NotificationChannel(channel, "合成渠道", NotificationManager.IMPORTANCE_NONE))
            assertEquals(NotificationManager.IMPORTANCE_NONE, manager.getNotificationChannel(channel).importance)
            try { notifier.notify("合成提醒", "不应发送"); fail("notification denial was ignored") }
            catch (_: IllegalStateException) {}
        } finally {
            manager.cancelAll()
            manager.deleteNotificationChannel(channel)
        }
    }

    @Test fun backgroundRefreshIsUniqueAndCancelable() {
        val manager = WorkManager.getInstance(context)
        try {
            RefreshWorker.schedule(context).result.get(10, TimeUnit.SECONDS)
            RefreshWorker.schedule(context).result.get(10, TimeUnit.SECONDS)
            val jobs = manager.getWorkInfosForUniqueWork(RefreshWorker.NAME).get(10, TimeUnit.SECONDS)
                .filter { !it.state.isFinished }
            assertEquals(1, jobs.size)
            assertTrue(jobs.single().tags.contains(RefreshWorker.NAME))
            RefreshWorker.cancel(context).result.get(10, TimeUnit.SECONDS)
            assertTrue(manager.getWorkInfosForUniqueWork(RefreshWorker.NAME).get(10, TimeUnit.SECONDS)
                .all { it.state == WorkInfo.State.CANCELLED || it.state.isFinished })
        } finally { manager.cancelAllWork().result.get(10, TimeUnit.SECONDS) }
    }

    private class MemoryVault : Vault {
        var value = ""
        override fun load() = value
        override fun save(value: String) { this.value = value }
    }
}
