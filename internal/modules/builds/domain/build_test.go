package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeGitURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/acme/api":      "https://github.com/acme/api.git",
		"https://github.com/acme/api.git":  "https://github.com/acme/api.git",
		"https://github.com/acme/api/":     "https://github.com/acme/api.git",
		"https://gitlab.com/g/sub/api.git": "https://gitlab.com/g/sub/api.git",
	} {
		got, err := NormalizeGitURL(in)
		assert.NoError(t, err, in)
		assert.Equal(t, want, got)
	}
	for _, bad := range []string{
		"http://github.com/acme/api",
		"git@github.com:acme/api.git",
		"ssh://git@github.com/acme/api",
		"https://user:tok@github.com/acme/api",
		"https://10.0.0.5/acme/api",
		"https://169.254.169.254/latest/meta",
		"https://[::1]/a/b",
		"https://kubernetes/a/b",
		"https://registry.internal/a/b",
		"https://github.com/acme",
		"https://github.com/acme/api?x=1",
		"https://github.com/acme/api#main",
		"file:///etc/passwd",
	} {
		_, err := NormalizeGitURL(bad)
		assert.ErrorIs(t, err, ErrInvalidGitURL, bad)
	}
}

func TestValidatePathAndRef(t *testing.T) {
	for _, ok := range []string{"", "Dockerfile", "docker/Dockerfile.prod", "apps/api"} {
		assert.NoError(t, ValidatePath(ok), ok)
	}
	for _, bad := range []string{"../x", "a/../../x", "/etc/passwd", "a b", "$(id)", "a;b", "a/", "-rf"} {
		assert.ErrorIs(t, ValidatePath(bad), ErrInvalidPath, bad)
	}
	for _, ok := range []string{"", "main", "v1.2.3", "feature/x_y"} {
		assert.NoError(t, ValidateGitRef(ok), ok)
	}
	for _, bad := range []string{"-x", "a b", "a..b", "a#b", "a:b", "$(id)"} {
		assert.ErrorIs(t, ValidateGitRef(bad), ErrInvalidGitRef, bad)
	}
}

func TestValidateName(t *testing.T) {
	assert.NoError(t, ValidateName("web"))
	for _, bad := range []string{"", "a", "Web", "-web", "web-", "a/b", "a_b"} {
		assert.ErrorIs(t, ValidateName(bad), ErrInvalidName, bad)
	}
}
