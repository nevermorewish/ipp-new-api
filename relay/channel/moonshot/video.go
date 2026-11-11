package moonshot

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
)

const maxVideoBytes = 64 << 20

// Bound CPU and temporary-file usage independently of relay concurrency.
var videoSlots = make(chan struct{}, 2)

type videoFrame struct {
	Seconds float64
	JPEG    []byte
}

type videoExtractor func(context.Context, string) ([]videoFrame, error)

// Transform the final wire body, including passthrough and parameter overrides.
// Only message content is traversed: URLs inside tool arguments are not media.
// Videos become sampled frames, and Chat image_url strings become the object
// form, because some upstreams reject both shapes.
func convertMediaBody(ctx context.Context, body []byte, format types.RelayFormat, extract videoExtractor) ([]byte, error) {
	var request map[string]any
	if err := common.UnmarshalUseNumber(body, &request); err != nil {
		return nil, fmt.Errorf("invalid JSON for video conversion")
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	cache := make(map[string][]videoFrame)
	changed := false
	videoCount := 0
	for _, field := range []string{"messages", "input"} {
		messages, _ := request[field].([]any)
		for _, raw := range messages {
			message, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			parts, ok := message["content"].([]any)
			if !ok {
				continue
			}
			var output []any
			for _, part := range parts {
				block, _ := part.(map[string]any)
				if format == types.RelayFormatOpenAI && block["type"] == "image_url" {
					if imageURL, ok := block["image_url"].(string); ok && imageURL != "" {
						block["image_url"] = map[string]any{"url": imageURL}
						changed = true
					}
				}
				ref, isVideo, err := videoReference(block)
				if err != nil {
					return nil, err
				}
				if !isVideo {
					output = append(output, part)
					continue
				}
				videoCount++
				if videoCount > 2 {
					return nil, fmt.Errorf("video conversion supports at most 2 videos per request")
				}
				frames, found := cache[ref]
				if !found {
					frames, err = extract(ctx, ref)
					if err != nil {
						return nil, fmt.Errorf("video %d: %w", videoCount, err)
					}
					cache[ref] = frames
				}
				if len(frames) == 0 {
					return nil, fmt.Errorf("video contains no decodable frames")
				}
				textType := "text"
				if format == types.RelayFormatOpenAIResponses {
					textType = "input_text"
				}
				output = append(output, map[string]any{"type": textType, "text": fmt.Sprintf("[Video %d: %d sampled frames in chronological order. Audio is not included.]", videoCount, len(frames))})
				for _, frame := range frames {
					encoded := base64.StdEncoding.EncodeToString(frame.JPEG)
					output = append(output, map[string]any{"type": textType, "text": fmt.Sprintf("[Video %d at %.2f seconds]", videoCount, frame.Seconds)})
					image := map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/jpeg;base64," + encoded}}
					if format == types.RelayFormatClaude {
						image = map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/jpeg", "data": encoded}}
					} else if format == types.RelayFormatOpenAIResponses {
						image = map[string]any{"type": "input_image", "image_url": "data:image/jpeg;base64," + encoded}
					}
					output = append(output, image)
				}
				changed = true
			}
			message["content"] = output
		}
	}
	if !changed {
		return body, nil
	}
	return common.Marshal(request)
}

func videoReference(block map[string]any) (string, bool, error) {
	kind, _ := block["type"].(string)
	var source any
	switch kind {
	case "video_url":
		source = block["video_url"]
	case "input_video", "video":
		source = block["video_url"]
		if source == nil {
			source = block["source"]
		}
		if source == nil {
			source = block["url"]
		}
	case "document":
		s, _ := block["source"].(map[string]any)
		mime, _ := s["media_type"].(string)
		if !strings.HasPrefix(mime, "video/") {
			return "", false, nil
		}
		source = s
	default:
		return "", false, nil
	}
	if ref, ok := source.(string); ok && ref != "" {
		return ref, true, nil
	}
	if s, ok := source.(map[string]any); ok {
		if ref, ok := s["url"].(string); ok && ref != "" {
			return ref, true, nil
		}
		data, _ := s["data"].(string)
		mime, _ := s["media_type"].(string)
		if data != "" && strings.HasPrefix(mime, "video/") {
			return "data:" + mime + ";base64," + data, true, nil
		}
	}
	return "", true, fmt.Errorf("video block requires an HTTP(S) URL or base64 video data")
}

func loadVideo(ctx context.Context, ref string) ([]byte, error) {
	if strings.HasPrefix(ref, "data:") {
		header, data, ok := strings.Cut(ref, ",")
		if !ok || !strings.HasPrefix(header, "data:video/") || !strings.HasSuffix(header, ";base64") {
			return nil, fmt.Errorf("video data must be a base64 video data URL")
		}
		if len(data) > base64.StdEncoding.EncodedLen(maxVideoBytes) {
			return nil, fmt.Errorf("video exceeds 64 MiB")
		}
		decoded, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return nil, fmt.Errorf("invalid video base64")
		}
		if len(decoded) > maxVideoBytes {
			return nil, fmt.Errorf("video exceeds 64 MiB")
		}
		return decoded, nil
	}
	u, err := url.Parse(ref)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("video URL must use HTTP(S) without embedded credentials")
	}
	if err := service.ValidateSSRFProtectedFetchURL(ref); err != nil {
		return nil, fmt.Errorf("video URL is blocked by fetch policy")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ref, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid video URL")
	}
	resp, err := service.GetSSRFProtectedHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("video download failed or timed out")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("video download returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxVideoBytes {
		return nil, fmt.Errorf("video exceeds 64 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxVideoBytes+1))
	if err != nil {
		return nil, fmt.Errorf("could not read video download")
	}
	if len(data) > maxVideoBytes {
		return nil, fmt.Errorf("video exceeds 64 MiB")
	}
	return data, nil
}

func extractVideoFrames(ctx context.Context, ref string) ([]videoFrame, error) {
	select {
	case videoSlots <- struct{}{}:
		defer func() { <-videoSlots }()
	case <-ctx.Done():
		return nil, fmt.Errorf("video processing timed out or was cancelled")
	}
	data, err := loadVideo(ctx, ref)
	if err != nil {
		return nil, err
	}
	// Pin the demuxer and forbid network protocols. Playlists must never cause
	// ffmpeg to fetch additional URLs or open arbitrary local resources.
	demuxer := ""
	switch {
	case len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp")):
		demuxer = "mov"
	case bytes.HasPrefix(data, []byte{0x1a, 0x45, 0xdf, 0xa3}):
		demuxer = "matroska"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "AVI ":
		demuxer = "avi"
	default:
		return nil, fmt.Errorf("unsupported video container; use MP4, MOV, WebM, MKV or AVI")
	}
	dir, err := os.MkdirTemp("", "moonshot-video-")
	if err != nil {
		return nil, fmt.Errorf("cannot create video workspace")
	}
	defer os.RemoveAll(dir)
	input := filepath.Join(dir, "input")
	if err := os.WriteFile(input, data, 0600); err != nil {
		return nil, fmt.Errorf("cannot save video")
	}
	probe := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-protocol_whitelist", "file", "-f", demuxer, "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", input)
	durationBytes, err := probe.Output()
	if err != nil {
		return nil, fmt.Errorf("cannot probe video; ensure ffprobe is installed and the video is valid")
	}
	duration, err := strconv.ParseFloat(strings.TrimSpace(string(durationBytes)), 64)
	if err != nil || math.IsNaN(duration) || math.IsInf(duration, 0) || duration <= 0 || duration > 7200 {
		return nil, fmt.Errorf("video duration must be between 0 and 7200 seconds")
	}
	count := min(16, max(1, int(math.Ceil(duration/2))))
	interval := duration / float64(count)
	filter := fmt.Sprintf("fps=%.9f:start_time=0:round=up,scale=768:768:force_original_aspect_ratio=decrease", 1/interval)
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-threads", "1", "-protocol_whitelist", "file", "-f", demuxer, "-i", input, "-map", "0:v:0", "-an", "-vf", filter, "-frames:v", strconv.Itoa(count), "-threads", "1", "-filter_threads", "1", "-q:v", "4", filepath.Join(dir, "frame-%02d.jpg"))
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("cannot extract video frames; ensure ffmpeg is installed and the video is valid")
	}
	var frames []videoFrame
	for i := range count {
		jpeg, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("frame-%02d.jpg", i+1)))
		if os.IsNotExist(err) {
			break
		}
		if err != nil || len(jpeg) == 0 || len(jpeg) > 1<<20 {
			return nil, fmt.Errorf("invalid extracted video frame")
		}
		frames = append(frames, videoFrame{Seconds: float64(i) * interval, JPEG: jpeg})
	}
	if len(frames) == 0 {
		return nil, fmt.Errorf("video contains no decodable frames")
	}
	return frames, nil
}
