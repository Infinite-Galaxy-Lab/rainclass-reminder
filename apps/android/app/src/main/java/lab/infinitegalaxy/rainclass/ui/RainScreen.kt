package lab.infinitegalaxy.rainclass.ui

import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import kotlinx.coroutines.delay
import lab.infinitegalaxy.rainclass.R

@OptIn(ExperimentalMaterial3Api::class)
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
    var filter by rememberSaveable { mutableStateOf(TaskFilter.ACTIVE) }
    var query by rememberSaveable { mutableStateOf("") }
    var course by rememberSaveable { mutableStateOf("") }
    var order by rememberSaveable { mutableStateOf(TaskOrder.TIME) }
    var showSettings by rememberSaveable { mutableStateOf(false) }
    var confirmSignOut by rememberSaveable { mutableStateOf(false) }
    val now by rememberCurrentTime()
    val presentation = remember(state.snapshot.tasks, now) { TaskPresentation(state.snapshot.tasks, now) }
    val sections = remember(presentation, filter, query, course, order) { presentation.select(filter, query, course, order) }
    val signedIn = state.snapshot.authState != "signed_out"
    val listState = rememberLazyListState()
    LaunchedEffect(filter, query, course, order) { listState.scrollToItem(0) }
    LaunchedEffect(presentation.courses, signedIn) {
        if (course !in presentation.courses) course = ""
        if (!signedIn) { query = ""; filter = TaskFilter.ACTIVE }
    }

    Scaffold(containerColor = Canvas, topBar = {
        TopAppBar(title = {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Image(painterResource(R.mipmap.ic_launcher), null, Modifier.size(34.dp))
                Spacer(Modifier.width(10.dp))
                Column {
                    Text("雨课堂提醒", fontSize = 19.sp, fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    Text(if (state.snapshot.fetchedAt > 0) "同步于 ${taskTime(state.snapshot.fetchedAt, now)}" else "课程安排 · 只读同步",
                        fontSize = 10.sp, color = Muted, maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
            }
        }, actions = {
            if (signedIn) IconButton(onClick = onRefresh, enabled = !state.busy) {
                if (state.busy) CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp)
                else Icon(Icons.Outlined.Sync, "刷新")
            }
            IconButton(onClick = { showSettings = true }) { Icon(Icons.Outlined.Settings, "设置") }
        }, colors = TopAppBarDefaults.topAppBarColors(containerColor = Canvas))
    }) { insets ->
        Box(Modifier.fillMaxSize().padding(insets), contentAlignment = Alignment.TopCenter) {
            LazyColumn(Modifier.widthIn(max = 600.dp).fillMaxSize(), state = listState,
                contentPadding = PaddingValues(horizontal = 20.dp, vertical = 12.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                if (!signedIn || state.snapshot.authState == "expired") item {
                    ConnectionCard(signedIn, state.busy, onLogin)
                }
                if (signedIn) {
                    item {
                        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            StatCard("待办安排", presentation.pending, Modifier.weight(1f), Blue) {
                                query = ""; course = ""; filter = TaskFilter.ACTIVE
                            }
                            StatCard("24h 截止", presentation.dueSoon, Modifier.weight(1f).testTag("summary-due-soon"), Blue) {
                                query = ""; course = ""; filter = TaskFilter.DUE_SOON
                            }
                            StatCard("已截止", presentation.overdue, Modifier.weight(1f), Amber) {
                                query = ""; course = ""; filter = TaskFilter.OVERDUE
                            }
                        }
                    }
                    item {
                        TaskControls(presentation, filter, query, course, order,
                            { filter = it }, { query = it }, { course = it }, { order = it })
                    }
                    if (state.busy) item {
                        Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                            LinearProgressIndicator(Modifier.fillMaxWidth())
                            Text("正在同步课程与任务。首次同步可能需要几分钟，已有任务仍可查看。",
                                fontSize = 12.sp, color = Muted)
                        }
                    }
                }
                state.message?.let { message ->
                    item {
                        OutlinedCard(shape = RoundedCornerShape(14.dp)) {
                            Row(Modifier.padding(start = 14.dp, end = 6.dp, top = 8.dp, bottom = 8.dp), verticalAlignment = Alignment.CenterVertically) {
                                Text(message, Modifier.weight(1f), fontSize = 12.sp, color = MaterialTheme.colorScheme.error)
                                if (signedIn && state.snapshot.authState != "expired") TextButton(onClick = onRefresh, enabled = !state.busy) { Text("重试") }
                            }
                        }
                    }
                }
                if (signedIn && !notificationsAllowed) item {
                    OutlinedCard(onClick = onNotificationPermission, shape = RoundedCornerShape(14.dp)) {
                        Row(Modifier.padding(14.dp), verticalAlignment = Alignment.CenterVertically) {
                            Icon(Icons.Outlined.NotificationsOff, null, Modifier.size(20.dp), tint = Amber)
                            Text("开启通知，才能收到提醒", Modifier.weight(1f).padding(horizontal = 10.dp), fontSize = 12.sp)
                            Icon(Icons.Outlined.ChevronRight, null, Modifier.size(18.dp))
                        }
                    }
                }
                if (signedIn && sections.isEmpty() && !state.busy) item {
                    Column(Modifier.fillMaxWidth().padding(vertical = 28.dp), horizontalAlignment = Alignment.CenterHorizontally) {
                        Icon(Icons.Outlined.TaskAlt, null, Modifier.size(34.dp), tint = Blue)
                        Text(when {
                            state.snapshot.fetchedAt == 0L -> "准备好后，点击刷新同步任务"
                            query.isNotBlank() || course.isNotEmpty() -> "没有匹配的任务"
                            filter == TaskFilter.ACTIVE -> "当前没有待办安排"
                            else -> "这里暂时没有符合条件的任务"
                        }, Modifier.padding(top = 12.dp), color = Ink, fontSize = 14.sp)
                        if (query.isNotBlank() || course.isNotEmpty() || filter != TaskFilter.ALL) TextButton(onClick = {
                            query = ""; course = ""; filter = TaskFilter.ALL
                        }) { Text("查看全部任务") }
                    }
                }
                sections.forEach { group ->
                    item(key = "section-${group.section}") {
                        Row(Modifier.fillMaxWidth().padding(top = 6.dp, bottom = 2.dp), verticalAlignment = Alignment.CenterVertically) {
                            Text(group.section.label, Modifier.weight(1f), fontSize = 14.sp, fontWeight = FontWeight.SemiBold, color = Ink)
                            Text("${group.items.size} 项", fontSize = 11.sp, color = Muted)
                        }
                    }
                    items(group.items, key = { "task-${it.task.id}" }) { TaskCard(it, now, onOpenTask) }
                }
                if (signedIn) item {
                    Text("临近截止或开课的安排优先显示。课件不参与截止提醒。\n后台提醒可能受系统省电影响。",
                        Modifier.padding(top = 8.dp, bottom = 12.dp), fontSize = 11.sp, lineHeight = 18.sp, color = Muted)
                }
            }
        }
    }
    if (showSettings) ReminderSettings(state, signedIn, onBackground, { showSettings = false }, {
        showSettings = false; confirmSignOut = true
    })
    if (confirmSignOut) AlertDialog(onDismissRequest = { confirmSignOut = false }, title = { Text("退出登录？") },
        text = { Text("这会清除本机登录凭据、任务缓存和提醒记录，不影响雨课堂里的数据。") },
        confirmButton = { TextButton(onClick = { confirmSignOut = false; onSignOut() }) { Text("退出登录") } },
        dismissButton = { TextButton(onClick = { confirmSignOut = false }) { Text("取消") } })
}

@Composable
private fun StatCard(label: String, count: Int, modifier: Modifier, color: Color, onClick: () -> Unit) {
    Card(onClick, modifier, shape = RoundedCornerShape(16.dp), colors = CardDefaults.cardColors(containerColor = Color.White)) {
        Column(Modifier.padding(horizontal = 12.dp, vertical = 14.dp)) {
            Text(count.toString(), fontSize = 26.sp, fontWeight = FontWeight.SemiBold, color = color)
            Text(label, color = Muted, fontSize = 10.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
    }
}

@Composable
private fun ConnectionCard(signedIn: Boolean, busy: Boolean, onLogin: () -> Unit) {
    Card(shape = RoundedCornerShape(20.dp), colors = CardDefaults.cardColors(containerColor = Ink)) {
        Column(Modifier.fillMaxWidth().padding(22.dp)) {
            Icon(Icons.Outlined.CloudSync, null, Modifier.size(28.dp), tint = Color(0xFFAACDFF))
            Text(if (signedIn) "重新连接雨课堂" else "连接你的课堂", Modifier.padding(top = 12.dp),
                color = Color.White, fontSize = 22.sp, fontWeight = FontWeight.SemiBold)
            Text(if (signedIn) "登录已失效，本机缓存仍可查看。重新登录后恢复同步。" else "在官方页面扫码登录，集中查看课程任务与截止时间。",
                Modifier.padding(vertical = 12.dp), color = Color(0xFFCED9EA), fontSize = 13.sp)
            Button(onLogin, enabled = !busy, colors = ButtonDefaults.buttonColors(containerColor = Color.White, contentColor = Ink)) {
                Text(if (signedIn) "重新登录" else "登录雨课堂")
            }
        }
    }
}

@Composable
private fun rememberCurrentTime(): State<Long> {
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    return produceState(System.currentTimeMillis(), lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (true) { value = System.currentTimeMillis(); delay(30_000) }
        }
    }
}
