package grok

import (
	"encoding/json"
	"strings"

	"github.com/aurora-develop/grok2api/internal/platform"
)

// ConsoleModels maps the public model name → the console.x.ai model field.
// Mirrors xai_console_chat.py CONSOLE_MODELS.
var ConsoleModels = map[string]string{
	"grok-4.5-console":                     "grok-4.5",
	"grok-4.5-low":                         "grok-4.5",
	"grok-4.5-medium":                      "grok-4.5",
	"grok-4.5-high":                        "grok-4.5",
	"grok-4.5-xhigh":                       "grok-4.5",
	"grok-4.3-console":                     "grok-4.3",
	"grok-4.3-low":                         "grok-4.3",
	"grok-4.3-medium":                      "grok-4.3",
	"grok-4.3-high":                        "grok-4.3",
	"grok-4.20-0309-reasoning-console":     "grok-4.20-0309-reasoning",
	"grok-4.20-0309-console":               "grok-4.20-0309",
	"grok-4.20-0309-non-reasoning-console": "grok-4.20-0309-non-reasoning",
	"grok-4.20-multi-agent-console":        "grok-4.20-multi-agent-0309",
	"grok-4.20-multi-agent-low":            "grok-4.20-multi-agent-0309",
	"grok-4.20-multi-agent-medium":         "grok-4.20-multi-agent-0309",
	"grok-4.20-multi-agent-high":           "grok-4.20-multi-agent-0309",
	"grok-4.20-multi-agent-xhigh":          "grok-4.20-multi-agent-0309",
	"grok-build-console":                   "grok-build-0.1",
}

// Models requiring a reasoning field in the payload.
var modelsWithReasoningField = map[string]struct{}{
	"grok-4.3":                   {},
	"grok-4.5":                   {},
	"grok-4.20-multi-agent-0309": {},
}

// Model name suffix → fixed effort value.
var modelFixedEffort = map[string]string{
	"grok-4.3-low":                 "low",
	"grok-4.3-medium":              "medium",
	"grok-4.3-high":                "high",
	"grok-4.5-low":                 "low",
	"grok-4.5-medium":              "medium",
	"grok-4.5-high":                "high",
	"grok-4.5-xhigh":               "xhigh",
	"grok-4.20-multi-agent-low":    "low",
	"grok-4.20-multi-agent-medium": "medium",
	"grok-4.20-multi-agent-high":   "high",
	"grok-4.20-multi-agent-xhigh":  "xhigh",
}

// Models with custom max_output_tokens (default is 1_000_000).
var modelMaxOutputTokens = map[string]int{
	"grok-4.20-multi-agent-0309": 2_000_000,
	"grok-build-0.1":             256_000,
	"grok-4.5":                   2_000_000,
}

// Models supporting web_search / x_search tools.
var modelsWithSearchTools = map[string]struct{}{
	"grok-4.5":                     {},
	"grok-4.20-multi-agent-0309":   {},
	"grok-4.20-0309":               {},
	"grok-4.20-0309-reasoning":     {},
	"grok-4.20-0309-non-reasoning": {},
	"grok-4.3":                     {},
	"grok-build-0.1":               {},
}

// effortMap maps OpenAI reasoning_effort → console API effort.
var effortMap = map[string]string{
	"none":    "none",
	"minimal": "low",
	"low":     "low",
	"medium":  "medium",
	"high":    "high",
	"xhigh":   "xhigh",
}

// BuildConsolePayload builds the JSON payload for POST console.x.ai/v1/responses.
// Converts OpenAI messages to Responses API input format.
func BuildConsolePayload(messages []map[string]any, model string, temperature, topP float64, reasoningEffort string, stream bool) map[string]any {
	inputItems := []map[string]any{}
	for _, msg := range messages {
		role, _ := msg["role"].(string)
		if role == "" {
			role = "user"
		}
		apiRole := "user"
		switch role {
		case "system", "developer":
			apiRole = "system"
		case "assistant":
			apiRole = "assistant"
		}
		content := msg["content"]
		var blocks []map[string]any
		switch c := content.(type) {
		case string:
			blocks = []map[string]any{{"type": "input_text", "text": c}}
		case []any:
			for _, b := range c {
				bm, ok := b.(map[string]any)
				if !ok {
					continue
				}
				bt, _ := bm["type"].(string)
				switch bt {
				case "text":
					text, _ := bm["text"].(string)
					blocks = append(blocks, map[string]any{"type": "input_text", "text": text})
				case "image_url":
					urlObj, _ := bm["image_url"].(map[string]any)
					url := ""
					if urlObj != nil {
						url, _ = urlObj["url"].(string)
					}
					if url != "" {
						blocks = append(blocks, map[string]any{"type": "input_image", "image_url": url})
					}
				default:
					text, _ := bm["text"].(string)
					if text == "" {
						text = toString(bm)
					}
					blocks = append(blocks, map[string]any{"type": "input_text", "text": text})
				}
			}
		default:
			blocks = []map[string]any{{"type": "input_text", "text": toString(content)}}
		}
		if len(blocks) > 0 {
			inputItems = append(inputItems, map[string]any{
				"role":    apiRole,
				"content": blocks,
			})
		}
	}

	effort := modelFixedEffort[model]
	if effort == "" {
		if reasoningEffort == "" {
			reasoningEffort = "medium"
		}
		effort = effortMap[reasoningEffort]
		if effort == "" {
			effort = "medium"
		}
	}

	consoleModel := ConsoleModels[model]
	if consoleModel == "" {
		consoleModel = model
	}

	maxTokens := 1_000_000
	if v, ok := modelMaxOutputTokens[consoleModel]; ok {
		maxTokens = v
	}

	payload := map[string]any{
		"model":             consoleModel,
		"input":             inputItems,
		"max_output_tokens": maxTokens,
		"temperature":       temperature,
		"top_p":             topP,
		"store":             false,
		"include":           []string{"reasoning.encrypted_content"},
		"stream":            stream,
	}
	if _, ok := modelsWithReasoningField[consoleModel]; ok {
		payload["reasoning"] = map[string]any{"effort": effort}
	}
	if _, ok := modelsWithSearchTools[consoleModel]; ok {
		payload["tools"] = []map[string]any{
			{"type": "web_search", "enable_image_understanding": true},
			{"type": "x_search", "enable_video_understanding": true},
		}
		payload["tool_choice"] = "auto"
	}
	return payload
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// ConsoleStreamAdapter parses console.x.ai SSE events and yields text tokens.
// Only response.output_text.delta is emitted; response.completed extracts usage.
type ConsoleStreamAdapter struct {
	TextBuf []string
	Usage   map[string]any
	done    bool
}

// NewConsoleStreamAdapter returns a fresh adapter.
func NewConsoleStreamAdapter() *ConsoleStreamAdapter { return &ConsoleStreamAdapter{} }

// Feed parses one SSE event. Returns the text tokens emitted (0 or 1).
func (s *ConsoleStreamAdapter) Feed(eventType, data string) ([]string, *platform.AppError) {
	if s.done {
		return nil, nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(data), &obj); err != nil {
		return nil, nil
	}
	switch eventType {
	case "response.output_text.delta":
		delta, _ := obj["delta"].(string)
		if delta == "" {
			return nil, nil
		}
		s.TextBuf = append(s.TextBuf, delta)
		return []string{delta}, nil
	case "response.completed":
		resp, _ := obj["response"].(map[string]any)
		if resp != nil {
			s.Usage, _ = resp["usage"].(map[string]any)
		}
		s.done = true
	case "error":
		msg, _ := obj["message"].(string)
		if msg == "" {
			msg = toString(obj)
		}
		return nil, platform.UpstreamError("Console API error: "+msg, 502, "")
	}
	return nil, nil
}

// FullText returns the accumulated text.
func (s *ConsoleStreamAdapter) FullText() string {
	return strings.Join(s.TextBuf, "")
}

// ClassifyConsoleLine parses a raw SSE line into (kind, value).
// kind is one of "event", "data", "done", "skip".
func ClassifyConsoleLine(line string) (kind, value string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "skip", ""
	}
	if strings.HasPrefix(line, "event:") {
		return "event", strings.TrimSpace(line[6:])
	}
	if strings.HasPrefix(line, "data:") {
		data := strings.TrimSpace(line[5:])
		if data == "[DONE]" {
			return "done", ""
		}
		return "data", data
	}
	return "skip", ""
}
