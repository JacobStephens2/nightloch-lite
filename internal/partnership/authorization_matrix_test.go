package partnership

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	mailmessage "github.com/JacobStephens2/nightloch-lite/internal/mail"
)

// perimeter is one Protectee surrounded by every actor the Slice 3 gate names,
// so a single fixture can be asked the same question from every side. The
// unauthenticated visitor is not a field: they are viewer id 0, which is what
// every entry point sees when no session resolves to a User.
type perimeter struct {
	protectee int64
	// partnerA and partnerB are two current Partners of the same Protectee.
	// Two are needed because "another current Partner" is its own actor: a
	// Partner's own authority must never reach a Partnership they do not hold.
	partnerA, partnerB int64
	// former accepted and then ended their own Partnership, so they keep a
	// receipt and nothing else.
	former int64
	// invitee holds a pending invitation and a User of their own, which is the
	// strongest an unaccepted invitee can be.
	invitee int64
	// stranger is a signed-in User with no relationship to this Protectee at
	// all — they hold their own Partnership elsewhere, so any leak here would
	// be an authorization bug rather than an empty database.
	stranger int64

	partnershipA, partnershipB int64
	// endedPartnership is the receipt former keeps.
	endedPartnership int64
	// pendingInvitation is the live invitation addressed to invitee.
	pendingInvitation Invitation
	// unrelatedPartnership belongs to stranger's own protection and exists
	// only to prove that holding a Partnership somewhere grants nothing here.
	unrelatedProtectee, unrelatedPartnership int64
}

const unauthenticatedViewer int64 = 0

// endNow is the Partner's own immediate ending, failing the test on refusal.
// It is the counterpart of startEnding, which is the Protectee's exit.
func endNow(t *testing.T, service *Service, partnershipID, partnerID int64) {
	t.Helper()
	if _, err := service.EndPartnership(context.Background(), EndCommand{
		PartnershipID: partnershipID, PartnerID: partnerID, Confirmation: "END",
	}); err != nil {
		t.Fatalf("EndPartnership: %v", err)
	}
}

func newPerimeter(t *testing.T, service *Service, db *sql.DB) perimeter {
	t.Helper()
	ctx := context.Background()
	fixture := perimeter{
		protectee: createUser(t, db, "protectee@example.com"),
		partnerA:  createUser(t, db, "partner-a@example.com"),
		partnerB:  createUser(t, db, "partner-b@example.com"),
		former:    createUser(t, db, "former@example.com"),
		invitee:   createUser(t, db, "invitee@example.com"),
		stranger:  createUser(t, db, "stranger@example.com"),
	}
	fixture.partnershipA = acceptFor(t, service,
		invite(t, service, fixture.protectee, "partner-a@example.com", true), fixture.partnerA).PartnershipID
	fixture.partnershipB = acceptFor(t, service,
		invite(t, service, fixture.protectee, "partner-b@example.com", false), fixture.partnerB).PartnershipID
	fixture.endedPartnership = acceptFor(t, service,
		invite(t, service, fixture.protectee, "former@example.com", true), fixture.former).PartnershipID
	if _, err := service.EndPartnership(ctx, EndCommand{
		PartnershipID: fixture.endedPartnership, PartnerID: fixture.former, Confirmation: "END",
	}); err != nil {
		t.Fatalf("end the former Partner's Partnership: %v", err)
	}
	fixture.pendingInvitation = invite(t, service, fixture.protectee, "invitee@example.com", false)

	// The stranger protects somebody else entirely.
	fixture.unrelatedProtectee = createUser(t, db, "elsewhere@example.com")
	fixture.unrelatedPartnership = acceptFor(t, service,
		invite(t, service, fixture.unrelatedProtectee, "stranger@example.com", true), fixture.stranger).PartnershipID
	return fixture
}

// actors names every side of the perimeter for table-driven assertions.
func (p perimeter) actors() []struct {
	name string
	id   int64
} {
	return []struct {
		name string
		id   int64
	}{
		{"Protectee", p.protectee},
		{"current Partner", p.partnerA},
		{"another current Partner", p.partnerB},
		{"former Partner", p.former},
		{"pending invitee", p.invitee},
		{"unrelated User", p.stranger},
		{"unauthenticated visitor", unauthenticatedViewer},
	}
}

func TestOnlyTheInvitedUserActsOnAnInvitation(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()
	fixture := newPerimeter(t, service, db)
	reference := ByID(fixture.pendingInvitation.ID)

	for _, actor := range fixture.actors() {
		invitee := actor.name == "pending invitee"
		// Reviewing by id resolves only for the invited address, so everyone
		// else cannot even confirm the invitation exists.
		review, err := service.ReviewInvitation(ctx, reference, actor.id)
		switch {
		case invitee:
			if err != nil || !review.MayAccept() || review.InviteeEmail != "invitee@example.com" {
				t.Fatalf("the invitee could not review their own invitation: %+v, %v", review, err)
			}
		case !errors.Is(err, ErrInvitationUnavailable):
			t.Fatalf("%s reviewed an invitation by id: %+v, %v", actor.name, review, err)
		}

		if _, err := service.AcceptInvitation(ctx, reference, actor.id); invitee != (err == nil) {
			t.Fatalf("%s accepting = %v", actor.name, err)
		}
		if invitee {
			// The invitee's acceptance is the one that must land, so the rest
			// of the matrix continues against the invitation it consumed.
			continue
		}
		if _, err := service.DeclineInvitation(ctx, reference, actor.id); err == nil {
			t.Fatalf("%s declined an invitation addressed to somebody else", actor.name)
		}
	}

	if state := invitationState(t, service, fixture.pendingInvitation.ID); state != string(InvitationAccepted) {
		t.Fatalf("invitation state after the matrix = %q", state)
	}
}

func TestOnlyTheInvitingProtecteeWithdrawsOrResendsAnInvitation(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()
	fixture := newPerimeter(t, service, db)

	for _, actor := range fixture.actors() {
		if actor.id == fixture.protectee {
			continue
		}
		if _, err := service.ResendInvitation(ctx, fixture.pendingInvitation.ID, actor.id); !errors.Is(err, ErrInvitationUnavailable) {
			t.Fatalf("%s resent another Protectee's invitation: %v", actor.name, err)
		}
		if _, err := service.CancelInvitation(ctx, fixture.pendingInvitation.ID, actor.id); !errors.Is(err, ErrInvitationUnavailable) {
			t.Fatalf("%s cancelled another Protectee's invitation: %v", actor.name, err)
		}
	}
	if state := invitationState(t, service, fixture.pendingInvitation.ID); state != string(InvitationPending) {
		t.Fatalf("the invitation left the pending state: %q", state)
	}
	if _, err := service.CancelInvitation(ctx, fixture.pendingInvitation.ID, fixture.protectee); err != nil {
		t.Fatalf("the inviting Protectee could not withdraw their own invitation: %v", err)
	}
}

func TestRosterAndPendingInvitationsReachOnlyTheProtecteeAndTheirCurrentPartners(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()
	fixture := newPerimeter(t, service, db)
	unreachable(t, service, "partner-b@example.com")

	// The Protectee's own view is the Roster; every other actor reads this
	// Protectee only through People you protect, which a live Partnership is
	// the sole way into.
	for _, actor := range fixture.actors() {
		view, err := service.ProtecteeView(ctx, actor.id)
		if err != nil {
			t.Fatalf("%s own view: %v", actor.name, err)
		}
		if actor.id != fixture.protectee && len(view.Partners) > 0 && actor.id != fixture.unrelatedProtectee {
			for _, entry := range view.Partners {
				if entry.PartnershipID == fixture.partnershipA || entry.PartnershipID == fixture.partnershipB {
					t.Fatalf("%s read this Protectee's Roster as their own: %+v", actor.name, entry)
				}
			}
		}

		if actor.id == fixture.protectee {
			if state := reachabilityOn(view.Partners, "partner-b@example.com"); state != ReachabilityUnreachable {
				t.Fatalf("the Protectee's own Roster reads partner-b as %q", state)
			}
		}

		protecting, err := service.ProtectingView(ctx, actor.id)
		if err != nil {
			t.Fatalf("%s People you protect: %v", actor.name, err)
		}
		var reached *ProtecteeEntry
		for index, entry := range protecting {
			if entry.ProtecteeID == fixture.protectee {
				reached = &protecting[index]
			}
		}
		current := actor.id == fixture.partnerA || actor.id == fixture.partnerB
		if current != (reached != nil) {
			t.Fatalf("%s reaching this Protectee = %v", actor.name, reached != nil)
		}
		if reached == nil {
			continue
		}
		// A current Partner reads the whole Roster and the pending
		// invitations, identically to every other entitled viewer (S3-15,
		// S3-45).
		if len(reached.Roster) != 2 || len(reached.PendingInvitations) != 1 {
			t.Fatalf("%s read Roster=%d pending=%d, want the full 2 and 1",
				actor.name, len(reached.Roster), len(reached.PendingInvitations))
		}
		// Delivery state has no surface of its own: it rides on the Roster
		// row, so it reaches exactly the viewers the row reaches and nobody
		// else (S3-45). Every unentitled actor was already refused the row.
		if state := reachabilityOn(reached.Roster, "partner-b@example.com"); state != ReachabilityUnreachable {
			t.Fatalf("%s read partner-b's reachability as %q, want the standing warning",
				actor.name, state)
		}
	}
}

func TestActivityAndReceiptsAnswerOnlyTheAudienceThatHoldsThem(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()
	fixture := newPerimeter(t, service, db)

	for _, actor := range fixture.actors() {
		// Activity: the Protectee and their current Partners, nobody else.
		view, err := service.Activity(ctx, fixture.protectee, actor.id, ActivityQuery{})
		entitled := actor.id == fixture.protectee || actor.id == fixture.partnerA || actor.id == fixture.partnerB
		switch {
		case entitled && err != nil:
			t.Fatalf("%s could not read Activity they hold: %v", actor.name, err)
		case entitled && len(view.Entries) == 0:
			t.Fatalf("%s read an empty Activity", actor.name)
		case !entitled && !errors.Is(err, ErrActivityUnavailable):
			t.Fatalf("%s read Activity: %d entries, %v", actor.name, len(view.Entries), err)
		}

		// The receipt for an ended Partnership answers its own former Partner
		// and nobody else — not the Protectee it named, and not a Partner who
		// still holds a Partnership with the same Protectee.
		receipt, err := service.EndedPartnershipReceipt(ctx, fixture.endedPartnership, actor.id)
		keepsReceipt := actor.id == fixture.former
		switch {
		case keepsReceipt && err != nil:
			t.Fatalf("the former Partner lost their own receipt: %v", err)
		case keepsReceipt && receipt.ProtecteeLabel() != "protectee@example.com":
			t.Fatalf("the former Partner's receipt named %q", receipt.ProtecteeLabel())
		case !keepsReceipt && !errors.Is(err, ErrPartnershipUnavailable):
			t.Fatalf("%s read somebody else's receipt: %+v, %v", actor.name, receipt, err)
		}
	}
}

func TestNoPartnerReachesAnotherPartnersPartnershipOrTheProtecteesOwnControls(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()
	fixture := newPerimeter(t, service, db)

	for _, actor := range fixture.actors() {
		// Ending is the Partner's own power over their own Partnership. Even
		// another current Partner of the same Protectee — the closest thing to
		// a quorum this product has — cannot exercise it.
		holder := actor.id == fixture.partnerA
		if _, err := service.EndablePartnership(ctx, fixture.partnershipA, actor.id); holder != (err == nil) {
			t.Fatalf("%s previewing another Partner's ending = %v", actor.name, err)
		}
		if !holder {
			if _, err := service.EndPartnership(ctx, EndCommand{
				PartnershipID: fixture.partnershipA, PartnerID: actor.id, Confirmation: "END",
			}); !errors.Is(err, ErrPartnershipUnavailable) {
				t.Fatalf("%s ended a Partnership they do not hold: %v", actor.name, err)
			}
		}

		// Cooling-off is the Protectee's own exit and nobody else's, including
		// the Partner the Partnership is with.
		if actor.id == fixture.protectee {
			continue
		}
		if _, err := service.CoolingOffPreview(ctx, fixture.partnershipA, actor.id); !errors.Is(err, ErrPartnershipUnavailable) {
			t.Fatalf("%s previewed a Cooling-off that is not theirs: %v", actor.name, err)
		}
		command := CoolingOffCommand{PartnershipID: fixture.partnershipA, ProtecteeID: actor.id}
		if _, err := service.StartCoolingOff(ctx, command); !errors.Is(err, ErrPartnershipUnavailable) {
			t.Fatalf("%s scheduled somebody else's Partnership to end: %v", actor.name, err)
		}
		if _, err := service.CancelCoolingOff(ctx, command); !errors.Is(err, ErrPartnershipUnavailable) {
			t.Fatalf("%s cancelled somebody else's Cooling-off: %v", actor.name, err)
		}
	}

	// Nothing above changed a thing.
	var live, ended int
	if err := db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM partnerships WHERE protectee_id = ? AND ended_at IS NULL),
		(SELECT COUNT(*) FROM partnerships WHERE protectee_id = ? AND ended_at IS NOT NULL)`,
		fixture.protectee, fixture.protectee).Scan(&live, &ended); err != nil {
		t.Fatal(err)
	}
	if live != 2 || ended != 1 {
		t.Fatalf("Partnerships after the matrix: live=%d ended=%d, want 2 and 1", live, ended)
	}
}

// relationshipCase is one relationship state asked of one authorization
// predicate. Each case builds its own database, because the answer to "may
// this Protectee loosen?" depends on every Partnership they hold, so states
// cannot share a fixture without changing each other's answer.
type relationshipCase struct {
	name string
	// build returns the actor and the Protectee the predicate is asked about.
	build func(t *testing.T, service *Service, db *sql.DB) (actor, protectee int64)
	// loosen and viewLogs are the deny-closed answers this state must give.
	loosen, viewLogs bool
}

func relationshipStates() []relationshipCase {
	return []relationshipCase{{
		name: "unpartnered Protectee",
		build: func(t *testing.T, _ *Service, db *sql.DB) (int64, int64) {
			protectee := createUser(t, db, "protectee@example.com")
			return protectee, protectee
		},
		loosen: true, viewLogs: true,
	}, {
		name: "Protectee holding only a pending invitation",
		build: func(t *testing.T, service *Service, db *sql.DB) (int64, int64) {
			protectee := createUser(t, db, "protectee@example.com")
			invite(t, service, protectee, "invitee@example.com", true)
			return protectee, protectee
		},
		// A pending invitation grants nothing to anybody, so the Protectee is
		// still unpartnered and still free (S3-42).
		loosen: true, viewLogs: true,
	}, {
		name: "pending invitee",
		build: func(t *testing.T, service *Service, db *sql.DB) (int64, int64) {
			protectee := createUser(t, db, "protectee@example.com")
			invitee := createUser(t, db, "invitee@example.com")
			invite(t, service, protectee, "invitee@example.com", true)
			return invitee, protectee
		},
		loosen: false, viewLogs: false,
	}, {
		name: "partnered Protectee",
		build: func(t *testing.T, service *Service, db *sql.DB) (int64, int64) {
			protectee, _, _ := firstPartnership(t, service, db, true)
			return protectee, protectee
		},
		loosen: false, viewLogs: true,
	}, {
		name: "current Partner with Log sharing on",
		build: func(t *testing.T, service *Service, db *sql.DB) (int64, int64) {
			protectee, partner, _ := firstPartnership(t, service, db, true)
			return partner, protectee
		},
		loosen: true, viewLogs: true,
	}, {
		name: "current Partner with Log sharing off",
		build: func(t *testing.T, service *Service, db *sql.DB) (int64, int64) {
			protectee, partner, _ := firstPartnership(t, service, db, false)
			return partner, protectee
		},
		// Powers and Log sharing are separate choices: a Partner who may act
		// on the Policy still reads no domain log unless it was shared.
		loosen: true, viewLogs: false,
	}, {
		name: "current Partner during Cooling-off",
		build: func(t *testing.T, service *Service, db *sql.DB) (int64, int64) {
			protectee, partner, partnership := firstPartnership(t, service, db, true)
			startEnding(t, service, partnership, protectee)
			return partner, protectee
		},
		// A scheduled ending changes nothing until its deadline arrives: the
		// Partnership stays active and the Partner keeps every power.
		loosen: true, viewLogs: true,
	}, {
		name: "Protectee during Cooling-off",
		build: func(t *testing.T, service *Service, db *sql.DB) (int64, int64) {
			protectee, _, partnership := firstPartnership(t, service, db, true)
			startEnding(t, service, partnership, protectee)
			return protectee, protectee
		},
		loosen: false, viewLogs: true,
	}, {
		name: "former Partner",
		build: func(t *testing.T, service *Service, db *sql.DB) (int64, int64) {
			protectee, partner, partnership := firstPartnership(t, service, db, true)
			endNow(t, service, partnership, partner)
			return partner, protectee
		},
		loosen: false, viewLogs: false,
	}, {
		name: "Protectee whose last Partnership ended",
		build: func(t *testing.T, service *Service, db *sql.DB) (int64, int64) {
			protectee, partner, partnership := firstPartnership(t, service, db, true)
			endNow(t, service, partnership, partner)
			return protectee, protectee
		},
		loosen: true, viewLogs: true,
	}, {
		name: "deleted User asked about their own Policy",
		build: func(t *testing.T, service *Service, db *sql.DB) (int64, int64) {
			protectee, _, _ := firstPartnership(t, service, db, true)
			if _, err := service.DeleteUser(context.Background(), protectee); err != nil {
				t.Fatal(err)
			}
			// A session that outlives its User must read as unknown, not as
			// an unpartnered Protectee who is free to loosen.
			return protectee, protectee
		},
		loosen: false, viewLogs: false,
	}, {
		name: "surviving Partner of a deleted Protectee",
		build: func(t *testing.T, service *Service, db *sql.DB) (int64, int64) {
			protectee, partner, _ := firstPartnership(t, service, db, true)
			if _, err := service.DeleteUser(context.Background(), protectee); err != nil {
				t.Fatal(err)
			}
			return partner, protectee
		},
		loosen: false, viewLogs: false,
	}, {
		name: "unrelated signed-in User",
		build: func(t *testing.T, service *Service, db *sql.DB) (int64, int64) {
			protectee, _, _ := firstPartnership(t, service, db, true)
			return createUser(t, db, "stranger@example.com"), protectee
		},
		loosen: false, viewLogs: false,
	}, {
		name: "User who protects somebody else",
		build: func(t *testing.T, service *Service, db *sql.DB) (int64, int64) {
			protectee, _, _ := firstPartnership(t, service, db, true)
			elsewhere := createUser(t, db, "elsewhere@example.com")
			stranger := createUser(t, db, "stranger@example.com")
			partnerUp(t, service, elsewhere, stranger, "stranger@example.com", true)
			return stranger, protectee
		},
		loosen: false, viewLogs: false,
	}, {
		name: "reciprocal direction the actor does not hold",
		build: func(t *testing.T, service *Service, db *sql.DB) (int64, int64) {
			// The actor is protected by the Protectee in the other direction,
			// with Log sharing on there. That reverse Partnership is a
			// separate relationship and grants nothing here.
			protectee, partner, _ := firstPartnership(t, service, db, false)
			partnerUp(t, service, partner, protectee, "protectee@example.com", true)
			return partner, protectee
		},
		loosen: true, viewLogs: false,
	}, {
		name: "repeated Partnership after the first ended",
		build: func(t *testing.T, service *Service, db *sql.DB) (int64, int64) {
			protectee, partner, partnership := firstPartnership(t, service, db, true)
			endNow(t, service, partnership, partner)
			// The new Partnership carries its own Log-sharing choice, and the
			// ended one's must not outlive it.
			partnerUp(t, service, protectee, partner, "partner@example.com", false)
			return partner, protectee
		},
		loosen: true, viewLogs: false,
	}}
}

func TestAuthorizationPredicatesAreDenyClosedAcrossEveryRelationshipState(t *testing.T) {
	t.Parallel()
	for _, scenario := range relationshipStates() {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			service, db := newTestService(t)
			ctx := context.Background()
			actor, protectee := scenario.build(t, service, db)

			loosen, err := service.CanLoosen(ctx, actor, protectee)
			if err != nil || loosen != scenario.loosen {
				t.Fatalf("CanLoosen = %v, %v; want %v", loosen, err, scenario.loosen)
			}
			viewLogs, err := service.CanViewLogs(ctx, actor, protectee)
			if err != nil || viewLogs != scenario.viewLogs {
				t.Fatalf("CanViewLogs = %v, %v; want %v", viewLogs, err, scenario.viewLogs)
			}
		})
	}
}

func TestAuthorizationPredicatesDenyMalformedAndUnknownIdentities(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()
	protectee, partner, _ := firstPartnership(t, service, db, true)
	const missing int64 = 999999

	for _, malformed := range []struct {
		name             string
		actor, protectee int64
	}{
		{"no session", unauthenticatedViewer, protectee},
		{"negative actor", -1, protectee},
		{"negative Protectee", partner, -1},
		{"zero Protectee", partner, 0},
		{"both unset", 0, 0},
		{"unknown actor", missing, protectee},
		{"unknown Protectee", partner, missing},
		// A Protectee identified as themselves is the one case where equal
		// ids permit, so an unknown id repeated must still be refused.
		{"unknown identity repeated", missing, missing},
	} {
		loosen, err := service.CanLoosen(ctx, malformed.actor, malformed.protectee)
		if err != nil || loosen {
			t.Fatalf("CanLoosen(%s) = %v, %v; want a refusal", malformed.name, loosen, err)
		}
		viewLogs, err := service.CanViewLogs(ctx, malformed.actor, malformed.protectee)
		if err != nil || viewLogs {
			t.Fatalf("CanViewLogs(%s) = %v, %v; want a refusal", malformed.name, viewLogs, err)
		}
	}
}

// reachabilityOn is one Partner's reachability as this Roster states it, or a
// sentinel when the row is absent, so a missing row can never be mistaken for
// a reassuring one.
func reachabilityOn(roster []RosterEntry, partnerEmail string) Reachability {
	for _, entry := range roster {
		if entry.PartnerEmail == partnerEmail {
			return entry.Reachability
		}
	}
	return Reachability("absent")
}

// unreachable makes Nightloch's mail to partnerEmail bounce for good, which is
// the only way a Roster row acquires a reachability warning to be audited.
func unreachable(t *testing.T, service *Service, partnerEmail string) {
	t.Helper()
	ctx := context.Background()
	sender := &mailmessage.RecordingSender{}
	if _, err := service.DispatchDue(ctx, sender, 50); err != nil {
		t.Fatal(err)
	}
	var messageID string
	if err := service.db.QueryRow(`SELECT COALESCE(provider_message_id, '') FROM mail_delivery_jobs
		WHERE recipient_email = ? AND provider_message_id IS NOT NULL
		ORDER BY id DESC LIMIT 1`, partnerEmail).Scan(&messageID); err != nil {
		t.Fatalf("no message was ever addressed to %s: %v", partnerEmail, err)
	}
	if err := service.RecordDeliveryEvent(ctx, DeliveryEvent{
		ProviderEventID:   "evt-" + partnerEmail,
		ProviderMessageID: messageID,
		Type:              EventBounced,
		Permanent:         true,
		OccurredAt:        testClock.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
}
