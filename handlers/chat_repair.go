package handlers

import (
	"errors"
	"fmt"
	"log"
	"strings"

	"agentic-ai-backend/services"
)

// maxToolCallRepairs is how many times the model is asked again after it sends a
// tool call that cannot be used. Each retry is another provider request, so the
// number is small.
const maxToolCallRepairs = 2

// invalidToolCallError is a tool call the model sent that cannot be run: its
// arguments are empty, are not valid JSON (a long file is often cut off by the
// output limit mid-string), or do not match the tool's schema. It is the model's
// mistake, not a failure of the provider, so it is told what was wrong and asked
// again instead of ending the whole turn with an error.
type invalidToolCallError struct {
	Tool      string
	Arguments bool  // the arguments were empty or not JSON, as opposed to failing the schema
	Truncated bool  // the provider said the answer was cut off (finish_reason "length")
	Err       error // the schema problem, when Arguments is false
}

func (e *invalidToolCallError) Error() string {
	if e.Arguments {
		return fmt.Sprintf("native tool call %q memiliki arguments invalid", e.Tool)
	}
	return fmt.Sprintf("native tool call ditolak: %v", e.Err)
}

func (e *invalidToolCallError) Unwrap() error { return e.Err }

// toolRepairMessage tells the model what went wrong. It names the tool only when it
// is one we know, and quotes nothing the model sent except a clipped schema error
// with control characters removed, so a crafted call cannot steer the retry.
func toolRepairMessage(e *invalidToolCallError) string {
	tool := "tool"
	if _, known := services.ToolRegistry[e.Tool]; known {
		tool = e.Tool
	}
	var why string
	switch {
	case e.Truncated:
		why = "jawabanmu terpotong oleh batas panjang output sehingga arguments tidak lengkap"
	case e.Arguments:
		why = "arguments kosong atau bukan JSON objek yang valid"
	default:
		why = "arguments ditolak: " + clipForModel(e.Err.Error(), 160)
	}
	return fmt.Sprintf("[SISTEM] Panggilan %s tidak dijalankan karena %s. Tidak ada perubahan yang dibuat. Panggil tool itu lagi dengan arguments JSON yang lengkap. Jika isi file panjang, perpendek isinya atau tulis file yang lebih kecil terlebih dahulu.", tool, why)
}

func clipForModel(text string, limit int) string {
	text = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, text)
	if runes := []rune(text); len(runes) > limit {
		return string(runes[:limit]) + "..."
	}
	return text
}

// callAIProviderWithRepair is callAIProvider for a turn that may call tools. When
// the model answers with a tool call that cannot be used, it is told why and asked
// again, up to maxToolCallRepairs times. Any other error, including provider and
// network errors, is returned at once and never retried.
func callAIProviderWithRepair(apiKey, baseURL string, request chatRequest) (string, error) {
	messages := append([]chatMessage(nil), request.Messages...)
	for attempt := 0; ; attempt++ {
		request.Messages = messages
		answer, err := callAIProvider(apiKey, baseURL, request)
		var invalid *invalidToolCallError
		if err == nil || !errors.As(err, &invalid) || attempt >= maxToolCallRepairs {
			return answer, err
		}
		log.Printf("[CHAT] tool call tidak valid dari model (%v); meminta ulang (%d/%d)", err, attempt+1, maxToolCallRepairs)
		messages = append(messages, chatMessage{Role: "user", Content: toolRepairMessage(invalid)})
	}
}
