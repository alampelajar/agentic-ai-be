package handlers

import (
	"strings"
	"testing"
)

var readOnlyDesktopTools = []string{"list-files", "read-file", "desktop.status", "desktop.screenshot", "desktop.windows", "desktop.inspect"}

func marker(payload string) string { return "<AGENT_TOOL>" + payload + "</AGENT_TOOL>" }

func TestEnforceAgentToolMarkersKeepsGrantedTools(t *testing.T) {
	for _, payload := range []string{
		`{"action":"desktop.status","arguments":{}}`,
		`{"action":"desktop.inspect","arguments":{"window":"1"}}`,
		`{"tool":"desktop.windows"}`,
		`{"name":"desktop.screenshot"}`,
		`{"function":{"name":"desktop.status","arguments":"{}"}}`,
		`{"function":"desktop.status"}`,
		`{"arguments":{"action":"desktop.status"}}`,
		"```json\n{\"action\":\"desktop.status\"}\n```",
		`"{\"action\":\"desktop.status\"}"`,
		`  {"action":" desktop.status "}  `,
	} {
		message := "before " + marker(payload) + " after"
		if got := enforceAgentToolMarkers(message, readOnlyDesktopTools); got != message {
			t.Errorf("granted marker was changed\n payload: %s\n     got: %s", payload, got)
		}
	}
}

func TestEnforceAgentToolMarkersRemovesToolsTheAgentWasNotGranted(t *testing.T) {
	for _, payload := range []string{
		`{"action":"desktop.type","arguments":{"window":"1","text":"x"}}`,
		`{"action":"desktop.click","target":{"type":"coordinate"}}`,
		`{"action":"desktop.hotkey","arguments":{"keys":["alt","f4"]}}`,
		`{"tool":"desktop.focus_window"}`,
		`{"name":"desktop.scroll"}`,
		`{"function":{"name":"desktop.type"}}`,
		`{"function":"desktop.type"}`,
		`{"arguments":{"tool":"desktop.double_click"}}`,
		`{"input":"{\"name\":\"desktop.type\"}"}`,
		"```json\n{\"action\":\"desktop.type\"}\n```",
		`"{\"action\":\"desktop.type\"}"`,
		// The frontend lowercases names, so a case change must not slip through.
		`{"action":"DESKTOP.TYPE"}`,
		`{"action":"Desktop.Type"}`,
		// The action field wins over arguments.action, exactly like the frontend.
		`{"action":"desktop.type","arguments":{"action":"desktop.status"}}`,
		// Not verifiable: fail closed.
		`not json`,
		`{"action":5}`,
		`{}`,
		`[]`,
		`{"action":null,"tool":null}`,
		`{"action":"unknown.tool"}`,
	} {
		message := "before " + marker(payload) + " after"
		got := enforceAgentToolMarkers(message, readOnlyDesktopTools)
		if strings.Contains(got, "<AGENT_TOOL>") {
			t.Errorf("ungranted marker survived\n payload: %s\n     got: %s", payload, got)
		}
		if !strings.Contains(got, "tidak diizinkan") || !strings.HasPrefix(got, "before ") || !strings.HasSuffix(got, " after") {
			t.Errorf("surrounding text or notice is wrong\n payload: %s\n     got: %s", payload, got)
		}
	}
}

func TestEnforceAgentToolMarkersHandlesSeveralMarkers(t *testing.T) {
	message := marker(`{"action":"desktop.status"}`) + " then " + marker(`{"action":"desktop.type"}`) + " then " + marker(`{"action":"desktop.windows"}`)
	got := enforceAgentToolMarkers(message, readOnlyDesktopTools)
	if strings.Count(got, "<AGENT_TOOL>") != 2 || strings.Contains(got, "desktop.type\"}") {
		t.Fatalf("only the ungranted marker should be removed: %s", got)
	}
}

func TestEnforceAgentToolMarkersWithoutAnyDesktopSkill(t *testing.T) {
	// An agent with no skills (or no agent at all) is granted no desktop tools.
	for _, allowed := range [][]string{nil, {"list-files", "read-file"}} {
		got := enforceAgentToolMarkers(marker(`{"action":"desktop.status"}`), allowed)
		if strings.Contains(got, "<AGENT_TOOL>") {
			t.Errorf("marker survived for allowed=%v: %s", allowed, got)
		}
	}
}

func TestEnforceAgentToolMarkersLeavesOtherTextAlone(t *testing.T) {
	for _, message := range []string{
		"plain answer",
		"<LOCAL_AGENT_TOOL>{\"action\":\"list-files\"}</LOCAL_AGENT_TOOL>",
		"mentions <AGENT_TOOL> without closing it",
		"",
	} {
		if got := enforceAgentToolMarkers(message, readOnlyDesktopTools); got != message {
			t.Errorf("message changed: %q -> %q", message, got)
		}
	}
}

func TestEnforceAgentToolMarkersDoesNotEchoUnsafeNames(t *testing.T) {
	got := enforceAgentToolMarkers(marker(`{"action":"x\"; <script>alert(1)</script>"}`), readOnlyDesktopTools)
	if strings.Contains(got, "<script>") || strings.Contains(got, "<AGENT_TOOL>") {
		t.Fatalf("unsafe tool name was echoed: %s", got)
	}
}
