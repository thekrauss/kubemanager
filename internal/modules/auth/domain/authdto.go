package domain

type AssignRoleRequest struct {
	UserID    string `json:"user_id" validate:"required,uuid"`
	ProjectID string `json:"project_id" validate:"required,uuid"`
	RoleName  string `json:"role_name" validate:"required"`
}

type CheckPermissionRequest struct {
	UserID     string `json:"user_id" validate:"required,uuid"`
	ProjectID  string `json:"project_id" validate:"required,uuid"`
	Permission string `json:"permission" validate:"required"`
}

type PermissionsResponse struct {
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
}

type RoleDTO struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}

type ProjectMemberDTO struct {
	UserID    string `json:"user_id"`
	Email     string `json:"email"`
	FullName  string `json:"full_name"`
	AvatarURL string `json:"avatar_url"`
	RoleName  string `json:"role_name"`
	Status    string `json:"status"`
	JoinedAt  string `json:"joined_at"`
}
