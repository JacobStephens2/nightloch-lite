package enforcement

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

// Removing a Device is the other half of minting, and address quarantine is
// what makes the per-Protectee log isolation of a later slice sound rather
// than hopeful: a tunnel address is the security principal AdGuard Home keys
// its client rules and its query log on, so handing one to a new occupant
// while the previous occupant's queries are still retained would disclose one
// Protectee's browsing to another.

func remove(t *testing.T, service *Service, email, name string) RemovedDevice {
	t.Helper()
	removed, err := service.RemoveDevice(context.Background(), RemoveCommand{
		ProtecteeEmail: email, DeviceName: name,
	})
	if err != nil {
		t.Fatalf("RemoveDevice(%q, %q): %v", email, name, err)
	}
	return removed
}

// fillPoolAbove takes every assignable address from octet upwards, so a test
// about the one address below it is a test about that address rather than
// about a pool with 252 alternatives.
func fillPoolAbove(t *testing.T, db *sql.DB, protecteeID int64, octet int) {
	t.Helper()
	for filler := octet; filler <= lastDeviceOctet; filler++ {
		if _, err := db.Exec(
			`INSERT INTO devices(protectee_id, name, public_key, address, created_at)
			 VALUES (?, ?, ?, ?, unixepoch())`,
			protecteeID, "filler-"+deviceAddress(filler).String(),
			fillerKey(filler), deviceAddress(filler).String()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRemoveDeviceEndsTheRecordAndWithdrawsTheTunnelPeer(t *testing.T) {
	t.Parallel()
	service, tunnel, db := newTestService(t)
	createUser(t, db, "protectee@example.com")
	minted := mint(t, service, "protectee@example.com", "Pixel")
	tunnel.Calls = nil

	removed := remove(t, service, " Protectee@Example.com ", "  pixel  ")

	if removed.Device.ID != minted.Device.ID || removed.Device.Address != minted.Device.Address {
		t.Fatalf("removed Device = %+v, want the minted one", removed.Device)
	}
	if !removed.Device.RemovedAt.Equal(testClock) {
		t.Fatalf("removed at = %s, want %s", removed.Device.RemovedAt, testClock)
	}
	assertCalls(t, tunnel, []string{"remove-peer " + minted.Device.PublicKey})

	var removedAt sql.NullInt64
	if err := db.QueryRow("SELECT removed_at FROM devices WHERE id = ?", minted.Device.ID).Scan(&removedAt); err != nil {
		t.Fatal(err)
	}
	if !removedAt.Valid || removedAt.Int64 != testClock.Unix() {
		t.Fatalf("recorded removal instant = %+v, want %d", removedAt, testClock.Unix())
	}

	// The record has ended: a removed Device is not one of the Protectee's
	// Devices any more, whatever the row still holds for quarantine.
	membership, err := service.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(membership.Devices) != 0 {
		t.Fatalf("removed Device is still listed: %+v", membership.Devices)
	}
	if len(membership.UnaccountedPeers) != 0 {
		t.Fatalf("removal left membership no record explains: %+v", membership.UnaccountedPeers)
	}
}

func TestARemovedDevicesAddressIsNotReallocatedWhileQuarantined(t *testing.T) {
	t.Parallel()
	service, _, db := newTestService(t)
	createUser(t, db, "protectee@example.com")
	first := mint(t, service, "protectee@example.com", "Pixel")
	remove(t, service, "protectee@example.com", "Pixel")

	next := mint(t, service, "protectee@example.com", "Laptop")

	if next.Device.Address == first.Device.Address {
		t.Fatalf("a quarantined address %s was handed straight to the next Device", next.Device.Address)
	}
	if got := next.Device.Address.String(); got != "10.66.0.3" {
		t.Fatalf("next address = %s, want the lowest unquarantined address 10.66.0.3", got)
	}
}

func TestTheQuarantineWindowOutlastsTheQueryLogRetention(t *testing.T) {
	t.Parallel()
	if QuarantineWindow <= QueryLogRetention {
		t.Fatalf("quarantine window %s does not outlast query-log retention %s; a reused address could resolve to a prior occupant's retained queries",
			QuarantineWindow, QueryLogRetention)
	}
}

func TestQuarantineExpiryIsDerivedFromTheRemovalInstantRatherThanStored(t *testing.T) {
	t.Parallel()
	service, _, db := newTestService(t)
	createUser(t, db, "protectee@example.com")
	mint(t, service, "protectee@example.com", "Pixel")
	removed := remove(t, service, "protectee@example.com", "Pixel")

	if want := testClock.Add(QuarantineWindow); !removed.ReusableAt.Equal(want) {
		t.Fatalf("reusable at = %s, want %s", removed.ReusableAt, want)
	}

	// Nothing but the removal instant is recorded, so no second copy of the
	// expiry exists to drift from the retention actually in force.
	rows, err := db.Query("SELECT name FROM pragma_table_info('devices')")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(column, "quarantine") || strings.Contains(column, "reusable") || strings.Contains(column, "expir") {
			t.Fatalf("devices holds a stored quarantine expiry in column %q", column)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	// Moving the removal instant moves the whole window with it, which is what
	// "derived" has to mean to be worth anything. The record is aged wholesale,
	// as one written a window ago would be: a removal cannot predate the
	// minting it ends, and the schema says so.
	earlier := testClock.Add(-QuarantineWindow)
	if _, err := db.Exec(
		"UPDATE devices SET created_at = ?, removed_at = ? WHERE address = '10.66.0.2'",
		earlier.Add(-time.Hour).Unix(), earlier.Unix()); err != nil {
		t.Fatal(err)
	}
	if got := mint(t, service, "protectee@example.com", "Laptop").Device.Address.String(); got != "10.66.0.2" {
		t.Fatalf("address = %s, want 10.66.0.2 once its derived window had passed", got)
	}
}

func TestAQuarantinedAddressBecomesAllocatableAtTheExactInstantItsWindowPasses(t *testing.T) {
	t.Parallel()
	service, _, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	mint(t, service, "protectee@example.com", "Pixel")
	fillPoolAbove(t, db, protectee, firstDeviceOctet+1)
	removed := remove(t, service, "protectee@example.com", "Pixel")

	// The boundary is asserted against the injected clock at the exact second
	// on either side of it. Nothing here sleeps.
	service.now = func() time.Time { return removed.ReusableAt.Add(-time.Second) }
	_, err := service.MintDevice(context.Background(), MintCommand{
		ProtecteeEmail: "protectee@example.com", DeviceName: "Laptop",
	})
	if !errors.Is(err, ErrAddressPoolExhausted) {
		t.Fatalf("error one second before the window passes = %v, want ErrAddressPoolExhausted", err)
	}

	service.now = func() time.Time { return removed.ReusableAt }
	again, err := service.MintDevice(context.Background(), MintCommand{
		ProtecteeEmail: "protectee@example.com", DeviceName: "Laptop",
	})
	if err != nil {
		t.Fatalf("MintDevice at the instant the window passes: %v", err)
	}
	if again.Device.Address != removed.Device.Address {
		t.Fatalf("address = %s, want the released %s", again.Device.Address, removed.Device.Address)
	}
}

func TestMintingNamesTheExhaustionAndTheRemedyItActuallyHas(t *testing.T) {
	t.Parallel()
	service, _, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	mint(t, service, "protectee@example.com", "Pixel")
	fillPoolAbove(t, db, protectee, firstDeviceOctet+1)

	// A pool full of live Devices has one remedy: remove one.
	_, err := service.MintDevice(context.Background(), MintCommand{
		ProtecteeEmail: "protectee@example.com", DeviceName: "Laptop",
	})
	if !errors.Is(err, ErrAddressPoolExhausted) {
		t.Fatalf("error = %v, want ErrAddressPoolExhausted", err)
	}
	if !strings.Contains(err.Error(), "remove a Device") {
		t.Fatalf("a pool full of live Devices did not name its remedy: %v", err)
	}

	// A pool whose only free address is quarantined has a different one, and
	// saying "remove a Device" there would send an Operator to remove one more
	// Device and make the wait longer.
	removed := remove(t, service, "protectee@example.com", "Pixel")
	_, err = service.MintDevice(context.Background(), MintCommand{
		ProtecteeEmail: "protectee@example.com", DeviceName: "Laptop",
	})
	if !errors.Is(err, ErrAddressPoolExhausted) {
		t.Fatalf("error = %v, want ErrAddressPoolExhausted", err)
	}
	if !strings.Contains(err.Error(), "quarantine") || !strings.Contains(err.Error(), removed.ReusableAt.Format(time.RFC3339)) {
		t.Fatalf("a quarantined pool did not name the wait and when it ends: %v", err)
	}
}

func TestRemovingAnAlreadyRemovedDeviceIsRefusedWithoutASecondHelperCall(t *testing.T) {
	t.Parallel()
	service, tunnel, db := newTestService(t)
	createUser(t, db, "protectee@example.com")
	mint(t, service, "protectee@example.com", "Pixel")
	remove(t, service, "protectee@example.com", "Pixel")
	tunnel.Calls = nil

	_, err := service.RemoveDevice(context.Background(), RemoveCommand{
		ProtecteeEmail: "protectee@example.com", DeviceName: "Pixel",
	})

	if !errors.Is(err, ErrUnknownDevice) {
		t.Fatalf("error = %v, want ErrUnknownDevice", err)
	}
	if len(tunnel.Calls) != 0 {
		t.Fatalf("a second removal reached the helper: %v", tunnel.Calls)
	}
	// And it did not restart the quarantine clock either: a second removal
	// that moved removed_at would extend the window every time it was tried.
	var removedAt int64
	if err := db.QueryRow("SELECT removed_at FROM devices WHERE address = '10.66.0.2'").Scan(&removedAt); err != nil {
		t.Fatal(err)
	}
	if removedAt != testClock.Unix() {
		t.Fatalf("removal instant moved to %d, want %d", removedAt, testClock.Unix())
	}
}

func TestRemovingSomethingThatIsNotADeviceIsRefused(t *testing.T) {
	t.Parallel()
	service, tunnel, db := newTestService(t)
	createUser(t, db, "protectee@example.com")
	createUser(t, db, "other@example.com")
	mint(t, service, "protectee@example.com", "Pixel")
	tunnel.Calls = nil

	for name, command := range map[string]struct {
		command RemoveCommand
		want    error
	}{
		"a Protectee who does not exist": {
			RemoveCommand{ProtecteeEmail: "nobody@example.com", DeviceName: "Pixel"}, ErrUnknownProtectee,
		},
		"another Protectee's Device": {
			RemoveCommand{ProtecteeEmail: "other@example.com", DeviceName: "Pixel"}, ErrUnknownDevice,
		},
		"a Device that was never minted": {
			RemoveCommand{ProtecteeEmail: "protectee@example.com", DeviceName: "Laptop"}, ErrUnknownDevice,
		},
		"an address instead of a name": {
			RemoveCommand{ProtecteeEmail: "protectee@example.com", DeviceName: "10.66.0.2"}, ErrUnknownDevice,
		},
		"an unnamed Device": {
			RemoveCommand{ProtecteeEmail: "protectee@example.com", DeviceName: "   "}, ErrInvalidDeviceName,
		},
		"an address that is not an email": {
			RemoveCommand{ProtecteeEmail: "not-an-email", DeviceName: "Pixel"}, ErrInvalidEmail,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.RemoveDevice(context.Background(), command.command); !errors.Is(err, command.want) {
				t.Fatalf("error = %v, want %v", err, command.want)
			}
		})
	}
	if len(tunnel.Calls) != 0 {
		t.Fatalf("a refused removal reached the helper: %v", tunnel.Calls)
	}
}

func TestATunnelThatWillNotWithdrawThePeerLeavesTheDeviceLive(t *testing.T) {
	t.Parallel()
	for name, failure := range map[string]error{
		"the helper is not installed": ErrHelperMissing,
		"the helper refused":          ErrHelperRefused,
		"the tunnel is down":          ErrTunnelUnavailable,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			service, tunnel, db := newTestService(t)
			createUser(t, db, "protectee@example.com")
			minted := mint(t, service, "protectee@example.com", "Pixel")
			tunnel.RemovePeerError = failure

			_, err := service.RemoveDevice(context.Background(), RemoveCommand{
				ProtecteeEmail: "protectee@example.com", DeviceName: "Pixel",
			})

			if !errors.Is(err, failure) {
				t.Fatalf("error = %v, want %v", err, failure)
			}
			// A record ended against a tunnel that still carries the peer
			// would be a Device nobody can see and nobody can remove.
			var removedAt sql.NullInt64
			if err := db.QueryRow("SELECT removed_at FROM devices WHERE id = ?", minted.Device.ID).Scan(&removedAt); err != nil {
				t.Fatal(err)
			}
			if removedAt.Valid {
				t.Fatalf("the record ended at %d although the peer was never withdrawn", removedAt.Int64)
			}
		})
	}
}

func TestARemovedDeviceFreesItsNameImmediatelyAndItsAddressOnlyLater(t *testing.T) {
	t.Parallel()
	service, _, db := newTestService(t)
	createUser(t, db, "protectee@example.com")
	mint(t, service, "protectee@example.com", "Pixel")
	removed := remove(t, service, "protectee@example.com", "Pixel")

	// The name is a label, not a principal: a replacement phone may carry it
	// the same day. The address is the principal, and waits out the window.
	replacement := mint(t, service, "protectee@example.com", "Pixel")

	if replacement.Device.Address == removed.Device.Address {
		t.Fatalf("the replacement took the quarantined address %s", replacement.Device.Address)
	}
	if replacement.Device.PublicKey == removed.Device.PublicKey {
		t.Fatal("the replacement reused the removed Device's key")
	}
}

func TestQuarantinedAddressesAreReportedSoAnOperatorCanSeeTheWait(t *testing.T) {
	t.Parallel()
	service, _, db := newTestService(t)
	createUser(t, db, "protectee@example.com")
	mint(t, service, "protectee@example.com", "Pixel")
	mint(t, service, "protectee@example.com", "Laptop")
	removed := remove(t, service, "protectee@example.com", "Pixel")

	membership, err := service.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(membership.Quarantined) != 1 {
		t.Fatalf("quarantined addresses = %+v, want exactly the removed Device's", membership.Quarantined)
	}
	held := membership.Quarantined[0]
	if held.Address != removed.Device.Address || !held.RemovedAt.Equal(testClock) || !held.ReusableAt.Equal(removed.ReusableAt) {
		t.Fatalf("quarantine = %+v, want %s removed at %s reusable at %s",
			held, removed.Device.Address, testClock, removed.ReusableAt)
	}

	// Once the window has passed the address is not held any more, and the
	// listing says so rather than showing a wait that has already ended.
	service.now = func() time.Time { return removed.ReusableAt }
	membership, err = service.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(membership.Quarantined) != 0 {
		t.Fatalf("an expired quarantine is still reported: %+v", membership.Quarantined)
	}
}
