package handlers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Streaming: with ?stream=1 the chat answers as Server-Sent Events while the model writes:
//   event: delta  data: {"text":"…"}      a new piece of the answer
//   event: done   data: {…}               the same object a JSON reply would carry
//   event: error  data: {"error":"…","detail":"…"}
// When the person stops (closes the request), the call to the provider is cancelled too.

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Model string         `json:"model"`
	Error *providerError `json:"error,omitempty"`
}

// wantsChatStream is true when the client asked for a stream and the answer can be streamed as
// it is: a combo picks its model as it goes and native tool calls are rebuilt after they arrive,
// so those keep the JSON reply.
func wantsChatStream(c *gin.Context, isCombo bool, request chatRequest) bool {
	return c.Query("stream") == "1" && !isCombo && len(request.Tools) == 0
}

// streamAIProviderWithRetry streams one answer. A model that refuses chatMaxTokens is asked again
// without a limit, as callAIProviderWithRepair does, as long as nothing was sent yet.
func streamAIProviderWithRetry(ctx context.Context, apiKey, baseURL string, request chatRequest, onDelta func(string)) (string, error) {
	sent := false
	forward := func(text string) {
		sent = true
		onDelta(text)
	}
	answer, err := streamAIProvider(ctx, apiKey, baseURL, request, forward)
	if err != nil && !sent && request.MaxTokens > 0 && maxTokensRefused(err) {
		log.Printf("[CHAT] provider menolak max_tokens=%d (%v); mengulang tanpa batas", request.MaxTokens, err)
		request.MaxTokens = 0
		return streamAIProvider(ctx, apiKey, baseURL, request, forward)
	}
	return answer, err
}

// streamAIProvider calls an OpenAI-compatible provider with stream=true and hands each piece of
// the answer to onDelta. A provider that answers with plain JSON instead is read as one piece.
func streamAIProvider(ctx context.Context, apiKey, baseURL string, request chatRequest, onDelta func(string)) (string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return "", errors.New("base URL provider kosong")
	}
	endpoint := baseURL
	if !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint += "/chat/completions"
	}
	request.Stream = true
	body, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("gagal membuat JSON request: %w", err)
	}
	timeout := providerCallTimeout
	if request.Timeout > 0 && request.Timeout < timeout {
		timeout = request.Timeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("gagal membuat HTTP request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	log.Printf("[CHAT AI] Stream endpoint: %s model: %s", endpoint, request.Model)
	start := time.Now()
	resp, err := providerHTTPClient(0).Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("gagal menghubungi provider AI: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		diagnostic := providerRateLimitDiagnostic(resp, responseBody)
		return "", formatProviderHTTPErrorWithDiagnostic(resp.StatusCode, responseBody, diagnostic)
	}

	// Some providers ignore stream=true and answer with one JSON object.
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if err != nil {
			return "", fmt.Errorf("gagal membaca response provider: %w", err)
		}
		var single chatResponse
		if json.Unmarshal(responseBody, &single) != nil || len(single.Choices) == 0 {
			return "", fmt.Errorf("response provider bukan stream maupun JSON yang valid (body=%s)", sanitizeProviderBody(responseBody))
		}
		answer, err := decodeProviderContent(single.Choices[0].Message.Content)
		if err != nil {
			return "", err
		}
		if answer == "" {
			return "", errors.New("provider response memiliki choices tetapi content kosong")
		}
		onDelta(answer)
		return answer, nil
	}

	var answer strings.Builder
	finish, model := "", request.Model
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue // comments such as ": OPENROUTER PROCESSING", and event names
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk streamChunk
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		if chunk.Error != nil {
			return answer.String(), fmt.Errorf("provider stream error: %s", formatProviderError(*chunk.Error))
		}
		if chunk.Model != "" {
			model = chunk.Model
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				answer.WriteString(choice.Delta.Content)
				onDelta(choice.Delta.Content)
			}
			if choice.FinishReason != "" {
				finish = choice.FinishReason
			}
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return answer.String(), ctx.Err()
		}
		return answer.String(), fmt.Errorf("stream provider terputus: %w", err)
	}
	log.Printf("[CHAT AI] Stream selesai setelah %s (finish_reason=%s)", time.Since(start), finish)

	text := strings.TrimSpace(answer.String())
	if text == "" && finish == "length" {
		limit := "batas model"
		if request.MaxTokens > 0 {
			limit = fmt.Sprintf("%d token", request.MaxTokens)
		}
		return "", fmt.Errorf("jawaban model terpotong karena mencapai batas panjang jawaban (%s) sebelum selesai; minta perubahan yang lebih kecil atau coba model lain (model=%s)", limit, model)
	}
	if text == "" {
		return "", fmt.Errorf("provider stream selesai tanpa jawaban (model=%s finish_reason=%s)", model, finish)
	}
	return answer.String(), nil
}

// chatStreamWriter writes Server-Sent Events to the client and flushes each one at once.
type chatStreamWriter struct{ c *gin.Context }

func startChatStream(c *gin.Context) chatStreamWriter {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // a proxy must not hold the events back
	c.Status(http.StatusOK)
	c.Writer.Flush()
	return chatStreamWriter{c: c}
}

func (w chatStreamWriter) send(event string, payload any) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fmt.Fprintf(w.c.Writer, "event: %s\ndata: %s\n\n", event, encoded)
	w.c.Writer.Flush()
}
