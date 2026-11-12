package handlers

import (
	"errors"
	"fmt"
	"strings"
)

// applyEdit replaces the single occurrence of oldText in content with newText. The model usually
// sends "\n" line breaks; a file saved with "\r\n" is matched by converting the edit to "\r\n".
func applyEdit(content, oldText, newText string) (string, error) {
	count := strings.Count(content, oldText)
	if count == 0 && strings.Contains(content, "\r\n") && !strings.Contains(oldText, "\r\n") {
		crlf := func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }
		if strings.Contains(content, crlf(oldText)) {
			oldText, newText = crlf(oldText), crlf(newText)
			count = strings.Count(content, oldText)
		}
	}
	switch {
	case count == 0:
		return "", errors.New("old_text was not found in the file. Read the file again with workspace.read_file and copy the exact text, including spaces and line breaks")
	case count > 1:
		return "", fmt.Errorf("old_text appears %d times in the file. Include more of the surrounding lines so it matches exactly one place", count)
	}
	return strings.Replace(content, oldText, newText, 1), nil
}

// plannedEdit is the file's content after the edit in args, read from the workspace now.
func plannedEdit(handle *workspaceHandle, args toolArgs) (string, error) {
	path, _ := args.str("path")
	oldText, _ := args.str("old_text")
	newText, _ := args.str("new_text")
	content, _, err := handle.readFile(path)
	if err != nil {
		_, message := fileFailure(err)
		return "", errors.New(message)
	}
	return applyEdit(content, oldText, newText)
}
