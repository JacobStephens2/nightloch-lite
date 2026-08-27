package enforcement

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Plane answers one question about the enforcement plane: is it actually
// enforcing? It reads the same tunnel adapter Devices are minted through and
// the resolver that carries the Baseline list, and it changes neither.
//
// It is separate from Service because the facts need no database and no tunnel
// settings: the serving process reports them without holding the Operator
// configuration that minting requires, and an Operator command mints without
// holding administration credentials for the resolver.
type Plane struct {
	tunnel   Tunnel
	resolver Resolver
}

// NewPlane builds the readiness reader over the plane's two dependencies.
func NewPlane(tunnel Tunnel, resolver Resolver) *Plane {
	return &Plane{tunnel: tunnel, resolver: resolver}
}

// TunnelUp reports whether the tunnel interface is carrying Devices. Reading
// membership is the whole check: the helper answers only for an interface that
// exists and is up, and every way it can fail to answer — an absent helper, a
// refused request, an interface that is not there — means no Device's traffic
// is reaching the enforcement plane. The cause is kept in the error so the
// fact that failed is named with what went wrong rather than only that it did.
func (p *Plane) TunnelUp(ctx context.Context) error {
	if _, err := p.tunnel.Peers(ctx); err != nil {
		return fmt.Errorf("the tunnel interface is not carrying Devices: %w", err)
	}
	return nil
}

// ResolverEnforcing reports whether AdGuard Home is reachable and filtering.
// The two failures stay separate all the way out: an unreachable resolver is
// an unknown, while a resolver answering with filtering off is a Protectee
// resolving every name they ask for while the product reports itself up.
func (p *Plane) ResolverEnforcing(ctx context.Context) error {
	status, err := p.resolver.Status(ctx)
	if err != nil {
		// A Resolver that could not answer reports an unknown, whatever went
		// wrong underneath. The adapter already says so; anything else that
		// implements the interface is held to the same fact here rather than
		// being allowed to surface an error nothing classifies.
		if errors.Is(err, ErrResolverUnreachable) {
			return err
		}
		return fmt.Errorf("%w: %w", ErrResolverUnreachable, err)
	}
	var off []string
	if !status.Running {
		off = append(off, "not running")
	}
	if !status.ProtectionEnabled {
		off = append(off, "protection off")
	}
	if !status.FilteringEnabled {
		off = append(off, "filtering off")
	}
	if len(off) > 0 {
		return fmt.Errorf("%w: %s", ErrResolverNotFiltering, strings.Join(off, ", "))
	}
	return nil
}
