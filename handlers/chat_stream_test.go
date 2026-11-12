package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/services"
)

func sseProvider(t *testing.T, handler func(w http.ResponseWriter, request chatRequest)) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var request chatRequest
		_ = json.Unmarshal(body, &request)
		handler(w, request)
	}))
	t.Cleanup(server.Close)
	return server
}

func writeSSE(w http.ResponseWriter, chunks ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, chunk := range chunks {
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		w.(http.Flusher).Flush()
	}
}

func delta(text string) string {
	encoded, _ := json.Marshal(map[string]any{"model": "m", "choices": []any{map[string]any{"delta": map[string]any{"content": text}}}})
	return string(encoded)
}

func TestTheAnswerArrivesPieceByPiece(t *testing.T) {
	var asked chatRequest
	server := sseProvider(t, func(w http.ResponseWriter, request chatRequest) {
		asked = request
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": OPENROUTER PROCESSING\n\n")
		writeSSE(w, delta("Halo"), delta(", "), delta("Rina"), `{"choices":[{"delta":{},"finish_reason":"stop"}]}`, "[DONE]")
	})
	var pieces []string
	answer, err := streamAIProviderWithRetry(context.Background(), "key", server.URL, baseRequest(), func(text string) { pieces = append(pieces, text) })
	if err != nil || answer != "Halo, Rina" {
		t.Fatalf("answer = %q, err = %v", answer, err)
	}
	if strings.Join(pieces, "|") != "Halo|, |Rina" || !asked.Stream {
		t.Fatalf("pieces = %v, stream asked = %v", pieces, asked.Stream)
	}
}

func TestAProviderThatIgnoresStreamingIsReadAsOnePiece(t *testing.T) {
	server := sseProvider(t, func(w http.ResponseWriter, _ chatRequest) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(textReply("sekaligus")))
	})
	var pieces []string
	answer, err := streamAIProvider(context.Background(), "key", server.URL, baseRequest(), func(text string) { pieces = append(pieces, text) })
	if err != nil || answer != "sekaligus" || len(pieces) != 1 {
		t.Fatalf("answer = %q, pieces = %v, err = %v", answer, pieces, err)
	}
}

func TestStreamFailuresAreReported(t *testing.T) {
	cases := map[string]func(w http.ResponseWriter, request chatRequest){
		"provider stream error": func(w http.ResponseWriter, _ chatRequest) {
			writeSSE(w, delta("sebagian"), `{"error":{"message":"upstream overloaded"}}`)
		},
		"terpotong": func(w http.ResponseWriter, _ chatRequest) {
			writeSSE(w, `{"choices":[{"delta":{},"finish_reason":"length"}]}`, "[DONE]")
		},
		"tanpa jawaban": func(w http.ResponseWriter, _ chatRequest) {
			writeSSE(w, "[DONE]")
		},
		"provider HTTP 503": func(w http.ResponseWriter, _ chatRequest) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"message":"busy"}}`))
		},
	}
	for want, handler := range cases {
		server := sseProvider(t, handler)
		if _, err := streamAIProvider(context.Background(), "key", server.URL, baseRequest(), func(string) {}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want an error with %q, got %v", want, err)
		}
	}
}

func TestARefusedTokenLimitIsDroppedBeforeStreaming(t *testing.T) {
	var limits []int
	server := sseProvider(t, func(w http.ResponseWriter, request chatRequest) {
		limits = append(limits, request.MaxTokens)
		if request.MaxTokens > 8192 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"max_tokens must be less than or equal to 8192"}}`))
			return
		}
		writeSSE(w, delta("ok"), "[DONE]")
	})
	request := baseRequest()
	request.MaxTokens = chatMaxTokens
	answer, err := streamAIProviderWithRetry(context.Background(), "key", server.URL, request, func(string) {})
	if err != nil || answer != "ok" || len(limits) != 2 || limits[1] != 0 {
		t.Fatalf("answer = %q, err = %v, limits = %v", answer, err, limits)
	}
}

func TestStoppingCancelsTheProviderCall(t *testing.T) {
	released := make(chan struct{})
	server := sseProvider(t, func(w http.ResponseWriter, _ chatRequest) {
		writeSSE(w, delta("mulai"))
		select {
		case <-released:
		case <-time.After(5 * time.Second):
		}
	})
	defer close(released)
	ctx, cancel := context.WithCancel(context.Background())
	started := time.Now()
	_, err := streamAIProvider(ctx, "key", server.URL, baseRequest(), func(string) { cancel() })
	if !errors.Is(err, context.Canceled) || time.Since(started) > 3*time.Second {
		t.Fatalf("a stop must end the call at once, err = %v after %s", err, time.Since(started))
	}
}

func TestAnswersStreamExceptForCombos(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context := func(query string) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/api/chat"+query, nil)
		return c
	}
	plain := baseRequest()
	withTools := baseRequest()
	withTools.Tools = []providerToolDefinition{{Type: "function"}}
	if !wantsChatStream(context("?stream=1"), false, plain) || !wantsChatStream(context("?stream=1"), false, withTools) {
		t.Error("an answer asked as a stream must stream, also when tools are offered")
	}
	if wantsChatStream(context(""), false, plain) || wantsChatStream(context("?stream=1"), true, plain) {
		t.Error("no stream unless asked, and never for a combo")
	}
}

func TestStreamEventsAreWrittenAndFlushed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/chat?stream=1", nil)
	stream := startChatStream(c)
	stream.send("delta", gin.H{"text": "Halo"})
	stream.send("done", gin.H{"ok": true, "message": "Halo"})
	if recorder.Header().Get("Content-Type") != "text/event-stream" || !recorder.Flushed {
		t.Fatalf("headers = %v flushed = %v", recorder.Header(), recorder.Flushed)
	}
	want := "event: delta\ndata: {\"text\":\"Halo\"}\n\nevent: done\ndata: {\"message\":\"Halo\",\"ok\":true}\n\n"
	if recorder.Body.String() != want {
		t.Fatalf("body = %q", recorder.Body.String())
	}
}

// A native tool call arrives in pieces; it comes out as the same marker the JSON path returns.
func TestAStreamedToolCallBecomesTheUsualMarker(t *testing.T) {
	name := services.ProviderToolName("workspace.read_file")
	piece := func(fields map[string]any) string {
		encoded, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{fields}}}}})
		return string(encoded)
	}
	server := sseProvider(t, func(w http.ResponseWriter, _ chatRequest) {
		writeSSE(w,
			delta("Saya baca dulu. "),
			piece(map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": name, "arguments": ""}}),
			piece(map[string]any{"index": 0, "function": map[string]any{"arguments": `{"path":`}}),
			piece(map[string]any{"index": 0, "function": map[string]any{"arguments": `"index.html"}`}}),
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`, "[DONE]")
	})
	jsonAnswer, jsonErr := normalizeNativeToolCall(func() providerToolCall {
		var call providerToolCall
		call.Type, call.Function.Name = "function", name
		call.Function.Arguments, _ = json.Marshal(`{"path":"index.html"}`)
		return call
	}())
	answer, err := streamAIProvider(context.Background(), "key", server.URL, baseRequest(), func(string) {})
	if err != nil || jsonErr != nil || answer != jsonAnswer || !strings.Contains(answer, "index.html") {
		t.Fatalf("answer = %q (%v), want %q (%v)", answer, err, jsonAnswer, jsonErr)
	}

	broken := sseProvider(t, func(w http.ResponseWriter, _ chatRequest) {
		writeSSE(w, piece(map[string]any{"index": 0, "type": "function", "function": map[string]any{"name": name, "arguments": `{"path":`}}),
			`{"choices":[{"delta":{},"finish_reason":"length"}]}`, "[DONE]")
	})
	_, err = streamAIProvider(context.Background(), "key", broken.URL, baseRequest(), func(string) {})
	var invalid *invalidToolCallError
	if !errors.As(err, &invalid) || !invalid.Truncated {
		t.Fatalf("a cut-off tool call must be reported as one, got %v", err)
	}
}
