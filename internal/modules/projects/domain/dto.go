package domain

import "time"

type CreateProjectRequest struct {
	Name        string `json:"name" binding:"required,min=3,max=30"`
	Description string `json:"description"`

	CpuLimit    string `json:"cpu_limit"`
	MemoryLimit string `json:"memory_limit"`
}

type ProjectResponse struct {
	ProjectID  string `json:"project_id,omitempty"`
	WorkflowID string `json:"workflow_id"`
	Status     string `json:"status"`
	Message    string `json:"message"`
}

type ProjectStatusResponse struct {
	ProjectID string           `json:"project_id"`
	Status    string           `json:"status"`
	Phase     string           `json:"phase"`
	K8sInfo   *NamespaceStatus `json:"k8s_info,omitempty"`
	CreatedAt time.Time        `json:"created_at"`
}

type NamespaceStatus struct {
	Name   string `json:"name"`
	Exists bool   `json:"exists"`
	Phase  string `json:"phase"`
}
