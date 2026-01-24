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

func containsPermission(slice []PermissionType, item PermissionType) bool {
	for _, p := range slice {
		if p == item {
			return true
		}
	}
	return false
}
