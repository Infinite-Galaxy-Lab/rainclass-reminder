package lab.infinitegalaxy.rainclass.ui

import lab.infinitegalaxy.rainclass.data.Task
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

enum class TaskFilter(val label: String) {
    ACTIVE("待办"), DUE_SOON("24h 内截止"), ALL("全部"), OVERDUE("已截止"), DONE("已完成"), MATERIALS("课件")
}

enum class TaskOrder(val label: String) { TIME("时间优先"), COURSE("按课程") }

enum class TaskSection(val label: String) {
    UPCOMING("接下来的安排"), UNDATED("未设时间"), STARTED("已开始的课堂"),
    OVERDUE("已截止"), DONE("已完成"), MATERIALS("课件与视频")
}

data class PresentedTask(val task: Task, val section: TaskSection, val time: Long) {
    val active: Boolean get() = when (section) {
        TaskSection.UPCOMING, TaskSection.UNDATED, TaskSection.STARTED -> true
        else -> false
    }
    val statusLabel: String get() = when (section) {
        TaskSection.DONE -> "已完成"
        TaskSection.OVERDUE -> "已截止"
        TaskSection.STARTED -> "已开始"
        TaskSection.MATERIALS -> if (task.status == "done") "已完成" else "学习资料"
        else -> if (task.status == "unknown") "状态未知" else "待完成"
    }
}

data class TaskListSection(val section: TaskSection, val items: List<PresentedTask>)

class TaskPresentation(tasks: List<Task>, private val now: Long) {
    private val items = tasks.map { task ->
        val time = when (task.type) { "video" -> 0; "class" -> task.startAt; else -> task.deadline }
        val section = when {
            task.type == "video" -> TaskSection.MATERIALS
            task.status == "done" -> TaskSection.DONE
            task.status == "expired" || (task.type != "class" && time > 0 && time <= now) -> TaskSection.OVERDUE
            time <= 0 -> TaskSection.UNDATED
            task.type == "class" && time <= now -> TaskSection.STARTED
            else -> TaskSection.UPCOMING
        }
        PresentedTask(task, section, time)
    }

    val pending: Int = items.count { it.active }
    val dueSoon: Int = items.count { isDueSoon(it) }
    val overdue: Int = items.count { it.section == TaskSection.OVERDUE }
    val courses: List<String> = tasks.map { it.course }.filter { it.isNotBlank() }.distinct().sorted()

    fun count(filter: TaskFilter): Int = items.count { matches(it, filter) }

    fun select(filter: TaskFilter, query: String, course: String, order: TaskOrder): List<TaskListSection> {
        val keyword = query.trim()
        val selected = items.filter {
            matches(it, filter) && (course.isEmpty() || it.task.course == course) &&
                (keyword.isEmpty() || it.task.title.contains(keyword, true) || it.task.course.contains(keyword, true))
        }.sortedWith { a, b ->
            compareValues(a.section.ordinal, b.section.ordinal).takeIf { it != 0 }
                ?: (if (order == TaskOrder.COURSE) compareValues(a.task.course, b.task.course) else 0).takeIf { it != 0 }
                ?: compareTime(a, b).takeIf { it != 0 }
                ?: compareValuesBy(a, b, { it.task.course }, { it.task.title }, { it.task.id })
        }
        return selected.groupBy { it.section }.map { (section, members) -> TaskListSection(section, members) }
    }

    private fun matches(item: PresentedTask, filter: TaskFilter): Boolean = when (filter) {
        TaskFilter.ACTIVE -> item.active
        TaskFilter.DUE_SOON -> isDueSoon(item)
        TaskFilter.ALL -> true
        TaskFilter.OVERDUE -> item.section == TaskSection.OVERDUE
        TaskFilter.DONE -> item.task.status == "done"
        TaskFilter.MATERIALS -> item.section == TaskSection.MATERIALS
    }

    private fun isDueSoon(item: PresentedTask): Boolean =
        item.section == TaskSection.UPCOMING && item.task.type != "class" && item.time - now <= 86_400_000L

    private fun compareTime(a: PresentedTask, b: PresentedTask): Int {
        if ((a.time > 0) != (b.time > 0)) return if (a.time > 0) -1 else 1
        return when (a.section) {
            TaskSection.OVERDUE, TaskSection.DONE, TaskSection.STARTED -> compareValues(b.time, a.time)
            else -> compareValues(a.time, b.time)
        }
    }
}

fun taskTime(time: Long, now: Long, zone: ZoneId = ZoneId.systemDefault()): String {
    val date = Instant.ofEpochMilli(time).atZone(zone)
    val today = Instant.ofEpochMilli(now).atZone(zone).toLocalDate()
    val pattern = when (date.toLocalDate()) {
        today -> "'今天' HH:mm"
        today.plusDays(1) -> "'明天' HH:mm"
        else -> if (date.year == today.year) "M月d日 HH:mm" else "yyyy年M月d日 HH:mm"
    }
    return date.format(DateTimeFormatter.ofPattern(pattern, Locale.CHINA))
}

fun remainingTime(time: Long, now: Long): String {
    val minutes = ((time - now + 59_999) / 60_000).coerceAtLeast(1)
    return when {
        minutes < 60 -> "还剩 ${minutes} 分钟"
        minutes < 1440 -> "还剩 ${minutes / 60} 小时${if (minutes % 60 > 0) " ${minutes % 60} 分钟" else ""}"
        else -> "还剩 ${minutes / 1440} 天${if (minutes % 1440 / 60 > 0) " ${minutes % 1440 / 60} 小时" else ""}"
    }
}
