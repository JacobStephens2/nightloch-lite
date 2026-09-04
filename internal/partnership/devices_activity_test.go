package partnership

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestDevicesActivityCategoryAndProse(t *testing.T) {
	t.Parallel()

	// 1. Verify Category definition and Label
	if CategoryDevices != "devices" {
		t.Fatalf("CategoryDevices = %q, want %q", CategoryDevices, "devices")
	}
	if got := CategoryDevices.Label(); got != "Devices" {
		t.Fatalf("CategoryDevices.Label() = %q, want %q", got, "Devices")
	}

	// 2. Verify ActivityCategories includes CategoryDevices
	categories := ActivityCategories()
	found := false
	for _, c := range categories {
		if c == CategoryDevices {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ActivityCategories() does not include CategoryDevices: %v", categories)
	}

	// 3. Verify categoryOfAction
	if got := categoryOfAction[ActionDeviceEnrolled]; got != CategoryDevices {
		t.Fatalf("categoryOfAction[%s] = %q, want %q", ActionDeviceEnrolled, got, CategoryDevices)
	}
	if got := categoryOfAction[ActionDeviceRemoved]; got != CategoryDevices {
		t.Fatalf("categoryOfAction[%s] = %q, want %q", ActionDeviceRemoved, got, CategoryDevices)
	}

	// 4. Verify Summary and Details for ActionDeviceEnrolled
	enrolledEntry := ActivityEntry{
		Action:         ActionDeviceEnrolled,
		ProtecteeEmail: "alice@example.com",
		ActorEmail:     "alice@example.com",
		OccurredAt:     time.Now().UTC(),
		details: activityDetails{
			DeviceName: "MacBook Pro",
		},
	}
	if enrolledEntry.Category() != CategoryDevices {
		t.Fatalf("enrolledEntry.Category() = %q, want %q", enrolledEntry.Category(), CategoryDevices)
	}
	enrolledSummary := enrolledEntry.Summary()
	if !strings.Contains(enrolledSummary, "alice@example.com") {
		t.Errorf("enrolledSummary missing actor: %q", enrolledSummary)
	}
	if !strings.Contains(enrolledSummary, `Device "MacBook Pro"`) {
		t.Errorf("enrolledSummary missing device name: %q", enrolledSummary)
	}
	if !strings.Contains(enrolledSummary, "Traffic from it is filtered through Nightloch") {
		t.Errorf("enrolledSummary missing filter notice: %q", enrolledSummary)
	}
	if !strings.HasSuffix(enrolledSummary, ".") {
		t.Errorf("enrolledSummary not a finished sentence: %q", enrolledSummary)
	}

	// 5. Verify Summary for ActionDeviceRemoved by Protectee
	removedByProtectee := ActivityEntry{
		Action:         ActionDeviceRemoved,
		ProtecteeEmail: "alice@example.com",
		ActorEmail:     "alice@example.com",
		OccurredAt:     time.Now().UTC(),
		details: activityDetails{
			DeviceName: "MacBook Pro",
		},
	}
	summaryProtectee := removedByProtectee.Summary()
	if !strings.Contains(summaryProtectee, "alice@example.com") {
		t.Errorf("removed summary missing actor: %q", summaryProtectee)
	}
	if !strings.Contains(summaryProtectee, `Device "MacBook Pro"`) {
		t.Errorf("removed summary missing device name: %q", summaryProtectee)
	}
	if !strings.Contains(summaryProtectee, "Protection on that hardware has ended") {
		t.Errorf("removed summary missing consequence: %q", summaryProtectee)
	}

	// 6. Verify Summary for ActionDeviceRemoved by Partner
	removedByPartner := ActivityEntry{
		Action:         ActionDeviceRemoved,
		ProtecteeEmail: "alice@example.com",
		ActorEmail:     "bob@example.com",
		TargetEmail:    "alice@example.com",
		OccurredAt:     time.Now().UTC(),
		details: activityDetails{
			DeviceName: "MacBook Pro",
		},
	}
	summaryPartner := removedByPartner.Summary()
	if !strings.Contains(summaryPartner, "bob@example.com") {
		t.Errorf("removed summary missing partner actor: %q", summaryPartner)
	}
	if !strings.Contains(summaryPartner, `Device "MacBook Pro"`) {
		t.Errorf("removed summary missing device name: %q", summaryPartner)
	}
	if !strings.Contains(summaryPartner, "Protection on that hardware has ended") {
		t.Errorf("removed summary missing consequence: %q", summaryPartner)
	}
}

func TestDevicesActivityFiltering(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()

	protectee := createUser(t, db, "protectee@example.com")
	partner := createUser(t, db, "partner@example.com")

	inv := invite(t, service, protectee, "partner@example.com", false)
	acceptFor(t, service, inv, partner)

	now := time.Now().UTC().Truncate(time.Second)
	// Record a device_enrolled entry
	record(t, service, activityEntry{
		ProtecteeID: protectee,
		Action:      ActionDeviceEnrolled,
		ActorUserID: &protectee,
		ActorEmail:  "protectee@example.com",
		OccurredAt:  now,
		Details: map[string]any{
			"device_name": "Work Laptop",
		},
	})
	// Record a device_removed entry
	record(t, service, activityEntry{
		ProtecteeID: protectee,
		Action:      ActionDeviceRemoved,
		ActorUserID: &partner,
		ActorEmail:  "partner@example.com",
		TargetEmail: "protectee@example.com",
		OccurredAt:  now.Add(time.Minute),
		Details: map[string]any{
			"device_name": "Work Laptop",
		},
	})

	// Query with Category: CategoryDevices
	view, err := service.Activity(ctx, protectee, protectee, ActivityQuery{
		Category: CategoryDevices,
	})
	if err != nil {
		t.Fatalf("Activity with CategoryDevices: %v", err)
	}
	if len(view.Entries) != 2 {
		t.Fatalf("got %d entries for CategoryDevices, want 2", len(view.Entries))
	}
	if view.Entries[0].Action != ActionDeviceRemoved {
		t.Errorf("newest entry = %s, want %s", view.Entries[0].Action, ActionDeviceRemoved)
	}
	if view.Entries[1].Action != ActionDeviceEnrolled {
		t.Errorf("older entry = %s, want %s", view.Entries[1].Action, ActionDeviceEnrolled)
	}
}
