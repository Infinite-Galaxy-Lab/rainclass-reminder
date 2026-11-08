# 视觉资产

## 当前图标

`docs/assets/icon.png` 为雨课堂官方图标，**仅作项目识别用途**，版权归学堂在线所有。

计划替换为自有设计。届时只需覆盖 `docs/assets/icon.png`，README 会自动引用新图。

## 设计方向

官方图标是蓝底 + 白色「书页 + 声波」组合，语义是"课堂内容播送"。本项目要做的是**在内容到达之前提醒你**，因此新图标要在这层语义上再叠一个"时间压力"信号。

三个必须一眼看懂的元素：

1. **书页 / 课堂** —— 承接官方语义，让用户知道这是雨课堂生态
2. **铃铛 或 脉冲** —— 提醒本身
3. **倒计时 / 环形进度 或 表盘刻度** —— 截止是核心，不是泛化的通知

色彩建议沿用官方的蓝（约 `#2B6FFF`）作为主色，用暖橙（约 `#FF7A45`）只标"临近截止"，让颜色本身承担紧迫度语义。移动端 App 在同一套语言上继续扩展。

## 生图提示词

用于后续生成自有图标。当前先用官方图标占位。

### 中文版

```
应用图标设计，方形圆角，扁平化极简风格，无文字。

主体：一本半开的书本，右页向上翻起，书页边缘转化为一圈细的环形进度条（表示倒计时）；
环形缺口处悬着一枚小型铃铛，铃铛底部带轻微声波弧线。

配色：主色深蓝 #2B6FFF 渐变至 #4C8BFF，铃声与进度条用暖橙 #FF7A45 作唯一强调色，
背景为纯净的白或极浅灰，无场景、无阴影、无立体质感。

构图：图形居中，四周留出约 15% 安全边距，圆角矩形裁切，1:1 正方形。

要求：几何精确、线条干净、y 轴左右对称，适合 16px 到 512px 全尺寸缩放下都清晰可辨。
风格接近 Google Material Symbols 与 macOS Big Sur 图标的结合，不要拟物、不要 3D、不要插画感。
```

### English

```
App icon, rounded square, flat minimal vector, no text.

Subject: a half-open book with the right page lifting upward; the page edge transitions into a thin
circular progress ring representing a countdown. A small bell sits in the ring's gap, with two short
sound-wave arcs beneath it.

Color: primary deep blue gradient #2B6FFF to #4C8BFF; the bell and ring use warm orange #FF7A45 as the
single accent. Background pure white or near-white. No scene, no drop shadow, no 3D.

Composition: centered mark, ~15% safe margin, rounded-square crop, 1:1.

Requirements: geometrically precise, clean strokes, left-right symmetric, legible at every size from
16px to 512px. Style between Google Material Symbols and macOS Big Sur icons. Not skeuomorphic,
not 3D, not illustrative.
```

### 备用变体（更强调"截止"）

把主体换成一枚**日历页 + 环形进度**，而非书本：

```
Subject: a single calendar page with its top-right corner peeling; a circular progress ring
completes only 3/4 around it, the remaining quarter in warm orange. No bell — the ring alone
carries urgency.
```

## 输出规格

| 用途 | 尺寸 | 格式 |
|---|---|---|
| README / 网页 | 512×512 | PNG |
| 桌面端窗口图标 | 256×256 | PNG / ICO |
| 移动端 App（后续） | 1024×1024 | PNG，无透明通道要求 |
| Favicon | 32×32 | PNG / ICO |
