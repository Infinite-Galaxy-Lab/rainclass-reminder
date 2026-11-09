package lab.infinitegalaxy.rainclass.ui

import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import lab.infinitegalaxy.rainclass.BuildConfig

@Composable
internal fun ReminderSettings(state: ScreenState, signedIn: Boolean, onBackground: (Boolean) -> Unit,
                              onDismiss: () -> Unit, onSignOut: () -> Unit) {
    AlertDialog(onDismissRequest = onDismiss, title = { Text("提醒设置") }, text = {
        Column {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text("后台同步与提醒", fontWeight = FontWeight.Medium)
                    Text("系统周期调度，可能延后", fontSize = 12.sp, color = Muted)
                }
                Switch(checked = state.background, onCheckedChange = onBackground)
            }
            Text("登录凭据仅在本机加密保存。应用只读取任务，不答题、不提交。", fontSize = 12.sp,
                color = Muted, modifier = Modifier.padding(top = 16.dp))
            Text("Android ${BuildConfig.VERSION_NAME}", fontSize = 11.sp, color = Muted, modifier = Modifier.padding(top = 16.dp))
            if (signedIn) TextButton(onClick = onSignOut, enabled = !state.busy) {
                Text("退出登录并清除本地任务", color = MaterialTheme.colorScheme.error)
            }
        }
    }, confirmButton = { TextButton(onClick = onDismiss) { Text("完成") } })
}
