package lab.infinitegalaxy.rainclass

import android.Manifest
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.provider.Settings
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.activity.viewModels
import androidx.compose.runtime.*
import androidx.core.app.NotificationManagerCompat
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import lab.infinitegalaxy.rainclass.ui.RainScreen
import lab.infinitegalaxy.rainclass.ui.RainTheme
import lab.infinitegalaxy.rainclass.ui.RainViewModel

class MainActivity : ComponentActivity() {
    private val model: RainViewModel by viewModels()
    private var notificationsAllowed by mutableStateOf(false)
    private val login = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) {
        if (it.resultCode == RESULT_OK) model.loginSucceeded()
    }
    private val notificationPermission = registerForActivityResult(ActivityResultContracts.RequestPermission()) {
        notificationsAllowed = NotificationManagerCompat.from(this).areNotificationsEnabled()
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent {
            val state by model.state.collectAsStateWithLifecycle()
            RainTheme {
                RainScreen(state, notificationsAllowed,
                    onLogin = { login.launch(Intent(this, LoginActivity::class.java)) },
                    onRefresh = model::refresh, onSignOut = model::signOut,
                    onBackground = model::background,
                    onNotificationPermission = {
                        if (Build.VERSION.SDK_INT >= 33 && !notificationsAllowed) {
                            notificationPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
                        } else {
                            startActivity(Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS)
                                .putExtra(Settings.EXTRA_APP_PACKAGE, packageName))
                        }
                    }, onOpenTask = { url ->
                        val uri = Uri.parse(url)
                        if (uri.scheme == "https" && uri.host == "changjiang.yuketang.cn") {
                            runCatching { startActivity(Intent(Intent.ACTION_VIEW, uri)) }
                        }
                    })
            }
        }
    }

    override fun onResume() {
        super.onResume()
        notificationsAllowed = NotificationManagerCompat.from(this).areNotificationsEnabled()
        model.load()
    }
}
