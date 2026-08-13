package main

import (
	"context"
	"fmt"
	"strings"
)

// schedulePlan is what has to change for Symphony to pick a ticket up. Every
// condition is read from WORKFLOW.md rather than assumed, so a project that
// configures Symphony differently is handled without changes here.
type schedulePlan struct {
	key          string
	addLabels    []string // required labels the ticket does not carry yet
	removeLabels []string // required labels to strip when unscheduling
	toStatus     string   // active state to move it to, empty when already active
	// unschedule reports that the plan takes the ticket out of Symphony's queue
	// rather than putting it in.
	unschedule bool
	// refusal explains why the ticket cannot be scheduled at all.
	refusal string
}

// nothingToDo reports whether the ticket already satisfies every condition.
func (p schedulePlan) nothingToDo() bool {
	return p.refusal == "" && len(p.addLabels) == 0 &&
		len(p.removeLabels) == 0 && p.toStatus == ""
}

// summary describes what was, or would be, changed.
func (p schedulePlan) summary() string {
	var parts []string
	if len(p.addLabels) > 0 {
		parts = append(parts, "labelled "+strings.Join(p.addLabels, ", "))
	}
	if len(p.removeLabels) > 0 {
		parts = append(parts, "removed "+strings.Join(p.removeLabels, ", "))
	}
	if p.toStatus != "" {
		parts = append(parts, "moved to "+p.toStatus)
	}
	return strings.Join(parts, " and ")
}

// scheduled reports whether a ticket already satisfies every condition, which is
// what makes the key a toggle rather than a one-way action.
func scheduled(cfg symphonyConfig, t Ticket) bool {
	for _, want := range cfg.Tracker.RequiredLabels {
		if !containsFold(t.Labels, want) {
			return false
		}
	}
	return containsFold(cfg.Tracker.ActiveStates, t.Status)
}

// symphonyHasIt reports whether Symphony holds a live session for the ticket.
// Running, blocked and retrying all mean it is in hand; only the scheduled marker
// means nothing is in flight.
func symphonyHasIt(t Ticket) bool {
	return t.Symphony != "" && t.Symphony != SymphonyScheduled
}

// planToggle decides what the key does: a ticket Symphony would already pick up
// is taken back out of the queue, anything else is put into it.
//
// Unscheduling only strips the required labels. Undoing the status change too
// would mean guessing where the ticket came from, and a ticket sitting in To Do
// without the label is simply not Symphony's business.
//
// A ticket Symphony already has a session for cannot be unscheduled: removing the
// label would not stop the work, it would only make the dashboard disagree with
// what is happening.
func planToggle(cfg symphonyConfig, t Ticket) schedulePlan {
	if !scheduled(cfg, t) {
		return planSchedule(cfg, t)
	}

	plan := schedulePlan{key: t.Key, unschedule: true}
	if project := cfg.Tracker.Provider.ProjectKey; project != "" {
		prefix, _, _ := strings.Cut(t.Key, "-")
		if !strings.EqualFold(prefix, project) {
			plan.refusal = fmt.Sprintf("Symphony only watches project %s", project)
			return plan
		}
	}
	if symphonyHasIt(t) {
		plan.refusal = fmt.Sprintf("Symphony is already %s %s; stop the session first",
			t.Symphony, t.Key)
		return plan
	}
	for _, label := range cfg.Tracker.RequiredLabels {
		if containsFold(t.Labels, label) {
			plan.removeLabels = append(plan.removeLabels, label)
		}
	}
	return plan
}

func containsFold(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(strings.TrimSpace(s), strings.TrimSpace(want)) {
			return true
		}
	}
	return false
}

// planSchedule works out how to make a ticket eligible for Symphony.
//
// Symphony's conditions come from the tracker block of WORKFLOW.md: the issue
// must belong to project_key, carry every required_label, and sit in one of
// active_states. terminal_states are refused rather than reopened, since
// restarting finished work is a bigger decision than a keystroke should make.
func planSchedule(cfg symphonyConfig, t Ticket) schedulePlan {
	plan := schedulePlan{key: t.Key}
	tracker := cfg.Tracker

	if project := tracker.Provider.ProjectKey; project != "" {
		prefix, _, _ := strings.Cut(t.Key, "-")
		if !strings.EqualFold(prefix, project) {
			plan.refusal = fmt.Sprintf("Symphony only watches project %s", project)
			return plan
		}
	}
	if containsFold(tracker.TerminalStates, t.Status) {
		plan.refusal = fmt.Sprintf("%s is %s, which Symphony treats as finished", t.Key, t.Status)
		return plan
	}
	if len(tracker.ActiveStates) == 0 {
		plan.refusal = "WORKFLOW.md lists no active_states for Symphony"
		return plan
	}

	for _, want := range tracker.RequiredLabels {
		if !containsFold(t.Labels, want) {
			plan.addLabels = append(plan.addLabels, want)
		}
	}
	if !containsFold(tracker.ActiveStates, t.Status) {
		// The first active state is the queue Symphony draws from; its own
		// workflow moves To Do on to In Progress when it starts.
		plan.toStatus = tracker.ActiveStates[0]
	}
	return plan
}

// applySchedule carries out a plan: labels first, then the transition, so a
// failure to transition still leaves the ticket labelled and visibly closer.
func applySchedule(ctx context.Context, jira *jiraClient, plan schedulePlan) error {
	if err := jira.UpdateLabels(ctx, plan.key, plan.addLabels, plan.removeLabels); err != nil {
		return err
	}
	if plan.toStatus == "" {
		return nil
	}

	transitions, err := jira.Transitions(ctx, plan.key)
	if err != nil {
		return err
	}
	for _, tr := range transitions {
		if !strings.EqualFold(tr.To, plan.toStatus) || len(tr.Required) > 0 {
			continue
		}
		return jira.ApplyTransition(ctx, plan.key, tr.ID, nil)
	}
	return fmt.Errorf("no transition from %s to %s that needs no extra fields",
		plan.key, plan.toStatus)
}
