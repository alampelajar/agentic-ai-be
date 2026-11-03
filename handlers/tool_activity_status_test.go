package handlers

import (
	"bytes"
	"net/http"
	"testing"
)

// These requests are refused before anything is read from or written to the
// database, so they need no test database.
func TestCreateToolActivityRejectsUnknownStatuses(t *testing.T) {
	cases := map[string]string{
		"unknown approval":   `{"tool_name":"desktop.type","approval_status":"granted","execution_status":"completed"}`,
		"unknown execution":  `{"tool_name":"desktop.type","approval_status":"approved_once","execution_status":"success"}`,
		"empty approval":     `{"tool_name":"desktop.type","approval_status":"","execution_status":"completed"}`,
		"missing execution":  `{"tool_name":"desktop.type","approval_status":"denied"}`,
		"free text":          `{"tool_name":"desktop.type","approval_status":"approved by <b>admin</b>","execution_status":"completed"}`,
		"case differs":       `{"tool_name":"desktop.type","approval_status":"Denied","execution_status":"cancelled"}`,
		"overlong tool name": `{"tool_name":"` + string(bytes.Repeat([]byte("a"), 101)) + `","approval_status":"denied","execution_status":"cancelled"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			response := serveActivity(httptestRequest(http.MethodPost, "/api/tool-activities", bytes.NewBufferString(body), 1))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", response.Code, response.Body.String())
			}
		})
	}
}

// Every outcome the frontend reports in AI Chat has to stay accepted.
func TestToolActivityStatusesCoverWhatTheFrontendSends(t *testing.T) {
	for _, status := range []string{"not_required", "approved_once", "denied", "expired"} {
		if !activityApprovalStatuses[status] {
			t.Errorf("approval status %q is no longer accepted", status)
		}
	}
	for _, status := range []string{"completed", "failed", "cancelled"} {
		if !activityExecutionStatuses[status] {
			t.Errorf("execution status %q is no longer accepted", status)
		}
	}
}
