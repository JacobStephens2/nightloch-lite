package enforcement

import (
	"context"
	"net/netip"
	"slices"
	"time"
)

// RecordingTunnel is the Tunnel a test uses instead of a droplet, following
// the mail package's recording sender. It holds real membership rather than a
// canned answer, so a test asserts what the tunnel would actually carry, and
// it records every call in order so an ordering claim can be checked.
type RecordingTunnel struct {
	// Calls is every invocation in order, rendered as the helper would have
	// received it: "add-peer <key> <address>", "remove-peer <key>", "show".
	Calls []string

	// AddPeerError, RemovePeerError, and PeersError make each operation fail.
	// A failing AddPeer records the call and changes no membership, which is
	// what the helper does when it refuses.
	AddPeerError    error
	RemovePeerError error
	PeersError      error

	// Handshakes is the latest handshake to report for a public key. A key
	// with no entry has never completed one.
	Handshakes map[string]time.Time

	members []Peer
}

func (t *RecordingTunnel) AddPeer(_ context.Context, publicKey string, address netip.Addr) error {
	t.Calls = append(t.Calls, "add-peer "+publicKey+" "+address.String())
	if t.AddPeerError != nil {
		return t.AddPeerError
	}
	t.removeMember(publicKey)
	t.members = append(t.members, Peer{PublicKey: publicKey, AllowedIPs: address.String() + "/32"})
	return nil
}

func (t *RecordingTunnel) RemovePeer(_ context.Context, publicKey string) error {
	t.Calls = append(t.Calls, "remove-peer "+publicKey)
	if t.RemovePeerError != nil {
		return t.RemovePeerError
	}
	t.removeMember(publicKey)
	return nil
}

func (t *RecordingTunnel) Peers(context.Context) ([]Peer, error) {
	t.Calls = append(t.Calls, "show")
	if t.PeersError != nil {
		return nil, t.PeersError
	}
	peers := make([]Peer, 0, len(t.members))
	for _, member := range t.members {
		member.LatestHandshake = t.Handshakes[member.PublicKey]
		peers = append(peers, member)
	}
	return peers, nil
}

func (t *RecordingTunnel) removeMember(publicKey string) {
	t.members = slices.DeleteFunc(t.members, func(peer Peer) bool {
		return peer.PublicKey == publicKey
	})
}

// enforcingResolver is the state a healthy AdGuard Home reports: running, with
// protection and the filtering engine both on.
var enforcingResolver = ResolverStatus{Running: true, ProtectionEnabled: true, FilteringEnabled: true}

// RecordingResolver is the Resolver a test uses instead of a droplet. It
// records status checks and client additions and deletions in order.
type RecordingResolver struct {
	// Calls is every invocation in order: "status", "add-client <name> <ip>",
	// "remove-client <name>".
	Calls []string

	// Reported is the state to answer with. Its zero value is a resolver that
	// is down, so a test that means "healthy" has to say so.
	Reported ResolverStatus

	// StatusError makes the read fail, which is a resolver whose state is
	// unknown rather than one that is known not to be filtering.
	StatusError error

	AddClientError    error
	RemoveClientError error

	clients map[string]netip.Addr
}

func (r *RecordingResolver) Status(context.Context) (ResolverStatus, error) {
	r.Calls = append(r.Calls, "status")
	if r.StatusError != nil {
		return ResolverStatus{}, r.StatusError
	}
	return r.Reported, nil
}

func (r *RecordingResolver) AddClient(_ context.Context, name string, ip netip.Addr) error {
	r.Calls = append(r.Calls, "add-client "+name+" "+ip.String())
	if r.AddClientError != nil {
		return r.AddClientError
	}
	if r.clients == nil {
		r.clients = make(map[string]netip.Addr)
	}
	r.clients[name] = ip
	return nil
}

func (r *RecordingResolver) RemoveClient(_ context.Context, name string) error {
	r.Calls = append(r.Calls, "remove-client "+name)
	if r.RemoveClientError != nil {
		return r.RemoveClientError
	}
	if r.clients != nil {
		delete(r.clients, name)
	}
	return nil
}

// HasClient reports whether the named client is currently registered.
func (r *RecordingResolver) HasClient(name string) bool {
	if r.clients == nil {
		return false
	}
	_, ok := r.clients[name]
	return ok
}

// EnforcingResolver is a RecordingResolver reporting a resolver that is up and
// filtering, which is the state most tests want to hold fixed while they vary
// something else.
func EnforcingResolver() *RecordingResolver {
	return &RecordingResolver{Reported: enforcingResolver, clients: make(map[string]netip.Addr)}
}
