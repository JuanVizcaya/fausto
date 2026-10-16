package jira

import "strings"

// ADF turns a task's Markdown body into a minimal Atlassian Document Format
// document: headings stay headings, list items become bullets, and other
// lines join into paragraphs, all as plain text. footer, when set, is a
// final paragraph.
func ADF(markdown, footer string) map[string]any {
	var content []any
	var para []string
	var bullets []any
	flushPara := func() {
		if len(para) > 0 {
			content = append(content, block("paragraph", strings.Join(para, " ")))
			para = nil
		}
	}
	flushList := func() {
		if len(bullets) > 0 {
			content = append(content, map[string]any{"type": "bulletList", "content": bullets})
			bullets = nil
		}
	}
	for _, line := range strings.Split(markdown, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			flushPara()
			flushList()
		case strings.HasPrefix(line, "#"):
			flushPara()
			flushList()
			level := len(line) - len(strings.TrimLeft(line, "#"))
			h := block("heading", strings.TrimSpace(line[level:]))
			h["attrs"] = map[string]any{"level": min(level, 6)}
			content = append(content, h)
		case strings.HasPrefix(line, "- "), strings.HasPrefix(line, "* "):
			flushPara()
			bullets = append(bullets, map[string]any{"type": "listItem", "content": []any{block("paragraph", strings.TrimSpace(line[2:]))}})
		default:
			flushList()
			para = append(para, line)
		}
	}
	flushPara()
	flushList()
	if footer != "" {
		content = append(content, block("paragraph", footer))
	}
	if content == nil {
		content = []any{}
	}
	return map[string]any{"type": "doc", "version": 1, "content": content}
}

// block is an ADF node of type typ holding text; empty text makes an empty
// node, since ADF rejects empty text nodes.
func block(typ, text string) map[string]any {
	n := map[string]any{"type": typ}
	if text != "" {
		n["content"] = []any{map[string]any{"type": "text", "text": text}}
	}
	return n
}
