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
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/JacobStephens2/nightloch-lite/internal/emailaddr"
)

// maxDeviceNameLength bounds the one free-text field a Device carries. It is a
// label an Operator and later a Protectee reads in a list, not a description.
const maxDeviceNameLength = 64

// MaxDevicesPerProtectee bounds the number of live Devices a Protectee may hold
// simultaneously.
const MaxDevicesPerProtectee = 5

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
	// ErrDeviceQuotaExceeded rejects enrolling a 6th live Device for a Protectee.
	ErrDeviceQuotaExceeded = errors.New("device quota exceeded: you may have at most 5 live Devices, remove an unused Device first")
	// ErrUnauthorizedRemoval rejects a Device removal attempt by an actor who is
	// neither the Protectee nor an active Partner.
	ErrUnauthorizedRemoval = errors.New("unauthorized to remove this Device")
	// ErrUnknownDevice rejects removing something that is not one of that
	// Protectee's live Devices, which is also what a Device already removed
	// is. Removal is therefore refused rather than repeated: the tunnel is
	// never invoked a second time for a peer already withdrawn, and the
	// quarantine clock never restarts.
	ErrUnknownDevice = errors.New("unknown Device")
	// ErrAddressPoolExhausted rejects minting when the tunnel network holds no
	// address a Device could be given. It is wrapped with the remedy the pool
	// actually has, which is not the same remedy in both cases.
	ErrAddressPoolExhausted = errors.New("no tunnel address is free")
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
	// RemovedAt is the instant the record ended, and the zero time for a live
	// Device. The whole quarantine window on the Device's address is derived
	// from it; nothing else about the window is recorded.
	RemovedAt time.Time
}

// KeyPair is one Device's identity at the moment it is minted. The private
// half exists only long enough to be rendered into the configuration handed to
// the Protectee; nothing in this package writes it anywhere.
type KeyPair struct {
	PrivateKey string
	PublicKey  string
}

// DeviceAuthorizer decides whether an actor is authorized to perform Device operations.
type DeviceAuthorizer interface {
	CanRemoveDevice(ctx context.Context, actorID, protecteeID int64) (bool, error)
}

// DeviceAuthorizerFunc adapts a bare function into a DeviceAuthorizer.
type DeviceAuthorizerFunc func(ctx context.Context, actorID, protecteeID int64) (bool, error)

// CanRemoveDevice implements DeviceAuthorizer.
func (f DeviceAuthorizerFunc) CanRemoveDevice(ctx context.Context, actorID, protecteeID int64) (bool, error) {
	return f(ctx, actorID, protecteeID)
}

// Service is the deep enforcement module. Callers hand it an Operator's or
// Protectee's intent; address allocation, transaction ordering against the
// tunnel and resolver, key generation, and canonical rendering all stay inside.
type Service struct {
	db         *sql.DB
	tunnel     Tunnel
	resolver   Resolver
	authorizer DeviceAuthorizer
	settings   TunnelSettings
	now        func() time.Time
	newKeys    func() (KeyPair, error)
}

// NewService builds a Service over the process's serialized SQLite handle,
// an injected Tunnel, and an injected Resolver. Settings are validated once
// here rather than at every render, so RenderConfiguration is total.
func NewService(db *sql.DB, tunnel Tunnel, resolver Resolver, settings TunnelSettings) (*Service, error) {
	if resolver == nil {
		return nil, errors.New("resolver is required")
	}
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	return &Service{db: db, tunnel: tunnel, resolver: resolver, settings: settings, now: time.Now, newKeys: generateKeyPair}, nil
}

// SetAuthorizer configures the DeviceAuthorizer used to verify access for Device operations.
// When unconfigured (nil), authorized operations fail closed.
func (s *Service) SetAuthorizer(authorizer DeviceAuthorizer) {
	s.authorizer = authorizer
}

// MintCommand is one Operator's request to mint a Device for a Protectee.
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
// EnrollDevice allocates a tunnel address, generates keys, admits the peer to
// WireGuard, and registers the client in AdGuard Home for a Protectee.
//
// A Protectee may hold at most MaxDevicesPerProtectee live Devices. Enrolling
// a 6th is refused with ErrDeviceQuotaExceeded.
func (s *Service) EnrollDevice(ctx context.Context, protecteeID int64, name string) (MintedDevice, error) {
	if protecteeID <= 0 {
		return MintedDevice{}, ErrUnknownProtectee
	}
	normalizedName, err := normalizeDeviceName(name)
	if err != nil {
		return MintedDevice{}, err
	}
	return s.createDevice(ctx, protecteeID, normalizedName, true)
}

// MintDevice allocates the lowest free tunnel address, records the Device,
// admits it to the tunnel, and registers the client with AdGuard Home, in one
// ordering this module owns: the record is written, the tunnel is changed, and
// the resolver client is added inside a transaction that commits only once
// both adapters have accepted the peer and client, and a failure withdraws the
// peer again and rolls back. Neither half is therefore reachable without the
// other — a Device an Operator can see is a Device the tunnel will carry and
// AdGuard Home will filter, and a peer on the tunnel is a Device with a record
// and an owner.
func (s *Service) MintDevice(ctx context.Context, command MintCommand) (MintedDevice, error) {
	email, err := emailaddr.Normalize(command.ProtecteeEmail)
	if err != nil {
		return MintedDevice{}, err
	}
	name, err := normalizeDeviceName(command.DeviceName)
	if err != nil {
		return MintedDevice{}, err
	}

	var protecteeID int64
	err = s.db.QueryRowContext(ctx,
		"SELECT id FROM users WHERE email = ? AND deleted_at IS NULL", email).Scan(&protecteeID)
	if errors.Is(err, sql.ErrNoRows) {
		return MintedDevice{}, fmt.Errorf("%w: %s", ErrUnknownProtectee, email)
	}
	if err != nil {
		return MintedDevice{}, fmt.Errorf("read Protectee: %w", err)
	}

	return s.createDevice(ctx, protecteeID, name, false)
}

func (s *Service) createDevice(ctx context.Context, protecteeID int64, name string, enforceQuota bool) (MintedDevice, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MintedDevice{}, fmt.Errorf("begin mint: %w", err)
	}
	defer tx.Rollback()

	var exists int
	err = tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM users WHERE id = ? AND deleted_at IS NULL", protecteeID).Scan(&exists)
	if err != nil {
		return MintedDevice{}, fmt.Errorf("read Protectee: %w", err)
	}
	if exists == 0 {
		return MintedDevice{}, fmt.Errorf("%w: user id %d", ErrUnknownProtectee, protecteeID)
	}

	var liveCount int
	if err := tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM devices WHERE protectee_id = ? AND removed_at IS NULL",
		protecteeID).Scan(&liveCount); err != nil {
		return MintedDevice{}, fmt.Errorf("read existing Devices count: %w", err)
	}
	if enforceQuota && liveCount >= MaxDevicesPerProtectee {
		return MintedDevice{}, ErrDeviceQuotaExceeded
	}

	var nameDuplicate int
	if err := tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM devices WHERE protectee_id = ? AND name = ? AND removed_at IS NULL",
		protecteeID, name).Scan(&nameDuplicate); err != nil {
		return MintedDevice{}, fmt.Errorf("read existing Devices: %w", err)
	}
	if nameDuplicate > 0 {
		return MintedDevice{}, fmt.Errorf("%w: %s", ErrDeviceExists, name)
	}

	address, err := allocateAddress(ctx, tx, s.now().UTC())
	if err != nil {
		return MintedDevice{}, err
	}
	keys, err := s.newKeys()
	if err != nil {
		return MintedDevice{}, err
	}

	createdAt := s.now().UTC().Truncate(time.Second)
	result, err := tx.ExecContext(ctx,
		`INSERT INTO devices(protectee_id, name, public_key, address, created_at) VALUES (?, ?, ?, ?, ?)`,
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

	clientName := DeviceClientName(id)
	if err := s.resolver.AddClient(ctx, clientName, address); err != nil {
		if removeErr := s.tunnel.RemovePeer(ctx, keys.PublicKey); removeErr != nil {
			return MintedDevice{}, errors.Join(
				fmt.Errorf("register client with resolver: %w", err),
				fmt.Errorf("withdraw the peer after resolver failure: %w", removeErr))
		}
		return MintedDevice{}, fmt.Errorf("register client with resolver: %w", err)
	}

	if err := tx.Commit(); err != nil {
		var errs []error
		errs = append(errs, fmt.Errorf("commit Device: %w", err))
		if removeErr := s.tunnel.RemovePeer(ctx, keys.PublicKey); removeErr != nil {
			errs = append(errs, fmt.Errorf("withdraw the uncommitted peer from the tunnel: %w", removeErr))
		}
		if removeClientErr := s.resolver.RemoveClient(ctx, clientName); removeClientErr != nil {
			errs = append(errs, fmt.Errorf("withdraw the uncommitted client from resolver: %w", removeClientErr))
		}
		return MintedDevice{}, errors.Join(errs...)
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

// RemoveCommand is one Operator's request to remove a Protectee's Device. A
// Device is named the way an Operator knows it, which is the way it was minted.
type RemoveCommand struct {
	ProtecteeEmail string
	DeviceName     string
}

// RemovedDevice is what removal reports: the record as it ended, and the
// instant its tunnel address rejoins the pool. ReusableAt is derived from the
// removal instant and the retention in force, so it is an answer rather than a
// promise stored somewhere that could later disagree with the retention.
type RemovedDevice struct {
	Device     Device
	ReusableAt time.Time
}

// scanLiveDevice scans a single live Device row.
func scanLiveDevice(row *sql.Row) (Device, error) {
	var device Device
	var address string
	var createdAt int64
	err := row.Scan(&device.ID, &device.ProtecteeID, &device.Name, &device.PublicKey, &address, &createdAt)
	if err != nil {
		return Device{}, err
	}
	device.Address, err = netip.ParseAddr(address)
	if err != nil {
		return Device{}, fmt.Errorf("read Device address %q: %w", address, err)
	}
	device.CreatedAt = time.Unix(createdAt, 0).UTC()
	return device, nil
}

// LiveDeviceByID returns an active Device by ID, or ErrUnknownDevice if not found or already removed.
func (s *Service) LiveDeviceByID(ctx context.Context, deviceID int64) (Device, error) {
	if deviceID <= 0 {
		return Device{}, fmt.Errorf("%w: id %d", ErrUnknownDevice, deviceID)
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT id, protectee_id, name, public_key, address, created_at FROM devices
		 WHERE id = ? AND removed_at IS NULL`,
		deviceID)
	device, err := scanLiveDevice(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, fmt.Errorf("%w: id %d", ErrUnknownDevice, deviceID)
	}
	if err != nil {
		return Device{}, fmt.Errorf("read Device: %w", err)
	}
	return device, nil
}

// AuthorizeRemoval verifies whether actorID is authorized to remove deviceID.
// If authorized, it returns the Device. If unauthorized, it returns ErrUnauthorizedRemoval.
// If the Device does not exist or has already been removed, it returns ErrUnknownDevice.
func (s *Service) AuthorizeRemoval(ctx context.Context, actorID, deviceID int64) (Device, error) {
	if actorID <= 0 || deviceID <= 0 {
		return Device{}, ErrUnauthorizedRemoval
	}
	if s.authorizer == nil {
		return Device{}, ErrUnauthorizedRemoval
	}

	device, err := s.LiveDeviceByID(ctx, deviceID)
	if err != nil {
		return Device{}, err
	}

	allowed, err := s.authorizer.CanRemoveDevice(ctx, actorID, device.ProtecteeID)
	if err != nil {
		return Device{}, fmt.Errorf("check removal authorization: %w", err)
	}
	if !allowed {
		return Device{}, ErrUnauthorizedRemoval
	}
	return device, nil
}

// DevicesForProtectee lists all live Devices belonging to protecteeID, ordered
// chronologically.
func (s *Service) DevicesForProtectee(ctx context.Context, protecteeID int64) ([]Device, error) {
	if protecteeID <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, protectee_id, name, public_key, address, created_at FROM devices
		 WHERE protectee_id = ? AND removed_at IS NULL
		 ORDER BY created_at ASC, id ASC`,
		protecteeID)
	if err != nil {
		return nil, fmt.Errorf("read Protectee Devices: %w", err)
	}
	defer rows.Close()

	var devices []Device
	for rows.Next() {
		var device Device
		var address string
		var createdAt int64
		if err := rows.Scan(&device.ID, &device.ProtecteeID, &device.Name, &device.PublicKey, &address, &createdAt); err != nil {
			return nil, fmt.Errorf("read Protectee Devices: %w", err)
		}
		device.Address, err = netip.ParseAddr(address)
		if err != nil {
			return nil, fmt.Errorf("read Device address %q: %w", address, err)
		}
		device.CreatedAt = time.Unix(createdAt, 0).UTC()
		devices = append(devices, device)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read Protectee Devices: %w", err)
	}
	return devices, nil
}

// RemoveDeviceByID ends a Device record by ID, removes its AdGuard Home client,
// and withdraws its WireGuard peer.
//
// The actor must be authorized by the configured DeviceAuthorizer (either the Protectee
// owning the Device or an active Partner protecting that Protectee).
func (s *Service) RemoveDeviceByID(ctx context.Context, actorID, deviceID int64) (RemovedDevice, error) {
	device, err := s.AuthorizeRemoval(ctx, actorID, deviceID)
	if err != nil {
		return RemovedDevice{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RemovedDevice{}, fmt.Errorf("begin removal: %w", err)
	}
	defer tx.Rollback()

	return s.removeDeviceTx(ctx, tx, device)
}

// RemoveDevice ends a Device's record, removes its AdGuard Home client, and
// withdraws its tunnel peer, in the mirror of the ordering minting uses: the
// record is ended inside a transaction that commits only once both the resolver
// client and the tunnel peer have been withdrawn, and a commit that fails
// re-admits them. A Device an Operator can still see is therefore still a Device
// the tunnel carries.
//
// The address is not returned to the pool. It stays out of allocation for
// QuarantineWindow past this instant, which is strictly longer than the
// query-log retention it has to outlast, so a later occupant of the address
// can never resolve to this Protectee's retained queries.
//
// Only a live Device can be removed. Removing one that has already been
// removed is refused rather than repeated, which is what keeps a second
// attempt from invoking the helper again or restarting the quarantine clock.
func (s *Service) RemoveDevice(ctx context.Context, command RemoveCommand) (RemovedDevice, error) {
	email, err := emailaddr.Normalize(command.ProtecteeEmail)
	if err != nil {
		return RemovedDevice{}, err
	}
	name, err := normalizeDeviceName(command.DeviceName)
	if err != nil {
		return RemovedDevice{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RemovedDevice{}, fmt.Errorf("begin removal: %w", err)
	}
	defer tx.Rollback()

	var protecteeID int64
	err = tx.QueryRowContext(ctx,
		"SELECT id FROM users WHERE email = ? AND deleted_at IS NULL", email).Scan(&protecteeID)
	if errors.Is(err, sql.ErrNoRows) {
		return RemovedDevice{}, fmt.Errorf("%w: %s", ErrUnknownProtectee, email)
	}
	if err != nil {
		return RemovedDevice{}, fmt.Errorf("read Protectee: %w", err)
	}

	row := tx.QueryRowContext(ctx,
		`SELECT id, protectee_id, name, public_key, address, created_at FROM devices
		 WHERE protectee_id = ? AND name = ? AND removed_at IS NULL`,
		protecteeID, name)
	device, err := scanLiveDevice(row)
	if errors.Is(err, sql.ErrNoRows) {
		return RemovedDevice{}, fmt.Errorf("%w: %s", ErrUnknownDevice, name)
	}
	if err != nil {
		return RemovedDevice{}, fmt.Errorf("read Device: %w", err)
	}

	return s.removeDeviceTx(ctx, tx, device)
}

func (s *Service) removeDeviceTx(ctx context.Context, tx *sql.Tx, device Device) (RemovedDevice, error) {
	removedAt := s.now().UTC().Truncate(time.Second)
	// Guarded on removed_at again rather than on the id alone, so a concurrent
	// removal that won cannot be overwritten with a later instant and a longer
	// window than the one already in force.
	result, err := tx.ExecContext(ctx,
		"UPDATE devices SET removed_at = ? WHERE id = ? AND removed_at IS NULL", removedAt.Unix(), device.ID)
	if err != nil {
		return RemovedDevice{}, fmt.Errorf("end the Device record: %w", err)
	}
	ended, err := result.RowsAffected()
	if err != nil {
		return RemovedDevice{}, fmt.Errorf("end the Device record: %w", err)
	}
	if ended != 1 {
		return RemovedDevice{}, fmt.Errorf("%w: %s", ErrUnknownDevice, device.Name)
	}
	device.RemovedAt = removedAt

	clientName := DeviceClientName(device.ID)
	if err := s.resolver.RemoveClient(ctx, clientName); err != nil {
		return RemovedDevice{}, fmt.Errorf("remove AdGuard Home client: %w", err)
	}

	if err := s.tunnel.RemovePeer(ctx, device.PublicKey); err != nil {
		if reAddErr := s.resolver.AddClient(ctx, clientName, device.Address); reAddErr != nil {
			return RemovedDevice{}, errors.Join(
				fmt.Errorf("withdraw the Device from the tunnel: %w", err),
				fmt.Errorf("re-add the AdGuard Home client whose tunnel withdrawal failed: %w", reAddErr))
		}
		return RemovedDevice{}, fmt.Errorf("withdraw the Device from the tunnel: %w", err)
	}

	if err := tx.Commit(); err != nil {
		var errs []error
		errs = append(errs, fmt.Errorf("commit the removal: %w", err))
		if addErr := s.tunnel.AddPeer(ctx, device.PublicKey, device.Address); addErr != nil {
			errs = append(errs, fmt.Errorf("re-admit the Device whose removal did not commit: %w", addErr))
		}
		if reAddErr := s.resolver.AddClient(ctx, clientName, device.Address); reAddErr != nil {
			errs = append(errs, fmt.Errorf("re-add the AdGuard Home client whose removal did not commit: %w", reAddErr))
		}
		return RemovedDevice{}, errors.Join(errs...)
	}

	return RemovedDevice{Device: device, ReusableAt: ReusableAt(removedAt)}, nil
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

// Membership is the whole of what the records and the tunnel together say, and
// it is deliberately not just a list of Devices. Minting commits the record
// and the peer in one ordering, but a process killed between the two halves
// can still leave the tunnel carrying a peer no record explains — membership
// nobody owns, which no listing of Devices could ever show. Reporting both
// sides is what makes "neither is reachable without the other" a claim an
// Operator can check rather than one this package merely asserts.
type Membership struct {
	// Devices is every live Device, ordered by tunnel address. A removed
	// Device is not one: its record ended, and the row that survives it exists
	// only to hold its address out of the pool.
	Devices []DeviceStatus
	// UnaccountedPeers is tunnel membership belonging to no live Device.
	UnaccountedPeers []Peer
	// Quarantined is every address a removed Device still holds, ordered by
	// address, with the instant each rejoins the pool. Without it an Operator
	// refused a mint for exhaustion could not tell whether the remedy is to
	// remove a Device or to wait.
	Quarantined []QuarantinedAddress
}

// Devices reports every live Device with its tunnel membership, any tunnel
// membership no record explains, and every address a removed Device still
// holds in quarantine. A tunnel that cannot be reached — an uninstalled
// helper, a refused invocation, an interface that is down — is an error, because reporting it as an empty membership would read
// as "no Device is connected" when the truth is that nothing is known.
func (s *Service) Devices(ctx context.Context) (Membership, error) {
	now := s.now().UTC()
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, protectee_id, name, public_key, address, created_at, removed_at FROM devices`)
	if err != nil {
		return Membership{}, fmt.Errorf("read Devices: %w", err)
	}
	defer rows.Close()

	var devices []Device
	var quarantined []QuarantinedAddress
	for rows.Next() {
		var device Device
		var address string
		var createdAt int64
		var removedAt sql.NullInt64
		if err := rows.Scan(&device.ID, &device.ProtecteeID, &device.Name,
			&device.PublicKey, &address, &createdAt, &removedAt); err != nil {
			return Membership{}, fmt.Errorf("read Devices: %w", err)
		}
		device.Address, err = netip.ParseAddr(address)
		if err != nil {
			return Membership{}, fmt.Errorf("read Device address %q: %w", address, err)
		}
		device.CreatedAt = time.Unix(createdAt, 0).UTC()
		if !removedAt.Valid {
			devices = append(devices, device)
			continue
		}
		device.RemovedAt = time.Unix(removedAt.Int64, 0).UTC()
		// An expired quarantine is not reported: its address is already back
		// in the pool, and showing a wait that has ended would read as one
		// that has not.
		if hold, held := quarantineOf(device.Address, device.RemovedAt, now); held {
			quarantined = append(quarantined, hold)
		}
	}
	if err := rows.Err(); err != nil {
		return Membership{}, fmt.Errorf("read Devices: %w", err)
	}
	// Ordered here rather than in SQL: the column is text, so the database
	// would sort 10.66.0.10 before 10.66.0.2.
	slices.SortFunc(devices, func(a, b Device) int { return a.Address.Compare(b.Address) })
	slices.SortFunc(quarantined, func(a, b QuarantinedAddress) int { return a.Address.Compare(b.Address) })

	peers, err := s.tunnel.Peers(ctx)
	if err != nil {
		return Membership{}, fmt.Errorf("read tunnel membership: %w", err)
	}
	onTunnel := make(map[string]Peer, len(peers))
	for _, peer := range peers {
		onTunnel[peer.PublicKey] = peer
	}

	membership := Membership{Devices: make([]DeviceStatus, 0, len(devices)), Quarantined: quarantined}
	recorded := make(map[string]bool, len(devices))
	for _, device := range devices {
		recorded[device.PublicKey] = true
		peer, present := onTunnel[device.PublicKey]
		membership.Devices = append(membership.Devices, DeviceStatus{
			Device: device, OnTunnel: present, LatestHandshake: peer.LatestHandshake,
		})
	}
	for _, peer := range peers {
		if !recorded[peer.PublicKey] {
			membership.UnaccountedPeers = append(membership.UnaccountedPeers, peer)
		}
	}
	return membership, nil
}

// allocateAddress returns the lowest tunnel address that is neither held by a
// live Device nor still in quarantine from a removed one. The host address is
// outside the loop's bounds rather than filtered out of it, so no change to
// allocation can ever hand a Device the tunnel's own address.
//
// Quarantine is read here rather than swept elsewhere: an address leaves
// quarantine because its window has passed, not because a job ran, so there is
// no sweep to fall behind and no state to be stale.
func allocateAddress(ctx context.Context, tx *sql.Tx, now time.Time) (netip.Addr, error) {
	inUse, quarantined, err := unavailableAddresses(ctx, tx, now)
	if err != nil {
		return netip.Addr{}, err
	}
	for octet := firstDeviceOctet; octet <= lastDeviceOctet; octet++ {
		candidate := deviceAddress(octet)
		_, held := quarantined[candidate.String()]
		if !inUse[candidate.String()] && !held {
			return candidate, nil
		}
	}
	return netip.Addr{}, exhaustionError(quarantined)
}

// unavailableAddresses splits every address a Device could not be given into
// the two reasons it could not: in use by a live Device, or still quarantined
// from a removed one. They are kept apart rather than merged into one set
// because the two have different remedies, and an Operator refused a mint has
// to be told which one they are looking at.
func unavailableAddresses(ctx context.Context, tx *sql.Tx, now time.Time) (map[string]bool, map[string]QuarantinedAddress, error) {
	rows, err := tx.QueryContext(ctx, "SELECT address, removed_at FROM devices")
	if err != nil {
		return nil, nil, fmt.Errorf("read allocated addresses: %w", err)
	}
	defer rows.Close()

	inUse := map[string]bool{}
	quarantined := map[string]QuarantinedAddress{}
	for rows.Next() {
		var address string
		var removedAt sql.NullInt64
		if err := rows.Scan(&address, &removedAt); err != nil {
			return nil, nil, fmt.Errorf("read allocated addresses: %w", err)
		}
		parsed, err := netip.ParseAddr(address)
		if err != nil {
			return nil, nil, fmt.Errorf("read allocated address %q: %w", address, err)
		}
		if !removedAt.Valid {
			inUse[address] = true
			continue
		}
		hold, held := quarantineOf(parsed, time.Unix(removedAt.Int64, 0).UTC(), now)
		if !held {
			continue
		}
		// One address accumulates ended records over a deployment's life. The
		// longest hold is the one that governs it.
		if existing, seen := quarantined[address]; !seen || hold.ReusableAt.After(existing.ReusableAt) {
			quarantined[address] = hold
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("read allocated addresses: %w", err)
	}
	return inUse, quarantined, nil
}

// exhaustionError names the exhaustion and the remedy the pool actually has.
// A pool full of live Devices is fixed by removing one; a pool whose free
// addresses are all in quarantine is fixed by waiting, and telling an Operator
// to remove a Device there would only make the wait longer.
func exhaustionError(quarantined map[string]QuarantinedAddress) error {
	earliest := time.Time{}
	for _, hold := range quarantined {
		if earliest.IsZero() || hold.ReusableAt.Before(earliest) {
			earliest = hold.ReusableAt
		}
	}
	if earliest.IsZero() {
		return fmt.Errorf("%w: remove a Device before minting another", ErrAddressPoolExhausted)
	}
	return fmt.Errorf("%w: every address a Device is not using is in quarantine; the first leaves it at %s, or remove a live Device to free one now",
		ErrAddressPoolExhausted, earliest.Format(time.RFC3339))
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
