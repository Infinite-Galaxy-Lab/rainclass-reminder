package lab.infinitegalaxy.rainclass.ui

import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.ArrowForward
import androidx.compose.material.icons.automirrored.outlined.OpenInNew
import androidx.compose.material.icons.outlined.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import lab.infinitegalaxy.rainclass.R
import lab.infinitegalaxy.rainclass.BuildConfig
import lab.infinitegalaxy.rainclass.data.Task
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

private val Ink = Color(0xFF152C4A)
private val Blue = Color(0xFF246BCE)
private val Muted = Color(0xFF6E7D91)

@Composable
fun RainTheme(content: @Composable () -> Unit) {
    MaterialTheme(colorScheme = lightColorScheme(primary = Blue, secondary = Color(0xFFE18A32),
        background = Color(0xFFF5F7FB), surface = Color.White, onSurface = Ink), content = content)
}

@Composable
fun RainScreen(
    state: ScreenState,
    notificationsAllowed: Boolean,
    onLogin: () -> Unit,
    onRefresh: () -> Unit,
    onSignOut: () -> Unit,
    onBackground: (Boolean) -> Unit,
    onNotificationPermission: () -> Unit,
    onOpenTask: (String) -> Unit,
) {
    var filter by remember { mutableStateOf("待完成") }
    var query by remember { mutableStateOf("") }
    var showSettings by remember { mutableStateOf(false) }
    var confirmSignOut by remember { mutableStateOf(false) }
    val signedIn = state.snapshot.authState != "signed_out"
    val visible = state.snapshot.tasks.filter { task ->
        (filter == "全部" || (filter == "已完成") == (task.status == "done")) &&
            (query.isBlank() || task.title.contains(query, true) || task.course.contains(query, true))
    }
    val pending = state.snapshot.tasks.count { it.status != "done" && it.type != "video" }
    val upcoming = state.snapshot.tasks.count {
        it.status != "done" && it.deadline in System.currentTimeMillis()..(System.currentTimeMillis() + 86400000)
            && it.type != "video"
    }

    Scaffold(containerColor = MaterialTheme.colorScheme.background) { insets ->
        Box(Modifier.fillMaxSize().padding(insets), contentAlignment = Alignment.TopCenter) {
            LazyColumn(Modifier.widthIn(max = 600.dp).fillMaxSize(), contentPadding = PaddingValues(22.dp),
                verticalArrangement = Arrangement.spacedBy(16.dp)) {
                item {
                    Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.fillMaxWidth()) {
                        Image(painterResource(R.mipmap.ic_launcher), "雨课堂提醒图标", Modifier.size(46.dp))
                        Spacer(Modifier.width(12.dp))
                        Column(Modifier.weight(1f)) {
                            Text("雨课堂提醒", fontWeight = FontWeight.Bold, fontSize = 21.sp, color = Ink)
                            Text("RAINCLASS REMINDER", color = Muted, fontSize = 10.sp, letterSpacing = 1.1.sp)
                        }
                        IconButton(onClick = { showSettings = true }) { Icon(Icons.Outlined.Settings, "设置", tint = Ink) }
                    }
                }
                item {
                    Column(Modifier.padding(top = 12.dp, bottom = 4.dp)) {
                        Text("把截止时间，\n留在视线里。", fontSize = 30.sp, lineHeight = 40.sp,
                            fontWeight = FontWeight.Bold, color = Ink)
                        Spacer(Modifier.height(10.dp))
                        Text(if (state.snapshot.fetchedAt > 0) "上次同步 ${formatTime(state.snapshot.fetchedAt)} · 只读同步"
                             else "只读同步课程任务，提前提醒重要安排。", fontSize = 12.sp, color = Muted)
                    }
                }
                if (!signedIn || state.snapshot.authState == "expired") {
                    item {
                        Card(colors = CardDefaults.cardColors(containerColor = Ink), shape = RoundedCornerShape(22.dp)) {
                            Column(Modifier.fillMaxWidth().padding(22.dp)) {
                                Icon(Icons.Outlined.CloudSync, null, tint = Color(0xFFAACDFF), modifier = Modifier.size(30.dp))
                                Spacer(Modifier.height(16.dp))
                                Text(if (signedIn) "重新连接雨课堂" else "连接你的课堂", color = Color.White,
                                    fontSize = 21.sp, fontWeight = FontWeight.SemiBold)
                                Text("在官方页面登录，随后即可查看课程任务和截止时间。", color = Color(0xFFCED9EA),
                                    fontSize = 13.sp, modifier = Modifier.padding(vertical = 12.dp))
                                Button(onClick = onLogin, colors = ButtonDefaults.buttonColors(containerColor = Color.White,
                                    contentColor = Ink), enabled = !state.busy) {
                                    Text(if (signedIn) "重新登录" else "登录雨课堂")
                                    Spacer(Modifier.width(8.dp)); Icon(Icons.AutoMirrored.Outlined.ArrowForward, null, Modifier.size(18.dp))
                                }
                            }
                        }
                    }
                }
                if (signedIn) {
                    item {
                        Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                            StatCard("待关注", pending, Modifier.weight(1f), Blue)
                            StatCard("24 小时内截止", upcoming, Modifier.weight(1f), Color(0xFFD57B25))
                        }
                    }
                    item {
                        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                            Text("我的任务", fontSize = 19.sp, fontWeight = FontWeight.Bold, modifier = Modifier.weight(1f))
                            TextButton(onClick = onRefresh, enabled = !state.busy) {
                                Icon(Icons.Outlined.Sync, null, Modifier.size(18.dp)); Spacer(Modifier.width(6.dp))
                                Text(if (state.busy) "正在同步" else "刷新")
                            }
                        }
                        OutlinedTextField(query, { query = it }, Modifier.fillMaxWidth(),
                            placeholder = { Text("搜索课程或任务") }, singleLine = true,
                            leadingIcon = { Icon(Icons.Outlined.Search, null) }, shape = RoundedCornerShape(14.dp))
                        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            listOf("待完成", "全部", "已完成").forEach { label ->
                                FilterChip(selected = filter == label, onClick = { filter = label }, label = { Text(label) })
                            }
                        }
                    }
                    if (state.busy) item { LinearProgressIndicator(Modifier.fillMaxWidth()) }
                }
                state.message?.let { message ->
                    item { Text(message, color = MaterialTheme.colorScheme.error, fontSize = 13.sp) }
                }
                if (signedIn && !notificationsAllowed) {
                    item {
                        OutlinedCard(onClick = onNotificationPermission, shape = RoundedCornerShape(16.dp)) {
                            Row(Modifier.padding(16.dp), verticalAlignment = Alignment.CenterVertically) {
                                Icon(Icons.Outlined.NotificationsOff, null, tint = Color(0xFFD57B25))
                                Spacer(Modifier.width(12.dp))
                                Column(Modifier.weight(1f)) {
                                    Text("允许通知，才能收到提醒", fontSize = 14.sp, fontWeight = FontWeight.Medium)
                                    Text("点击开启通知权限", color = Muted, fontSize = 12.sp)
                                }
                                Icon(Icons.Outlined.ChevronRight, null)
                            }
                        }
                    }
                }
                if (signedIn && visible.isEmpty() && !state.busy) {
                    item {
                        Column(Modifier.fillMaxWidth().padding(vertical = 28.dp), horizontalAlignment = Alignment.CenterHorizontally) {
                            Icon(Icons.Outlined.TaskAlt, null, tint = Blue, modifier = Modifier.size(38.dp))
                            Text(if (state.snapshot.fetchedAt == 0L) "准备好后，点击刷新同步任务" else "这里暂时没有符合条件的任务",
                                modifier = Modifier.padding(top = 12.dp), color = Muted, fontSize = 13.sp)
                        }
                    }
                }
                items(visible, key = { it.id }) { task -> TaskCard(task, onOpenTask) }
                item {
                    Text("提醒由系统安排，省电模式下可能延后。\n课件仅供查看，不按课程结束时间提醒。", color = Muted,
                        fontSize = 11.sp, lineHeight = 19.sp, modifier = Modifier.padding(top = 12.dp, bottom = 16.dp))
                }
            }
        }
    }

    if (showSettings) AlertDialog(onDismissRequest = { showSettings = false }, title = { Text("提醒设置") },
        text = {
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
                if (signedIn) TextButton(onClick = { showSettings = false; confirmSignOut = true }, enabled = !state.busy) {
                    Text("退出登录并清除本地任务", color = MaterialTheme.colorScheme.error)
                }
            }
        }, confirmButton = { TextButton(onClick = { showSettings = false }) { Text("完成") } })
    if (confirmSignOut) AlertDialog(onDismissRequest = { confirmSignOut = false }, title = { Text("退出登录？") },
        text = { Text("这会清除本机登录凭据、任务缓存和提醒记录，不影响雨课堂里的数据。") },
        confirmButton = { TextButton(onClick = { confirmSignOut = false; onSignOut() }) { Text("退出登录") } },
        dismissButton = { TextButton(onClick = { confirmSignOut = false }) { Text("取消") } })
}

@Composable
private fun StatCard(label: String, count: Int, modifier: Modifier, color: Color) {
    Card(modifier, shape = RoundedCornerShape(18.dp), colors = CardDefaults.cardColors(containerColor = Color.White)) {
        Column(Modifier.padding(18.dp)) {
            Text(count.toString().padStart(2, '0'), fontSize = 30.sp, fontWeight = FontWeight.Bold, color = color)
            Text(label, color = Muted, fontSize = 12.sp)
        }
    }
}

@Composable
private fun TaskCard(task: Task, onOpen: (String) -> Unit) {
    val expired = task.deadline > 0 && task.deadline < System.currentTimeMillis() && task.status != "done" && task.type != "video"
    Card(onClick = { onOpen(task.url) }, enabled = task.url.isNotBlank(), shape = RoundedCornerShape(18.dp),
        colors = CardDefaults.cardColors(containerColor = Color.White)) {
        Column(Modifier.fillMaxWidth().padding(18.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(task.label, fontSize = 11.sp, color = Blue,
                    modifier = Modifier.background(Color(0xFFEDF4FF), RoundedCornerShape(6.dp)).padding(horizontal = 8.dp, vertical = 4.dp))
                Spacer(Modifier.weight(1f))
                Text(when {
                    task.status == "done" -> "已完成"
                    expired || task.status == "expired" -> "已截止"
                    task.status == "pending" -> "待完成"
                    else -> "状态未知"
                }, fontSize = 11.sp,
                    color = if (expired) Color(0xFFBD6328) else Muted)
            }
            Text(task.title, fontSize = 16.sp, fontWeight = FontWeight.SemiBold,
                maxLines = 3, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(top = 12.dp))
            Text(task.course, fontSize = 12.sp, color = Muted, modifier = Modifier.padding(top = 6.dp))
            if (task.score != null) Text("得分 ${number(task.score)}" + (task.totalScore?.let { " / ${number(it)}" } ?: ""),
                fontSize = 12.sp, color = Blue, modifier = Modifier.padding(top = 10.dp))
            val time = if (task.type == "class") task.startAt else if (task.type != "video") task.deadline else 0
            if (time > 0) {
                HorizontalDivider(Modifier.padding(vertical = 12.dp), color = Color(0xFFF0F3F7))
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Icon(Icons.Outlined.Schedule, null, Modifier.size(14.dp), tint = Muted)
                    Spacer(Modifier.width(6.dp))
                    Text((if (task.type == "class") "开始 " else "截止 ") + formatTime(time), fontSize = 11.sp, color = Muted)
                    Spacer(Modifier.weight(1f)); Icon(Icons.AutoMirrored.Outlined.OpenInNew, "打开任务", Modifier.size(15.dp), tint = Blue)
                }
            }
        }
    }
}

private fun formatTime(time: Long): String = SimpleDateFormat("MM-dd HH:mm", Locale.CHINA).format(Date(time))
private fun number(value: Double): String = if (value % 1 == 0.0) value.toLong().toString() else value.toString()
