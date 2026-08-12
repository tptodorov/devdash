package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type pickerStage int

const (
	stageLoading pickerStage = iota
	stageTransitions
	stageField
	stageApplying
)

// picker drives the status-change flow: fetch the allowed transitions, let the
// user choose one, collect any values JIRA requires, then apply it.
type picker struct {
	key       string
	summary   string
	current   string
	ticketURL string

	stage pickerStage
	err   error

	items  []Transition
	cursor int

	chosen      Transition
	queue       []RequiredField // required fields still to answer
	values      map[string]any  // answers collected so far
	fieldCursor int
}

func (p *picker) field() *RequiredField {
	if len(p.queue) == 0 {
		return nil
	}
	return &p.queue[0]
}

// choiceCount is the number of options on screen at the current stage.
func (p *picker) choiceCount() int {
	switch p.stage {
	case stageTransitions:
		return len(p.items)
	case stageField:
		if f := p.field(); f != nil {
			return len(f.Allowed)
		}
	}
	return 0
}

func (p *picker) moveCursor(delta int) {
	n := p.choiceCount()
	if n == 0 {
		return
	}
	switch p.stage {
	case stageTransitions:
		p.cursor = clamp(p.cursor+delta, 0, n-1)
	case stageField:
		p.fieldCursor = clamp(p.fieldCursor+delta, 0, n-1)
	}
}

// advance consumes the next required field, reporting whether the transition is
// ready to apply.
func (p *picker) advance() (ready bool) {
	for len(p.queue) > 0 && len(p.queue[0].Allowed) == 0 {
		p.queue = p.queue[1:] // unreachable in practice; Blocked() screens these
	}
	if len(p.queue) == 0 {
		return true
	}
	p.stage = stageField
	p.fieldCursor = 0
	return false
}

// selectTransition records the highlighted transition and decides what is next.
func (p *picker) selectTransition() (ready bool, err error) {
	if p.cursor < 0 || p.cursor >= len(p.items) {
		return false, nil
	}
	t := p.items[p.cursor]
	if blocked := t.Blocked(); len(blocked) > 0 {
		return false, fmt.Errorf("%q needs %s, which this tool cannot set — press o to open %s in JIRA",
			t.Name, strings.Join(blocked, " and "), p.key)
	}

	p.chosen = t
	p.values = map[string]any{}
	p.queue = nil
	for _, f := range t.Required {
		if len(f.Allowed) > 0 {
			p.queue = append(p.queue, f)
		}
	}
	return p.advance(), nil
}

// selectFieldValue records the highlighted value for the current field.
func (p *picker) selectFieldValue() (ready bool) {
	f := p.field()
	if f == nil || p.fieldCursor < 0 || p.fieldCursor >= len(f.Allowed) {
		return false
	}
	p.values[f.Key] = fieldValue(f.Allowed[p.fieldCursor])
	p.queue = p.queue[1:]
	return p.advance()
}

// back steps out of field selection; it reports whether the picker should close.
func (p *picker) back() (close bool) {
	if p.err != nil {
		p.err = nil
		if p.stage == stageField {
			p.stage = stageTransitions
		}
		return false
	}
	if p.stage == stageField {
		p.stage = stageTransitions
		p.queue = nil
		p.values = nil
		return false
	}
	return true
}

// ---------- rendering ----------

const pickerMaxWidth = 76

func (a *app) pickerView(lay layout, bodyHeight int) []string {
	p := a.picker
	boxW := min(pickerMaxWidth, lay.width-6)
	if boxW < 34 {
		boxW = 34
	}
	inner := boxW - 4 // rounded border plus one column of padding each side

	var lines []string
	add := func(ss ...seg) { lines = append(lines, segsPad(ss, inner, false)) }

	add(seg{text: trunc(p.key, inner), st: keySelStyle})
	if p.summary != "" {
		add(seg{text: trunc(p.summary, inner), st: mutedStyle})
	}

	switch p.stage {
	case stageLoading:
		add()
		add(seg{text: "Loading transitions…", st: warnStyle})

	case stageApplying:
		add()
		add(seg{text: "Moving to " + p.chosen.To + "…", st: warnStyle})

	case stageTransitions:
		add(seg{text: "now: ", st: faintStyle},
			seg{text: trunc(p.current, inner-5), st: lipgloss.NewStyle().Foreground(statusColor(p.current, ""))})
		add()
		lines = append(lines, a.transitionRows(inner, bodyHeight)...)

	case stageField:
		f := p.field()
		add(seg{text: "→ " + trunc(p.chosen.To, inner-2), st: lipgloss.NewStyle().
			Bold(true).Foreground(statusColor(p.chosen.To, p.chosen.ToCategory))})
		add()
		add(seg{text: trunc(f.Name+" is required", inner), st: normalStyle})
		add()
		lines = append(lines, a.fieldRows(f, inner, bodyHeight)...)
	}

	if p.err != nil {
		add()
		for _, l := range wrapText(p.err.Error(), inner) {
			add(seg{text: l, st: badStyle})
		}
	}

	add()
	add(seg{text: a.pickerKeys(), st: faintStyle})

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))

	out := strings.Split(box, "\n")
	// Centre the panel horizontally within the body area.
	if padLeft := (lay.width - boxW) / 2; padLeft > 0 {
		indent := strings.Repeat(" ", padLeft)
		for i := range out {
			out[i] = indent + out[i]
		}
	}
	return out
}

func (a *app) transitionRows(inner, bodyHeight int) []string {
	p := a.picker
	lo, hi := window(p.cursor, len(p.items), maxRows(bodyHeight, len(p.items)))

	var rows []string
	for i := lo; i < hi; i++ {
		t := p.items[i]
		marker, numSty := "  ", faintStyle
		if i == p.cursor {
			marker, numSty = "▸ ", keySelStyle
		}

		toSty := lipgloss.NewStyle().Foreground(statusColor(t.To, t.ToCategory))
		if i == p.cursor {
			toSty = toSty.Bold(true)
		}

		// Note the transition's own name when it differs from where it lands,
		// and flag the entry that would be a no-op.
		var note string
		switch {
		case strings.EqualFold(t.To, p.current):
			note = "  (current)"
		case !strings.EqualFold(t.Name, t.To):
			note = fmt.Sprintf("  via %q", t.Name)
		}
		if blocked := t.Blocked(); len(blocked) > 0 {
			note += "  needs " + strings.Join(blocked, "+")
		} else if t.NeedsInput() {
			note += "  +1 step"
		}

		num := fmt.Sprintf("%d ", i+1)
		if i+1 > 9 {
			num = "  "
		}
		ss := []seg{
			{text: marker, st: keySelStyle},
			{text: num, st: numSty},
			{text: t.To, st: toSty},
			{text: note, st: faintStyle},
		}
		rows = append(rows, segsPad(trimSegs(ss, inner), inner, false))
	}
	if hi-lo < len(p.items) {
		rows = append(rows, segsPad([]seg{{
			text: fmt.Sprintf("  … %d more", len(p.items)-(hi-lo)), st: faintStyle,
		}}, inner, false))
	}
	return rows
}

func (a *app) fieldRows(f *RequiredField, inner, bodyHeight int) []string {
	p := a.picker
	lo, hi := window(p.fieldCursor, len(f.Allowed), maxRows(bodyHeight, len(f.Allowed)))

	var rows []string
	for i := lo; i < hi; i++ {
		marker, sty := "  ", mutedStyle
		if i == p.fieldCursor {
			marker, sty = "▸ ", summarySelSty
		}
		num := fmt.Sprintf("%d ", i+1)
		if i+1 > 9 {
			num = "  "
		}
		ss := []seg{
			{text: marker, st: keySelStyle},
			{text: num, st: faintStyle},
			{text: f.Allowed[i].Name, st: sty},
		}
		rows = append(rows, segsPad(trimSegs(ss, inner), inner, false))
	}
	if hi-lo < len(f.Allowed) {
		rows = append(rows, segsPad([]seg{{
			text: fmt.Sprintf("  … %d more", len(f.Allowed)-(hi-lo)), st: faintStyle,
		}}, inner, false))
	}
	return rows
}

func (a *app) pickerKeys() string {
	switch a.picker.stage {
	case stageLoading, stageApplying:
		return "esc cancel"
	case stageField:
		return "↑↓ choose   ⏎ apply   esc back   o open"
	default:
		return "↑↓ choose   ⏎ select   esc cancel   o open"
	}
}

// maxRows leaves room for the panel's chrome inside the body area.
func maxRows(bodyHeight, items int) int {
	n := bodyHeight - 9
	if n < 3 {
		n = 3
	}
	return min(n, items)
}

// window returns the slice bounds that keep cursor visible in size rows.
func window(cursor, total, size int) (int, int) {
	if size >= total {
		return 0, total
	}
	lo := cursor - size/2
	lo = clamp(lo, 0, total-size)
	return lo, lo + size
}

// trimSegs shortens the trailing segments so the row fits within w columns.
func trimSegs(ss []seg, w int) []seg {
	for segsWidth(ss) > w && len(ss) > 0 {
		last := len(ss) - 1
		over := segsWidth(ss) - w
		if l := len([]rune(ss[last].text)); l > over {
			ss[last].text = trunc(ss[last].text, l-over)
		} else {
			ss = ss[:last]
		}
	}
	return ss
}

// wrapText breaks s into lines of at most w columns, splitting on spaces.
func wrapText(s string, w int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	line := words[0]
	for _, word := range words[1:] {
		if len(line)+1+len(word) > w {
			lines = append(lines, line)
			line = word
			continue
		}
		line += " " + word
	}
	return append(lines, line)
}
