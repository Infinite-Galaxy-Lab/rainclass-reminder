package lab.infinitegalaxy.rainclass

import android.content.Context
import android.database.sqlite.SQLiteDatabase
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import lab.infinitegalaxy.rainclass.data.SessionVault
import lab.infinitegalaxy.rainclass.data.Snapshot
import mobile.Engine
import mobile.Mobile
import mobile.Notifications
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File
import java.util.UUID

@RunWith(AndroidJUnit4::class)
class SessionPersistenceTest {
    private val context: Context get() = ApplicationProvider.getApplicationContext()

    @Test fun sessionSurvivesFreshVaultAndEngineInstances() {
        val directory = File(context.noBackupFilesDir, "session-persistence-${UUID.randomUUID()}")
        val account = "synthetic-account-${UUID.randomUUID()}"
        val sessionId = "synthetic-session-${UUID.randomUUID()}"
        val session = JSONObject()
            .put("account", account)
            .put("cookie", JSONObject().put("sessionid", sessionId))
            .toString()
        val notifications = object : Notifications {
            override fun notify(title: String, body: String) = Unit
        }
        var initialEngine: Engine? = null
        var reopenedEngine: Engine? = null
        var signedOutEngine: Engine? = null

        try {
            SessionVault(context).save("")
            initialEngine = Mobile.newEngine(
                directory.absolutePath,
                SessionVault(context),
                notifications,
                java.util.TimeZone.getDefault().id,
            )
            initialEngine.close()
            initialEngine = null

            SQLiteDatabase.openDatabase(
                File(directory, "state.db").absolutePath,
                null,
                SQLiteDatabase.OPEN_READWRITE,
            ).use { database ->
                database.execSQL(
                    "INSERT OR REPLACE INTO kv(key,value) VALUES(?,?)",
                    arrayOf<Any?>("mobile_account", account),
                )
            }

            SessionVault(context).save(session)
            assertTrue(context.getSharedPreferences("session-vault", Context.MODE_PRIVATE).contains("value"))
            assertEquals(session, SessionVault(context).load())

            val reopenedVault = SessionVault(context)
            assertEquals(session, reopenedVault.load())
            reopenedEngine = Mobile.newEngine(
                directory.absolutePath,
                reopenedVault,
                notifications,
                java.util.TimeZone.getDefault().id,
            )
            assertEquals("ready", Snapshot.parse(reopenedEngine.snapshot()).authState)

            reopenedEngine.signOut()
            assertEquals("signed_out", Snapshot.parse(reopenedEngine.snapshot()).authState)
            assertEquals("", reopenedVault.load())
            assertFalse(context.getSharedPreferences("session-vault", Context.MODE_PRIVATE).contains("value"))
            reopenedEngine.close()
            reopenedEngine = null

            val signedOutVault = SessionVault(context)
            assertEquals("", signedOutVault.load())
            signedOutEngine = Mobile.newEngine(
                directory.absolutePath,
                signedOutVault,
                notifications,
                java.util.TimeZone.getDefault().id,
            )
            assertEquals("signed_out", Snapshot.parse(signedOutEngine.snapshot()).authState)
        } finally {
            runCatching { initialEngine?.close() }
            runCatching { reopenedEngine?.close() }
            runCatching { signedOutEngine?.close() }
            runCatching { SessionVault(context).save("") }
            directory.deleteRecursively()
        }
    }
}



