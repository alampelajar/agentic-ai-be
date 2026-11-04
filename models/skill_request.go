package models

type SkillManifest struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	Version              string   `json:"version"`
	Description          string   `json:"description"`
	Author               string   `json:"author"`
	Platforms            []string `json:"platforms"`
	Instructions         string   `json:"instructions"`
	Tools                []string `json:"tools"`
	RequestedPermissions []string `json:"requested_permissions"`
}

type SkillCreateRequest struct {
	Manifest     SkillManifest `json:"manifest" binding:"required"`
	Instructions string        `json:"instructions"`
}
