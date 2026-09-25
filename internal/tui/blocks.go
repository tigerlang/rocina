package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Block struct {
	Kind   string    `json:"kind"`
	Text   string    `json:"text,omitempty"`
	Tool   string    `json:"tool,omitempty"`
	Args   string    `json:"args,omitempty"`
	Result string    `json:"result,omitempty"`
	CallID string    `json:"call_id,omitempty"`
	Done   bool      `json:"done"`
	At     time.Time `json:"at"`
}

func wrapText(s string, width int) []string {
	if width < 4 {
		width = 4
	}
	var out []string
	for _, paragraph := range strings.Split(s, "\n") {
		paragraph = strings.TrimRight(paragraph, " ")
		if paragraph == "" {
			out = append(out, "")
			continue
		}
		line := ""
		for _, word := range strings.Fields(paragraph) {
			// A token longer than the width (a URL, a base64 blob, a raw JSON
			// fragment) must be hard-wrapped; otherwise the line overflows the
			// panel and pushes the sidebar off screen.
			for _, chunk := range hardWrap(word, width) {
				if line == "" {
					line = chunk
					continue
				}
				if len([]rune(line))+1+len([]rune(chunk)) <= width {
					line += " " + chunk
				} else {
					out = append(out, line)
					line = chunk
				}
			}
		}
		if line != "" {
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		out = append(out, "")
	}
	return out
}

// hardWrap splits a word that is wider than the width into width-sized runes.
// A word that already fits is returned whole.
func hardWrap(word string, width int) []string {
	runes := []rune(word)
	if len(runes) <= width {
		return []string{word}
	}
	out := make([]string, 0, len(runes)/width+1)
	for len(runes) > width {
		out = append(out, string(runes[:width]))
		runes = runes[width:]
	}
	if len(runes) > 0 {
		out = append(out, string(runes))
	}
	return out
}

func formatArgs(args string) []string {
	args = strings.TrimSpace(args)
	if args == "" {
		return nil
	}
	var object map[string]any
	if err := json.Unmarshal([]byte(args), &object); err != nil {
		return []string{args}
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, fmt.Sprintf("%s: %s", key, stringify(object[key])))
	}
	return out
}

func stringify(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(encoded)
}

// resultPreview caps a tool result to maxLines. A maxLines of zero means the
// result is expanded, so every line is shown.
func resultPreview(result string, width, maxLines int) []string {
	lines := wrapText(strings.TrimRight(result, "\n"), width)
	if maxLines <= 0 || len(lines) <= maxLines {
		return lines
	}
	out := append([]string{}, lines[:maxLines]...)
	out = append(out, fmt.Sprintf("… (%d more lines)", len(lines)-maxLines))
	return out
}
