import test from "node:test";
import assert from "node:assert/strict";
import { groupTasks, selectTaskGroups, formatScore } from "./assets/task-view.mjs";

const task = (classroom, extra = {}) => ({
  id: `ykt:${classroom}:101`, title: "练习一", course: "示例课程", type: "homework",
  deadline: "12-01 12:00", deadline_iso: "2026-12-01T12:00:00+08:00",
  start_at_iso: "", status: "pending", expired: false,
  url: `https://example.org/classroom/${classroom}`, score: null, total_score: null, ...extra,
});
const state = { course: "", type: "all", q: "", scope: "all" };

test("跨课堂合并保留各自状态、分数和链接，且不修改输入", () => {
  const tasks = [task(1, { status: "done", score: 0, total_score: 5 }), task(2, { score: 2.75, total_score: 5 })];
  const before = structuredClone(tasks);
  const groups = groupTasks(tasks);
  assert.equal(groups.length, 1);
  assert.equal(groups[0].status, "pending");
  assert.deepEqual(groups[0].members, tasks);
  assert.equal(groups[0].members[0].score, 0);
  assert.equal(groups[0].members[1].url, tasks[1].url);
  assert.deepEqual(tasks, before);
});

test("同一课堂重复或身份不明时不合并", () => {
  assert.equal(groupTasks([task(1), task(1, { id: "ykt:1:102" }), task(2)]).length, 3);
  for (const extra of [{ id: "unknown" }, { id: "" }, { course: "" }, { title: "" }]) {
    assert.equal(groupTasks([task(1, extra), task(2, extra)]).length, 2);
  }
});

test("课程、标题、类型、截止及开始时间不同均保持独立", () => {
  for (const extra of [{ course: "另一门课" }, { title: "练习二" }, { type: "exam" },
    { deadline_iso: "2026-12-02T12:00:00+08:00" }, { start_at_iso: "2026-12-01T10:00:00+08:00" }]) {
    assert.equal(groupTasks([task(1), task(2, extra)]).length, 2);
  }
  assert.equal(groupTasks([task(1, { deadline: "", deadline_iso: "" }), task(2, { deadline: "", deadline_iso: "" })]).length, 1);
});

test("只有全组完成才显示已完成，过期课堂不掩盖待办", () => {
  assert.equal(groupTasks([task(1, { status: "done" }), task(2, { status: "done" })])[0].status, "done");
  const groups = groupTasks([task(1, { expired: true, status: "expired" }), task(2)]);
  assert.equal(groups[0].expired, false);
  assert.equal(groups[0].status, "pending");
});

test("范围筛选保留组内完成课堂，课堂筛选仅显示选中课堂", () => {
  const tasks = [task(1, { status: "done" }), task(2)];
  for (const scope of ["due", "open"]) {
    assert.equal(selectTaskGroups(tasks, { ...state, scope })[0].members.length, 2);
  }
  const selected = selectTaskGroups(tasks, { ...state, course: "2", scope: "due" });
  assert.equal(selected[0].members.length, 1);
  assert.equal(selected[0].id, "ykt:2:101");
  assert.equal(selectTaskGroups(tasks, { ...state, course: "1", scope: "due" }).length, 0);
  assert.equal(selectTaskGroups(tasks, { ...state, type: "exam" }).length, 0);
  assert.equal(selectTaskGroups(tasks, { ...state, type: "exam" }, true).length, 1);
  assert.equal(selectTaskGroups(tasks, { ...state, q: "无匹配" }).length, 0);
});

test("分数展示区分缺失、零分、小数和无总分", () => {
  assert.equal(formatScore({ score: 0, total_score: 5 }), "得分 0/5");
  assert.equal(formatScore({ score: 2.75, total_score: 5 }), "得分 2.75/5");
  assert.equal(formatScore({ score: 2.75, total_score: null }), "得分 2.75");
  for (const score of [undefined, null, NaN, Infinity, "0"]) {
    assert.equal(formatScore({ score, total_score: 5 }), "");
  }
});
