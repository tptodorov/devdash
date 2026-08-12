package main

import (
	"strings"
	"testing"
)

func samplePicker() *picker {
	return &picker{
		key:     "PROJ-1",
		current: "Awaiting CR",
		stage:   stageTransitions,
		items: []Transition{
			{ID: "21", Name: "In Progress", To: "In Progress", ToCategory: "In Progress"},
			{ID: "41", Name: "Closed", To: "Closed", ToCategory: "Done", Required: []RequiredField{
				{Key: "resolution", Name: "Resolution", Allowed: []FieldOption{
					{ID: "10000", Name: "Done"}, {ID: "10001", Name: "Won't Do"},
				}},
			}},
			{ID: "51", Name: "Needs Comment", To: "Reviewed", Required: []RequiredField{
				{Key: "comment", Name: "Comment"},
			}},
		},
	}
}

func TestPickerAppliesSimpleTransitionImmediately(t *testing.T) {
	p := samplePicker()
	p.cursor = 0

	ready, err := p.selectTransition()
	if err != nil {
		t.Fatalf("selectTransition() error = %v", err)
	}
	if !ready {
		t.Fatal("a transition with no required fields should be ready at once")
	}
	if p.chosen.ID != "21" {
		t.Errorf("chosen = %q, want 21", p.chosen.ID)
	}
	if len(p.values) != 0 {
		t.Errorf("values = %v, want empty", p.values)
	}
}

func TestPickerCollectsRequiredResolution(t *testing.T) {
	p := samplePicker()
	p.cursor = 1 // Closed, which requires a resolution

	ready, err := p.selectTransition()
	if err != nil {
		t.Fatalf("selectTransition() error = %v", err)
	}
	if ready {
		t.Fatal("Closed must not apply before a resolution is chosen")
	}
	if p.stage != stageField {
		t.Fatalf("stage = %v, want stageField", p.stage)
	}
	if f := p.field(); f == nil || f.Key != "resolution" {
		t.Fatalf("field() = %+v, want resolution", p.field())
	}

	p.fieldCursor = 1 // Won't Do
	if !p.selectFieldValue() {
		t.Fatal("choosing the last required value should be ready to apply")
	}
	got, ok := p.values["resolution"].(map[string]string)
	if !ok || got["id"] != "10001" {
		t.Errorf("values[resolution] = %v, want id 10001", p.values["resolution"])
	}
}

func TestPickerRefusesTransitionItCannotSatisfy(t *testing.T) {
	p := samplePicker()
	p.cursor = 2 // Needs Comment, a required field with no selectable values

	ready, err := p.selectTransition()
	if ready {
		t.Error("a transition needing an unsupported field must not apply")
	}
	if err == nil {
		t.Fatal("expected an explanatory error")
	}
	if !strings.Contains(err.Error(), "Comment") || !strings.Contains(err.Error(), "PROJ-1") {
		t.Errorf("error = %q, want it to name the field and the ticket", err)
	}
}

func TestPickerBackStepsOutOfFieldStage(t *testing.T) {
	p := samplePicker()
	p.cursor = 1
	if _, err := p.selectTransition(); err != nil {
		t.Fatalf("selectTransition() error = %v", err)
	}

	if closed := p.back(); closed {
		t.Error("back() from the field stage should return to the transition list, not close")
	}
	if p.stage != stageTransitions {
		t.Errorf("stage = %v, want stageTransitions", p.stage)
	}
	if len(p.values) != 0 {
		t.Errorf("values should be discarded on back(), got %v", p.values)
	}

	if closed := p.back(); !closed {
		t.Error("back() from the transition list should close the picker")
	}
}

func TestPickerBackClearsErrorFirst(t *testing.T) {
	p := samplePicker()
	p.cursor = 2
	_, err := p.selectTransition()
	if err == nil {
		t.Fatal("expected an error to be raised")
	}
	p.err = err

	if closed := p.back(); closed {
		t.Error("the first back() should dismiss the error, not close the picker")
	}
	if p.err != nil {
		t.Errorf("err = %v, want nil after dismissing", p.err)
	}
	if closed := p.back(); !closed {
		t.Error("the second back() should close the picker")
	}
}

func TestPickerCursorStaysInRange(t *testing.T) {
	p := samplePicker()
	p.moveCursor(-5)
	if p.cursor != 0 {
		t.Errorf("cursor = %d, want 0", p.cursor)
	}
	p.moveCursor(50)
	if p.cursor != len(p.items)-1 {
		t.Errorf("cursor = %d, want %d", p.cursor, len(p.items)-1)
	}
}

// The cursor is restored by ticket identity, so a status change that reorders
// the groups must not leave the cursor pointing at a different ticket.
func TestSettleKeepsCursorOnSameTicket(t *testing.T) {
	a := &app{
		tickets: []Ticket{
			{Key: "PROJ-1", Status: "Awaiting CR", Category: "In Progress"},
			{Key: "PROJ-2", Status: "Awaiting CR", Category: "In Progress"},
			{Key: "PROJ-3", Status: "Backlog", Category: "To Do"},
		},
	}
	a.settle()

	a.cursor = 2 // PROJ-3, last row
	if got := a.sel[a.cursor].ticketKey; got != "PROJ-3" {
		t.Fatalf("setup: cursor on %q, want PROJ-3", got)
	}

	// PROJ-3 moves to In Progress, which sorts it above the Backlog group.
	a.tickets[2].Status = "In Progress"
	a.settle()

	if got := a.sel[a.cursor].ticketKey; got != "PROJ-3" {
		t.Errorf("after reorder the cursor is on %q, want PROJ-3", got)
	}
}

func TestSettleClampsCursorWhenRowDisappears(t *testing.T) {
	a := &app{tickets: []Ticket{
		{Key: "PROJ-1", Status: "To Do", Category: "To Do"},
		{Key: "PROJ-2", Status: "To Do", Category: "To Do"},
	}}
	a.settle()
	a.cursor = 1

	// PROJ-2 is resolved and drops out of the query.
	a.tickets = a.tickets[:1]
	a.settle()

	if a.cursor != 0 {
		t.Errorf("cursor = %d, want 0 after the row disappeared", a.cursor)
	}
}
