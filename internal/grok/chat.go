package grok

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/aurora-develop/grok2api/internal/config"
	"github.com/aurora-develop/grok2api/internal/model"
	"github.com/aurora-develop/grok2api/internal/platform"
)

// FrameEventKind enumerates the event kinds emitted by StreamAdapter.
type FrameEventKind string

const (
	EventText          FrameEventKind = "text"           // final text token
	EventThinking      FrameEventKind = "thinking"       // reasoning token
	EventImage         FrameEventKind = "image"          // generated image final URL
	EventImageProgress FrameEventKind = "image_progress" // generated image progress percent
	EventSoftStop      FrameEventKind = "soft_stop"      // stream end signal
	EventSkip          FrameEventKind = "skip"           // filtered frame
)

// FrameEvent is one parsed event from StreamAdapter.Feed.
type FrameEvent struct {
	Kind           FrameEventKind
	Content        string
	ImageID        string
	RolloutID      string
	MessageTag     string
	MessageStepID  *int
	AnnotationData map[string]any
}

// BuildChatPayload constructs the JSON payload for POST /rest/app-chat/conversations/new.
// fileAttachments are pre-uploaded file_metadata IDs.
func BuildChatPayload(message string, modeID model.ModeId, fileAttachments []string, toolOverrides map[string]any, modelConfigOverride map[string]any, requestOverrides map[string]any) map[string]any {
	cfg := config.Global()
	temporary := cfg.GetBool("features.temporary", true)
	memory := cfg.GetBool("features.memory", false)
	customInstruction := strings.TrimSpace(cfg.GetStr("features.custom_instruction", ""))

	tools := toolOverrides
	if tools == nil {
		tools = map[string]any{
			"gmailSearch":           false,
			"googleCalendarSearch":  false,
			"outlookSearch":         false,
			"outlookCalendarSearch": false,
			"googleDriveSearch":     false,
		}
	}
	if fileAttachments == nil {
		fileAttachments = []string{}
	}

	payload := map[string]any{
		"collectionIds":             []any{},
		"disabledConnectorIds":      []any{},
		"deviceEnvInfo": map[string]any{
			"darkModeEnabled":  false,
			"devicePixelRatio": 1,
			"screenHeight":     1080,
			"screenWidth":      1920,
			"viewportHeight":   945,
			"viewportWidth":    1920,
		},
		"disableMemory":               !memory,
		"disableSearch":               false,
		"disableSelfHarmShortCircuit": false,
		"disableTextFollowUps":        false,
		"enableImageGeneration":       true,
		"enableImageStreaming":        true,
		"enableSideBySide":            true,
		"fileAttachments":             fileAttachments,
		"forceConcise":                false,
		"forceSideBySide":             false,
		"imageAttachments":            []any{},
		"imageGenerationCount":        2,
		"isAsyncChat":                 false,
		"message":                     message,
		"modeId":                      modeID.ApiStr(),
		"responseMetadata":            map[string]any{},
		"returnImageBytes":            false,
		"returnRawGrokInXaiRequest":   false,
		"searchAllConnectors":         false,
		"sendFinalMetadata":           true,
		"temporary":                   temporary,
		"toolOverrides":               tools,
	}
	if customInstruction != "" {
		payload["customPersonality"] = customInstruction
	}
	if modelConfigOverride != nil {
		payload["responseMetadata"] = map[string]any{"modelConfigOverride": modelConfigOverride}
	}
	for k, v := range requestOverrides {
		if v == nil {
			continue
		}
		payload[k] = v
	}
	return payload
}

// BuildResponsePayload constructs the JSON payload for POST /rest/app-chat/conversations/{id}/responses.
// Unlike BuildChatPayload, this sends a follow-up message in an existing conversation
// and requires responseId pointing to the parent message to reply to.
func BuildResponsePayload(message string, responseID string, modeID model.ModeId, fileAttachments []string, toolOverrides map[string]any, requestOverrides map[string]any) map[string]any {
	base := BuildChatPayload(message, modeID, fileAttachments, toolOverrides, nil, requestOverrides)
	base["responseId"] = responseID
	return base
}

// ClassifyLine parses a raw SSE line and returns (kind, data).
// kind is one of "data", "done", "skip".
func ClassifyLine(line string) (kind, data string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "skip", ""
	}
	if strings.HasPrefix(line, "data:") {
		data = strings.TrimSpace(line[5:])
		if data == "[DONE]" {
			return "done", ""
		}
		return "data", data
	}
	if strings.HasPrefix(line, "event:") {
		return "skip", ""
	}
	if strings.HasPrefix(line, "{") {
		return "data", line
	}
	return "skip", ""
}

// StreamErrorFromPayload converts an in-band stream error JSON object to
// an UpstreamError. Returns nil when no error is present.
func StreamErrorFromPayload(obj map[string]any) *platform.AppError {
	errObj, ok := obj["error"].(map[string]any)
	if !ok {
		return nil
	}
	rawMsg := ""
	if v, ok := errObj["message"].(string); ok && v != "" {
		rawMsg = v
	} else if v, ok := errObj["error"].(string); ok && v != "" {
		rawMsg = v
	} else {
		rawMsg = "Upstream stream error"
	}
	message := rawMsg
	code := 0
	if v, ok := errObj["code"].(float64); ok {
		code = int(v)
	}
	status := 502
	if code == 8 || strings.Contains(strings.ToLower(message), "too many requests") ||
		strings.Contains(strings.ToLower(message), "rate limit") {
		status = 429
	}
	encoded, _ := json.Marshal(obj)
	return platform.UpstreamError(
		"Upstream stream error: "+message,
		status,
		truncBodyStr(string(encoded), 400),
	)
}

// RaiseForStreamError returns an UpstreamError if *data* (raw JSON) contains
// an upstream stream error payload. Returns nil if no error.
func RaiseForStreamError(data []byte) *platform.AppError {
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil
	}
	return StreamErrorFromPayload(obj)
}

// StreamAdapter parses upstream SSE frames and emits FrameEvents.
// One instance per HTTP request.
type StreamAdapter struct {
	cardCache         map[string]map[string]any
	citationOrder     []string
	citationMap       map[string]int
	lastCitationIndex int
	pendingCitations  []citationRef
	annotations       []map[string]any
	textOffset        int
	emittedKeys       map[string]struct{}
	lastRollout       string
	contentStarted    bool
	webSearchResults  []map[string]any
	webSearchURLs     map[string]struct{}
	ThinkingBuf       []string
	TextBuf           []string
	ImageURLs         [][2]string // [(url, imageUuid), ...]

	// Conversation tracking — populated from the first frames of conversations/new.
	ConversationID string
	LastResponseID string // userResponse.responseId from the current message
}

type citationRef struct {
	URL    string
	Title  string
	Needle string
}

// NewStreamAdapter returns a stateless-per-request StreamAdapter.
func NewStreamAdapter() *StreamAdapter {
	return &StreamAdapter{
		cardCache:         map[string]map[string]any{},
		citationMap:       map[string]int{},
		emittedKeys:       map[string]struct{}{},
		webSearchURLs:     map[string]struct{}{},
		lastCitationIndex: -1,
	}
}

// Feed parses one JSON data payload and returns 0-N events. If the payload
// contains an in-band stream error, a non-nil *platform.AppError is returned
// instead of (or in addition to) events.
func (s *StreamAdapter) Feed(data []byte) ([]FrameEvent, *platform.AppError) {
	if errObj := RaiseForStreamError(data); errObj != nil {
		return nil, errObj
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, nil
	}
	result, _ := obj["result"].(map[string]any)
	if result == nil {
		return nil, nil
	}

	// Frame 1: conversation creation (only on conversations/new).
	if conv, _ := result["conversation"].(map[string]any); conv != nil {
		if cid, _ := conv["conversationId"].(string); cid != "" {
			s.ConversationID = cid
		}
		return nil, nil
	}

	resp, _ := result["response"].(map[string]any)
	if resp == nil {
		return nil, nil
	}

	// Frame 2: user response echo — capture responseId for conversation tracking.
	if userResp, _ := resp["userResponse"].(map[string]any); userResp != nil {
		if rid, _ := userResp["responseId"].(string); rid != "" {
			s.LastResponseID = rid
		}
	}

	events := []FrameEvent{}

	if cardRaw, ok := resp["cardAttachment"].(map[string]any); ok {
		events = append(events, s.handleCard(cardRaw)...)
	}

	// webSearchResults: collect search sources.
	if wsr, ok := resp["webSearchResults"].(map[string]any); ok {
		if results, ok := wsr["results"].([]any); ok {
			for _, it := range results {
				item, ok := it.(map[string]any)
				if !ok {
					continue
				}
				u, _ := item["url"].(string)
				if u == "" {
					continue
				}
				if _, seen := s.webSearchURLs[u]; seen {
					continue
				}
				s.webSearchURLs[u] = struct{}{}
				item["type"] = "web"
				s.webSearchResults = append(s.webSearchResults, item)
			}
		}
	}

	// xSearchResults: collect X/Twitter post sources.
	if xsr, ok := resp["xSearchResults"].(map[string]any); ok {
		if results, ok := xsr["results"].([]any); ok {
			for _, it := range results {
				item, ok := it.(map[string]any)
				if !ok {
					continue
				}
				postID, _ := item["postId"].(string)
				username, _ := item["username"].(string)
				if postID == "" || username == "" {
					continue
				}
				u := fmt.Sprintf("https://x.com/%s/status/%s", username, postID)
				if _, seen := s.webSearchURLs[u]; seen {
					continue
				}
				s.webSearchURLs[u] = struct{}{}
				raw, _ := item["text"].(string)
				raw = strings.TrimSpace(spaceRE.ReplaceAllString(raw, " "))
				var title string
				if raw != "" {
					if len(raw) > 50 {
						title = fmt.Sprintf("𝕏/@%s: %s...", username, raw[:50])
					} else {
						title = fmt.Sprintf("𝕏/@%s: %s", username, raw)
					}
				} else {
					title = "𝕏/@" + username
				}
				s.webSearchResults = append(s.webSearchResults, map[string]any{
					"url":   u,
					"title": title,
					"type":  "x_post",
				})
			}
		}
	}

	token, _ := resp["token"].(string)
	think, _ := resp["isThinking"].(bool)
	tag, _ := resp["messageTag"].(string)
	rollout, _ := resp["rolloutId"].(string)
	var stepID *int
	if v, ok := resp["messageStepId"].(float64); ok {
		n := int(v)
		stepID = &n
	}

	if tag == "tool_usage_card" {
		// Tool usage cards are skipped in this minimal port (no ReasoningAggregator).
		if s.contentStarted {
			return events, nil
		}
		return events, nil
	}
	if tag == "raw_function_result" {
		return events, nil
	}
	if _, ok := resp["toolUsageCardId"]; ok {
		if _, hasWSR := resp["webSearchResults"]; !hasWSR {
			if _, hasCER := resp["codeExecutionResult"]; !hasCER {
				return events, nil
			}
		}
	}

	// Thinking token.
	if token != "" && think {
		if s.contentStarted {
			raw := strings.TrimSpace(token)
			if raw != "" {
				if !strings.HasSuffix(raw, "\n") {
					raw += "\n"
				}
				s.ThinkingBuf = append(s.ThinkingBuf, raw)
			}
			return events, nil
		}
		// Pass through thinking tokens with optional agent header.
		raw := token
		if strings.HasPrefix(raw, "- ") {
			raw = raw[2:]
		}
		if raw == "" {
			return events, nil
		}
		agent := rollout
		if agent != "" && agent != s.lastRollout {
			s.lastRollout = agent
			header := "\n[" + agent + "]\n"
			s.ThinkingBuf = append(s.ThinkingBuf, header)
			events = append(events, FrameEvent{
				Kind:      EventThinking,
				Content:   header,
				RolloutID: agent,
			})
		}
		s.appendReasoning(raw, agent, tag, stepID)
		return events, nil
	}

	// Final text token.
	if token != "" && !think && tag == "final" {
		s.contentStarted = true
		cleaned, localAnns := s.cleanToken(token)
		if cleaned != "" {
			s.TextBuf = append(s.TextBuf, cleaned)
			events = append(events, FrameEvent{Kind: EventText, Content: cleaned})
			for _, ann := range localAnns {
				ls, _ := ann["localStart"].(int)
				le, _ := ann["localEnd"].(int)
				ann["start_index"] = s.textOffset + ls
				ann["end_index"] = s.textOffset + le
				delete(ann, "localStart")
				delete(ann, "localEnd")
				s.annotations = append(s.annotations, ann)
				events = append(events, FrameEvent{Kind: EventSkip, AnnotationData: ann})
			}
			s.textOffset += len(cleaned)
		}
		return events, nil
	}

	if isSoftStop, _ := resp["isSoftStop"].(bool); isSoftStop {
		events = append(events, FrameEvent{Kind: EventSoftStop})
		return events, nil
	}
	if _, ok := resp["finalMetadata"]; ok {
		events = append(events, FrameEvent{Kind: EventSoftStop})
		return events, nil
	}
	return events, nil
}

func (s *StreamAdapter) appendReasoning(line, rollout, tag string, stepID *int) {
	if line == "" {
		return
	}
	key := rollout + ":" + line
	if _, ok := s.emittedKeys[key]; ok {
		return
	}
	s.emittedKeys[key] = struct{}{}
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	s.ThinkingBuf = append(s.ThinkingBuf, line)
}

// handleCard caches card data and emits image events on progress=100.
func (s *StreamAdapter) handleCard(cardRaw map[string]any) []FrameEvent {
	jsonStr, _ := cardRaw["jsonData"].(string)
	if jsonStr == "" {
		return nil
	}
	var jd map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &jd); err != nil {
		return nil
	}
	cardID, _ := jd["id"].(string)
	if cardID != "" {
		s.cardCache[cardID] = jd
	}
	chunk, _ := jd["image_chunk"].(map[string]any)
	if chunk == nil {
		return nil
	}
	events := []FrameEvent{}
	progress, _ := chunk["progress"].(float64)
	imageUUID, _ := chunk["imageUuid"].(string)
	if progress != 0 {
		events = append(events, FrameEvent{
			Kind:    EventImageProgress,
			Content: fmt.Sprintf("%d", int(progress)),
			ImageID: imageUUID,
		})
	}
	if progress == 100 {
		if moderated, _ := chunk["moderated"].(bool); !moderated {
			imageURL, _ := chunk["imageUrl"].(string)
			if imageURL != "" {
				url := imageBaseURL + imageURL
				s.ImageURLs = append(s.ImageURLs, [2]string{url, imageUUID})
				events = append(events, FrameEvent{
					Kind:    EventImage,
					Content: url,
					ImageID: imageUUID,
				})
			}
		}
	}
	return events
}

// cleanToken strips <grok:render> tags from a token and returns the cleaned
// text plus any citation annotations (with local positions).
func (s *StreamAdapter) cleanToken(token string) (string, []map[string]any) {
	if !strings.Contains(token, "<grok:render") {
		return token, nil
	}
	cleaned := grokRenderRE.ReplaceAllStringFunc(token, func(match string) string {
		sub := grokRenderRE.FindStringSubmatch(match)
		if len(sub) < 4 {
			return ""
		}
		cardID := sub[1]
		renderType := sub[3]
		card := s.cardCache[cardID]
		if card == nil {
			return ""
		}
		switch renderType {
		case "render_searched_image":
			img, _ := card["image"].(map[string]any)
			title, _ := img["title"].(string)
			if title == "" {
				title = "image"
			}
			thumb, _ := img["thumbnail"].(string)
			if thumb == "" {
				thumb, _ = img["original"].(string)
			}
			link, _ := img["link"].(string)
			if link != "" {
				return fmt.Sprintf("[![%s](%s)](%s)", title, thumb, link)
			}
			return fmt.Sprintf("![%s](%s)", title, thumb)
		case "render_generated_image":
			return ""
		case "render_inline_citation":
			u, _ := card["url"].(string)
			if u == "" {
				return ""
			}
			index, ok := s.citationMap[u]
			if !ok {
				s.citationOrder = append(s.citationOrder, u)
				index = len(s.citationOrder)
				s.citationMap[u] = index
			}
			if index == s.lastCitationIndex {
				return ""
			}
			s.lastCitationIndex = index
			citation := fmt.Sprintf(" [[%d]](%s)", index, u)
			title, _ := card["title"].(string)
			if title == "" {
				for _, item := range s.webSearchResults {
					if iu, _ := item["url"].(string); iu == u {
						if t, _ := item["title"].(string); t != "" {
							title = t
						}
						break
					}
				}
			}
			if title == "" {
				title = u
			}
			s.pendingCitations = append(s.pendingCitations, citationRef{
				URL: u, Title: title, Needle: citation,
			})
			return citation
		}
		return ""
	})
	if strings.HasPrefix(cleaned, "\n") && strings.Contains(cleaned, "[[") {
		cleaned = strings.TrimLeft(cleaned, "\n")
	}

	annotations := []map[string]any{}
	if len(s.pendingCitations) > 0 {
		searchStart := 0
		for _, cite := range s.pendingCitations {
			pos := strings.Index(cleaned[searchStart:], cite.Needle)
			if pos < 0 {
				continue
			}
			pos += searchStart
			annotations = append(annotations, map[string]any{
				"type":       "url_citation",
				"url":        cite.URL,
				"title":      cite.Title,
				"localStart": pos,
				"localEnd":   pos + len(cite.Needle),
			})
			searchStart = pos + len(cite.Needle)
		}
		s.pendingCitations = s.pendingCitations[:0]
	}
	return cleaned, annotations
}

// Annotations returns the citation annotations collected so far.
func (s *StreamAdapter) Annotations() []map[string]any {
	out := make([]map[string]any, len(s.annotations))
	copy(out, s.annotations)
	return out
}

// SearchSources returns the structured search sources collected so far.
func (s *StreamAdapter) SearchSources() []map[string]any {
	if len(s.webSearchResults) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(s.webSearchResults))
	for _, item := range s.webSearchResults {
		u, _ := item["url"].(string)
		title, _ := item["title"].(string)
		if title == "" {
			title = u
		}
		typ, _ := item["type"].(string)
		if typ == "" {
			typ = "web"
		}
		out = append(out, map[string]any{
			"url":   u,
			"title": title,
			"type":  typ,
		})
	}
	return out
}

// FullText returns the accumulated text content.
func (s *StreamAdapter) FullText() string {
	return strings.Join(s.TextBuf, "")
}

// FullThinking returns the accumulated reasoning text.
func (s *StreamAdapter) FullThinking() string {
	return strings.Join(s.ThinkingBuf, "")
}

var (
	imageBaseURL = "https://assets.grok.com/"
	grokRenderRE = regexp.MustCompile(`<grok:render\s+card_id="([^"]+)"\s+card_type="([^"]+)"\s+type="([^"]+)"[^>]*>.*?</grok:render>`)
	spaceRE      = regexp.MustCompile(`\s+`)
)

// truncBodyStr truncates a string to at most n bytes.
func truncBodyStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
