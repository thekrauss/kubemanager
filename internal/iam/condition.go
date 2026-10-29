package iam

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Condition : restreint un binding. Format compatible avec les conditions
// Google (expressions CEL). Seule l'expiration est supportée pour l'instant ;
// le format ne changera pas quand d'autres expressions le seront.
type Condition struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Expression  string `json:"expression"`
}

var ErrUnsupportedCondition = errors.New(
	"unsupported condition: only \"request.time < timestamp('<RFC3339>')\" is supported")

var expiryExpr = regexp.MustCompile(`^\s*request\.time\s*<\s*timestamp\(\s*'([^']+)'\s*\)\s*$`)

// conditionDeadline : instant d'expiration d'une condition supportée.
func conditionDeadline(c *Condition) (time.Time, error) {
	m := expiryExpr.FindStringSubmatch(c.Expression)
	if m == nil {
		return time.Time{}, ErrUnsupportedCondition
	}
	t, err := time.Parse(time.RFC3339, m[1])
	if err != nil {
		return time.Time{}, ErrUnsupportedCondition
	}
	return t, nil
}

// ValidateCondition : à appeler à l'écriture d'une policy, pour refuser
// clairement une condition qui ne pourrait jamais être évaluée.
func ValidateCondition(c *Condition) error {
	if c == nil {
		return nil
	}
	if c.Title == "" {
		return fmt.Errorf("%w: condition title is required", ErrUnsupportedCondition)
	}
	_, err := conditionDeadline(c)
	return err
}

// conditionHolds : une condition invalide n'accorde jamais rien (échec fermé).
func conditionHolds(c *Condition, now time.Time) bool {
	if c == nil {
		return true
	}
	deadline, err := conditionDeadline(c)
	if err != nil {
		return false
	}
	return now.Before(deadline)
}
