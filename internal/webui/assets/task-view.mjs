export function classroomKey(task) {
  return /^ykt:([^:]+):[^:]+$/.exec(task.id || "")?.[1] || "";
}

const normalized = (value) => (value || "").trim().replace(/\s+/g, " ");

export function groupTasks(tasks) {
  const buckets = new Map();
  for (const task of tasks) {
    const key = classroomKey(task) && normalized(task.course) && normalized(task.title) && task.type
      ? JSON.stringify([normalized(task.course), normalized(task.title), task.type,
        task.deadline_iso || "", task.start_at_iso || ""])
      : task;
    if (!buckets.has(key)) buckets.set(key, []);
    buckets.get(key).push(task);
  }
  const groups = [];
  for (const members of buckets.values()) {
    // 同一课堂有重名项时无法可靠配对，整组保持独立。
    if (new Set(members.map(classroomKey)).size !== members.length) {
      groups.push(...members.map((task) => ({ ...task, members: [task] })));
      continue;
    }
    const pending = members.find((task) => task.status !== "done" && !task.expired)
      || members.find((task) => task.status !== "done") || members[0];
    groups.push({ ...pending, members });
  }
  return groups;
}

export function selectTaskGroups(tasks, state, skipType = false) {
  // 先限制课堂再分组；范围筛选留到分组后，保留同组已完成课堂的详情。
  const selected = state.course
    ? tasks.filter((task) => (classroomKey(task) || task.course || "") === state.course)
    : tasks;
  const query = state.q.trim().toLowerCase();
  return groupTasks(selected).filter((group) => {
    if (!skipType && state.type !== "all" && group.type !== state.type) return false;
    if (query && !`${group.title || ""}\n${group.course || ""}`.toLowerCase().includes(query)) return false;
    if (state.scope === "all") return true;
    return group.members.some((task) => task.status !== "done" && !task.expired
      && (state.scope !== "due" || Boolean(task.deadline)));
  });
}

export function formatScore(task) {
  if (!Number.isFinite(task.score)) return "";
  return Number.isFinite(task.total_score)
    ? `得分 ${task.score}/${task.total_score}` : `得分 ${task.score}`;
}
