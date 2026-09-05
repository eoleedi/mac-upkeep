package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/eoleedi/ua/internal/runner"
)

var statusStyles = map[runner.Status]lipgloss.Style{
	runner.Queued:      lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#4B5563", Dark: "#D1D5DB"}),
	runner.Running:     lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#005FCC", Dark: "#66B2FF"}).Bold(true),
	runner.Succeeded:   lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#087F23", Dark: "#5FE078"}).Bold(true),
	runner.Failed:      lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#B42318", Dark: "#FF7B72"}).Bold(true),
	runner.Canceled:    lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#8A4B00", Dark: "#FFB454"}),
	runner.Unavailable: lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#6B21A8", Dark: "#D8B4FE"}),
}

func (m Model) view() string {
	if m.confirmingQuit {
		return m.confirmationView()
	}

	lines := []string{m.headerView()}
	lines = append(lines, m.overviewView()...)
	lines = append(lines, m.logView()...)
	lines = append(lines, m.helpView()...)
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	for i := range lines {
		lines[i] = truncate(lines[i], m.width)
	}
	return strings.Join(lines, "\n")
}

func (m Model) headerView() string {
	counts := make(map[runner.Status]int)
	for _, task := range m.tasks {
		counts[task.Status]++
	}
	header := fmt.Sprintf("UPDATE ALL  Q:%d R:%d OK:%d FAIL:%d CANCEL:%d N/A:%d",
		counts[runner.Queued], counts[runner.Running], counts[runner.Succeeded],
		counts[runner.Failed], counts[runner.Canceled], counts[runner.Unavailable])
	return lipgloss.NewStyle().Bold(true).Render(header)
}

func (m Model) overviewView() []string {
	rows := m.overviewRows()
	if rows == 0 {
		return nil
	}
	start := m.selected - rows/2
	start = max(0, min(start, len(m.tasks)-rows))
	result := make([]string, 0, rows)
	for i := start; i < start+rows; i++ {
		task := m.tasks[i]
		selection := "  "
		if i == m.selected {
			selection = "> "
		}
		row := selection + styledMarker(task.Status) + " " + task.Task.Name
		if elapsed := m.elapsed(task); elapsed != "" {
			row += "  " + elapsed
		}
		if detail := failureDetail(task); detail != "" {
			row += "  " + detail
		}
		if task.RetryErr != nil {
			row += "  retry: " + task.RetryErr.Error()
		}
		if i == m.selected {
			row = lipgloss.NewStyle().Bold(true).Render(row)
		}
		result = append(result, truncate(row, m.width))
	}
	return result
}

func (m Model) logView() []string {
	innerWidth := max(1, m.width-2)
	focus := "OVERVIEW FOCUS"
	name := "No tasks"
	if m.focusLog {
		focus = "LOG FOCUS"
	}
	title := fmt.Sprintf(" LOG: %s | %s ", name, focus)
	if len(m.tasks) > 0 {
		name = m.tasks[m.selected].Task.Name
		title = fmt.Sprintf(" LOG: %s | %s | %s ", name, statusLabel(m.tasks[m.selected].Status), focus)
	}
	top := "+" + ansi.Truncate(title+strings.Repeat("-", innerWidth), innerWidth, "") + "+"
	content := strings.Split(m.viewport.View(), "\n")
	if len(content) > m.viewport.Height {
		content = content[:m.viewport.Height]
	}
	for len(content) < m.viewport.Height {
		content = append(content, "")
	}
	lines := make([]string, 0, m.viewport.Height+2)
	lines = append(lines, top)
	for _, line := range content {
		line = ansi.Truncate(line, innerWidth, "")
		lines = append(lines, "|"+line+strings.Repeat(" ", max(0, innerWidth-lipgloss.Width(line)))+"|")
	}
	lines = append(lines, "+"+strings.Repeat("-", innerWidth)+"+")
	return lines
}

func (m Model) helpView() []string {
	if m.showHelp && m.height >= 16 {
		return []string{
			"Overview: j/k select  Enter log  Page Up/Down scroll  c cancel  r retry",
			"Log: j/k lines  g/G start/end  Esc overview  ? help  q quit",
		}
	}
	if m.focusLog {
		return []string{"j/k scroll  g/G start/end  Esc overview  ? help  q quit"}
	}
	return []string{"j/k select  Enter log  PgUp/PgDn scroll  c cancel  r retry  ? help  q quit"}
}

func (m Model) confirmationView() string {
	count := m.activeTaskCount()
	noun := "tasks"
	if count == 1 {
		noun = "task"
	}
	message := fmt.Sprintf("Cancel %d active %s? [y] yes  [n] no", count, noun)
	boxWidth := min(max(1, m.width), lipgloss.Width(message)+4)
	message = truncate(message, max(1, boxWidth-4))
	box := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Bold(true).Padding(0, 1).Width(max(1, boxWidth-4)).Render(message)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m Model) elapsed(task taskView) string {
	if task.StartedAt.IsZero() {
		return ""
	}
	end := task.EndedAt
	if end.IsZero() {
		end = m.now
	}
	duration := end.Sub(task.StartedAt)
	if duration < 0 {
		duration = 0
	}
	return formatDuration(duration)
}

func formatDuration(duration time.Duration) string {
	seconds := int(duration.Round(time.Second) / time.Second)
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	if seconds < 3600 {
		return fmt.Sprintf("%dm%02ds", seconds/60, seconds%60)
	}
	return fmt.Sprintf("%dh%02dm%02ds", seconds/3600, seconds/60%60, seconds%60)
}

func failureDetail(task taskView) string {
	if task.Status != runner.Failed && task.Status != runner.Unavailable {
		return ""
	}
	if task.Err != nil {
		return task.Err.Error()
	}
	if task.Status == runner.Failed {
		return fmt.Sprintf("exit %d", task.ExitCode)
	}
	return "unavailable"
}

func styledMarker(status runner.Status) string {
	marker := statusMarker(status)
	if style, ok := statusStyles[status]; ok {
		return style.Render(marker)
	}
	return marker
}

func statusMarker(status runner.Status) string {
	switch status {
	case runner.Queued:
		return "[ ]"
	case runner.Running:
		return "[..]"
	case runner.Succeeded:
		return "[ok]"
	case runner.Failed:
		return "[x]"
	case runner.Canceled:
		return "[-]"
	case runner.Unavailable:
		return "[?]"
	default:
		return "[ ]"
	}
}

func statusLabel(status runner.Status) string {
	return strings.ToUpper(string(status))
}

func truncate(value string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(value, width, "")
}
