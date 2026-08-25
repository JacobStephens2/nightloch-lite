// Package enforcement owns Nightloch's enforcement plane: the tunnel
// addresses Devices are allocated from, the Device records themselves, and the
// canonical configuration a Protectee installs. WireGuard sits behind the
// injected Tunnel interface, and the privileged half of it is the nightloch-wg
// root helper (docs/adr/0004-reach-the-tunnel-through-a-root-helper.md), so
// everything in this package is testable without a droplet.
//
// Minting is an Operator action in this slice. Opening Device enrollment to
// Users is the Enrollment allowlist in Slice 5, and this package holds no gate
// that could be mistaken for one.
package enforcement

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"
	"unicode"

	"github.com/JacobStephens2/nightloch-lite/internal/emailaddr"
)

// maxDeviceNameLength bounds the one free-text field a Device carries. It is a
// label an Operator and later a Protectee reads in a list, not a description.
const maxDeviceNameLength = 64

var (
	// ErrInvalidEmail rejects an address that identifies no possible User.
	ErrInvalidEmail = emailaddr.ErrInvalid
	// ErrUnknownProtectee rejects minting for an address that is not a User.
	ErrUnknownProtectee = errors.New("no User holds that email address")
	// ErrInvalidDeviceName rejects a Device label Nightloch will not record.
	ErrInvalidDeviceName = errors.New("enter a name for the Device")
	// ErrDeviceExists rejects minting a Device that already exists rather than
	// allocating it a second tunnel address.
	ErrDeviceExists = errors.New("that Protectee already has a Device with that name")
	// ErrAddressPoolExhausted rejects minting when the tunnel network holds no
	// free address.
	ErrAddressPoolExhausted = errors.New("no tunnel address is free; remove a Device before minting another")
)

// Device is one enrolled tunnel identity. There is no private key field: the
// server holds the public key and the allocated address, and never the means
// to impersonate the Device.
type Device struct {
	ID          int64
	ProtecteeID int64
	Name        string
	PublicKey   string
	Address     netip.Addr
	CreatedAt   time.Time
}

// KeyPair is one Device's identity at the moment it is minted. The private
// half exists only long enough to be rendered into the configuration handed to
// the Protectee; nothing in this package writes it anywhere.
type KeyPair struct {
	PrivateKey string
	PublicKey  string
}

// Service is the deep enforcement module. Callers hand it an Operator's
// intent; address allocation, transaction ordering against the tunnel, key
// generation, and canonical rendering all stay inside.
type Service struct {
	db       *sql.DB
	tunnel   Tunnel
	settings TunnelSettings
	now      func() time.Time
	newKeys  func() (KeyPair, error)
}

// NewService builds a Service over the process's serialized SQLite handle and
// an injected Tunnel. Settings are validated once here rather than at every
// render, so RenderConfiguration is total.
func NewService(db *sql.DB, tunnel Tunnel, settings TunnelSettings) (*Service, error) {
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	return &Service{db: db, tunnel: tunnel, settings: settings, now: time.Now, newKeys: generateKeyPair}, nil
}

// MintCommand is one Operator's request to enroll a Device for a Protectee.
type MintCommand struct {
	ProtecteeEmail string
	DeviceName     string
}

// MintedDevice is what minting hands back once and never again: the recorded
// Device, and the configuration carrying the private key that was generated
// for it. Nothing here is recoverable from the database afterwards.
type MintedDevice struct {
	Device        Device
	Configuration string
}

// MintDevice allocates the lowest free tunnel address, records the Device, and
// admits it to the tunnel, in one ordering this module owns: the record is
// written and the tunnel is changed inside a transaction that commits only
// once the tunnel has accepted the peer, and a commit that fails withdraws the
// peer again. Neither half is therefore reachable without the other — a
// Device an Operator can see is a Device the tunnel will carry, and a peer on
// the tunnel is a Device with a record and an owner.
//
// The tunnel is invoked while the transaction is open, which holds Nightloch's
// single SQLite connection for the length of a subprocess. That is acceptable
// for an Operator command and is the reason minting is one: the alternative
// orderings all admit a window where the two disagree.
func (s *Service) MintDevice(ctx context.Context, command MintCommand) (MintedDevice, error) {
	email, err := emailaddr.Normalize(command.ProtecteeEmail)
	if err != nil {
		return MintedDevice{}, err
	}
	name, err := normalizeDeviceName(command.DeviceName)
	if err != nil {
		return MintedDevice{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MintedDevice{}, fmt.Errorf("begin mint: %w", err)
	}
	defer tx.Rollback()

	var protecteeID int64
	err = tx.QueryRowContext(ctx,
		"SELECT id FROM users WHERE email = ? AND deleted_at IS NULL", email).Scan(&protecteeID)
	if errors.Is(err, sql.ErrNoRows) {
		return MintedDevice{}, fmt.Errorf("%w: %s", ErrUnknownProtectee, email)
	}
	if err != nil {
		return MintedDevice{}, fmt.Errorf("read Protectee: %w", err)
	}

	var existing int
	if err := tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM devices WHERE user_id = ? AND name = ?", protecteeID, name).Scan(&existing); err != nil {
		return MintedDevice{}, fmt.Errorf("read existing Devices: %w", err)
	}
	if existing > 0 {
		return MintedDevice{}, fmt.Errorf("%w: %s", ErrDeviceExists, name)
	}

	address, err := allocateAddress(ctx, tx)
	if err != nil {
		return MintedDevice{}, err
	}
	keys, err := s.newKeys()
	if err != nil {
		return MintedDevice{}, err
	}

	createdAt := s.now().UTC().Truncate(time.Second)
	result, err := tx.ExecContext(ctx,
		`INSERT INTO devices(user_id, name, public_key, address, created_at) VALUES (?, ?, ?, ?, ?)`,
		protecteeID, name, keys.PublicKey, address.String(), createdAt.Unix())
	if err != nil {
		return MintedDevice{}, fmt.Errorf("record Device: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return MintedDevice{}, fmt.Errorf("record Device: %w", err)
	}

	if err := s.tunnel.AddPeer(ctx, keys.PublicKey, address); err != nil {
		return MintedDevice{}, fmt.Errorf("admit the Device to the tunnel: %w", err)
	}
	if err := tx.Commit(); err != nil {
		// The tunnel now carries membership no record explains. Withdraw it,
		// and say so plainly if that fails too: an Operator has to know the
		// tunnel is holding a peer Nightloch cannot account for.
		if removeErr := s.tunnel.RemovePeer(ctx, keys.PublicKey); removeErr != nil {
			return MintedDevice{}, errors.Join(
				fmt.Errorf("commit Device: %w", err),
				fmt.Errorf("withdraw the uncommitted peer from the tunnel: %w", removeErr))
		}
		return MintedDevice{}, fmt.Errorf("commit Device: %w", err)
	}

	device := Device{
		ID: id, ProtecteeID: protecteeID, Name: name,
		PublicKey: keys.PublicKey, Address: address, CreatedAt: createdAt,
	}
	return MintedDevice{
		Device:        device,
		Configuration: RenderConfiguration(device, keys.PrivateKey, s.settings),
	}, nil
}

// DeviceStatus is a recorded Device together with what the tunnel says about
// it. OnTunnel false is a real disagreement between the record and the
// interface, never a tunnel this module could not read: Devices returns an
// error in that case rather than a list.
type DeviceStatus struct {
	Device          Device
	OnTunnel        bool
	LatestHandshake time.Time
}

// Devices reports every recorded Device with its tunnel membership, ordered by
// tunnel address. A tunnel that cannot be reached — an uninstalled helper, a
// refused invocation, an interface that is down — is an error, because
// reporting it as an empty membership would read as "no Device is connected"
// when the truth is that nothing is known.
func (s *Service) Devices(ctx context.Context) ([]DeviceStatus, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, name, public_key, address, created_at FROM devices ORDER BY address`)
	if err != nil {
		return nil, fmt.Errorf("read Devices: %w", err)
	}
	defer rows.Close()

	var devices []Device
	for rows.Next() {
		var device Device
		var address string
		var createdAt int64
		if err := rows.Scan(&device.ID, &device.ProtecteeID, &device.Name, &device.PublicKey, &address, &createdAt); err != nil {
			return nil, fmt.Errorf("read Devices: %w", err)
		}
		device.Address, err = netip.ParseAddr(address)
		if err != nil {
			return nil, fmt.Errorf("read Device address %q: %w", address, err)
		}
		device.CreatedAt = time.Unix(createdAt, 0).UTC()
		devices = append(devices, device)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read Devices: %w", err)
	}

	peers, err := s.tunnel.Peers(ctx)
	if err != nil {
		return nil, fmt.Errorf("read tunnel membership: %w", err)
	}
	membership := make(map[string]Peer, len(peers))
	for _, peer := range peers {
		membership[peer.PublicKey] = peer
	}

	statuses := make([]DeviceStatus, 0, len(devices))
	for _, device := range devices {
		peer, onTunnel := membership[device.PublicKey]
		statuses = append(statuses, DeviceStatus{
			Device: device, OnTunnel: onTunnel, LatestHandshake: peer.LatestHandshake,
		})
	}
	return statuses, nil
}

// allocateAddress returns the lowest tunnel address no Device holds. The host
// address is outside the loop's bounds rather than filtered out of it, so no
// change to allocation can ever hand a Device the tunnel's own address.
func allocateAddress(ctx context.Context, tx *sql.Tx) (netip.Addr, error) {
	rows, err := tx.QueryContext(ctx, "SELECT address FROM devices")
	if err != nil {
		return netip.Addr{}, fmt.Errorf("read allocated addresses: %w", err)
	}
	defer rows.Close()

	taken := map[string]bool{}
	for rows.Next() {
		var address string
		if err := rows.Scan(&address); err != nil {
			return netip.Addr{}, fmt.Errorf("read allocated addresses: %w", err)
		}
		taken[address] = true
	}
	if err := rows.Err(); err != nil {
		return netip.Addr{}, fmt.Errorf("read allocated addresses: %w", err)
	}

	for octet := firstDeviceOctet; octet <= lastDeviceOctet; octet++ {
		candidate := deviceAddress(octet)
		if !taken[candidate.String()] {
			return candidate, nil
		}
	}
	return netip.Addr{}, ErrAddressPoolExhausted
}

// deviceAddress is the assignable tunnel address with the given final octet.
func deviceAddress(octet int) netip.Addr {
	address := tunnelNetwork.Addr().As4()
	address[3] = byte(octet)
	return netip.AddrFrom4(address)
}

// normalizeDeviceName trims the label and refuses one that is empty, too long,
// or carries anything but printable text.
func normalizeDeviceName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" || len([]rune(name)) > maxDeviceNameLength {
		return "", ErrInvalidDeviceName
	}
	for _, r := range name {
		if !unicode.IsGraphic(r) {
			return "", ErrInvalidDeviceName
		}
	}
	return name, nil
}

// generateKeyPair mints a WireGuard identity. The private scalar is clamped as
// wg's own genkey clamps it, so the key a Protectee installs is byte-identical
// to one the WireGuard tools would have produced.
func generateKeyPair() (KeyPair, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return KeyPair{}, fmt.Errorf("generate Device key: %w", err)
	}
	secret[0] &= 248
	secret[31] &= 127
	secret[31] |= 64
	private, err := ecdh.X25519().NewPrivateKey(secret)
	if err != nil {
		return KeyPair{}, fmt.Errorf("generate Device key: %w", err)
	}
	return KeyPair{
		PrivateKey: base64.StdEncoding.EncodeToString(private.Bytes()),
		PublicKey:  base64.StdEncoding.EncodeToString(private.PublicKey().Bytes()),
	}, nil
}
