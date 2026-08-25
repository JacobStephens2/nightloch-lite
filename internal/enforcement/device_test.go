package enforcement

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JacobStephens2/nightloch-lite/internal/database"
)

var testClock = time.Date(2026, 8, 25, 9, 30, 0, 0, time.UTC)

// newTestService stands the module up against a real migrated SQLite file and
// a recording tunnel. No test in this package needs a droplet, and none of them
// stubs SQLite: the schema's own constraints are part of what is under test.
func newTestService(t *testing.T) (*Service, *RecordingTunnel, *sql.DB) {
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
	service, err := NewService(db, tunnel, testSettings())
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return testClock }
	return service, tunnel, db
}

func createUser(t *testing.T, db *sql.DB, email string) int64 {
	t.Helper()
	result, err := db.Exec("INSERT INTO users(email, created_at) VALUES (?, unixepoch())", email)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mint(t *testing.T, service *Service, email, name string) MintedDevice {
	t.Helper()
	minted, err := service.MintDevice(context.Background(), MintCommand{ProtecteeEmail: email, DeviceName: name})
	if err != nil {
		t.Fatalf("MintDevice(%q, %q): %v", email, name, err)
	}
	return minted
}

func TestMintDeviceRecordsTheDeviceAndAdmitsItToTheTunnel(t *testing.T) {
	t.Parallel()
	service, tunnel, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")

	minted := mint(t, service, " Protectee@Example.com ", "  Pixel  ")

	if minted.Device.ProtecteeID != protectee {
		t.Fatalf("Device belongs to %d, want %d", minted.Device.ProtecteeID, protectee)
	}
	if minted.Device.Name != "Pixel" {
		t.Fatalf("Device name = %q", minted.Device.Name)
	}
	if got := minted.Device.Address.String(); got != "10.66.0.2" {
		t.Fatalf("first Device address = %s, want the lowest assignable address", got)
	}
	if !IsPublicKey(minted.Device.PublicKey) {
		t.Fatalf("Device public key %q is not a WireGuard key", minted.Device.PublicKey)
	}
	assertCalls(t, tunnel, []string{"add-peer " + minted.Device.PublicKey + " 10.66.0.2"})

	var name, publicKey, address string
	var createdAt int64
	row := db.QueryRow("SELECT name, public_key, address, created_at FROM devices WHERE id = ?", minted.Device.ID)
	if err := row.Scan(&name, &publicKey, &address, &createdAt); err != nil {
		t.Fatal(err)
	}
	if name != "Pixel" || publicKey != minted.Device.PublicKey || address != "10.66.0.2" {
		t.Fatalf("recorded Device = %q %q %q", name, publicKey, address)
	}
	if createdAt != testClock.Unix() {
		t.Fatalf("created at = %d, want %d", createdAt, testClock.Unix())
	}
}

func TestMintDeviceAllocatesTheLowestFreeAddressAndNeverTheHost(t *testing.T) {
	t.Parallel()
	service, _, db := newTestService(t)
	createUser(t, db, "protectee@example.com")

	var addresses []string
	for _, name := range []string{"Pixel", "Laptop", "Tablet"} {
		addresses = append(addresses, mint(t, service, "protectee@example.com", name).Device.Address.String())
	}
	if want := []string{"10.66.0.2", "10.66.0.3", "10.66.0.4"}; !equalStrings(addresses, want) {
		t.Fatalf("addresses = %v, want %v", addresses, want)
	}

	// A gap in the middle is filled before the pool grows, and the host
	// address is not in the pool to be filled with.
	if _, err := db.Exec("DELETE FROM devices WHERE address = '10.66.0.3'"); err != nil {
		t.Fatal(err)
	}
	if got := mint(t, service, "protectee@example.com", "Watch").Device.Address.String(); got != "10.66.0.3" {
		t.Fatalf("refilled address = %s, want 10.66.0.3", got)
	}

	var hostAllocations int
	if err := db.QueryRow("SELECT COUNT(*) FROM devices WHERE address = ?", HostAddress.String()).Scan(&hostAllocations); err != nil {
		t.Fatal(err)
	}
	if hostAllocations != 0 {
		t.Fatal("a Device was allocated the tunnel's own host address")
	}
}

func TestMintDeviceRefusesTheSameDeviceTwiceRatherThanAllocatingAgain(t *testing.T) {
	t.Parallel()
	service, tunnel, db := newTestService(t)
	createUser(t, db, "protectee@example.com")
	first := mint(t, service, "protectee@example.com", "Pixel")

	_, err := service.MintDevice(context.Background(), MintCommand{
		ProtecteeEmail: "protectee@example.com", DeviceName: "Pixel",
	})

	if !errors.Is(err, ErrDeviceExists) {
		t.Fatalf("second mint error = %v, want ErrDeviceExists", err)
	}
	assertCalls(t, tunnel, []string{"add-peer " + first.Device.PublicKey + " 10.66.0.2"})
	var devices int
	if err := db.QueryRow("SELECT COUNT(*) FROM devices").Scan(&devices); err != nil {
		t.Fatal(err)
	}
	if devices != 1 {
		t.Fatalf("%d Devices recorded, want 1", devices)
	}
}

func TestMintDeviceRefusesWhatItCannotRecord(t *testing.T) {
	t.Parallel()
	for name, testCase := range map[string]struct {
		command MintCommand
		want    error
	}{
		"unknown Protectee": {MintCommand{ProtecteeEmail: "stranger@example.com", DeviceName: "Pixel"}, ErrUnknownProtectee},
		"malformed address": {MintCommand{ProtecteeEmail: "not-an-address", DeviceName: "Pixel"}, ErrInvalidEmail},
		"unnamed Device":    {MintCommand{ProtecteeEmail: "protectee@example.com", DeviceName: "   "}, ErrInvalidDeviceName},
		"overlong name": {MintCommand{
			ProtecteeEmail: "protectee@example.com", DeviceName: strings.Repeat("a", maxDeviceNameLength+1),
		}, ErrInvalidDeviceName},
		"name with a newline": {MintCommand{
			ProtecteeEmail: "protectee@example.com", DeviceName: "Pixel\nAddress = 10.66.0.9",
		}, ErrInvalidDeviceName},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			service, tunnel, db := newTestService(t)
			createUser(t, db, "protectee@example.com")

			_, err := service.MintDevice(context.Background(), testCase.command)

			if !errors.Is(err, testCase.want) {
				t.Fatalf("error = %v, want %v", err, testCase.want)
			}
			if len(tunnel.Calls) != 0 {
				t.Fatalf("a refused mint reached the tunnel: %v", tunnel.Calls)
			}
			var devices int
			if err := db.QueryRow("SELECT COUNT(*) FROM devices").Scan(&devices); err != nil {
				t.Fatal(err)
			}
			if devices != 0 {
				t.Fatal("a refused mint recorded a Device")
			}
		})
	}
}

func TestMintDeviceRefusesAProtecteeWhoseAccountIsDeleted(t *testing.T) {
	t.Parallel()
	service, _, db := newTestService(t)
	createUser(t, db, "protectee@example.com")
	if _, err := db.Exec("UPDATE users SET deleted_at = unixepoch() WHERE email = 'protectee@example.com'"); err != nil {
		t.Fatal(err)
	}

	_, err := service.MintDevice(context.Background(), MintCommand{
		ProtecteeEmail: "protectee@example.com", DeviceName: "Pixel",
	})

	if !errors.Is(err, ErrUnknownProtectee) {
		t.Fatalf("error = %v, want ErrUnknownProtectee", err)
	}
}

// A tunnel that refuses the peer must leave no Device behind. The two halves
// are committed in one ordering, so a record an Operator can see is always a
// record the tunnel is carrying.
func TestATunnelThatRefusesThePeerLeavesNoDeviceRecord(t *testing.T) {
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
			tunnel.AddPeerError = failure

			_, err := service.MintDevice(context.Background(), MintCommand{
				ProtecteeEmail: "protectee@example.com", DeviceName: "Pixel",
			})

			if !errors.Is(err, failure) {
				t.Fatalf("error = %v, want %v", err, failure)
			}
			var devices int
			if err := db.QueryRow("SELECT COUNT(*) FROM devices").Scan(&devices); err != nil {
				t.Fatal(err)
			}
			if devices != 0 {
				t.Fatal("a Device survived a tunnel that never admitted it")
			}
			// The address it would have taken is free for the next mint.
			tunnel.AddPeerError = nil
			if got := mint(t, service, "protectee@example.com", "Pixel").Device.Address.String(); got != "10.66.0.2" {
				t.Fatalf("address after a failed mint = %s, want 10.66.0.2", got)
			}
		})
	}
}

func TestTheServerPersistsNoPrivateKey(t *testing.T) {
	t.Parallel()
	service, _, db := newTestService(t)
	createUser(t, db, "protectee@example.com")

	minted := mint(t, service, "protectee@example.com", "Pixel")

	privateKey := privateKeyFromConfiguration(t, minted.Configuration)
	if privateKey == "" {
		t.Fatal("the rendered configuration carries no private key")
	}
	if privateKey == minted.Device.PublicKey {
		t.Fatal("the private and public halves of the Device key are the same value")
	}
	// Every column of every table, not only the Device row: the assertion is
	// that the private key is nowhere in the database at all.
	for _, table := range tableNames(t, db) {
		for _, column := range columnNames(t, db, table) {
			var found int
			query := "SELECT COUNT(*) FROM " + table + " WHERE CAST(" + column + " AS TEXT) LIKE ?"
			if err := db.QueryRow(query, "%"+privateKey+"%").Scan(&found); err != nil {
				t.Fatal(err)
			}
			if found != 0 {
				t.Fatalf("the Device private key was persisted in %s.%s", table, column)
			}
		}
	}
}

// tableNames and columnNames read the whole result before issuing another
// query: Nightloch runs SQLite on a single connection, so a nested query while
// rows are open would wait on a connection that cannot be released.
func tableNames(t *testing.T, db *sql.DB) []string {
	t.Helper()
	return queryStrings(t, db, "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'")
}

func columnNames(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	return queryStrings(t, db, "SELECT name FROM pragma_table_info(?)", table)
}

func queryStrings(t *testing.T, db *sql.DB, query string, args ...any) []string {
	t.Helper()
	rows, err := db.Query(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return values
}

func TestTheMintedConfigurationIsTheCanonicalRendering(t *testing.T) {
	t.Parallel()
	service, _, db := newTestService(t)
	createUser(t, db, "protectee@example.com")

	minted := mint(t, service, "protectee@example.com", "Pixel")

	privateKey := privateKeyFromConfiguration(t, minted.Configuration)
	want := RenderConfiguration(minted.Device, privateKey, testSettings())
	if minted.Configuration != want {
		t.Fatalf("minted configuration =\n%s\nwant\n%s", minted.Configuration, want)
	}
}

func TestDevicesReportsRecordedDevicesWithTheirTunnelMembership(t *testing.T) {
	t.Parallel()
	service, tunnel, db := newTestService(t)
	createUser(t, db, "protectee@example.com")
	first := mint(t, service, "protectee@example.com", "Pixel")
	second := mint(t, service, "protectee@example.com", "Laptop")
	handshake := testClock.Add(-30 * time.Second)
	tunnel.Handshakes = map[string]time.Time{first.Device.PublicKey: handshake}

	statuses, err := service.Devices(context.Background())
	if err != nil {
		t.Fatalf("Devices: %v", err)
	}

	if len(statuses) != 2 {
		t.Fatalf("%d Devices reported, want 2", len(statuses))
	}
	if statuses[0].Device.Address != netip.MustParseAddr("10.66.0.2") {
		t.Fatalf("Devices are not ordered by address: %v", statuses[0].Device.Address)
	}
	if !statuses[0].OnTunnel || !statuses[0].LatestHandshake.Equal(handshake) {
		t.Fatalf("first Device status = %+v", statuses[0])
	}
	if !statuses[1].OnTunnel || !statuses[1].LatestHandshake.IsZero() {
		t.Fatalf("a Device that never handshook reported %v", statuses[1].LatestHandshake)
	}

	// A Device the tunnel is not carrying is a real disagreement, and is
	// reported as one rather than hidden.
	if err := tunnel.RemovePeer(context.Background(), second.Device.PublicKey); err != nil {
		t.Fatal(err)
	}
	statuses, err = service.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if statuses[1].OnTunnel {
		t.Fatal("a Device absent from the tunnel reported as on it")
	}
}

// An unreadable tunnel is never an empty Device list. Each way of failing
// keeps its own identity through the module, so an Operator can tell an
// uninstalled helper from a refusal from a plane that is down.
func TestAnUnreadableTunnelIsAnErrorRatherThanAnEmptyDeviceList(t *testing.T) {
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
			mint(t, service, "protectee@example.com", "Pixel")
			tunnel.PeersError = failure

			statuses, err := service.Devices(context.Background())

			if !errors.Is(err, failure) {
				t.Fatalf("error = %v, want %v", err, failure)
			}
			if statuses != nil {
				t.Fatalf("an unreadable tunnel still reported %d Devices", len(statuses))
			}
		})
	}
}

func TestMintingIsRefusedWhenTheAddressPoolIsExhausted(t *testing.T) {
	t.Parallel()
	service, tunnel, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	for octet := firstDeviceOctet; octet <= lastDeviceOctet; octet++ {
		if _, err := db.Exec(
			`INSERT INTO devices(user_id, name, public_key, address, created_at)
			 VALUES (?, ?, ?, ?, unixepoch())`,
			protectee, "filler-"+deviceAddress(octet).String(), fillerKey(octet), deviceAddress(octet).String()); err != nil {
			t.Fatal(err)
		}
	}

	_, err := service.MintDevice(context.Background(), MintCommand{
		ProtecteeEmail: "protectee@example.com", DeviceName: "Pixel",
	})

	if !errors.Is(err, ErrAddressPoolExhausted) {
		t.Fatalf("error = %v, want ErrAddressPoolExhausted", err)
	}
	if len(tunnel.Calls) != 0 {
		t.Fatalf("an exhausted pool still reached the tunnel: %v", tunnel.Calls)
	}
}

func TestNewServiceRefusesUnrenderableSettings(t *testing.T) {
	t.Parallel()
	db, err := database.Open(filepath.Join(t.TempDir(), "nightloch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := NewService(db, &RecordingTunnel{}, TunnelSettings{}); !errors.Is(err, ErrInvalidTunnelSettings) {
		t.Fatalf("NewService error = %v, want ErrInvalidTunnelSettings", err)
	}
}

// fillerKey is a distinct well-formed public key per octet, so filling the
// pool does not trip the schema's uniqueness on the key instead of the
// address under test.
func fillerKey(octet int) string {
	key := []byte("QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVphYmNkZWY=")
	digits := "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz+/"
	key[0] = digits[octet/64%64]
	key[1] = digits[octet%64]
	return string(key)
}

func privateKeyFromConfiguration(t *testing.T, configuration string) string {
	t.Helper()
	for line := range strings.SplitSeq(configuration, "\n") {
		if after, ok := strings.CutPrefix(line, "PrivateKey = "); ok {
			return after
		}
	}
	return ""
}

func assertCalls(t *testing.T, tunnel *RecordingTunnel, want []string) {
	t.Helper()
	if !equalStrings(tunnel.Calls, want) {
		t.Fatalf("tunnel calls = %q, want %q", tunnel.Calls, want)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
