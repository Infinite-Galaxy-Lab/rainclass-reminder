package lab.infinitegalaxy.rainclass.ui

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.Sort
import androidx.compose.material.icons.outlined.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

@Composable
internal fun TaskControls(
    presentation: TaskPresentation, filter: TaskFilter, query: String, course: String, order: TaskOrder,
    onFilter: (TaskFilter) -> Unit, onQuery: (String) -> Unit, onCourse: (String) -> Unit, onOrder: (TaskOrder) -> Unit,
) {
    var courseMenu by remember { mutableStateOf(false) }
    var orderMenu by remember { mutableStateOf(false) }
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        OutlinedTextField(query, onQuery, Modifier.fillMaxWidth(),
            placeholder = { Text("搜索课程或任务") }, singleLine = true,
            leadingIcon = { Icon(Icons.Outlined.Search, null) },
            trailingIcon = { if (query.isNotEmpty()) IconButton(onClick = { onQuery("") }) { Icon(Icons.Outlined.Close, "清除搜索") } },
            shape = RoundedCornerShape(14.dp))
        Row(Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            TaskFilter.entries.forEach { choice ->
                FilterChip(selected = filter == choice, onClick = { onFilter(choice) }, modifier = Modifier.testTag("filter-${choice.name}"),
                    label = { Text(choice.label) }, trailingIcon = { Text(presentation.count(choice).toString()) })
            }
        }
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.weight(1f)) {
                TextButton(onClick = { courseMenu = true }, modifier = Modifier.fillMaxWidth()) {
                    Icon(Icons.Outlined.School, null, Modifier.size(16.dp))
                    Text(course.ifEmpty { "全部课程" }, Modifier.padding(horizontal = 6.dp).weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis)
                    Icon(Icons.Outlined.ExpandMore, null, Modifier.size(16.dp))
                }
                DropdownMenu(expanded = courseMenu, onDismissRequest = { courseMenu = false }, modifier = Modifier.heightIn(max = 340.dp).widthIn(max = 300.dp)) {
                    (listOf("") + presentation.courses).forEach { choice ->
                        DropdownMenuItem(text = { Text(choice.ifEmpty { "全部课程" }, maxLines = 2) },
                            onClick = { onCourse(choice); courseMenu = false },
                            trailingIcon = { if (choice == course) Icon(Icons.Outlined.Check, null) })
                    }
                }
            }
            Box {
                TextButton(onClick = { orderMenu = true }) {
                    Icon(Icons.AutoMirrored.Outlined.Sort, null, Modifier.size(16.dp))
                    Text(order.label, Modifier.padding(start = 6.dp))
                }
                DropdownMenu(expanded = orderMenu, onDismissRequest = { orderMenu = false }) {
                    TaskOrder.entries.forEach { choice ->
                        DropdownMenuItem(text = { Text(choice.label) }, onClick = { onOrder(choice); orderMenu = false },
                            trailingIcon = { if (choice == order) Icon(Icons.Outlined.Check, null) })
                    }
                }
            }
        }
    }
}
