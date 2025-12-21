package domain

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

func RoleHasPermission(roleName, permSlug string) bool {

	switch roleName {
	case RoleOwner:
		return true

	case RoleDeveloper:
		allowed := []string{
			PermProjectView,
			PermProjectEdit,
			PermWorkloadCreate,
			PermLogsView,
		}
		for _, p := range allowed {
			if p == permSlug {
				return true
			}
		}

	case RoleViewer:
		allowed := []string{
			PermProjectView,
			PermLogsView,
		}
		for _, p := range allowed {
			if p == permSlug {
				return true
			}
		}
	}

	return false
}
