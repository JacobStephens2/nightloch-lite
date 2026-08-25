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
