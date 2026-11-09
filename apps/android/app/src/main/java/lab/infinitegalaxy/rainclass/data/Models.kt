package lab.infinitegalaxy.rainclass.data

import org.json.JSONObject

data class Task(
    val id: String,
    val type: String,
    val label: String,
    val title: String,
    val course: String,
    val deadline: Long,
    val startAt: Long,
    val status: String,
    val score: Double?,
    val totalScore: Double?,
    val url: String,
)

data class Snapshot(val authState: String = "signed_out", val fetchedAt: Long = 0, val tasks: List<Task> = emptyList()) {
    companion object {
        fun parse(raw: String): Snapshot {
            val json = JSONObject(raw)
            require(json.getInt("schema_version") == 1)
            val array = json.getJSONArray("tasks")
            return Snapshot(json.getString("auth_state"), json.getLong("fetched_at"), List(array.length()) { index ->
                val task = array.getJSONObject(index)
                Task(task.getString("id"), task.getString("type"), task.getString("label"),
                    task.getString("title"), task.getString("course"), task.getLong("deadline"),
                    task.getLong("start_at"), task.getString("status"), task.nullableDouble("score"),
                    task.nullableDouble("total_score"), task.getString("url"))
            })
        }
    }
}

private fun JSONObject.nullableDouble(name: String): Double? =
    if (isNull(name)) null else getDouble(name).takeIf { it.isFinite() }
