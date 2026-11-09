package lab.infinitegalaxy.rainclass.reminders

import android.Manifest
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import lab.infinitegalaxy.rainclass.MainActivity
import mobile.Notifications

class AndroidNotifications(private val context: Context, private val channelId: String = CHANNEL) : Notifications {
    companion object { const val CHANNEL = "deadline-reminders" }

    init {
        context.getSystemService(NotificationManager::class.java).createNotificationChannel(
            NotificationChannel(channelId, "截止与登录提醒", NotificationManager.IMPORTANCE_DEFAULT)
        )
    }

    override fun notify(title: String, body: String) {
        if (Build.VERSION.SDK_INT >= 33 && ContextCompat.checkSelfPermission(context,
                Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            throw IllegalStateException("notifications_disabled")
        }
        val manager = NotificationManagerCompat.from(context)
        if (!manager.areNotificationsEnabled() || context.getSystemService(NotificationManager::class.java)
                .getNotificationChannel(channelId)?.importance == NotificationManager.IMPORTANCE_NONE) {
            throw IllegalStateException("notifications_disabled")
        }
        val intent = PendingIntent.getActivity(context, 0, Intent(context, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        val plain = body.replace(Regex("\\[([^]]+)]\\([^)]+\\)"), "$1").replace("**", "")
        val notification = NotificationCompat.Builder(context, channelId)
            .setSmallIcon(android.R.drawable.ic_popup_reminder)
            .setContentTitle(title).setContentText(plain)
            .setStyle(NotificationCompat.BigTextStyle().bigText(plain))
            .setContentIntent(intent).setAutoCancel(true)
            .setVisibility(NotificationCompat.VISIBILITY_PRIVATE).build()
        manager.notify((title + body).hashCode(), notification)
    }
}
