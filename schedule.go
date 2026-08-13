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
	key       string
	addLabels []string // required labels the ticket does not carry yet
	toStatus  string   // active state to move it to, empty when already active
	// refusal explains why the ticket cannot be scheduled at all.
	refusal string
}

// nothingToDo reports whether the ticket already satisfies every condition.
func (p schedulePlan) nothingToDo() bool {
	return p.refusal == "" && len(p.addLabels) == 0 && p.toStatus == ""
}

// summary describes what was, or would be, changed.
func (p schedulePlan) summary() string {
	var parts []string
	if len(p.addLabels) > 0 {
		parts = append(parts, "labelled "+strings.Join(p.addLabels, ", "))
	}
	if p.toStatus != "" {
		parts = append(parts, "moved to "+p.toStatus)
	}
	return strings.Join(parts, " and ")
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
	if err := jira.AddLabels(ctx, plan.key, plan.addLabels); err != nil {
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
