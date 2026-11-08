// 本地面板前端逻辑：无框架、无构建步骤，直接操作 DOM。
//
// 三条硬约束：
//   1. 渲染用户数据只用 createElement，绝不用 innerHTML 拼接
//      （任务标题来自雨课堂接口，属外部输入）；
//   2. 请求地址只能取自下方 ENDPOINTS 常量表，不接受任何调用方传入的
//      URL，因此面板无法被用作向第三方地址发起请求的跳板；
//   3. 不假设后端字段一定存在，缺失时退化显示而不是抛错。

import { classroomKey, selectTaskGroups, formatScore } from "./task-view.mjs";

(function () {
  "use strict";

  // ---------- 基础 ----------

  const token = new URLSearchParams(location.search).get("token") || "";

  const $ = (id) => document.getElementById(id);

  const el = (tag, cls, text) => {
    const n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text !== undefined) n.textContent = text;
    return n;
  };

  const STATE_KEY = "ykt-panel-v2";

  // 任务类型：顺序即 chip 顺序；glyph 用于列表里的类型标识。
  const TYPES = [
    { key: "all", label: "全部", glyph: "" },
    { key: "homework", label: "作业", glyph: "✎" },
    { key: "exam", label: "考试", glyph: "▲" },
    { key: "discuss", label: "讨论", glyph: "❝" },
    { key: "class", label: "课堂", glyph: "◆" },
    { key: "video", label: "视频", glyph: "▶" },
    { key: "other", label: "其他", glyph: "•" },
  ];
  const typeOf = (k) => TYPES.find((t) => t.key === k) || TYPES[TYPES.length - 1];

  // ---------- 状态 ----------

  let tasks = [];
  let view = "tasks";
  let qrTimer = null;
  let searchTimer = null;

  const defaults = () => ({
    type: "all",
    course: "",
    q: "",
    // 三档范围：
    //   due  = 只看"有截止时间且未过期"（默认，这是提醒应用的主视图）
    //   open = 未完成的全部（含视频等无截止项）
    //   all  = 全部
    scope: "due",
  });

  const state = defaults();

  function loadState() {
    try {
      const raw = JSON.parse(sessionStorage.getItem(STATE_KEY) || "{}");
      // 只接受已知字段，避免旧版本残留的脏数据污染
      for (const k of Object.keys(defaults())) {
        if (raw[k] !== undefined) state[k] = raw[k];
      }
    } catch (e) {
      /* 存量数据损坏时静默回落默认值 */
    }
  }

  function saveState() {
    try {
      sessionStorage.setItem(STATE_KEY, JSON.stringify(state));
    } catch (e) {
      /* 隐私模式下写不进去，不影响当前会话使用 */
    }
  }

  // ---------- 时间格式化 ----------

  // 把秒数转成紧凑中文。刻意不显示秒，避免每 30 秒刷新时数字乱跳。
  function humanLeft(sec) {
    const abs = Math.abs(sec);
    const d = Math.floor(abs / 86400);
    const h = Math.floor((abs % 86400) / 3600);
    const m = Math.floor((abs % 3600) / 60);

    if (d > 0) return h > 0 ? `${d} 天 ${h} 小时` : `${d} 天`;
    if (h > 0) return m > 0 ? `${h} 小时 ${m} 分` : `${h} 小时`;
    if (m > 0) return `${m} 分钟`;
    return "不到 1 分钟";
  }

  // 剩余时间的语义级别。readonly 表示"无截止概念"（如视频）。
  function levelOf(t) {
    if (t.status === "done") return "done";
    if (t.expired) return "expired";
    if (!t.deadline) return "readonly";
    const sec = t.remaining_seconds || 0;
    if (sec <= 3600) return "urgent";
    if (sec <= 6 * 3600) return "warn";
    return "normal";
  }

  const LEVEL_MARK = {
    urgent: "●",
    warn: "▲",
    normal: "○",
    readonly: "",
    done: "✓",
    expired: "✕",
  };

  // ---------- 课程键 ----------

  // 任务 id 形如 ykt:<classroom_id>:<leaf_id>，第二段能区分同名课程。
  // 没有可靠 id 时退回课程名（此时同名课程本就无法区分）。
  function courseKey(t) {
    return classroomKey(t) || t.course || "";
  }

  // ---------- 请求 ----------

  // 接口端点表。URL 在此处一次性定型，之后不再接受任何参数拼接，
  // 面板因此无法被用于向第三方地址发起请求。
  const ENDPOINTS = Object.freeze({
    status: "/api/status",
    tasks: "/api/tasks",
    history: "/api/history",
    refresh: "/api/refresh",
    qrStart: "/api/qr/start",
    qrPoll: "/api/qr/poll",
  });

  async function getJSON(name) {
    const path = ENDPOINTS[name];
    if (!path) throw new Error("未知接口: " + name);
    const url = token ? path + "?token=" + encodeURIComponent(token) : path;

    const res = await fetch(url, { headers: { Accept: "application/json" } });
    if (res.status === 401) throw new Error("面板未授权，请检查访问 token");
    if (!res.ok) {
      const text = await res.text();
      throw new Error(text || "HTTP " + res.status);
    }
    return res.json();
  }

  // ---------- 筛选 ----------

  // skipType 用于统计 chip 角标：其余条件生效、类型不限。
  function applyFilters(list, skipType) {
    return selectTaskGroups(list, state, skipType);
  }

  // ---------- 渲染：左侧健康状态 ----------

  function renderHealth(s) {
    const box = $("health");
    const txt = $("health-text");
    let cls = "health";
    let label = "检查中";

    if (!s.logged_in) {
      cls += " is-bad";
      label = "未登录";
    } else if (!s.datasource_ok) {
      cls += " is-bad";
      label = "登录已失效";
    } else {
      cls += " is-ok";
      label = "运行中";
    }
    box.className = cls;
    txt.textContent = label;

    $("rail-task-count").textContent = "—";

    const ch = s.channels && s.channels.length ? s.channels.join(" · ") : "未配置推送通道";
    $("channels").textContent = ch;

    if (s.last_fetch && !s.last_fetch.startsWith("0001-01-01")) {
      const d = new Date(s.last_fetch);
      $("last-fetch").textContent = "抓取于 " + d.toLocaleTimeString("zh-CN", { hour12: false });
    } else {
      $("last-fetch").textContent = "尚未抓取";
    }

    renderSession(s);

    $("version").textContent = s.version && s.version !== "dev" ? "v" + s.version : "";

    // 登录失效才显示提示条；未登录同样需要提示。
    const needLogin = !s.logged_in || !s.datasource_ok;
    $("auth-alert").hidden = !needLogin;
  }

  // ---------- 渲染：登录态详情 ----------
  //
  // 面板需要回答一个具体问题："我这份登录态还值得信任吗？"
  // 三个信号：
  //   1. 这份登录是什么时候建立的（cookie_updated）
  //   2. 最近一次服务端回写 cookie 是什么时候（证明请求确实通着）
  //   3. cookie 带了几个字段（只有 1-2 个说明导入不完整）
  //
  // 布局：拆成两行而不是一行用 · 连接。侧栏只有 208px，
  // 单行会在「续期 11-08」和「12:48」之间折断，读起来像两个信息。
  function renderSession(s) {
    const box = $("session");
    if (!box) return;

    const lines = [];

    if (s.cookie_updated && !isZeroTime(s.cookie_updated)) {
      lines.push("登录 " + fmtShort(s.cookie_updated));
    }
    if (s.cookie_refreshed_at && !isZeroTime(s.cookie_refreshed_at)) {
      lines.push("续期 " + fmtShort(s.cookie_refreshed_at));
    } else if (s.logged_in && s.datasource_ok) {
      // 已登录且接口通，但还没记录到回写：说明这一轮还没请求过，
      // 或者服务端确实没下发——两种都不算异常，不制造焦虑。
      lines.push("续期待记录");
    }

    const n = s.cookie_count || 0;
    if (n > 0) {
      if (n < 2) {
        lines.push("cookie 不完整，建议重新导入");
        box.dataset.warn = "1";
      } else {
        lines.push(n + " 个 cookie");
        delete box.dataset.warn;
      }
    } else {
      delete box.dataset.warn;
    }

    box.innerHTML = "";
    for (const text of lines) box.appendChild(el("div", "rail-session-line", text));

    box.title =
      "雨课堂不会续期 sessionid，过期后需要重新登录。\n" +
      "csrftoken 等辅助 cookie 会自动回写。\n" +
      "cookie 少于 2 个说明导入不完整。";
  }

  // isZeroTime 识别 Go 的零值时间（0001-01-01）。
  function isZeroTime(iso) {
    return !iso || iso.startsWith("0001-01-01");
  }

  // fmtShort 输出「11-08 12:01」这种紧凑时间。
  function fmtShort(iso) {
    const d = new Date(iso);
    if (isNaN(d)) return "—";
    const p = (v) => String(v).padStart(2, "0");
    return p(d.getMonth() + 1) + "-" + p(d.getDate()) + " " + p(d.getHours()) + ":" + p(d.getMinutes());
  }

  // ---------- 渲染：类型 chips ----------

  function renderChips() {
    const box = $("type-chips");
    box.innerHTML = "";
    const base = applyFilters(tasks, true);

    for (const def of TYPES) {
      const n = def.key === "all" ? base.length : base.filter((t) => t.type === def.key).length;
      const active = state.type === def.key;

      const btn = el("button", "chip" + (active ? " is-active" : ""));
      btn.type = "button";
      btn.setAttribute("aria-pressed", active ? "true" : "false");
      if (n === 0 && def.key !== "all") btn.dataset.zero = "1";

      btn.appendChild(document.createTextNode(def.label));
      btn.appendChild(el("span", "chip-n", String(n)));

      btn.addEventListener("click", () => {
        state.type = def.key;
        saveState();
        renderChips();
        renderTasks();
      });
      box.appendChild(btn);
    }
  }

  // ---------- 渲染：课程下拉 ----------

  // 只在拿到新数据时重建，避免覆盖用户正在进行的操作。
  function renderCourseSelect() {
    const sel = $("filter-course");
    const names = new Map(); // key -> 课程名
    for (const t of tasks) {
      const k = courseKey(t);
      if (!k || !t.course) continue;
      if (!names.has(k)) names.set(k, t.course);
    }

    // 同名课程用 classroom_id 后缀区分，否则下拉里会有十几个一模一样的选项。
    const useCount = new Map();
    for (const name of names.values()) useCount.set(name, (useCount.get(name) || 0) + 1);

    const opts = [...names.entries()]
      .map(([k, name]) => ({ key: k, label: useCount.get(name) > 1 ? `${name}（${k}）` : name }))
      .sort((a, b) => a.label.localeCompare(b.label, "zh-Hans-CN"));

    sel.innerHTML = "";
    sel.appendChild(new Option("全部课程", ""));
    for (const o of opts) sel.appendChild(new Option(o.label, o.key));

    // 之前选中的课程可能已不在最新数据里
    if (![...sel.options].some((op) => op.value === state.course)) state.course = "";
    sel.value = state.course;
  }

  // ---------- 渲染：任务列表 ----------

  function renderTasks() {
    const box = $("tasks");
    box.innerHTML = "";

    const shown = applyFilters(tasks, false);

    $("task-count-note").textContent = shown.length
      ? `${shown.length} 项 · ${shown.reduce((n, t) => n + t.members.length, 0)} 条课堂记录`
      : `共 ${tasks.length} 条课堂记录`;

    $("rail-task-count").textContent = String(shown.length);

    renderChips();

    if (!shown.length) {
      box.appendChild(emptyState(tasks.length));
      return;
    }

    const frag = document.createDocumentFragment();
    for (const t of shown) frag.appendChild(t.members.length > 1 ? groupRow(t) : taskRow(t));
    box.appendChild(frag);
  }

  function emptyState(total) {
    const box = el("div", "state");

    if (!total) {
      box.appendChild(el("div", "state-ico", "◱"));
      box.appendChild(el("div", "state-title", "还没有任务"));
      box.appendChild(el("div", "", "点击右上角「刷新」抓取最新数据"));
      return box;
    }

    // 空结果有三种成因，提示必须区分，否则用户不知道该改哪里。
    const narrowed = Boolean(state.q.trim() || state.course || state.type !== "all");

    if (narrowed) {
      box.appendChild(el("div", "state-ico", "⌕"));
      box.appendChild(el("div", "state-title", "没有匹配的任务"));
      const cond = [];
      if (state.q.trim()) cond.push(`关键词「${state.q.trim()}」`);
      if (state.course) cond.push("所选课程");
      if (state.type !== "all") cond.push(typeOf(state.type).label + "类型");
      box.appendChild(el("div", "", "当前条件：" + cond.join(" + ")));
      const clear = el("button", "btn", "清除筛选");
      clear.type = "button";
      clear.addEventListener("click", resetFilters);
      box.appendChild(clear);
      return box;
    }

    // 没有筛选条件却为空：说明是该范围本身没有内容（如待办已清空）。
    box.appendChild(el("div", "state-ico", "✓"));
    box.appendChild(el("div", "state-title", "没有待办"));
    box.appendChild(el("div", "", "当前范围内没有需要关注的任务"));
    if (state.scope !== "all") {
      const widen = el("button", "btn", "查看全部任务");
      widen.type = "button";
      widen.addEventListener("click", () => {
        state.scope = "all";
        saveState();
        syncScopeButtons();
        renderTasks();
      });
      box.appendChild(widen);
    }
    return box;
  }

  function groupRow(t) {
    const details = el("details", "task-group");
    const summary = taskRow({ ...t, url: "", score: null, total_score: null }, "summary");
    summary.classList.add("group-summary");
    const done = t.members.filter((member) => member.status === "done").length;
    summary.querySelector(".task-sub").appendChild(el("span", "group-count", `${t.members.length} 个课堂 · ${done}/${t.members.length} 已完成`));
    details.appendChild(summary);
    const children = el("div", "group-members");
    for (const member of t.members) {
      const row = taskRow(member);
      row.querySelector(".course").textContent = `课堂 ${classroomKey(member)}`;
      children.appendChild(row);
    }
    details.appendChild(children);
    return details;
  }

  function taskRow(t, tag = "div") {
    const lvl = levelOf(t);

    const row = el(tag, "task");
    row.dataset.level = lvl;
    row.dataset.status = t.status;
    if (t.expired) row.dataset.expired = "1";

    // --- 左：标题 + 元信息 ---
    const main = el("div");
    main.style.minWidth = "0";

    const title = t.url
      ? el("a", "task-title", t.title || "（无标题）")
      : el("span", "task-title", t.title || "（无标题）");
    if (t.url) {
      title.href = t.url;
      title.target = "_blank";
      title.rel = "noopener noreferrer";
      title.appendChild(el("span", "title-arrow", "↗"));
    }
    main.appendChild(title);

    const sub = el("div", "task-sub");
    const def = typeOf(t.type);
    const kind = el("span", "kind");
    if (def.glyph) kind.appendChild(el("span", "kind-ico", def.glyph));
    kind.appendChild(document.createTextNode(t.type_label || def.label));
    sub.appendChild(kind);

    if (t.course) {
      sub.appendChild(el("span", "sep"));
      sub.appendChild(el("span", "course", t.course));
    }
    main.appendChild(sub);

    // --- 右：倒计时 + 截止时刻 ---
    const side = el("div", "task-side");
    const score = formatScore(t);
    if (score) side.appendChild(el("span", "task-score num", score));

    const due = el("span", "due");
    due.dataset.level = lvl;
    if (LEVEL_MARK[lvl]) due.appendChild(el("span", "due-mark", LEVEL_MARK[lvl]));

    let label;
    if (t.status === "done") label = "已完成";
    else if (t.expired) label = "已过期";
    else if (!t.deadline) label = "无截止";
    else label = humanLeft(t.remaining_seconds || 0);
    due.appendChild(el("span", "num", label));
    side.appendChild(due);

    if (t.deadline) side.appendChild(el("div", "due-sub num", "截止 " + t.deadline));

    row.appendChild(main);
    row.appendChild(side);
    return row;
  }

  // ---------- 渲染：推送记录 ----------

  function renderHistory(h) {
    const box = $("history");
    box.innerHTML = "";
    const recs = (h && h.records) || [];

    if (!recs.length) {
      const s = el("div", "state");
      s.appendChild(el("div", "state-ico", "↻"));
      s.appendChild(el("div", "state-title", "还没有推送记录"));
      s.appendChild(el("div", "", "任务进入提醒窗口后，这里会记录每次推送结果"));
      box.appendChild(s);
      return;
    }

    const frag = document.createDocumentFragment();
    for (const r of recs) {
      const row = el("div", "hist");

      const left = el("div");
      left.style.minWidth = "0";
      left.appendChild(el("div", "hist-title", r.task_title || "（无标题）"));

      const when = r.sent_at
        ? new Date(r.sent_at).toLocaleString("zh-CN", { hour12: false })
        : "—";
      const bits = [when, r.kind];
      if (r.offset_min) bits.push("提前 " + r.offset_min + " 分钟");
      if (r.channel) bits.push(r.channel);

      const meta = el("div", "hist-meta num", bits.filter(Boolean).join(" · "));
      if (r.error) {
        meta.classList.add("is-error");
        meta.textContent += " · " + r.error;
      }
      left.appendChild(meta);

      const map = { sent: "已发送", failed: "失败", suppressed: "已合并" };
      const badge = el("span", "badge", map[r.status] || r.status || "未知");
      badge.dataset.s = r.status || "";

      row.appendChild(left);
      row.appendChild(badge);
      frag.appendChild(row);
    }
    box.appendChild(frag);
  }

  // ---------- 视图切换 ----------

  const VIEW_META = {
    tasks: { title: "任务", sub: "按截止时间排序，临期优先" },
    history: { title: "推送记录", sub: "最近 50 条推送结果" },
  };

  // 范围说明：切换时同步副标题，让"为什么条数变了"有解释。
  const SCOPE_SUB = {
    due: "只看有截止时间的待办",
    open: "全部未完成任务（含无截止的课件/视频）",
    all: "全部任务，含已完成与已过期",
  };

  function setView(next) {
    view = next;
    for (const btn of document.querySelectorAll(".rail-item")) {
      btn.classList.toggle("is-active", btn.dataset.view === next);
    }
    $("view-tasks").hidden = next !== "tasks";
    $("view-history").hidden = next !== "history";

    const meta = VIEW_META[next] || VIEW_META.tasks;
    $("view-title").textContent = meta.title;
    // 任务视图的副标题由范围决定，其余视图用固定文案。
    $("view-sub").textContent =
      next === "tasks" ? SCOPE_SUB[state.scope] || meta.sub : meta.sub;
  }

  // ---------- 加载 ----------

  async function loadAll() {
    try {
      const [status, taskResp, history] = await Promise.all([
        getJSON("status"),
        getJSON("tasks"),
        getJSON("history"),
      ]);
      renderHealth(status);
      tasks = taskResp.tasks || [];
      renderCourseSelect();
      renderTasks();
      renderHistory(history);
    } catch (err) {
      console.error(err);
      const box = $("tasks");
      box.innerHTML = "";
      const s = el("div", "state");
      s.appendChild(el("div", "state-ico", "!"));
      s.appendChild(el("div", "state-title", "加载失败"));
      s.appendChild(el("div", "", err.message));
      box.appendChild(s);
    }
  }

  // ---------- 交互：筛选 ----------

  function resetFilters() {
    Object.assign(state, defaults());
    saveState();
    $("filter-search").value = "";
    $("search-clear").hidden = true;
    $("filter-course").value = "";
    syncScopeButtons();
    renderTasks();
    $("filter-search").focus();
  }

  function syncScopeButtons() {
    for (const b of document.querySelectorAll(".seg-btn")) {
      b.classList.toggle("is-active", b.dataset.scope === state.scope);
    }
    const sub = $("view-sub");
    if (view === "tasks" && SCOPE_SUB[state.scope]) {
      sub.textContent = SCOPE_SUB[state.scope];
    }
  }

  $("filter-search").addEventListener("input", () => {
    $("search-clear").hidden = !$("filter-search").value;
    clearTimeout(searchTimer);
    searchTimer = setTimeout(() => {
      state.q = $("filter-search").value;
      saveState();
      renderTasks();
    }, 150);
  });

  $("search-clear").addEventListener("click", () => {
    $("filter-search").value = "";
    $("search-clear").hidden = true;
    state.q = "";
    saveState();
    renderTasks();
    $("filter-search").focus();
  });

  $("filter-course").addEventListener("change", () => {
    state.course = $("filter-course").value;
    saveState();
    renderTasks();
  });

  for (const b of document.querySelectorAll(".seg-btn")) {
    b.addEventListener("click", () => {
      state.scope = b.dataset.scope;
      saveState();
      syncScopeButtons();
      renderTasks();
    });
  }

  for (const b of document.querySelectorAll(".rail-item")) {
    b.addEventListener("click", () => setView(b.dataset.view));
  }

  // ---------- 交互：刷新 ----------

  $("btn-refresh").addEventListener("click", async () => {
    const btn = $("btn-refresh");
    if (btn.disabled) return;
    btn.disabled = true;
    btn.classList.add("is-busy");

    try {
      const r = await getJSON("refresh");
      // 后端会把这个请求转成调度器的提前唤醒，抓取完成后数据才更新，
      // 因此这里轮询几次等新快照出现（而不是固定等一个时间）。
      await waitForNewSnapshot(r.requested_at);
      await loadAll();
    } catch (err) {
      console.error(err);
    } finally {
      btn.disabled = false;
      btn.classList.remove("is-busy");
    }
  });

  // 等后端完成一轮抓取：最多等 30 秒，每 2 秒探一次。
  async function waitForNewSnapshot(since) {
    if (!since) {
      await new Promise((r) => setTimeout(r, 2500));
      return;
    }
    const deadline = Date.now() + 30000;
    while (Date.now() < deadline) {
      await new Promise((r) => setTimeout(r, 2000));
      try {
        const s = await getJSON("status");
        if (s.last_fetch && new Date(s.last_fetch).getTime() > new Date(since).getTime()) return;
      } catch (e) {
        /* 探测失败就继续等，主循环会把结果补上 */
      }
    }
  }

  // ---------- 交互：扫码登录 ----------

  $("btn-login").addEventListener("click", openQR);
  $("btn-qr-close").addEventListener("click", closeQR);
  document.querySelector(".modal-backdrop").addEventListener("click", closeQR);

  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !$("qr-modal").hidden) closeQR();
  });

  function openQR() {
    $("qr-modal").hidden = false;
    $("qr-status").textContent = "正在申请二维码…";
    $("qr-veil").hidden = true;
    $("qr-img").removeAttribute("src");
    startQR();
  }

  function closeQR() {
    $("qr-modal").hidden = true;
    if (qrTimer) {
      clearInterval(qrTimer);
      qrTimer = null;
    }
  }

  async function startQR() {
    let res;
    try {
      res = await getJSON("qrStart");
    } catch (err) {
      $("qr-status").textContent = "申请二维码失败：" + err.message;
      return;
    }

    $("qr-img").src = res.png;
    $("qr-status").textContent = "等待扫码";

    if (qrTimer) clearInterval(qrTimer);
    qrTimer = setInterval(async () => {
      let p;
      try {
        p = await getJSON("qrPoll");
      } catch (err) {
        clearInterval(qrTimer);
        qrTimer = null;
        $("qr-status").textContent = "轮询失败：" + err.message;
        return;
      }

      if (p.status) $("qr-status").textContent = p.status;
      if (!p.done) return;

      clearInterval(qrTimer);
      qrTimer = null;

      if (p.error) {
        showVeil("登录失败：" + p.error);
        $("qr-status").textContent = "登录失败";
        return;
      }

      showVeil("登录成功");
      $("qr-status").textContent = "登录成功，正在刷新数据…";
      setTimeout(async () => {
        await loadAll();
        closeQR();
      }, 900);
    }, 2000);
  }

  function showVeil(text) {
    $("qr-veil-text").textContent = text;
    $("qr-veil").hidden = false;
  }

  // ---------- 启动 ----------

  loadState();
  $("filter-search").value = state.q;
  $("search-clear").hidden = !state.q;
  syncScopeButtons();
  setView(view);

  loadAll();
  // 每 30 秒同步一次视图（不触发后端抓取）。
  setInterval(() => {
    // 用户正在输入搜索词时不打断
    if (searchTimer) return;
    loadAll();
  }, 30000);
})();
