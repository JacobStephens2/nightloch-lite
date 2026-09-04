package enforcement

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/JacobStephens2/nightloch-lite/internal/database"
	"github.com/JacobStephens2/nightloch-lite/internal/partnership"
)

func newEnrollTestEnv(t *testing.T) (*Service, *RecordingTunnel, *RecordingResolver, *sql.DB) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "nightloch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := database.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	tunnel := &RecordingTunnel{}
	resolver := EnforcingResolver()
	service, err := NewService(db, tunnel, resolver, testSettings())
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return testClock }
	baseURL, err := url.Parse("https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	service.SetAuthorizer(partnership.NewService(db, baseURL))
	return service, tunnel, resolver, db
}

func createPartnership(t *testing.T, db *sql.DB, protecteeID, partnerID int64) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO partnerships(protectee_id, partner_id, edge_type, log_sharing, accepted_at)
		VALUES (?, ?, 'peer', 0, unixepoch())`, protecteeID, partnerID)
	if err != nil {
		t.Fatal(err)
	}
}

func TestEnrollDeviceMintsAndRegistersClient(t *testing.T) {
	t.Parallel()
	service, tunnel, resolver, db := newEnrollTestEnv(t)
	protecteeID := createUser(t, db, "protectee@example.com")

	minted, err := service.EnrollDevice(context.Background(), protecteeID, "Phone")
	if err != nil {
		t.Fatalf("EnrollDevice: %v", err)
	}

	if minted.Device.Name != "Phone" {
		t.Errorf("Device name = %q, want Phone", minted.Device.Name)
	}
	if minted.Device.Address.String() != "10.66.0.2" {
		t.Errorf("Device address = %s, want 10.66.0.2", minted.Device.Address)
	}
	if minted.Configuration == "" {
		t.Errorf("Configuration is empty")
	}

	clientName := DeviceClientName(minted.Device.ID)
	if want := "device-1"; clientName != want {
		t.Errorf("client name = %q, want %q", clientName, want)
	}

	if !resolver.HasClient(clientName) {
		t.Errorf("resolver does not have client %q", clientName)
	}
	wantResolverCall := "add-client " + clientName + " 10.66.0.2"
	if len(resolver.Calls) != 1 || resolver.Calls[0] != wantResolverCall {
		t.Errorf("resolver calls = %v, want [%s]", resolver.Calls, wantResolverCall)
	}

	wantTunnelCall := "add-peer " + minted.Device.PublicKey + " 10.66.0.2"
	if len(tunnel.Calls) != 1 || tunnel.Calls[0] != wantTunnelCall {
		t.Errorf("tunnel calls = %v, want [%s]", tunnel.Calls, wantTunnelCall)
	}
}

func TestEnrollDeviceEnforcesQuotaOfFive(t *testing.T) {
	t.Parallel()
	service, _, _, db := newEnrollTestEnv(t)
	protecteeID := createUser(t, db, "protectee@example.com")

	for i := 1; i <= MaxDevicesPerProtectee; i++ {
		name := "Device " + string(rune('A'+i-1))
		if _, err := service.EnrollDevice(context.Background(), protecteeID, name); err != nil {
			t.Fatalf("enroll device %d (%s): %v", i, name, err)
		}
	}

	// Attempting a 6th live device must be refused with ErrDeviceQuotaExceeded
	_, err := service.EnrollDevice(context.Background(), protecteeID, "Device 6")
	if !errors.Is(err, ErrDeviceQuotaExceeded) {
		t.Fatalf("6th device error = %v, want ErrDeviceQuotaExceeded", err)
	}

	// Removing one live device allows enrolling again
	var firstID int64
	err = db.QueryRow("SELECT id FROM devices WHERE protectee_id = ? AND removed_at IS NULL ORDER BY id LIMIT 1", protecteeID).Scan(&firstID)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.RemoveDeviceByID(context.Background(), protecteeID, firstID); err != nil {
		t.Fatalf("RemoveDeviceByID: %v", err)
	}

	// Now 5th can be enrolled
	if _, err := service.EnrollDevice(context.Background(), protecteeID, "Replacement"); err != nil {
		t.Fatalf("enroll after removal: %v", err)
	}
}

func TestEnrollDeviceRollbackOnWireGuardFailure(t *testing.T) {
	t.Parallel()
	service, tunnel, resolver, db := newEnrollTestEnv(t)
	protecteeID := createUser(t, db, "protectee@example.com")

	tunnel.AddPeerError = errors.New("wg helper failed")

	_, err := service.EnrollDevice(context.Background(), protecteeID, "Phone")
	if err == nil {
		t.Fatal("EnrollDevice succeeded, want error")
	}

	// WireGuard failure must leave no DB row and call no AGH client creation
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM devices").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("devices count = %d, want 0", count)
	}
	if len(resolver.Calls) != 0 {
		t.Errorf("resolver calls = %v, want none", resolver.Calls)
	}
}

func TestEnrollDeviceRollbackOnResolverFailure(t *testing.T) {
	t.Parallel()
	service, tunnel, resolver, db := newEnrollTestEnv(t)
	protecteeID := createUser(t, db, "protectee@example.com")

	resolver.AddClientError = errors.New("resolver unreachable")

	_, err := service.EnrollDevice(context.Background(), protecteeID, "Phone")
	if err == nil {
		t.Fatal("EnrollDevice succeeded, want error")
	}

	// Resolver failure must withdraw the WireGuard peer and roll back the DB row
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM devices").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("devices count = %d, want 0", count)
	}

	// Tunnel calls should have add-peer followed by remove-peer
	if len(tunnel.Calls) != 2 || tunnel.Calls[0][:8] != "add-peer" || tunnel.Calls[1][:11] != "remove-peer" {
		t.Errorf("tunnel calls = %v, want [add-peer ..., remove-peer ...]", tunnel.Calls)
	}
}

func TestRemoveDeviceByIDByProtecteeAndPartner(t *testing.T) {
	t.Parallel()
	service, tunnel, resolver, db := newEnrollTestEnv(t)
	protecteeID := createUser(t, db, "protectee@example.com")
	partnerID := createUser(t, db, "partner@example.com")
	createPartnership(t, db, protecteeID, partnerID)

	dev1, err := service.EnrollDevice(context.Background(), protecteeID, "Dev1")
	if err != nil {
		t.Fatal(err)
	}
	dev2, err := service.EnrollDevice(context.Background(), protecteeID, "Dev2")
	if err != nil {
		t.Fatal(err)
	}

	tunnel.Calls = nil
	resolver.Calls = nil

	// Protectee removes Dev1
	removed1, err := service.RemoveDeviceByID(context.Background(), protecteeID, dev1.Device.ID)
	if err != nil {
		t.Fatalf("Protectee RemoveDeviceByID: %v", err)
	}
	if removed1.Device.ID != dev1.Device.ID {
		t.Errorf("removed ID = %d, want %d", removed1.Device.ID, dev1.Device.ID)
	}
	if !removed1.Device.RemovedAt.Equal(testClock) {
		t.Errorf("removedAt = %v, want %v", removed1.Device.RemovedAt, testClock)
	}
	if resolver.HasClient(DeviceClientName(dev1.Device.ID)) {
		t.Errorf("resolver still has dev1 client")
	}
	if want := "remove-client " + DeviceClientName(dev1.Device.ID); resolver.Calls[0] != want {
		t.Errorf("resolver calls = %v, want first %q", resolver.Calls, want)
	}
	if want := "remove-peer " + dev1.Device.PublicKey; tunnel.Calls[0] != want {
		t.Errorf("tunnel calls = %v, want first %q", tunnel.Calls, want)
	}

	tunnel.Calls = nil
	resolver.Calls = nil

	// Active Partner removes Dev2
	removed2, err := service.RemoveDeviceByID(context.Background(), partnerID, dev2.Device.ID)
	if err != nil {
		t.Fatalf("Partner RemoveDeviceByID: %v", err)
	}
	if removed2.Device.ID != dev2.Device.ID {
		t.Errorf("removed ID = %d, want %d", removed2.Device.ID, dev2.Device.ID)
	}
	if resolver.HasClient(DeviceClientName(dev2.Device.ID)) {
		t.Errorf("resolver still has dev2 client")
	}
}

func TestRemoveDeviceByIDDeniesUnauthorizedActors(t *testing.T) {
	t.Parallel()
	service, _, _, db := newEnrollTestEnv(t)
	protecteeID := createUser(t, db, "protectee@example.com")
	strangerID := createUser(t, db, "stranger@example.com")

	dev, err := service.EnrollDevice(context.Background(), protecteeID, "Dev")
	if err != nil {
		t.Fatal(err)
	}

	// Stranger is neither protectee nor active partner
	_, err = service.RemoveDeviceByID(context.Background(), strangerID, dev.Device.ID)
	if !errors.Is(err, ErrUnauthorizedRemoval) {
		t.Fatalf("stranger removal error = %v, want ErrUnauthorizedRemoval", err)
	}

	// Unknown actor ID
	_, err = service.RemoveDeviceByID(context.Background(), 99999, dev.Device.ID)
	if !errors.Is(err, ErrUnauthorizedRemoval) {
		t.Fatalf("unknown actor removal error = %v, want ErrUnauthorizedRemoval", err)
	}

	// Already removed device
	_, err = service.RemoveDeviceByID(context.Background(), protecteeID, dev.Device.ID)
	if err != nil {
		t.Fatalf("first removal: %v", err)
	}
	_, err = service.RemoveDeviceByID(context.Background(), protecteeID, dev.Device.ID)
	if !errors.Is(err, ErrUnknownDevice) {
		t.Fatalf("second removal error = %v, want ErrUnknownDevice", err)
	}
}

func TestRemoveDeviceAddressQuarantined(t *testing.T) {
	t.Parallel()
	service, _, _, db := newEnrollTestEnv(t)
	protecteeID := createUser(t, db, "protectee@example.com")

	dev, err := service.EnrollDevice(context.Background(), protecteeID, "Dev1")
	if err != nil {
		t.Fatal(err)
	}
	firstAddr := dev.Device.Address

	// Remove it
	removed, err := service.RemoveDeviceByID(context.Background(), protecteeID, dev.Device.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := ReusableAt(testClock); !removed.ReusableAt.Equal(want) {
		t.Errorf("ReusableAt = %v, want %v", removed.ReusableAt, want)
	}

	// Next enrolled device must skip the quarantined address and receive the next IP (10.66.0.3)
	nextDev, err := service.EnrollDevice(context.Background(), protecteeID, "Dev2")
	if err != nil {
		t.Fatal(err)
	}
	if nextDev.Device.Address == firstAddr {
		t.Errorf("next device got quarantined address %s", firstAddr)
	}
	if nextDev.Device.Address.String() != "10.66.0.3" {
		t.Errorf("next device address = %s, want 10.66.0.3", nextDev.Device.Address)
	}
}

func TestMintDeviceAndRemoveDeviceMaintainAGHClientConsistency(t *testing.T) {
	t.Parallel()
	service, tunnel, resolver, db := newEnrollTestEnv(t)
	createUser(t, db, "protectee@example.com")

	minted, err := service.MintDevice(context.Background(), MintCommand{
		ProtecteeEmail: "protectee@example.com",
		DeviceName:     "Laptop",
	})
	if err != nil {
		t.Fatalf("MintDevice: %v", err)
	}

	clientName := DeviceClientName(minted.Device.ID)
	if !resolver.HasClient(clientName) {
		t.Errorf("resolver missing client %s after MintDevice", clientName)
	}

	removed, err := service.RemoveDevice(context.Background(), RemoveCommand{
		ProtecteeEmail: "protectee@example.com",
		DeviceName:     "Laptop",
	})
	if err != nil {
		t.Fatalf("RemoveDevice: %v", err)
	}
	if removed.Device.ID != minted.Device.ID {
		t.Errorf("removed ID = %d, want %d", removed.Device.ID, minted.Device.ID)
	}
	if resolver.HasClient(clientName) {
		t.Errorf("resolver still has client %s after RemoveDevice", clientName)
	}
	_ = tunnel
}

func TestRemoveDeviceByIDNilAuthorizerDeniesClosed(t *testing.T) {
	t.Parallel()
	service, _, _, db := newEnrollTestEnv(t)
	service.SetAuthorizer(nil)
	protecteeID := createUser(t, db, "protectee@example.com")
	dev, err := service.EnrollDevice(context.Background(), protecteeID, "Dev")
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.RemoveDeviceByID(context.Background(), protecteeID, dev.Device.ID)
	if !errors.Is(err, ErrUnauthorizedRemoval) {
		t.Fatalf("nil authorizer error = %v, want ErrUnauthorizedRemoval", err)
	}
}

func TestRemoveDeviceByIDAuthorizerFuncErrorPropagates(t *testing.T) {
	t.Parallel()
	service, _, _, db := newEnrollTestEnv(t)
	expectedErr := errors.New("authorization check failed")
	service.SetAuthorizer(DeviceAuthorizerFunc(func(ctx context.Context, actorID, protecteeID int64) (bool, error) {
		return false, expectedErr
	}))
	protecteeID := createUser(t, db, "protectee@example.com")
	dev, err := service.EnrollDevice(context.Background(), protecteeID, "Dev")
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.RemoveDeviceByID(context.Background(), protecteeID, dev.Device.ID)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("authorizer error = %v, want %v", err, expectedErr)
	}
}

func TestDevicesForProtectee(t *testing.T) {
	t.Parallel()
	service, _, _, db := newEnrollTestEnv(t)
	ctx := context.Background()

	protectee1 := createUser(t, db, "protectee1@example.com")
	protectee2 := createUser(t, db, "protectee2@example.com")

	dev1, err := service.EnrollDevice(ctx, protectee1, "Phone")
	if err != nil {
		t.Fatal(err)
	}
	dev2, err := service.EnrollDevice(ctx, protectee1, "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.EnrollDevice(ctx, protectee2, "Tablet")
	if err != nil {
		t.Fatal(err)
	}

	// Initial list: protectee1 has 2 devices
	devices, err := service.DevicesForProtectee(ctx, protectee1)
	if err != nil {
		t.Fatalf("DevicesForProtectee: %v", err)
	}
	if len(devices) != 2 {
		t.Fatalf("got %d devices, want 2", len(devices))
	}
	if devices[0].Name != "Phone" || devices[1].Name != "Laptop" {
		t.Fatalf("got devices %v, want Phone and Laptop", devices)
	}

	// After removing dev1, only dev2 remains
	if _, err := service.RemoveDeviceByID(ctx, protectee1, dev1.Device.ID); err != nil {
		t.Fatal(err)
	}
	devices, err = service.DevicesForProtectee(ctx, protectee1)
	if err != nil {
		t.Fatalf("DevicesForProtectee after removal: %v", err)
	}
	if len(devices) != 1 || devices[0].ID != dev2.Device.ID {
		t.Fatalf("got %v, want only dev2", devices)
	}

	// Invalid or 0 protectee ID returns empty
	empty, err := service.DevicesForProtectee(ctx, 0)
	if err != nil || len(empty) != 0 {
		t.Fatalf("DevicesForProtectee(0) = %v, %v, want empty nil", empty, err)
	}
}

func TestLiveDeviceByIDAndAuthorizeRemoval(t *testing.T) {
	t.Parallel()
	service, _, _, db := newEnrollTestEnv(t)
	ctx := context.Background()

	protectee := createUser(t, db, "protectee@example.com")
	partner := createUser(t, db, "partner@example.com")
	stranger := createUser(t, db, "stranger@example.com")
	createPartnership(t, db, protectee, partner)

	dev, err := service.EnrollDevice(ctx, protectee, "Work Laptop")
	if err != nil {
		t.Fatal(err)
	}

	// LiveDeviceByID finds it
	found, err := service.LiveDeviceByID(ctx, dev.Device.ID)
	if err != nil {
		t.Fatalf("LiveDeviceByID: %v", err)
	}
	if found.ID != dev.Device.ID || found.Name != "Work Laptop" {
		t.Fatalf("found = %+v, want dev", found)
	}

	// AuthorizeRemoval allows protectee
	authedDev, err := service.AuthorizeRemoval(ctx, protectee, dev.Device.ID)
	if err != nil {
		t.Fatalf("AuthorizeRemoval protectee: %v", err)
	}
	if authedDev.ID != dev.Device.ID {
		t.Fatalf("authedDev.ID = %d, want %d", authedDev.ID, dev.Device.ID)
	}

	// AuthorizeRemoval allows active partner
	authedDev, err = service.AuthorizeRemoval(ctx, partner, dev.Device.ID)
	if err != nil {
		t.Fatalf("AuthorizeRemoval partner: %v", err)
	}
	if authedDev.ID != dev.Device.ID {
		t.Fatalf("authedDev.ID = %d, want %d", authedDev.ID, dev.Device.ID)
	}

	// AuthorizeRemoval refuses stranger
	_, err = service.AuthorizeRemoval(ctx, stranger, dev.Device.ID)
	if !errors.Is(err, ErrUnauthorizedRemoval) {
		t.Fatalf("stranger err = %v, want ErrUnauthorizedRemoval", err)
	}

	// AuthorizeRemoval refuses invalid IDs
	_, err = service.AuthorizeRemoval(ctx, 0, dev.Device.ID)
	if !errors.Is(err, ErrUnauthorizedRemoval) {
		t.Fatalf("0 actor err = %v, want ErrUnauthorizedRemoval", err)
	}
	_, err = service.AuthorizeRemoval(ctx, protectee, 0)
	if !errors.Is(err, ErrUnauthorizedRemoval) {
		t.Fatalf("0 device err = %v, want ErrUnauthorizedRemoval", err)
	}

	// Remove device
	if _, err := service.RemoveDeviceByID(ctx, protectee, dev.Device.ID); err != nil {
		t.Fatal(err)
	}

	// LiveDeviceByID returns ErrUnknownDevice
	_, err = service.LiveDeviceByID(ctx, dev.Device.ID)
	if !errors.Is(err, ErrUnknownDevice) {
		t.Fatalf("removed LiveDeviceByID err = %v, want ErrUnknownDevice", err)
	}

	// AuthorizeRemoval returns ErrUnknownDevice for removed device
	_, err = service.AuthorizeRemoval(ctx, protectee, dev.Device.ID)
	if !errors.Is(err, ErrUnknownDevice) {
		t.Fatalf("removed AuthorizeRemoval err = %v, want ErrUnknownDevice", err)
	}
}
