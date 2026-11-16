package main

import (
	"github.com/gin-gonic/gin"

	"agentic-ai-backend/handlers"
)

// backgroundFeaturePaths are the features that left the person's app when it became a
// website builder only (WEBSITE-BUILDER.md, section 20). They are served to admins only,
// under /api/admin; the person's app no longer has screens for them. A test checks that
// none of them is registered on the signed-in user's group again.
var backgroundFeaturePaths = []string{
	"/agent/plan", "/agents", "/agent-catalog", "/skills", "/memories", "/memory-settings",
	"/workspaces", "/executions", "/workspace-tools", "/delegations", "/tool-activities",
	"/chat", "/generator", "/ai/providers", "/ai/models", "/ai/combos",
}

// registerBackgroundFeatures puts those features on the admin group. The handlers are
// unchanged and still scope their data to the signed-in admin. The person's own models
// and providers (/ai/models, /ai/providers) are not registered at all: admins manage
// the system models through the admin routes in main.go.
func registerBackgroundFeatures(admin gin.IRoutes, importLimit, executionLimit, chatLimit gin.HandlerFunc) {
	// Agents and their roles. The full list with models, which the admin app shows.
	admin.GET("/agent-catalog", handlers.GetAgents)
	admin.GET("/agents/:id", handlers.GetAgent)
	admin.POST("/agent/plan", handlers.AgentPlan)

	// Skills: guidance the agents use.
	admin.GET("/skills", handlers.GetSkills)
	admin.POST("/skills", handlers.CreateSkill)
	admin.GET("/skills/:id", handlers.GetSkill)
	admin.PUT("/skills/:id", handlers.UpdateSkill)
	admin.DELETE("/skills/:id", handlers.DeleteSkill)
	admin.POST("/skills/import/zip", importLimit, handlers.ImportSkillZIP)
	admin.POST("/skills/import/github", importLimit, handlers.ImportSkillGitHub)
	admin.POST("/skills/:id/validate", handlers.ValidateSkill)
	admin.GET("/agents/:id/skills", handlers.GetAgentSkills)
	admin.POST("/agents/:id/skills", handlers.AssignSkillToAgent)
	admin.PATCH("/agents/:id/skills/:skill_id", handlers.UpdateAgentSkill)
	admin.DELETE("/agents/:id/skills/:skill_id", handlers.RemoveSkillFromAgent)

	// Memory.
	admin.GET("/memories", handlers.GetMemories)
	admin.POST("/memories", handlers.CreateMemory)
	admin.PATCH("/memories/:id", handlers.UpdateMemory)
	admin.DELETE("/memories/:id", handlers.DeleteMemory)
	admin.GET("/memory-settings", handlers.GetMemorySettings)
	admin.PUT("/memory-settings", handlers.UpdateMemorySettings)

	// Workspaces, their code runs and the agent's workspace tools. Every website keeps
	// its own workspace in the background; this is for looking into one.
	admin.GET("/workspaces", handlers.GetWorkspaces)
	admin.POST("/workspaces", importLimit, handlers.CreateWorkspace)
	admin.GET("/workspaces/:id", handlers.GetWorkspace)
	admin.PATCH("/workspaces/:id", handlers.UpdateWorkspace)
	admin.DELETE("/workspaces/:id", handlers.DeleteWorkspace)
	admin.GET("/workspaces/:id/tree", handlers.GetWorkspaceTree)
	admin.GET("/workspaces/:id/file", handlers.GetWorkspaceFile)
	admin.PUT("/workspaces/:id/file", handlers.PutWorkspaceFile)
	admin.GET("/workspaces/:id/changes", handlers.GetWorkspaceChanges)
	admin.POST("/workspaces/:id/export", importLimit, handlers.ExportWorkspace)
	admin.POST("/workspaces/:id/github/publish", importLimit, handlers.PublishWorkspaceToGitHub)
	admin.GET("/workspaces/:id/executions", handlers.GetWorkspaceExecutions)
	admin.POST("/workspaces/:id/executions", executionLimit, handlers.CreateExecution)
	admin.GET("/executions/:id", handlers.GetExecution)
	admin.POST("/executions/:id/cancel", handlers.CancelExecution)
	admin.POST("/workspace-tools/run", executionLimit, handlers.RunWorkspaceTool)
	admin.POST("/workspace-tools/approvals/:id/approve", handlers.ApproveWorkspaceTool)
	admin.POST("/workspace-tools/approvals/:id/deny", handlers.DenyWorkspaceTool)

	// The agent chat and what it used.
	admin.POST("/chat", chatLimit, handlers.Chat)
	admin.POST("/generator/landing-page", chatLimit, handlers.GenerateLandingPage)
	admin.GET("/delegations", handlers.GetDelegations)
	admin.POST("/delegations", handlers.CreateDelegation)
	admin.DELETE("/delegations/:id", handlers.DeleteDelegation)
	admin.POST("/tool-activities", handlers.CreateToolActivity)
	admin.GET("/tool-activities", handlers.GetToolActivities)

	// How the models fare, and combos (the order of models).
	admin.GET("/ai/models/health", handlers.GetModelHealth)
	admin.GET("/ai/combos", handlers.GetCombos)
	admin.POST("/ai/combos", handlers.CreateCombo)
	admin.PUT("/ai/combos/:id", handlers.UpdateCombo)
	admin.DELETE("/ai/combos/:id", handlers.DeleteCombo)
}
