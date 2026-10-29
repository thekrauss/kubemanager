package domain

func RoleHasPermission(role RoleType, perm PermissionType) bool {

	switch role {
	case RoleTypes.Owner:
		return true

	case RoleTypes.Developer:
		allowed := []PermissionType{
			PermissionTypes.ProjectView,
			PermissionTypes.ProjectEdit,
			PermissionTypes.WorkloadCreate,
			PermissionTypes.LogsView,
		}
		return containsPermission(allowed, perm)

	case RoleTypes.Viewer:
		allowed := []PermissionType{
			PermissionTypes.ProjectView,
			PermissionTypes.LogsView,
		}
		return containsPermission(allowed, perm)

	case RoleTypes.User:
		return false
	}

	return false
}

func RolePermissions(role RoleType) []PermissionType {
	switch role {
	case RoleTypes.Owner:
		return PermissionTypes.All()
	case RoleTypes.Developer:
		return []PermissionType{
			PermissionTypes.ProjectView,
			PermissionTypes.ProjectEdit,
			PermissionTypes.WorkloadCreate,
			PermissionTypes.LogsView,
		}
	case RoleTypes.Viewer:
		return []PermissionType{
			PermissionTypes.ProjectView,
			PermissionTypes.LogsView,
		}
	default:
		return nil
	}
}

func containsPermission(slice []PermissionType, item PermissionType) bool {
	for _, p := range slice {
		if p == item {
			return true
		}
	}
	return false
}

// LegacyRoleToIAM : rôle projet legacy (OWNER, DEVELOPER, VIEWER) → rôle IAM
// équivalent (docs/architecture/iam.md §6.3). Un nom déjà IAM ("roles/…") est
// renvoyé tel quel.
func LegacyRoleToIAM(name string) string {
	switch name {
	case RoleTypes.Owner.String():
		return "roles/owner"
	case RoleTypes.Developer.String():
		return "roles/developer"
	case RoleTypes.Viewer.String():
		return "roles/viewer"
	default:
		return name
	}
}
