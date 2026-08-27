package enforcement

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPlaneReportsTheTunnelUpWhenTheHelperAnswers(t *testing.T) {
	t.Parallel()
	tunnel := &RecordingTunnel{}
	plane := NewPlane(tunnel, &RecordingResolver{Reported: enforcingResolver})

	if err := plane.TunnelUp(context.Background()); err != nil {
		t.Fatalf("TunnelUp with a readable tunnel: %v", err)
	}
	if len(tunnel.Calls) != 1 || tunnel.Calls[0] != "show" {
		t.Fatalf("tunnel calls = %v, want one read", tunnel.Calls)
	}
}

// Every way the tunnel can fail to answer is the tunnel being down as far as
// this fact is concerned, and each one keeps its own cause in the error so the
// log names what actually happened.
func TestPlaneReportsTheTunnelDownForEveryUnreadableTunnel(t *testing.T) {
	t.Parallel()
	for _, cause := range []error{ErrTunnelUnavailable, ErrHelperMissing, ErrHelperRefused} {
		plane := NewPlane(&RecordingTunnel{PeersError: cause}, &RecordingResolver{Reported: enforcingResolver})
		err := plane.TunnelUp(context.Background())
		if !errors.Is(err, cause) {
			t.Fatalf("TunnelUp with %v = %v, want an error carrying the cause", cause, err)
		}
	}
}

func TestPlaneReportsTheResolverEnforcingWhenItIsReachableAndFiltering(t *testing.T) {
	t.Parallel()
	resolver := &RecordingResolver{Reported: enforcingResolver}
	plane := NewPlane(&RecordingTunnel{}, resolver)

	if err := plane.ResolverEnforcing(context.Background()); err != nil {
		t.Fatalf("ResolverEnforcing with a filtering resolver: %v", err)
	}
	if len(resolver.Calls) != 1 || resolver.Calls[0] != "status" {
		t.Fatalf("resolver calls = %v, want one status read", resolver.Calls)
	}
}

// A resolver that is running but not filtering is the failure this fact exists
// to separate from an outage: DNS still answers, so nothing looks broken, and
// nothing is enforced. It must not read the same as an unreachable resolver.
func TestPlaneDistinguishesAnUnreachableResolverFromOneThatIsNotFiltering(t *testing.T) {
	t.Parallel()
	unreachable := NewPlane(&RecordingTunnel{}, &RecordingResolver{StatusError: errors.New("connection refused")})
	err := unreachable.ResolverEnforcing(context.Background())
	if !errors.Is(err, ErrResolverUnreachable) {
		t.Fatalf("ResolverEnforcing with an unreachable resolver = %v, want %v", err, ErrResolverUnreachable)
	}

	for name, reported := range map[string]ResolverStatus{
		"protection off": {Running: true, ProtectionEnabled: false, FilteringEnabled: true},
		"filtering off":  {Running: true, ProtectionEnabled: true, FilteringEnabled: false},
		"not running":    {Running: false, ProtectionEnabled: true, FilteringEnabled: true},
	} {
		err := NewPlane(&RecordingTunnel{}, &RecordingResolver{Reported: reported}).ResolverEnforcing(context.Background())
		if !errors.Is(err, ErrResolverNotFiltering) {
			t.Fatalf("ResolverEnforcing with %s = %v, want %v", name, err, ErrResolverNotFiltering)
		}
		if errors.Is(err, ErrResolverUnreachable) {
			t.Fatalf("ResolverEnforcing with %s read as unreachable", name)
		}
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("ResolverEnforcing with %s = %v, want the error to name the setting that is off", name, err)
		}
	}
}
