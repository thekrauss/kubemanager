package sts

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Même liste que les builds : 100.64.0.0/10 restait joignable.
func TestRefusePrivate(t *testing.T) {
	for _, addr := range []string{"10.0.0.1:443", "172.16.0.1:443", "192.168.1.1:443", "127.0.0.1:443", "169.254.169.254:80",
		"100.64.0.1:443", "100.127.255.254:443", "[::1]:443", "[::ffff:100.64.0.1]:443"} {
		assert.ErrorIs(t, refusePrivate("tcp", addr, nil), errPrivateAddress, addr)
	}
	for _, addr := range []string{"8.8.8.8:443", "100.63.255.255:443", "100.128.0.1:443"} {
		assert.NoError(t, refusePrivate("tcp", addr, nil), addr)
	}
}
