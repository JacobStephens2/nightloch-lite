package database

import (
	"strings"
	"testing"
)

// The Device table is what makes tunnel address allocation safe. These tests
// assert the constraints the enforcement module relies on rather than
// re-checks: a second Device cannot take an address a live Device already
// holds, a Device cannot outlive the User it belongs to, and a Device the
// helper would refuse cannot be recorded in the first place.

const deviceKey = "QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVphYmNkZWY="

const otherDeviceKey = "MDEyMzQ1Njc4OWFiY2RlZmdoaWprbG1ub3BxcnN0dXY="

// Three more well-formed keys, so a test about address or name uniqueness is
// decided by the index it names rather than by the key index.
const (
	thirdDeviceKey  = "dGhpcmQgLWRldmljZS1rZXktcGFkZGluZy1ieXRlcy0="
	fourthDeviceKey = "Zm91cnRoLWRldmljZS1rZXktcGFkZGluZy1ieXRlcy0="
	fifthDeviceKey  = "ZmlmdGggLWRldmljZS1rZXktcGFkZGluZy1ieXRlcy0="
)

func TestDeviceSchemaHoldsOneDevicePerTunnelAddress(t *testing.T) {
	t.Parallel()
	db := migratedDB(t)
	insert := `INSERT INTO devices(protectee_id, name, public_key, address, created_at)
		VALUES (?, ?, ?, ?, unixepoch())`

	if _, err := db.Exec(insert, 1, "Pixel", deviceKey, "10.66.0.2"); err != nil {
		t.Fatalf("first Device: %v", err)
	}
	if _, err := db.Exec(insert, 2, "Phone", otherDeviceKey, "10.66.0.2"); err == nil {
		t.Fatal("schema allowed two Devices at the same tunnel address")
	}
	if _, err := db.Exec(insert, 2, "Phone", deviceKey, "10.66.0.3"); err == nil {
		t.Fatal("schema allowed one public key on two Devices")
	}
	if _, err := db.Exec(insert, 1, "Pixel", otherDeviceKey, "10.66.0.3"); err == nil {
		t.Fatal("schema allowed one Protectee two Devices of the same name")
	}
	// A difference of case is the same Device, not a second one entitled to
	// its own tunnel address.
	if _, err := db.Exec(insert, 1, "pixel", otherDeviceKey, "10.66.0.3"); err == nil {
		t.Fatal("schema allowed one Protectee two Devices differing only in case")
	}
	// A different Protectee naming their Device the same thing is not a clash.
	if _, err := db.Exec(insert, 2, "Pixel", otherDeviceKey, "10.66.0.3"); err != nil {
		t.Fatalf("second Protectee's Device: %v", err)
	}
}

func TestDeviceSchemaRefusesWhatTheHelperWouldRefuse(t *testing.T) {
	t.Parallel()
	db := migratedDB(t)
	insert := `INSERT INTO devices(protectee_id, name, public_key, address, created_at)
		VALUES (?, ?, ?, ?, unixepoch())`

	for name, row := range map[string][]any{
		"unknown Protectee":   {99, "Pixel", deviceKey, "10.66.0.2"},
		"short public key":    {1, "Pixel", deviceKey[:43], "10.66.0.2"},
		"unnamed Device":      {1, "", deviceKey, "10.66.0.2"},
		"address with a CIDR": {1, "Pixel", deviceKey, "10.66.0.2/32"},
		"the host address":    {1, "Pixel", deviceKey, "10.66.0.1"},
		"off the tunnel":      {1, "Pixel", deviceKey, "10.67.0.2"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := db.Exec(insert, row...); err == nil {
				t.Fatalf("schema accepted %s", name)
			}
		})
	}
}

// A Device row now carries a safety property rather than only a record: it is
// what holds a removed Device's tunnel address out of the pool. So deleting
// the User it belongs to may not quietly take it away — that would return the
// address to circulation while the previous occupant's queries are still
// retained, which is the whole disclosure quarantine exists to prevent.
func TestAUserCannotBeDeletedOutFromUnderTheirDevices(t *testing.T) {
	t.Parallel()
	db := migratedDB(t)
	insert := `INSERT INTO devices(protectee_id, name, public_key, address, created_at, removed_at)
		VALUES (1, ?, ?, ?, unixepoch(), ?)`
	if _, err := db.Exec(insert, "Pixel", deviceKey, "10.66.0.2", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(insert, "Laptop", otherDeviceKey, "10.66.0.3", 1_800_000_000); err != nil {
		t.Fatal(err)
	}

	// Nightloch's own deletion leaves the users row standing and replaces the
	// identity on it, so this refusal is never reached in practice. It is here
	// so that a later teardown written to hard-delete fails loudly instead of
	// releasing addresses nobody knew were being held.
	if _, err := db.Exec("DELETE FROM users WHERE id = 1"); err == nil {
		t.Fatal("deleting a User silently took their Devices, and their address quarantine, with them")
	}

	var remaining int
	if err := db.QueryRow("SELECT COUNT(*) FROM devices").Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 2 {
		t.Fatalf("%d Devices survived the refused deletion, want 2", remaining)
	}
}

func TestTheDeviceMigrationsAreTheOnesTheyClaimToBe(t *testing.T) {
	t.Parallel()
	if !strings.Contains(migrations[deviceMigration-1], "CREATE TABLE devices") {
		t.Fatalf("migration %d does not create the Device table", deviceMigration)
	}
	if !strings.Contains(migrations[deviceRemovalMigration-1], "removed_at") {
		t.Fatalf("migration %d does not add the removal instant", deviceRemovalMigration)
	}
}

// Removal narrows the Device table's uniqueness to live Devices. That is the
// schema's part of quarantine and no more: which addresses are allocatable is
// the enforcement module's rule, derived from the removal instant, and the
// table must not be the thing that decides it.
func TestDeviceSchemaScopesUniquenessToLiveDevices(t *testing.T) {
	t.Parallel()
	db := migratedDB(t)
	insert := `INSERT INTO devices(protectee_id, name, public_key, address, created_at, removed_at)
		VALUES (?, ?, ?, ?, unixepoch(), ?)`

	if _, err := db.Exec(insert, 1, "Pixel", deviceKey, "10.66.0.2", 1_800_000_000); err != nil {
		t.Fatalf("removed Device: %v", err)
	}
	// A replacement phone may carry the removed one's name the same day: the
	// name is a label, not a principal.
	if _, err := db.Exec(insert, 1, "Pixel", otherDeviceKey, "10.66.0.3", nil); err != nil {
		t.Fatalf("replacement Device with the removed name: %v", err)
	}
	// The address is a principal, so the table permits its reallocation and
	// leaves the waiting to the module that knows the retention.
	if _, err := db.Exec(insert, 2, "Laptop", thirdDeviceKey, "10.66.0.2", nil); err != nil {
		t.Fatalf("reallocated address after removal: %v", err)
	}
	if _, err := db.Exec(insert, 2, "Tablet", fourthDeviceKey, "10.66.0.2", nil); err == nil {
		t.Fatal("schema allowed two live Devices at one tunnel address")
	}
	// One address accumulates ended records over a deployment's life.
	if _, err := db.Exec(insert, 2, "Watch", fifthDeviceKey, "10.66.0.3", 1_800_000_001); err != nil {
		t.Fatalf("second ended record at one address: %v", err)
	}
	// A key is never reissued, removed or not: a withdrawn Device's key must
	// not come back on a different address.
	if _, err := db.Exec(insert, 2, "Old key", deviceKey, "10.66.0.9", nil); err == nil {
		t.Fatal("schema allowed a removed Device's public key on a live Device")
	}
}
