package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

type fakeDNS struct {
	cnames map[string]string
	hosts  map[string][]string
}

func (f fakeDNS) LookupCNAME(_ context.Context, host string) (string, error) {
	if c, ok := f.cnames[host]; ok {
		return c, nil
	}
	return "", errors.New("no cname")
}

func (f fakeDNS) LookupHost(_ context.Context, host string) ([]string, error) {
	if ips, ok := f.hosts[host]; ok {
		return ips, nil
	}
	return nil, errors.New("no host")
}

func newVerifyService(dns fakeDNS) *SiteService {
	return NewSiteService(nil, nil, nil, dns, Config{BaseDomain: "betorun.app", EdgeTarget: "edge.betorun.app"}, nil)
}

func TestPointsToEdge(t *testing.T) {
	dns := fakeDNS{
		cnames: map[string]string{
			"www.shop.sn":   "edge.betorun.app.",
			"blog.shop.sn":  "shop.betorun.app.",
			"other.shop.sn": "someone-else.netlify.app.",
		},
		hosts: map[string][]string{
			"edge.betorun.app": {"203.0.113.10", "203.0.113.11"},
			"shop.sn":          {"203.0.113.10"},                 // apex en A
			"mixed.sn":         {"203.0.113.10", "198.51.100.7"}, // pointe aussi ailleurs
		},
	}
	s := newVerifyService(dns)
	ctx := context.Background()

	assert.True(t, s.pointsToEdge(ctx, "www.shop.sn", "shop.betorun.app"), "CNAME vers l'edge")
	assert.True(t, s.pointsToEdge(ctx, "blog.shop.sn", "shop.betorun.app"), "CNAME vers le sous-domaine du site")
	assert.True(t, s.pointsToEdge(ctx, "shop.sn", "shop.betorun.app"), "apex en A sur les IP de l'edge")

	assert.False(t, s.pointsToEdge(ctx, "other.shop.sn", "shop.betorun.app"), "CNAME ailleurs")
	assert.False(t, s.pointsToEdge(ctx, "mixed.sn", "shop.betorun.app"), "une IP hors edge suffit à refuser")
	assert.False(t, s.pointsToEdge(ctx, "nxdomain.sn", "shop.betorun.app"), "domaine inexistant")
}
