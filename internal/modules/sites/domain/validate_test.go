package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateSubdomain(t *testing.T) {
	for _, ok := range []string{"portfolio-awa", "abc", "a1b2"} {
		assert.NoError(t, ValidateSubdomain(ok), ok)
	}
	for _, bad := range []string{"ab", "-abc", "abc-", "Abc", "a_b", "a.b", "a--b", ""} {
		assert.ErrorIs(t, ValidateSubdomain(bad), ErrInvalidName, bad)
	}
	assert.ErrorIs(t, ValidateSubdomain("www"), ErrReservedName)
}

func TestNormalizeHostname(t *testing.T) {
	h, err := NormalizeHostname("  WWW.Example.com. ", "betorun.app")
	assert.NoError(t, err)
	assert.Equal(t, "www.example.com", h)

	for _, bad := range []string{"localhost", "1.2.3.4", "exa mple.com", "-a.com", "a..com", "ex_ample.com", ""} {
		_, err := NormalizeHostname(bad, "betorun.app")
		assert.ErrorIs(t, err, ErrInvalidHostname, bad)
	}
	for _, platform := range []string{"betorun.app", "shop.betorun.app"} {
		_, err := NormalizeHostname(platform, "betorun.app")
		assert.ErrorIs(t, err, ErrPlatformHostname, platform)
	}
}
