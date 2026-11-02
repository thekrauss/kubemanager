package domain

import (
	"database/sql/driver"
	"fmt"
)

// Sérialisation SQL des enums (enums.json : DBSerializer: true). Écrites à la
// main : le générateur (tools/enumgen) n'est plus dans le dépôt et ne les
// avait jamais produites, ce qui empêchait de lire un User depuis la base.
//
// Scan n'applique pas la validation stricte : la base fait foi, et une
// valeur historique inconnue ne doit pas rendre un compte illisible.

func (f RoleType) Value() (driver.Value, error) { return f.value, nil }

func (f *RoleType) Scan(src any) error {
	s, err := scanString(src)
	if err != nil {
		return fmt.Errorf("RoleType: %w", err)
	}
	f.value = s
	return nil
}

func (f PermissionType) Value() (driver.Value, error) { return f.value, nil }

func (f *PermissionType) Scan(src any) error {
	s, err := scanString(src)
	if err != nil {
		return fmt.Errorf("PermissionType: %w", err)
	}
	f.value = s
	return nil
}

func scanString(src any) (string, error) {
	switch v := src.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	default:
		return "", fmt.Errorf("unsupported type %T", src)
	}
}
