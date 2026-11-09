package lab.infinitegalaxy.rainclass.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.OpenInNew
import androidx.compose.material.icons.outlined.Schedule
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

@Composable
internal fun TaskCard(item: PresentedTask, now: Long, onOpen: (String) -> Unit) {
    val task = item.task
    val urgent = item.section == TaskSection.UPCOMING && item.time - now <= 86_400_000L
    val tint = when { item.section == TaskSection.OVERDUE -> Amber; urgent -> Blue; else -> Muted }
    Card(onClick = { onOpen(task.url) }, modifier = Modifier.testTag("task-${task.id}"), enabled = task.url.isNotBlank(), shape = RoundedCornerShape(18.dp),
        colors = CardDefaults.cardColors(containerColor = Color.White)) {
        Column(Modifier.fillMaxWidth().padding(18.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(task.label, fontSize = 11.sp, color = Blue,
                    modifier = Modifier.background(Color(0xFFEDF3FF), RoundedCornerShape(6.dp)).padding(horizontal = 8.dp, vertical = 4.dp))
                Spacer(Modifier.weight(1f))
                Text(item.statusLabel, fontSize = 11.sp, color = tint)
            }
            Text(task.title, fontSize = 16.sp, fontWeight = FontWeight.SemiBold,
                maxLines = 3, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(top = 12.dp))
            Text(task.course, fontSize = 12.sp, color = Muted, modifier = Modifier.padding(top = 6.dp))
            if (task.score != null) Text("得分 ${number(task.score)}" + (task.totalScore?.let { " / ${number(it)}" } ?: ""),
                fontSize = 12.sp, color = Blue, modifier = Modifier.padding(top = 10.dp))
            if (item.time > 0) {
                HorizontalDivider(Modifier.padding(vertical = 12.dp), color = Canvas)
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Icon(Icons.Outlined.Schedule, null, Modifier.size(14.dp), tint = tint)
                    Spacer(Modifier.width(6.dp))
                    Column(Modifier.weight(1f)) {
                        Text(taskTime(item.time, now) + if (task.type == "class") " 开始" else " 截止", fontSize = 12.sp, color = tint)
                        if (item.section == TaskSection.UPCOMING) Text(
                            if (task.type == "class") "尚未开始" else remainingTime(item.time, now),
                            fontSize = 11.sp, color = Muted, modifier = Modifier.padding(top = 3.dp))
                    }
                    Icon(Icons.AutoMirrored.Outlined.OpenInNew, "打开任务", Modifier.size(15.dp), tint = Blue)
                }
            }
        }
    }
}

private fun number(value: Double): String = if (value % 1 == 0.0) value.toLong().toString() else value.toString()
