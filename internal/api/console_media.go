package api

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/aurora-develop/grok2api/internal/account"
	"github.com/aurora-develop/grok2api/internal/grok"
	"github.com/aurora-develop/grok2api/internal/model"
	"github.com/aurora-develop/grok2api/internal/platform"
)

func (s *Server) handleConsoleImageGeneration(c *gin.Context, spec *model.Spec, prompt string, n int, size, responseFormat, aspectRatio, resolution string) {
	if strings.TrimSpace(prompt) == "" {
		writeAppError(c, platform.ValidationError("Missing prompt", "prompt"))
		return
	}
	if n <= 0 {
		n = 1
	}
	if n > 10 {
		writeAppError(c, platform.ValidationError("n must be between 1 and 10", "n"))
		return
	}
	format, err := normalizeConsoleImageFormat(responseFormat)
	if err != nil {
		writeAppError(c, platform.ValidationError(err.Error(), "response_format"))
		return
	}
	ratio, err := resolveConsoleAspectRatio(aspectRatio, size)
	if err != nil {
		writeAppError(c, platform.ValidationError(err.Error(), "size"))
		return
	}
	resolution, err = normalizeConsoleResolution(resolution)
	if err != nil {
		writeAppError(c, platform.ValidationError(err.Error(), "resolution"))
		return
	}
	lease, err := s.reserveConsoleMediaAccount(c, spec)
	if err != nil {
		writeAppError(c, err)
		return
	}
	defer s.Directory.Release(lease)

	payload := map[string]any{
		"model":           grok.ConsoleModels[spec.ModelName],
		"prompt":          prompt,
		"n":               n,
		"response_format": format,
	}
	if ratio != "" {
		payload["aspect_ratio"] = ratio
	}
	if resolution != "" {
		payload["resolution"] = resolution
	}
	result, err := s.Transport.ConsoleImageGeneration(c.Request.Context(), lease.Token, payload)
	if err != nil {
		s.feedbackError(lease.Token, err, lease.ModeID)
		writeAppError(c, err)
		return
	}
	s.feedback(lease.Token, account.FbSuccess, lease.ModeID, nil, nil)
	c.JSON(http.StatusOK, result)
}

func (s *Server) handleConsoleImageEdit(c *gin.Context, spec *model.Spec, modelName, prompt string) {
	responseFormat, err := normalizeConsoleImageFormat(c.Request.FormValue("response_format"))
	if err != nil {
		writeAppError(c, platform.ValidationError(err.Error(), "response_format"))
		return
	}
	n := 1
	if raw := strings.TrimSpace(c.Request.FormValue("n")); raw != "" {
		if parsed, parseErr := parseIntStr(raw); parseErr == nil {
			n = parsed
		}
	}
	if n < 1 || n > 10 {
		writeAppError(c, platform.ValidationError("n must be between 1 and 10", "n"))
		return
	}
	files := c.Request.MultipartForm.File["image[]"]
	if len(files) == 0 {
		files = c.Request.MultipartForm.File["image"]
	}
	if len(files) == 0 || len(files) > 3 {
		writeAppError(c, platform.ValidationError("Console image edit requires 1 to 3 images", "image"))
		return
	}
	images := make([]map[string]any, 0, len(files))
	for _, fh := range files {
		f, openErr := fh.Open()
		if openErr != nil {
			writeAppError(c, platform.ValidationError("Unable to read image: "+openErr.Error(), "image"))
			return
		}
		raw, readErr := io.ReadAll(io.LimitReader(f, 30<<20))
		_ = f.Close()
		if readErr != nil || len(raw) == 0 {
			writeAppError(c, platform.ValidationError("Invalid image", "image"))
			return
		}
		mime := strings.TrimSpace(fh.Header.Get("Content-Type"))
		if !strings.HasPrefix(strings.ToLower(mime), "image/") {
			writeAppError(c, platform.ValidationError("Unsupported image content type", "image"))
			return
		}
		images = append(images, map[string]any{
			"type": "image_url",
			"url":  "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw),
		})
	}
	ratio, err := resolveConsoleAspectRatio(c.Request.FormValue("aspect_ratio"), c.Request.FormValue("size"))
	if err != nil {
		writeAppError(c, platform.ValidationError(err.Error(), "size"))
		return
	}
	resolution, err := normalizeConsoleResolution(c.Request.FormValue("resolution"))
	if err != nil {
		writeAppError(c, platform.ValidationError(err.Error(), "resolution"))
		return
	}
	lease, err := s.reserveConsoleMediaAccount(c, spec)
	if err != nil {
		writeAppError(c, err)
		return
	}
	defer s.Directory.Release(lease)

	payload := map[string]any{
		"model":           grok.ConsoleModels[modelName],
		"prompt":          prompt,
		"n":               n,
		"response_format": responseFormat,
	}
	if len(images) == 1 {
		payload["image"] = images[0]
	} else {
		payload["images"] = images
	}
	if ratio != "" {
		payload["aspect_ratio"] = ratio
	}
	if resolution != "" {
		payload["resolution"] = resolution
	}
	result, err := s.Transport.ConsoleImageEdit(c.Request.Context(), lease.Token, payload)
	if err != nil {
		s.feedbackError(lease.Token, err, lease.ModeID)
		writeAppError(c, err)
		return
	}
	s.feedback(lease.Token, account.FbSuccess, lease.ModeID, nil, nil)
	c.JSON(http.StatusOK, result)
}

func (s *Server) reserveConsoleMediaAccount(c *gin.Context, spec *model.Spec) (*account.Lease, error) {
	lease, _ := reserveAccount(c.Request.Context(), s.Directory, spec, nil)
	if lease == nil && s.Refresh != nil {
		_ = s.Refresh.RefreshOnDemand(c.Request.Context())
		lease, _ = reserveAccount(c.Request.Context(), s.Directory, spec, nil)
	}
	if lease == nil {
		apiToken, _ := c.Get("api_token")
		if token, _ := apiToken.(string); strings.TrimSpace(token) != "" {
			lease = &account.Lease{Token: token, ModeID: int(model.ModeConsole)}
		}
	}
	if lease == nil {
		return nil, platform.RateLimitError("No available Console SSO accounts")
	}
	return lease, nil
}

func (s *Server) runConsoleVideoJob(job *videoJob, prompt string, spec *model.Spec, fallbackSSO string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	job.mu.Lock()
	job.Status = "in_progress"
	job.Progress = 1
	job.mu.Unlock()

	lease, _ := reserveAccount(ctx, s.Directory, spec, nil)
	if lease == nil && strings.TrimSpace(fallbackSSO) != "" {
		lease = &account.Lease{Token: fallbackSSO, ModeID: int(model.ModeConsole)}
	}
	if lease == nil {
		s.failVideoJob(job, "no available Console SSO accounts")
		return
	}
	defer s.Directory.Release(lease)

	resolution, ratio := resolveConsoleVideoSize(job.Size)
	payload := map[string]any{
		"model":      "grok-imagine-video",
		"prompt":     prompt,
		"duration":   job.Seconds,
		"resolution": resolution,
	}
	if ratio != "" {
		payload["aspect_ratio"] = ratio
	}
	created, err := s.Transport.ConsoleVideoGeneration(ctx, lease.Token, payload)
	if err != nil {
		s.feedbackError(lease.Token, err, lease.ModeID)
		s.failVideoJob(job, "Console video create: "+err.Error())
		return
	}
	requestID, _ := created["request_id"].(string)
	if strings.TrimSpace(requestID) == "" {
		s.failVideoJob(job, "Console video create response missing request_id")
		return
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		status, pollErr := s.Transport.ConsoleVideoStatus(ctx, lease.Token, requestID)
		if pollErr != nil {
			s.feedbackError(lease.Token, pollErr, lease.ModeID)
			s.failVideoJob(job, "Console video poll: "+pollErr.Error())
			return
		}
		if progress, ok := numberAsInt(status["progress"]); ok {
			job.mu.Lock()
			if progress > job.Progress {
				job.Progress = min(progress, 99)
			}
			job.mu.Unlock()
		}
		state, _ := status["status"].(string)
		switch strings.ToLower(strings.TrimSpace(state)) {
		case "done", "completed", "succeeded", "success", "ready":
			video, _ := status["video"].(map[string]any)
			videoURL, _ := video["url"].(string)
			if strings.TrimSpace(videoURL) == "" {
				s.failVideoJob(job, "Console video completed without URL")
				return
			}
			now := time.Now().Unix()
			job.mu.Lock()
			job.Status = "completed"
			job.Progress = 100
			job.CompletedAt = &now
			job.VideoURL = videoURL
			job.mu.Unlock()
			s.feedback(lease.Token, account.FbSuccess, lease.ModeID, nil, nil)
			s.cacheConsoleVideo(ctx, job, videoURL)
			return
		case "failed", "expired", "cancelled", "canceled", "error":
			s.failVideoJob(job, "Console video generation failed")
			return
		case "pending", "processing", "in_progress", "queued", "":
		default:
			s.failVideoJob(job, "invalid Console video status: "+state)
			return
		}
		select {
		case <-ctx.Done():
			s.failVideoJob(job, ctx.Err().Error())
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) cacheConsoleVideo(ctx context.Context, job *videoJob, videoURL string) {
	if s.Media == nil {
		return
	}
	reader, err := s.Transport.DownloadConsoleVideo(ctx, videoURL)
	if err != nil {
		return
	}
	defer reader.Close()
	raw, err := io.ReadAll(io.LimitReader(reader, 512<<20))
	if err != nil || len(raw) == 0 {
		return
	}
	path, err := s.Media.SaveVideo(raw, strings.TrimPrefix(job.ID, "video_"))
	if err == nil {
		job.mu.Lock()
		job.contentPath = path
		job.mu.Unlock()
	}
}

func normalizeConsoleImageFormat(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "url", nil
	}
	if value != "url" && value != "b64_json" {
		return "", fmt.Errorf("response_format must be url or b64_json")
	}
	return value, nil
}

func normalizeConsoleResolution(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "", nil
	}
	if value != "1k" && value != "2k" {
		return "", fmt.Errorf("resolution must be 1k or 2k")
	}
	return value, nil
}

func resolveConsoleAspectRatio(aspectRatio, size string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(aspectRatio))
	if value == "" {
		value = strings.ToLower(strings.TrimSpace(size))
	}
	if value == "" || value == "auto" {
		return "", nil
	}
	values := map[string]string{
		"1:1": "1:1", "16:9": "16:9", "9:16": "9:16", "4:3": "4:3", "3:4": "3:4",
		"3:2": "3:2", "2:3": "2:3", "2:1": "2:1", "1:2": "1:2",
		"1024x1024": "1:1", "1280x720": "16:9", "720x1280": "9:16",
		"1792x1024": "3:2", "1536x1024": "3:2", "1024x1792": "2:3", "1024x1536": "2:3",
	}
	if ratio := values[value]; ratio != "" {
		return ratio, nil
	}
	return "", fmt.Errorf("unsupported aspect_ratio or size")
}

func resolveConsoleVideoSize(size string) (resolution, ratio string) {
	switch strings.ToLower(strings.TrimSpace(size)) {
	case "1280x720", "720p", "16:9":
		return "720p", "16:9"
	case "720x1280", "9:16", "":
		return "720p", "9:16"
	case "854x480", "480p":
		return "480p", "16:9"
	case "480x854":
		return "480p", "9:16"
	default:
		return "720p", "9:16"
	}
}

func numberAsInt(value any) (int, bool) {
	switch number := value.(type) {
	case int:
		return number, true
	case float64:
		return int(number), true
	case int64:
		return int(number), true
	}
	return 0, false
}
