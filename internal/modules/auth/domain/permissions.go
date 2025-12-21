package domain

// Permissions
const (
	PermProjectView   = "project:view"
	PermProjectEdit   = "project:edit"
	PermProjectDelete = "project:delete"

	PermWorkloadCreate = "k8s:workload:create"
	PermWorkloadDelete = "k8s:workload:delete"
	PermLogsView       = "k8s:logs:view"
	PermShellExec      = "k8s:shell:exec"
)

const (
	RoleViewer    = "Viewer"
	RoleDeveloper = "Developer"
	RoleOwner     = "Owner"
)
