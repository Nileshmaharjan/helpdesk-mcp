package helpdesk

import (
	"errors"
	"strings"
	"testing"
)

func TestNewTicketValidate(t *testing.T) {
	ok := NewTicket{Subject: "Kiosk frozen", Body: "Stuck on loading", Priority: PriorityHigh, Requester: "x"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid ticket rejected: %v", err)
	}
	cases := map[string]NewTicket{
		"blank subject": {Subject: "  ", Body: "b", Priority: PriorityLow},
		"long subject":  {Subject: strings.Repeat("s", MaxSubject+1), Body: "b", Priority: PriorityLow},
		"blank body":    {Subject: "s", Body: "", Priority: PriorityLow},
		"long body":     {Subject: "s", Body: strings.Repeat("b", MaxBody+1), Priority: PriorityLow},
		"bad priority":  {Subject: "s", Body: "b", Priority: "asap"},
	}
	for name, n := range cases {
		if err := n.Validate(); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: want ErrInvalidInput, got %v", name, err)
		}
	}
}

func TestLifecycle(t *testing.T) {
	if CanTransition(StatusClosed, StatusOpen) {
		t.Error("closed tickets must be final")
	}
	if CanTransition(StatusEscalated, StatusEscalated) {
		t.Error("a no-op transition should be rejected")
	}
	if !CanTransition(StatusOpen, StatusEscalated) || !CanTransition(StatusEscalated, StatusClosed) {
		t.Error("open -> escalated -> closed must be allowed")
	}
}

func TestClampLimit(t *testing.T) {
	for _, c := range []struct{ in, def, want int }{{0, 5, 5}, {-3, 5, 5}, {7, 5, 7}, {500, 5, MaxLimit}} {
		if got := ClampLimit(c.in, c.def); got != c.want {
			t.Errorf("ClampLimit(%d, %d) = %d, want %d", c.in, c.def, got, c.want)
		}
	}
}
