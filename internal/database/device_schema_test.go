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

func TestDeviceSchemaHoldsOneDevicePerTunnelAddress(t *testing.T) {
	t.Parallel()
	db := migratedDB(t)
	insert := `INSERT INTO devices(user_id, name, public_key, address, created_at)
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
	// A different Protectee naming their Device the same thing is not a clash.
	if _, err := db.Exec(insert, 2, "Pixel", otherDeviceKey, "10.66.0.3"); err != nil {
		t.Fatalf("second Protectee's Device: %v", err)
	}
}

func TestDeviceSchemaRefusesWhatTheHelperWouldRefuse(t *testing.T) {
	t.Parallel()
	db := migratedDB(t)
	insert := `INSERT INTO devices(user_id, name, public_key, address, created_at)
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

func TestDeletingAUserTakesTheirDevicesWithThem(t *testing.T) {
	t.Parallel()
	db := migratedDB(t)
	if _, err := db.Exec(`INSERT INTO devices(user_id, name, public_key, address, created_at)
		VALUES (1, 'Pixel', ?, '10.66.0.2', unixepoch())`, deviceKey); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM users WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := db.QueryRow("SELECT COUNT(*) FROM devices").Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("%d Devices outlived their Protectee", remaining)
	}
}

func TestDeviceMigrationIsTheLatestSchemaVersion(t *testing.T) {
	t.Parallel()
	if !strings.Contains(migrations[deviceMigration-1], "CREATE TABLE devices") {
		t.Fatalf("migration %d does not create the Device table", deviceMigration)
	}
}
