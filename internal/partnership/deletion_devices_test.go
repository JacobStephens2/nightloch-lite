package partnership

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"testing"
	"time"
)

type recordingDeviceTunnel struct {
	peers       map[string]netip.Addr
	removeCalls []string
	addCalls    []string
	removeErr   error
}

func newRecordingDeviceTunnel() *recordingDeviceTunnel {
	return &recordingDeviceTunnel{peers: make(map[string]netip.Addr)}
}

func (t *recordingDeviceTunnel) RemovePeer(ctx context.Context, publicKey string) error {
	if t.removeErr != nil {
		return t.removeErr
	}
	t.removeCalls = append(t.removeCalls, publicKey)
	delete(t.peers, publicKey)
	return nil
}

func (t *recordingDeviceTunnel) AddPeer(ctx context.Context, publicKey string, address netip.Addr) error {
	t.addCalls = append(t.addCalls, publicKey)
	t.peers[publicKey] = address
	return nil
}

type recordingDeviceResolver struct {
	clients     map[string]netip.Addr
	removeCalls []string
	addCalls    []string
	removeErr   error
}

func newRecordingDeviceResolver() *recordingDeviceResolver {
	return &recordingDeviceResolver{clients: make(map[string]netip.Addr)}
}

func (r *recordingDeviceResolver) RemoveClient(ctx context.Context, name string) error {
	if r.removeErr != nil {
		return r.removeErr
	}
	r.removeCalls = append(r.removeCalls, name)
	delete(r.clients, name)
	return nil
}

func (r *recordingDeviceResolver) AddClient(ctx context.Context, name string, ip netip.Addr) error {
	r.addCalls = append(r.addCalls, name)
	r.clients[name] = ip
	return nil
}

func TestDeleteUserWithLiveDevicesDismantlesPeersAndClients(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	otherUser := createUser(t, db, "other@example.com")

	tunnel := newRecordingDeviceTunnel()
	resolver := newRecordingDeviceResolver()
	service.SetDeviceTeardown(tunnel, resolver)

	// Seed 2 devices for protectee
	key1 := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	addr1 := netip.MustParseAddr("10.66.0.2")
	res1, err := db.Exec(
		"INSERT INTO devices(protectee_id, name, public_key, address, created_at) VALUES (?, ?, ?, ?, ?)",
		protectee, "Laptop", key1, addr1.String(), testClock.Unix())
	if err != nil {
		t.Fatal(err)
	}
	dev1ID, _ := res1.LastInsertId()
	tunnel.peers[key1] = addr1
	resolver.clients["device-1"] = addr1

	key2 := "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB="
	addr2 := netip.MustParseAddr("10.66.0.3")
	res2, err := db.Exec(
		"INSERT INTO devices(protectee_id, name, public_key, address, created_at) VALUES (?, ?, ?, ?, ?)",
		protectee, "Phone", key2, addr2.String(), testClock.Unix())
	if err != nil {
		t.Fatal(err)
	}
	dev2ID, _ := res2.LastInsertId()
	tunnel.peers[key2] = addr2
	resolver.clients["device-2"] = addr2

	// Seed 1 device for otherUser (should survive)
	keyOther := "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC="
	addrOther := netip.MustParseAddr("10.66.0.4")
	if _, err := db.Exec(
		"INSERT INTO devices(protectee_id, name, public_key, address, created_at) VALUES (?, ?, ?, ?, ?)",
		otherUser, "Other Device", keyOther, addrOther.String(), testClock.Unix()); err != nil {
		t.Fatal(err)
	}
	tunnel.peers[keyOther] = addrOther
	resolver.clients["device-3"] = addrOther

	service.now = func() time.Time { return deletionClock }
	deletion, err := service.DeleteUser(context.Background(), protectee)
	if err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if deletion.DismantledDevices != 2 {
		t.Fatalf("deletion.DismantledDevices = %d, want 2", deletion.DismantledDevices)
	}

	// Verify tunnel removals
	if len(tunnel.removeCalls) != 2 {
		t.Fatalf("tunnel.removeCalls = %v, want 2 calls", tunnel.removeCalls)
	}
	if _, exists := tunnel.peers[key1]; exists {
		t.Fatalf("peer %s survived deletion in tunnel", key1)
	}
	if _, exists := tunnel.peers[key2]; exists {
		t.Fatalf("peer %s survived deletion in tunnel", key2)
	}
	if _, exists := tunnel.peers[keyOther]; !exists {
		t.Fatalf("unrelated peer %s was removed from tunnel", keyOther)
	}

	// Verify resolver removals
	if len(resolver.removeCalls) != 2 {
		t.Fatalf("resolver.removeCalls = %v, want 2 calls", resolver.removeCalls)
	}
	if _, exists := resolver.clients["device-1"]; exists {
		t.Fatalf("client device-1 survived deletion in resolver")
	}
	if _, exists := resolver.clients["device-2"]; exists {
		t.Fatalf("client device-2 survived deletion in resolver")
	}
	if _, exists := resolver.clients["device-3"]; !exists {
		t.Fatalf("unrelated client device-3 was removed from resolver")
	}

	// Verify database state: removed_at set to deletionClock.Unix()
	var removedAt1, removedAt2 sql.NullInt64
	if err := db.QueryRow("SELECT removed_at FROM devices WHERE id = ?", dev1ID).Scan(&removedAt1); err != nil {
		t.Fatal(err)
	}
	if !removedAt1.Valid || removedAt1.Int64 != deletionClock.Unix() {
		t.Fatalf("device 1 removed_at = %v, want %d", removedAt1, deletionClock.Unix())
	}
	if err := db.QueryRow("SELECT removed_at FROM devices WHERE id = ?", dev2ID).Scan(&removedAt2); err != nil {
		t.Fatal(err)
	}
	if !removedAt2.Valid || removedAt2.Int64 != deletionClock.Unix() {
		t.Fatalf("device 2 removed_at = %v, want %d", removedAt2, deletionClock.Unix())
	}

	// Verify otherUser's device is still live
	var otherRemovedAt sql.NullInt64
	if err := db.QueryRow("SELECT removed_at FROM devices WHERE public_key = ?", keyOther).Scan(&otherRemovedAt); err != nil {
		t.Fatal(err)
	}
	if otherRemovedAt.Valid {
		t.Fatalf("other user's device was marked removed: %v", otherRemovedAt)
	}
}

func TestDeleteUserRollsBackDismantledDevicesOnTunnelFailure(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")

	tunnel := newRecordingDeviceTunnel()
	resolver := newRecordingDeviceResolver()
	tunnel.removeErr = errors.New("tunnel withdrawal error")
	service.SetDeviceTeardown(tunnel, resolver)

	key1 := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	addr1 := netip.MustParseAddr("10.66.0.2")
	if _, err := db.Exec(
		"INSERT INTO devices(protectee_id, name, public_key, address, created_at) VALUES (?, ?, ?, ?, ?)",
		protectee, "Laptop", key1, addr1.String(), testClock.Unix()); err != nil {
		t.Fatal(err)
	}
	tunnel.peers[key1] = addr1
	resolver.clients["device-1"] = addr1

	_, err := service.DeleteUser(context.Background(), protectee)
	if err == nil {
		t.Fatal("expected DeleteUser to fail on tunnel error, but it succeeded")
	}

	// Device row in DB must still be live (transaction rolled back)
	var removedAt sql.NullInt64
	if err := db.QueryRow("SELECT removed_at FROM devices WHERE public_key = ?", key1).Scan(&removedAt); err != nil {
		t.Fatal(err)
	}
	if removedAt.Valid {
		t.Fatalf("device row was marked removed despite transaction failure: %v", removedAt)
	}

	// Resolver client must have been re-added during rollback
	if _, exists := resolver.clients["device-1"]; !exists {
		t.Fatal("resolver client was not restored during rollback")
	}
}

func TestDeleteUserRollsBackDismantledDevicesOnResolverFailure(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")

	tunnel := newRecordingDeviceTunnel()
	resolver := newRecordingDeviceResolver()
	resolver.removeErr = errors.New("resolver error")
	service.SetDeviceTeardown(tunnel, resolver)

	key1 := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	addr1 := netip.MustParseAddr("10.66.0.2")
	if _, err := db.Exec(
		"INSERT INTO devices(protectee_id, name, public_key, address, created_at) VALUES (?, ?, ?, ?, ?)",
		protectee, "Laptop", key1, addr1.String(), testClock.Unix()); err != nil {
		t.Fatal(err)
	}

	_, err := service.DeleteUser(context.Background(), protectee)
	if err == nil {
		t.Fatal("expected DeleteUser to fail on resolver error, but it succeeded")
	}

	var removedAt sql.NullInt64
	if err := db.QueryRow("SELECT removed_at FROM devices WHERE public_key = ?", key1).Scan(&removedAt); err != nil {
		t.Fatal(err)
	}
	if removedAt.Valid {
		t.Fatalf("device row was marked removed despite transaction failure: %v", removedAt)
	}
}

func TestDeleteUserWithoutConfiguredTeardownFailsIfDevicesExist(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")

	key1 := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	addr1 := netip.MustParseAddr("10.66.0.2")
	if _, err := db.Exec(
		"INSERT INTO devices(protectee_id, name, public_key, address, created_at) VALUES (?, ?, ?, ?, ?)",
		protectee, "Laptop", key1, addr1.String(), testClock.Unix()); err != nil {
		t.Fatal(err)
	}

	// service has no teardown configured
	_, err := service.DeleteUser(context.Background(), protectee)
	if err == nil {
		t.Fatal("expected DeleteUser to fail when live devices exist but teardown is not configured")
	}

	var removedAt sql.NullInt64
	if err := db.QueryRow("SELECT removed_at FROM devices WHERE public_key = ?", key1).Scan(&removedAt); err != nil {
		t.Fatal(err)
	}
	if removedAt.Valid {
		t.Fatalf("device was removed despite unconfigured teardown: %v", removedAt)
	}
}

func TestDeleteUserWithoutLiveDevicesSucceedsEvenIfTeardownNotConfigured(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")

	// No devices seeded, no teardown configured
	deletion, err := service.DeleteUser(context.Background(), protectee)
	if err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if deletion.DismantledDevices != 0 {
		t.Fatalf("deletion.DismantledDevices = %d, want 0", deletion.DismantledDevices)
	}
}

func TestDeleteUserRepeatIsIdempotentWithDevices(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")

	tunnel := newRecordingDeviceTunnel()
	resolver := newRecordingDeviceResolver()
	service.SetDeviceTeardown(tunnel, resolver)

	key1 := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	addr1 := netip.MustParseAddr("10.66.0.2")
	if _, err := db.Exec(
		"INSERT INTO devices(protectee_id, name, public_key, address, created_at) VALUES (?, ?, ?, ?, ?)",
		protectee, "Laptop", key1, addr1.String(), testClock.Unix()); err != nil {
		t.Fatal(err)
	}
	tunnel.peers[key1] = addr1
	resolver.clients["device-1"] = addr1

	first, err := service.DeleteUser(context.Background(), protectee)
	if err != nil {
		t.Fatalf("first DeleteUser: %v", err)
	}
	if first.AlreadyDeleted || first.DismantledDevices != 1 {
		t.Fatalf("first = %+v", first)
	}

	second, err := service.DeleteUser(context.Background(), protectee)
	if err != nil {
		t.Fatalf("second DeleteUser: %v", err)
	}
	if !second.AlreadyDeleted {
		t.Fatalf("second = %+v, want AlreadyDeleted", second)
	}
	// No additional calls to tunnel or resolver
	if len(tunnel.removeCalls) != 1 {
		t.Fatalf("tunnel.removeCalls = %v, want exactly 1 call across both deletions", tunnel.removeCalls)
	}
	if len(resolver.removeCalls) != 1 {
		t.Fatalf("resolver.removeCalls = %v, want exactly 1 call across both deletions", resolver.removeCalls)
	}
}

func TestDeleteUserRollbackOnSettleFailureReAddsPeersAndClients(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")

	tunnel := newRecordingDeviceTunnel()
	resolver := newRecordingDeviceResolver()
	service.SetDeviceTeardown(tunnel, resolver)

	key1 := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	addr1 := netip.MustParseAddr("10.66.0.2")
	if _, err := db.Exec(
		"INSERT INTO devices(protectee_id, name, public_key, address, created_at) VALUES (?, ?, ?, ?, ?)",
		protectee, "Laptop", key1, addr1.String(), testClock.Unix()); err != nil {
		t.Fatal(err)
	}
	tunnel.peers[key1] = addr1
	resolver.clients["device-1"] = addr1

	// Seed an activity entry so settleForDeletion will attempt to delete it
	if _, err := db.Exec(
		`INSERT INTO activity_entries(protectee_id, action, occurred_at, details) VALUES (?, 'device_enrolled', ?, '{}')`,
		protectee, testClock.Unix()); err != nil {
		t.Fatal(err)
	}

	// Install a trigger that aborts deletion from activity_entries, causing settleForDeletion to fail
	if _, err := db.Exec(`CREATE TRIGGER fail_activity_delete BEFORE DELETE ON activity_entries BEGIN SELECT RAISE(ABORT, 'forced activity delete failure'); END;`); err != nil {
		t.Fatal(err)
	}

	_, err := service.DeleteUser(context.Background(), protectee)
	if err == nil {
		t.Fatal("DeleteUser expected error due to trigger failure, got nil")
	}

	// Peer and client should have been re-added via rollbackDismantled
	if len(tunnel.addCalls) != 1 || tunnel.addCalls[0] != key1 {
		t.Fatalf("tunnel.addCalls = %v, want [%s] (re-added on rollback)", tunnel.addCalls, key1)
	}
	if _, ok := tunnel.peers[key1]; !ok {
		t.Fatalf("tunnel peer was not restored in peer map")
	}

	if len(resolver.addCalls) != 1 || resolver.addCalls[0] != "device-1" {
		t.Fatalf("resolver.addCalls = %v, want [device-1] (re-added on rollback)", resolver.addCalls)
	}
	if _, ok := resolver.clients["device-1"]; !ok {
		t.Fatalf("resolver client was not restored in client map")
	}

	// DB row should be rolled back: removed_at is still NULL
	var removedAt sql.NullInt64
	if err := db.QueryRow("SELECT removed_at FROM devices WHERE id = 1").Scan(&removedAt); err != nil {
		t.Fatal(err)
	}
	if removedAt.Valid {
		t.Fatalf("device row removed_at was set despite failed transaction: %v", removedAt)
	}
}
