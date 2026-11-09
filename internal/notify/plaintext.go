package notify

import "strings"

// plainText 把 Markdown 粗略转为纯文本（去链接语法、强调标记、表格）。
//
// 桌面通知通常只显示两三行，因此这里还会截断过长内容，
// 避免把整条卡片正文塞进通知气泡。
func plainText(md string) string {
	var b strings.Builder
	for _, line := range strings.Split(md, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "|") || strings.HasPrefix(t, "---") {
			continue
		}
		// [文字](url) → 文字
		for {
			open := strings.Index(t, "[")
			if open < 0 {
				break
			}
			mid := strings.Index(t[open:], "](")
			if mid < 0 {
				break
			}
			mid += open
			end := strings.Index(t[mid:], ")")
			if end < 0 {
				break
			}
			end += mid
			t = t[:open] + t[open+1:mid] + t[end+1:]
		}
		t = strings.ReplaceAll(t, "**", "")
		t = strings.ReplaceAll(t, "__", "")
		t = strings.TrimPrefix(t, "- ")
		if t != "" {
			b.WriteString(t)
			b.WriteString("\n")
		}
	}

	out := strings.TrimSpace(b.String())
	// 通知气泡高度有限：最多保留 5 行。
	lines := strings.Split(out, "\n")
	if len(lines) > 5 {
		out = strings.Join(lines[:5], "\n") + "\n…"
	}
	return out
}
