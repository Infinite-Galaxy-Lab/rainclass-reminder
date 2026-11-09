package lab.infinitegalaxy.rainclass.ui

import lab.infinitegalaxy.rainclass.data.Task
import org.junit.Assert.*
import org.junit.Test
import java.time.Instant
import java.time.ZoneId

class TaskPresentationTest {
    private val now = 1_800_000_000_000L
    private val hour = 3_600_000L

    private fun task(id: String, time: Long = now + hour, type: String = "homework",
                     status: String = "pending", course: String = "课程 A", title: String = id,
                     start: Long = 0) =
        Task(id, type, "示例类型", title, course, time, start, status, null, null, "https://example.com/")

    private fun TaskPresentation.ids(filter: TaskFilter = TaskFilter.ALL, query: String = "",
                                     course: String = "", order: TaskOrder = TaskOrder.TIME) =
        select(filter, query, course, order).flatMap { it.items }.map { it.task.id }

    @Test fun futureFirstHistoryRecentFirstAndInputUntouched() {
        val input = listOf(task("old", now - 10 * hour), task("far", now + 3 * hour),
            task("undated", 0), task("recent", now - hour), task("near", now + hour),
            task("done-old", now - 2 * hour, status = "done"), task("done-undated", 0, status = "done"),
            task("done-new", now - hour, status = "done"))
        val original = input.toList()
        val view = TaskPresentation(input, now)
        assertEquals(listOf("near", "far", "undated", "recent", "old", "done-new", "done-old", "done-undated"), view.ids())
        assertEquals(original, input)
        assertEquals(listOf("near", "far", "undated"), view.ids(TaskFilter.ACTIVE))
    }

    @Test fun stableTiesKeepSeparateClassroomRecords() {
        val input = listOf(task("b", title = "同名任务"), task("a", title = "同名任务"))
        assertEquals(listOf("a", "b"), TaskPresentation(input, now).ids())
        assertEquals(listOf("a", "b"), TaskPresentation(input.reversed(), now).ids())
    }

    @Test fun deadlineBoundaryAndDueSoonExcludeMaterialsDoneAndClasses() {
        val view = TaskPresentation(listOf(task("at-now", now), task("past", now - 1),
            task("next", now + 1), task("24h", now + 24 * hour), task("later", now + 24 * hour + 1),
            task("done", status = "done"), task("video", type = "video"),
            task("class", type = "class", start = now + hour), task("undated", 0),
            task("explicit-expired", status = "expired")), now)
        assertEquals(3, view.overdue)
        assertEquals(5, view.pending)
        assertEquals(2, view.dueSoon)
        assertEquals(listOf("next", "24h"), view.ids(TaskFilter.DUE_SOON))
        assertEquals(view.dueSoon, view.count(TaskFilter.DUE_SOON))
    }

    @Test fun materialsIgnoreFakeDeadlineAndRetainCompletionFilter() {
        val view = TaskPresentation(listOf(task("past-video", now - hour, "video"),
            task("future-video", type = "video"), task("done-video", type = "video", status = "done")), now)
        assertEquals(0, view.pending)
        assertEquals(0, view.overdue)
        assertEquals(0, view.dueSoon)
        assertEquals(3, view.count(TaskFilter.MATERIALS))
        assertEquals(listOf("done-video"), view.ids(TaskFilter.DONE))
        assertTrue(view.select(TaskFilter.MATERIALS, "", "", TaskOrder.TIME).single().items.all { it.time == 0L })
    }

    @Test fun classesUseStartAndDoNotInferEndFromStart() {
        val view = TaskPresentation(listOf(task("future", now - hour, "class", start = now + hour),
            task("started", now - hour, "class", start = now),
            task("older", type = "class", start = now - hour),
            task("unknown-time", type = "class")), now)
        assertEquals(0, view.overdue)
        assertEquals(4, view.pending)
        assertEquals(listOf("future", "unknown-time", "started", "older"), view.ids(TaskFilter.ACTIVE))
        assertEquals(listOf(TaskSection.UPCOMING, TaskSection.UNDATED, TaskSection.STARTED),
            view.select(TaskFilter.ACTIVE, "", "", TaskOrder.TIME).map { it.section })
    }

    @Test fun courseOrderingSearchAndFiltersCompose() {
        val view = TaskPresentation(listOf(task("b", now + hour, course = "课程 B", title = "Lab ONE"),
            task("a", now + 2 * hour, course = "课程 A", title = "Lab Two"),
            task("old", now - hour, course = "课程 A", title = "Lab Three"), task("blank", course = "")), now)
        assertEquals(listOf("课程 A", "课程 B"), view.courses)
        assertEquals(listOf("a", "b", "old"), view.ids(query = "  lAb  ", order = TaskOrder.COURSE))
        assertEquals(listOf("b", "a"), view.ids(TaskFilter.ACTIVE, "lab"))
        assertEquals(listOf("a"), view.ids(TaskFilter.ACTIVE, "lab", "课程 A"))
        assertEquals(listOf("old"), view.ids(TaskFilter.OVERDUE, "课程 A"))
        assertTrue(view.ids(query = "missing").isEmpty())
    }

    @Test fun timeLabelsKeepYearsAndUseLocalCalendar() {
        val zone = ZoneId.of("Asia/Shanghai")
        val reference = Instant.parse("2026-12-31T15:30:00Z").toEpochMilli()
        assertEquals("今天 23:30", taskTime(reference, reference, zone))
        assertEquals("明天 00:30", taskTime(reference + hour, reference, zone))
        assertEquals("2027年1月2日 23:30", taskTime(reference + 48 * hour, reference, zone))
        assertEquals("12月29日 23:30", taskTime(reference - 48 * hour, reference, zone))
        assertEquals("还剩 1 分钟", remainingTime(reference + 1, reference))
        assertEquals("还剩 1 小时 1 分钟", remainingTime(reference + hour + 1, reference))
    }
}
