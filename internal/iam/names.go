package iam

import (
	"errors"
	"strings"

	"github.com/google/uuid"
)

// ResourceName : nom canonique et stable d'une ressource, utilisé dans les
// policies, l'API et l'audit. Les segments variables sont des UUID immuables.
//
//	platform
//	organizations/{org}
//	projects/{project}
//	projects/{project}/sites/{site}/domains/{domain}
type ResourceName string

// Platform : racine, porte les rôles du staff betorun.
const Platform ResourceName = "platform"

var ErrInvalidResourceName = errors.New("invalid resource name")

func (n ResourceName) String() string { return string(n) }

func OrganizationName(id uuid.UUID) ResourceName {
	return ResourceName(OrganizationsPrefix + id.String())
}

func ProjectName(id uuid.UUID) ResourceName {
	return ResourceName(ProjectsPrefix + id.String())
}

// Child : nom d'une sous-ressource, ex. ProjectName(p).Child("sites", s).
func (n ResourceName) Child(collection string, id uuid.UUID) ResourceName {
	return ResourceName(string(n) + "/" + collection + "/" + id.String())
}

// Validate : "platform" ou suite de paires collection/UUID, en commençant par
// organizations ou projects.
func (n ResourceName) Validate() error {
	if n == Platform {
		return nil
	}
	parts := strings.Split(string(n), "/")
	if len(parts) < 2 || len(parts)%2 != 0 {
		return ErrInvalidResourceName
	}
	if parts[0] != CollectionOrganizations && parts[0] != CollectionProjects {
		return ErrInvalidResourceName
	}
	for i := 0; i < len(parts); i += 2 {
		if !isCamel(parts[i]) {
			return ErrInvalidResourceName
		}
		if _, err := uuid.Parse(parts[i+1]); err != nil {
			return ErrInvalidResourceName
		}
	}
	return nil
}

// isCamel : collections en lowerCamelCase (serviceAccounts, apiKeys).
func isCamel(s string) bool {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

// Organization : UUID si le nom est "organizations/{id}".
func (n ResourceName) Organization() (uuid.UUID, bool) {
	return n.root(CollectionOrganizations)
}

// Project : UUID du projet si le nom est sous "projects/{id}".
func (n ResourceName) Project() (uuid.UUID, bool) {
	return n.root(CollectionProjects)
}

func (n ResourceName) root(collection string) (uuid.UUID, bool) {
	parts := strings.SplitN(string(n), "/", 3)
	if len(parts) < 2 || parts[0] != collection {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(parts[1])
	return id, err == nil
}

// localAncestry : la ressource puis ses parents dans le même nom, jusqu'à la
// racine du nom (projet ou organisation). Les parents hors du nom
// (organisation d'un projet, platform) sont ajoutés par l'Engine.
func (n ResourceName) localAncestry() []ResourceName {
	if n == Platform {
		return []ResourceName{Platform}
	}
	parts := strings.Split(string(n), "/")
	out := make([]ResourceName, 0, len(parts)/2)
	for end := len(parts); end >= 2; end -= 2 {
		out = append(out, ResourceName(strings.Join(parts[:end], "/")))
	}
	return out
}
