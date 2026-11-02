package domain

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVariableChange_Validate(t *testing.T) {
	assert.NoError(t, VariableChange{Env: map[string]string{"LOG_LEVEL": "x", "_A1": ""}}.Validate())
	for _, bad := range []string{"", "1A", "A-B", "A B", "A=B", "A.B", strings.Repeat("A", 129)} {
		assert.ErrorIs(t, VariableChange{Env: map[string]string{bad: "x"}}.Validate(), ErrVariableName, bad)
		assert.ErrorIs(t, VariableChange{Secrets: map[string]string{bad: "x"}}.Validate(), ErrVariableName, bad)
		assert.ErrorIs(t, VariableChange{Unset: []string{bad}}.Validate(), ErrVariableName, bad)
	}
	assert.ErrorIs(t, VariableChange{Secrets: map[string]string{"A": strings.Repeat("x", MaxVariableBytes+1)}}.Validate(), ErrVariableTooBig)
	// Un message d'erreur ne cite jamais une valeur.
	err := VariableChange{Secrets: map[string]string{"A": "s3cret" + strings.Repeat("x", MaxVariableBytes)}}.Validate()
	assert.NotContains(t, err.Error(), "s3cret")
}

func TestVariableChange_Conflicts(t *testing.T) {
	v := map[string]string{"A": "1"}
	assert.ErrorIs(t, VariableChange{Env: v, Unset: []string{"A"}}.Validate(), ErrVariableConflict)
	assert.ErrorIs(t, VariableChange{Secrets: v, Unset: []string{"A"}}.Validate(), ErrVariableConflict)
	assert.ErrorIs(t, VariableChange{Env: v, Secrets: v}.Validate(), ErrVariableConflict)
	assert.NoError(t, VariableChange{Env: v, Secrets: map[string]string{"B": "2"}, Unset: []string{"C"}}.Validate())
}

func TestCheckTotals(t *testing.T) {
	assert.NoError(t, CheckTotals(map[string]string{"A": "b"}))
	many := map[string]string{}
	for i := 0; i <= MaxVariables; i++ {
		many[strings.Repeat("A", i+1)] = ""
	}
	assert.ErrorIs(t, CheckTotals(many), ErrTooManyVariable)
	big := map[string]string{}
	for _, k := range []string{"A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L", "M"} {
		big[k] = strings.Repeat("x", MaxVariableBytes)
	}
	assert.ErrorIs(t, CheckTotals(big), ErrTooManyVariable)
}
