package services

import "strings"

// Cloud Workspace tools (Phase 6). They live in the same ToolRegistry as every
// other tool; this file only adds their definitions and the rule for which agent
// may use which of them.

const (
	ToolWorkspaceListFiles    = "workspace.list_files"
	ToolWorkspaceReadFile     = "workspace.read_file"
	ToolWorkspaceWriteFile    = "workspace.write_file"
	ToolWorkspaceGetChanges   = "workspace.get_changes"
	ToolWorkspaceRunTest      = "workspace.run_test"
	ToolWorkspaceBuild        = "workspace.build"
	ToolWorkspaceGetExecution = "workspace.get_execution"
)

var cloudToolNames = []string{ToolWorkspaceListFiles, ToolWorkspaceReadFile, ToolWorkspaceWriteFile, ToolWorkspaceGetChanges, ToolWorkspaceRunTest, ToolWorkspaceBuild, ToolWorkspaceGetExecution}

func init() {
	pathProperty := map[string]any{"type": "string", "description": "Path relative to the workspace root, for example src/main.go. Absolute paths and .. are refused."}
	targetProperty := func(values ...string) map[string]any {
		enum := make([]any, len(values))
		for i, value := range values {
			enum[i] = value
		}
		return map[string]any{"type": "string", "enum": enum, "description": "Which toolchain to use. Defaults to the workspace's default runtime."}
	}
	define := func(name, description, permission string, timeout int, properties map[string]any, required ...string) {
		schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
		if len(required) > 0 {
			schema["required"] = required
		}
		ToolRegistry[name] = ToolDefinition{Name: name, Description: description, RequiredPermission: permission, TimeoutSeconds: timeout, InputSchema: schema}
	}
	define(ToolWorkspaceListFiles, "List files and folders in the cloud workspace. Optional path selects a folder.", "workspace.read", 10, map[string]any{"path": map[string]any{"type": "string", "description": "Folder relative to the workspace root; leave empty for the root."}})
	define(ToolWorkspaceReadFile, "Read a text file from the cloud workspace.", "workspace.read", 10, map[string]any{"path": pathProperty}, "path")
	define(ToolWorkspaceWriteFile, "Create or replace a text file in the cloud workspace. The user must approve every write.", "workspace.write", 15, map[string]any{"path": pathProperty, "content": map[string]any{"type": "string", "description": "The complete new content of the file."}}, "path", "content")
	define(ToolWorkspaceGetChanges, "List the files added, modified or deleted in the cloud workspace since it was created or imported.", "workspace.read", 10, map[string]any{})
	define(ToolWorkspaceRunTest, "Run the project's tests in the cloud sandbox. The user must approve each run. Returns a job id; use workspace.get_execution for the result.", "workspace.execute", 15, map[string]any{"target": targetProperty("go", "node", "python")})
	define(ToolWorkspaceBuild, "Build the project in the cloud sandbox. The user must approve each run. Returns a job id; use workspace.get_execution for the result.", "workspace.execute", 15, map[string]any{"target": targetProperty("go", "node")})
	define(ToolWorkspaceGetExecution, "Read the status and output of a test or build job. wait_seconds (at most 20) waits for it to finish.", "workspace.read", 30, map[string]any{"job_id": map[string]any{"type": "integer", "description": "The job id returned by workspace.run_test or workspace.build."}, "wait_seconds": map[string]any{"type": "integer", "description": "Seconds to wait for the job to finish, 0 to 20."}}, "job_id")
}

// IsCloudTool reports whether name is one of the Cloud Workspace tools.
func IsCloudTool(name string) bool {
	for _, known := range cloudToolNames {
		if known == name {
			return true
		}
	}
	return false
}

// CloudToolNeedsApproval is true for tools that change files or run commands.
func CloudToolNeedsApproval(name string) bool {
	switch name {
	case ToolWorkspaceWriteFile, ToolWorkspaceRunTest, ToolWorkspaceBuild:
		return true
	}
	return false
}

// CloudToolsForAgent is the set of cloud tools an agent may use, decided by the
// agent's role (its slug) and nothing the model says. Coding changes files and
// builds; Testing and Debugging read and run; Review and Planning only read.
// An agent whose role is not recognised gets read-only access.
func CloudToolsForAgent(slug string) []string {
	slug = strings.ToLower(strings.TrimSpace(slug))
	read := []string{ToolWorkspaceListFiles, ToolWorkspaceReadFile, ToolWorkspaceGetChanges}
	has := func(parts ...string) bool {
		for _, part := range parts {
			if strings.Contains(slug, part) {
				return true
			}
		}
		return false
	}
	switch {
	case has("coding", "code"):
		if has("review") {
			return read // "code review" is a reviewer, not an author
		}
		return append(read, ToolWorkspaceWriteFile, ToolWorkspaceRunTest, ToolWorkspaceBuild, ToolWorkspaceGetExecution)
	case has("testing", "test"):
		return append(read, ToolWorkspaceRunTest, ToolWorkspaceBuild, ToolWorkspaceGetExecution)
	case has("debug"):
		return append(read, ToolWorkspaceRunTest, ToolWorkspaceGetExecution)
	default: // review, planning and anything else
		return read
	}
}

// CloudToolAllowedForAgent checks one tool against CloudToolsForAgent.
func CloudToolAllowedForAgent(tool, slug string) bool {
	for _, allowed := range CloudToolsForAgent(slug) {
		if allowed == tool {
			return true
		}
	}
	return false
}
