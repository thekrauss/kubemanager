package iamapi

import (
	"github.com/gin-gonic/gin"
	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"

	"github.com/thekrauss/kubemanager/internal/iam"
)

// ── Ascendance d'une ressource ───────────────────────────────────────────

type AncestryRequest struct {
	Resource string `query:"resource" binding:"required" description:"projects/<uuid> ou organizations/<uuid>"`
}

type AncestryResponse struct {
	// Ancestors : la ressource puis ses parents, jusqu'à "platform".
	Ancestors []iam.ResourceName `json:"ancestors"`
}

// AncestryPermission : lire l'ascendance = voir la ressource.
func AncestryPermission(r iam.ResourceName) (iam.Permission, error) {
	if _, ok := r.Project(); ok {
		return iam.ProjectsGet, nil
	}
	if _, ok := r.Organization(); ok {
		return iam.OrganizationsGet, nil
	}
	return "", iam.ErrPolicyNotSupported
}

func (h *Controller) Ancestry(c *gin.Context, in *AncestryRequest) (*AncestryResponse, error) {
	out, err := h.Engine.Ancestry(c.Request.Context(), iam.ResourceName(in.Resource))
	if err != nil {
		return nil, err
	}
	return &AncestryResponse{Ancestors: out}, nil
}

// ── Rôles personnalisés ──────────────────────────────────────────────────

type ListCustomRolesRequest struct {
	Parent string `query:"parent" binding:"required" description:"organizations/<uuid> ou projects/<uuid>"`
}

type CreateCustomRoleRequest struct {
	Parent string `json:"parent" binding:"required" description:"organizations/<uuid> ou projects/<uuid>"`
	RoleID string `json:"role_id" binding:"required" description:"3-64 caractères : lettres, chiffres, '_' ou '.' (ex: ciDeployer)"`
	iam.CustomRoleInput
}

type CustomRoleNameRequest struct {
	Name string `query:"name" binding:"required" description:"organizations|projects/<uuid>/roles/<id>"`
}

type UpdateCustomRoleRequest struct {
	Name string `json:"name" binding:"required"`
	Etag string `json:"etag" binding:"required" description:"Etag lu (écritures concurrentes refusées)"`
	iam.CustomRoleInput
}

func (h *Controller) ListCustomRoles(c *gin.Context, in *ListCustomRolesRequest) ([]iam.CustomRole, error) {
	return h.CustomRoles.List(c.Request.Context(), iam.ResourceName(in.Parent))
}

func (h *Controller) CreateCustomRole(c *gin.Context, in *CreateCustomRoleRequest) (*iam.CustomRole, error) {
	p, err := caller(c)
	if err != nil {
		return nil, err
	}
	return h.CustomRoles.Create(c.Request.Context(), p, iam.ResourceName(in.Parent), in.RoleID, in.CustomRoleInput)
}

func (h *Controller) GetCustomRole(c *gin.Context, in *CustomRoleNameRequest) (*iam.CustomRole, error) {
	return h.CustomRoles.Get(c.Request.Context(), in.Name)
}

func (h *Controller) UpdateCustomRole(c *gin.Context, in *UpdateCustomRoleRequest) (*iam.CustomRole, error) {
	p, err := caller(c)
	if err != nil {
		return nil, err
	}
	return h.CustomRoles.Update(c.Request.Context(), p, in.Name, in.Etag, in.CustomRoleInput)
}

func (h *Controller) DeleteCustomRole(c *gin.Context, in *CustomRoleNameRequest) error {
	p, err := caller(c)
	if err != nil {
		return err
	}
	return h.CustomRoles.Delete(c.Request.Context(), p, in.Name)
}

// ── Groupes (/organizations/:id/groups) ──────────────────────────────────

type OrgPath struct {
	OrgID string `path:"id" binding:"required"`
}

type GroupPath struct {
	OrgID   string `path:"id" binding:"required"`
	GroupID string `path:"groupID" binding:"required"`
}

type CreateGroupRequest struct {
	OrgID       string `path:"id" binding:"required"`
	GroupID     string `json:"group_id" binding:"required" description:"3-63 caractères : minuscules, chiffres, tirets (ex: equipe-paiement)"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}

type UpdateGroupRequest struct {
	OrgID       string `path:"id" binding:"required"`
	GroupID     string `path:"groupID" binding:"required"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}

type AddGroupMemberRequest struct {
	OrgID   string `path:"id" binding:"required"`
	GroupID string `path:"groupID" binding:"required"`
	Member  string `json:"member" binding:"required" description:"user:<email|uuid> ou serviceAccount:<uuid>"`
}

type GroupMemberPath struct {
	OrgID   string `path:"id" binding:"required"`
	GroupID string `path:"groupID" binding:"required"`
	Member  string `query:"member" binding:"required" description:"Principal du membre (user:<uuid>, serviceAccount:<uuid>)"`
}

func (h *Controller) ListGroups(c *gin.Context, in *OrgPath) ([]iam.Group, error) {
	id, err := ids(in.OrgID)
	if err != nil {
		return nil, err
	}
	return h.Groups.List(c.Request.Context(), id[0])
}

func (h *Controller) CreateGroup(c *gin.Context, in *CreateGroupRequest) (*iam.Group, error) {
	p, err := caller(c)
	if err != nil {
		return nil, err
	}
	id, err := ids(in.OrgID)
	if err != nil {
		return nil, err
	}
	return h.Groups.Create(c.Request.Context(), p, id[0], in.GroupID, in.DisplayName, in.Description)
}

func (h *Controller) GetGroup(c *gin.Context, in *GroupPath) (*iam.Group, error) {
	id, err := ids(in.OrgID, in.GroupID)
	if err != nil {
		return nil, err
	}
	return h.Groups.Get(c.Request.Context(), id[0], id[1])
}

func (h *Controller) UpdateGroup(c *gin.Context, in *UpdateGroupRequest) (*iam.Group, error) {
	id, err := ids(in.OrgID, in.GroupID)
	if err != nil {
		return nil, err
	}
	return h.Groups.Update(c.Request.Context(), id[0], id[1], in.DisplayName, in.Description)
}

func (h *Controller) DeleteGroup(c *gin.Context, in *GroupPath) error {
	p, err := caller(c)
	if err != nil {
		return err
	}
	id, err := ids(in.OrgID, in.GroupID)
	if err != nil {
		return err
	}
	return h.Groups.Delete(c.Request.Context(), p, id[0], id[1])
}

func (h *Controller) ListGroupMembers(c *gin.Context, in *GroupPath) ([]iam.GroupMember, error) {
	id, err := ids(in.OrgID, in.GroupID)
	if err != nil {
		return nil, err
	}
	return h.Groups.ListMembers(c.Request.Context(), id[0], id[1])
}

func (h *Controller) AddGroupMember(c *gin.Context, in *AddGroupMemberRequest) (*iam.GroupMember, error) {
	p, err := caller(c)
	if err != nil {
		return nil, err
	}
	id, err := ids(in.OrgID, in.GroupID)
	if err != nil {
		return nil, err
	}
	return h.Groups.AddMember(c.Request.Context(), p, id[0], id[1], iam.Principal(in.Member))
}

func (h *Controller) RemoveGroupMember(c *gin.Context, in *GroupMemberPath) error {
	id, err := ids(in.OrgID, in.GroupID)
	if err != nil {
		return err
	}
	if in.Member == "" {
		return betoerrors.New(betoerrors.CodeInvalidInput, "member is required")
	}
	return h.Groups.RemoveMember(c.Request.Context(), id[0], id[1], iam.Principal(in.Member))
}
