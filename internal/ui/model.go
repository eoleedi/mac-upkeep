package ui

import (
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/eoleedi/ua/internal/runner"
)

type Controller interface {
	Retry(id string) error
	Cancel()
}

type TerminateMsg struct{}

type tickMsg struct {
	at time.Time
}

type eventChannelClosedMsg struct{}

type taskView struct {
	Task       runner.Task
	Status     runner.Status
	Log        string
	Normalizer runner.Normalizer
	ExitCode   int
	Err        error
	StartedAt  time.Time
	EndedAt    time.Time
}

type Model struct {
	tasks          []taskView
	selected       int
	viewport       viewport.Model
	focusLog       bool
	showHelp       bool
	confirmingQuit bool
	canceled       bool
	followLog      bool
	width          int
	height         int
	controller     Controller
	events         <-chan runner.Event
	now            time.Time
	retryErr       error
}

func New(tasks []runner.Task, controller Controller, events <-chan runner.Event) Model {
	views := make([]taskView, len(tasks))
	for i, task := range tasks {
		views[i].Task = task
	}

	return Model{
		tasks:      views,
		viewport:   viewport.New(1, 1),
		followLog:  true,
		width:      80,
		height:     24,
		controller: controller,
		events:     events,
		now:        time.Now(),
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(waitForEvent(m.events), tick())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case TerminateMsg:
		m.controller.Cancel()
		return m, tea.Quit
	case runner.Event:
		m.applyEvent(msg)
		return m, waitForEvent(m.events)
	case eventChannelClosedMsg:
		m.events = nil
		return m, nil
	case tickMsg:
		m.now = msg.at
		if m.hasActiveTasks() {
			return m, tick()
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.width = max(1, msg.Width)
		m.height = max(1, msg.Height)
		m.resizeViewport()
		return m, nil
	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m Model) View() string {
	return m.view()
}

func (m Model) ExitCode() int {
	states := make(map[string]runner.Status, len(m.tasks))
	for _, task := range m.tasks {
		states[task.Task.ID] = task.Status
	}
	return runner.Aggregate(states)
}

func (m Model) CanceledByUser() bool {
	return m.canceled
}

func (m Model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.confirmingQuit {
		switch key {
		case "y":
			m.controller.Cancel()
			m.canceled = true
			return m, tea.Quit
		case "q", "ctrl+c":
			m.controller.Cancel()
			m.canceled = true
			return m, tea.Quit
		case "n", "esc":
			m.confirmingQuit = false
		}
		return m, nil
	}

	switch key {
	case "q", "ctrl+c":
		if m.hasActiveTasks() {
			m.confirmingQuit = true
			return m, nil
		}
		return m, tea.Quit
	case "?":
		m.showHelp = !m.showHelp
		m.resizeViewport()
		return m, nil
	case "r":
		m.retrySelected()
		return m, nil
	case "pgup":
		m.viewport.PageUp()
		m.followLog = false
		return m, nil
	case "pgdown":
		m.viewport.PageDown()
		m.followLog = m.viewport.AtBottom()
		return m, nil
	case "g":
		m.viewport.GotoTop()
		m.followLog = false
		return m, nil
	case "G":
		m.viewport.GotoBottom()
		m.followLog = true
		return m, nil
	}

	if m.focusLog {
		switch key {
		case "esc":
			m.focusLog = false
		case "up", "k":
			m.viewport.LineUp(1)
			m.followLog = false
		case "down", "j":
			m.viewport.LineDown(1)
			m.followLog = m.viewport.AtBottom()
		}
		return m, nil
	}

	switch key {
	case "enter":
		m.focusLog = true
	case "up", "k":
		m.selectTask(m.selected - 1)
	case "down", "j":
		m.selectTask(m.selected + 1)
	}
	return m, nil
}

func (m *Model) applyEvent(event runner.Event) {
	for i := range m.tasks {
		task := &m.tasks[i]
		if task.Task.ID != event.TaskID {
			continue
		}

		if event.Status == runner.Queued && terminal(task.Status) {
			task.Log = ""
			task.Normalizer = runner.Normalizer{}
			task.ExitCode = 0
			task.Err = nil
			task.StartedAt = time.Time{}
			task.EndedAt = time.Time{}
			m.retryErr = nil
		}
		if event.Output != "" {
			task.Log = task.Normalizer.Append(task.Log, event.Output)
		}
		if event.Status != "" {
			task.Status = event.Status
			task.ExitCode = event.ExitCode
			task.Err = event.Err
			switch event.Status {
			case runner.Running:
				task.StartedAt = eventTime(event.At)
				task.EndedAt = time.Time{}
			case runner.Succeeded, runner.Failed, runner.Canceled, runner.Unavailable:
				task.EndedAt = eventTime(event.At)
			}
		}
		if i == m.selected {
			m.refreshViewport()
		}
		return
	}
}

func (m *Model) refreshViewport() {
	if len(m.tasks) == 0 {
		m.viewport.SetContent("")
		return
	}
	offset := m.viewport.YOffset
	m.viewport.SetContent(m.tasks[m.selected].Log)
	if m.followLog {
		m.viewport.GotoBottom()
	} else {
		m.viewport.SetYOffset(offset)
	}
}

func (m *Model) selectTask(selected int) {
	if len(m.tasks) == 0 {
		return
	}
	m.selected = max(0, min(selected, len(m.tasks)-1))
	m.followLog = true
	m.refreshViewport()
}

func (m *Model) retrySelected() {
	if len(m.tasks) == 0 {
		return
	}
	task := m.tasks[m.selected]
	if task.Status != runner.Failed && task.Status != runner.Canceled {
		return
	}
	m.retryErr = m.controller.Retry(task.Task.ID)
}

func (m Model) hasActiveTasks() bool {
	for _, task := range m.tasks {
		if task.Status == runner.Queued || task.Status == runner.Running {
			return true
		}
	}
	return false
}

func (m Model) activeTaskCount() int {
	count := 0
	for _, task := range m.tasks {
		if task.Status == runner.Queued || task.Status == runner.Running {
			count++
		}
	}
	return count
}

func (m *Model) resizeViewport() {
	helpRows := m.helpRows()
	overviewRows := m.overviewRows()
	m.viewport.Width = max(1, m.width-2)
	m.viewport.Height = max(1, m.height-1-overviewRows-helpRows-2)
	m.refreshViewport()
}

func (m Model) overviewRows() int {
	available := max(1, (m.height-5)/2)
	return min(len(m.tasks), available)
}

func (m Model) helpRows() int {
	if m.showHelp && m.height >= 16 {
		return 2
	}
	return 1
}

func waitForEvent(events <-chan runner.Event) tea.Cmd {
	if events == nil {
		return nil
	}
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return eventChannelClosedMsg{}
		}
		return event
	}
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(at time.Time) tea.Msg { return tickMsg{at: at} })
}

func eventTime(at time.Time) time.Time {
	if at.IsZero() {
		return time.Now()
	}
	return at
}

func terminal(status runner.Status) bool {
	switch status {
	case runner.Succeeded, runner.Failed, runner.Canceled, runner.Unavailable:
		return true
	default:
		return false
	}
}
