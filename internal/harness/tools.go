package harness

import (
	"regexp"
	"strings"
)

type ExtractedToolCall struct {
	Name      string                 `json:"name"`
	Input     map[string]interface{} `json:"input"`
	CardTitle string                 `json:"card_title,omitempty"`
}

var (
	// Regex matching markdown code blocks with file names like ```python:test.py or ```bash
	fenceRegex = regexp.MustCompile("(?s)```([a-zA-Z0-9_-]+)?(?::([^\n\r]+))?\n(.*?)\n```")
	// Regex matching file header pattern like "File: filename.ext" or "Create filename.ext"
	fileCardRegex = regexp.MustCompile(`(?i)(?:file|create|path):\s*([a-zA-Z0-9_\-\.\/]+)`)
)

// ExtractToolCalls scans raw assistant response text and converts code fences / operations into Anthropic tool calls
func ExtractToolCalls(text string) ([]ExtractedToolCall, string) {
	var tools []ExtractedToolCall
	cleanText := text

	matches := fenceRegex.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return tools, cleanText
	}

	for _, idx := range matches {
		lang := ""
		if idx[2] != -1 && idx[3] != -1 {
			lang = text[idx[2]:idx[3]]
		}
		path := ""
		if idx[4] != -1 && idx[5] != -1 {
			path = strings.TrimSpace(text[idx[4]:idx[5]])
		}
		codeContent := text[idx[6]:idx[7]]

		// Check if language or path suggests a file write or command execution
		if path != "" {
			tools = append(tools, ExtractedToolCall{
				Name: "Write",
				Input: map[string]interface{}{
					"file_path": path,
					"content":   codeContent,
				},
			})
		} else if lang == "bash" || lang == "sh" || lang == "powershell" {
			tools = append(tools, ExtractedToolCall{
				Name: "Bash",
				Input: map[string]interface{}{
					"command": strings.TrimSpace(codeContent),
				},
			})
		}
	}

	return tools, cleanText
}
