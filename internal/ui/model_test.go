package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/eoleedi/ua/internal/runner"
)

type fakeController struct {
	retries  []string
	retryErr error
	cancels  int
}

func (c *fakeController) Retry(id string) error {
	c.retries = append(c.retries, id)
	return c.retryErr
}

func (c *fakeController) Cancel() { c.cancels++ }

func newTestModel(t *testing.T, tasks []runner.Task) Model {
	t.Helper()
	model := New(tasks, &fakeController{}, make(chan runner.Event, 16))
	model = updateModel(t, model, tea.WindowSizeMsg{Width: 80, Height: 24})
	return model
}

func runningTestModel(t *testing.T) Model {
	t.Helper()
	model := newTestModel(t, []runner.Task{{ID: "brew", Name: "Homebrew"}})
	return updateModel(t, model, runner.Event{TaskID: "brew", Status: runner.Running, At: time.Unix(10, 0)})
}

func updateModel(t *testing.T, model Model, msg tea.Msg) Model {
	t.Helper()
	updated, _ := model.Update(msg)
	result, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want ui.Model", updated)
	}
	return result
}

func updateWithCommand(t *testing.T, model Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := model.Update(msg)
	result, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want ui.Model", updated)
	}
	return result, cmd
}

func press(t *testing.T, model Model, key string) Model {
	t.Helper()
	updated, _ := pressWithCommand(t, model, key)
	return updated
}

func pressWithCommand(t *testing.T, model Model, key string) (Model, tea.Cmd) {
	t.Helper()
	var msg tea.KeyMsg
	switch key {
	case "up":
		msg.Type = tea.KeyUp
	case "down":
		msg.Type = tea.KeyDown
	case "enter":
		msg.Type = tea.KeyEnter
	case "esc":
		msg.Type = tea.KeyEsc
	case "pgup":
		msg.Type = tea.KeyPgUp
	case "pgdown":
		msg.Type = tea.KeyPgDown
	case "ctrl+c":
		msg.Type = tea.KeyCtrlC
	default:
		msg.Type = tea.KeyRunes
		msg.Runes = []rune(key)
	}
	return updateWithCommand(t, model, msg)
}

func assertQuit(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected tea.Quit command, got nil")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("command returned %T, want tea.QuitMsg", cmd())
	}
}

func TestModelUpdatesStatusAndLogFromEvents(t *testing.T) {
	model := newTestModel(t, []runner.Task{{ID: "brew", Name: "Homebrew"}, {ID: "npm", Name: "npm"}})
	model = updateModel(t, model, runner.Event{TaskID: "brew", Status: runner.Running})
	model = updateModel(t, model, runner.Event{TaskID: "brew", Output: "old progress\rupgrading\n"})
	view := model.View()
	if !strings.Contains(view, "Homebrew") || !strings.Contains(view, "upgrading") || !strings.Contains(view, "RUNNING") {
		t.Fatalf("view missing state:\n%s", view)
	}
	if strings.Contains(view, "old progress") || model.tasks[1].Status != "" || model.tasks[1].Log != "" {
		t.Fatalf("event was not normalized or changed another task:\n%s", view)
	}
}

func TestSelectionBoundsAndFocus(t *testing.T) {
	model := newTestModel(t, []runner.Task{{ID: "a", Name: "Alpha"}, {ID: "b", Name: "Beta"}})
	model = press(t, model, "up")
	if model.selected != 0 {
		t.Fatalf("selection moved above zero: %d", model.selected)
	}
	model = press(t, model, "down")
	model = press(t, model, "down")
	if model.selected != 1 {
		t.Fatalf("selection moved beyond final task: %d", model.selected)
	}
	model = press(t, model, "enter")
	if !model.focusLog || !strings.Contains(model.View(), "LOG FOCUS") {
		t.Fatal("enter did not visibly focus the log")
	}
	model = press(t, model, "esc")
	if model.focusLog || !strings.Contains(model.View(), "OVERVIEW FOCUS") {
		t.Fatal("escape did not visibly focus the overview")
	}
}

func TestLogScrollingAndFollowMode(t *testing.T) {
	model := newTestModel(t, []runner.Task{{ID: "a", Name: "Alpha"}})
	model = updateModel(t, model, tea.WindowSizeMsg{Width: 50, Height: 12})
	model = updateModel(t, model, runner.Event{TaskID: "a", Status: runner.Running})
	model = updateModel(t, model, runner.Event{TaskID: "a", Output: numberedLines(40)})
	if !model.followLog || !model.viewport.AtBottom() {
		t.Fatal("new live output was not followed")
	}

	model = press(t, model, "pgup")
	pausedOffset := model.viewport.YOffset
	if model.followLog || pausedOffset == 0 {
		t.Fatal("page up did not pause follow mode")
	}
	model = updateModel(t, model, runner.Event{TaskID: "a", Output: "new output\n"})
	if model.viewport.YOffset != pausedOffset {
		t.Fatalf("paused viewport moved from %d to %d", pausedOffset, model.viewport.YOffset)
	}
	beforePageDown := model.viewport.YOffset
	model = press(t, model, "pgdown")
	if model.viewport.YOffset <= beforePageDown || model.followLog != model.viewport.AtBottom() {
		t.Fatal("page down did not scroll or synchronize follow mode")
	}
	model = press(t, model, "pgup")

	model = press(t, model, "enter")
	before := model.viewport.YOffset
	model = press(t, model, "up")
	if model.viewport.YOffset >= before {
		t.Fatal("up did not scroll one line in log focus")
	}
	model = press(t, model, "g")
	if model.viewport.YOffset != 0 || model.followLog {
		t.Fatal("g did not jump to the start with follow paused")
	}
	model = press(t, model, "G")
	if !model.viewport.AtBottom() || !model.followLog {
		t.Fatal("G did not jump to the bottom and resume follow")
	}
	model = updateModel(t, model, runner.Event{TaskID: "a", Output: "latest\n"})
	if !model.viewport.AtBottom() {
		t.Fatal("output did not auto-follow after returning to bottom")
	}
	model = press(t, model, "up")
	model = press(t, model, "down")
	if model.viewport.AtBottom() && !model.followLog {
		t.Fatal("line scrolling to bottom did not resume follow")
	}
}

func TestHelpToggle(t *testing.T) {
	model := newTestModel(t, []runner.Task{{ID: "a", Name: "Alpha"}})
	compact := model.View()
	model = press(t, model, "?")
	if !model.showHelp || model.View() == compact || !strings.Contains(model.View(), "Page Up/Down") {
		t.Fatal("? did not show expanded help")
	}
	model = press(t, model, "?")
	if model.showHelp {
		t.Fatal("second ? did not hide help")
	}
}

func TestRetryOnlyDispatchesForFailedOrCanceledTasks(t *testing.T) {
	statuses := []runner.Status{runner.Queued, runner.Running, runner.Succeeded, runner.Unavailable, runner.Failed, runner.Canceled}
	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			controller := &fakeController{}
			model := New([]runner.Task{{ID: "a", Name: "Alpha"}}, controller, make(chan runner.Event, 1))
			model = updateModel(t, model, runner.Event{TaskID: "a", Status: status})
			model = press(t, model, "r")
			want := status == runner.Failed || status == runner.Canceled
			if got := len(controller.retries) == 1; got != want {
				t.Fatalf("retry dispatched=%v, want %v", got, want)
			}
		})
	}
}

func TestRetryErrorIsVisible(t *testing.T) {
	controller := &fakeController{retryErr: errors.New("retry unavailable")}
	model := New([]runner.Task{{ID: "a", Name: "Alpha"}}, controller, make(chan runner.Event, 1))
	model = updateModel(t, model, runner.Event{TaskID: "a", Status: runner.Failed})
	model = press(t, model, "r")
	if !strings.Contains(model.View(), "retry unavailable") {
		t.Fatalf("retry error not visible:\n%s", model.View())
	}
}

func TestAcceptedRetryQueuedEventClearsOldState(t *testing.T) {
	model := newTestModel(t, []runner.Task{{ID: "a", Name: "Alpha"}})
	model = updateModel(t, model, runner.Event{TaskID: "a", Output: "old log\n"})
	model = updateModel(t, model, runner.Event{TaskID: "a", Status: runner.Failed, ExitCode: 7, Err: errors.New("failed"), At: time.Unix(20, 0)})
	model = updateModel(t, model, runner.Event{TaskID: "a", Status: runner.Queued, At: time.Unix(30, 0)})
	task := model.tasks[0]
	if task.Log != "" || task.ExitCode != 0 || task.Err != nil || !task.StartedAt.IsZero() || !task.EndedAt.IsZero() {
		t.Fatalf("retry did not clear old state: %+v", task)
	}
}

func TestTickUpdatesElapsedTimeAndReschedulesWhileActive(t *testing.T) {
	model := newTestModel(t, []runner.Task{{ID: "a", Name: "Alpha"}})
	model = updateModel(t, model, runner.Event{TaskID: "a", Status: runner.Running, At: time.Unix(10, 0)})
	model, cmd := updateWithCommand(t, model, tickMsg{at: time.Unix(75, 0)})
	if model.now != time.Unix(75, 0) || cmd == nil || !strings.Contains(model.View(), "1m05s") {
		t.Fatalf("active tick was not reflected or rescheduled:\n%s", model.View())
	}
	model = updateModel(t, model, runner.Event{TaskID: "a", Status: runner.Succeeded, At: time.Unix(80, 0)})
	_, cmd = updateWithCommand(t, model, tickMsg{at: time.Unix(81, 0)})
	if cmd != nil {
		t.Fatal("tick rescheduled after all tasks became terminal")
	}
}

func TestResponsiveLayoutAtRequiredSizes(t *testing.T) {
	tasks := make([]runner.Task, 8)
	for i := range tasks {
		tasks[i] = runner.Task{ID: fmt.Sprintf("task-%d", i), Name: fmt.Sprintf("Task %d", i)}
	}
	for _, size := range []tea.WindowSizeMsg{{Width: 80, Height: 24}, {Width: 50, Height: 12}} {
		t.Run(fmt.Sprintf("%dx%d", size.Width, size.Height), func(t *testing.T) {
			model := newTestModel(t, tasks)
			for i := range tasks {
				model = updateModel(t, model, runner.Event{TaskID: tasks[i].ID, Status: runner.Queued})
			}
			for range len(tasks) - 1 {
				model = press(t, model, "down")
			}
			model = updateModel(t, model, size)
			view := model.View()
			lines := strings.Split(view, "\n")
			if len(lines) > size.Height {
				t.Fatalf("view has %d lines at height %d:\n%s", len(lines), size.Height, view)
			}
			for _, line := range lines {
				if lipgloss.Width(line) > size.Width {
					t.Fatalf("line width %d exceeds %d: %q", lipgloss.Width(line), size.Width, line)
				}
			}
			if !strings.Contains(view, "Task 7") || !strings.Contains(view, "LOG") {
				t.Fatalf("selection-centered overview or log pane missing:\n%s", view)
			}
		})
	}
}

func TestViewHasAggregateCountsAndNonColorMarkers(t *testing.T) {
	statuses := []runner.Status{runner.Queued, runner.Running, runner.Succeeded, runner.Failed, runner.Canceled, runner.Unavailable}
	tasks := make([]runner.Task, len(statuses))
	modelTasks := make([]runner.Task, len(statuses))
	for i := range statuses {
		tasks[i] = runner.Task{ID: fmt.Sprint(i), Name: string(statuses[i])}
		modelTasks[i] = tasks[i]
	}
	model := newTestModel(t, modelTasks)
	for i, status := range statuses {
		model = updateModel(t, model, runner.Event{TaskID: tasks[i].ID, Status: status})
	}
	view := model.View()
	for _, text := range []string{"Q:1", "R:1", "OK:1", "FAIL:1", "CANCEL:1", "N/A:1", "[ ]", "[..]", "[ok]", "[x]", "[-]", "[?]", ">"} {
		if !strings.Contains(view, text) {
			t.Fatalf("view missing %q:\n%s", text, view)
		}
	}
}

func TestQuitWhileRunningRequiresConfirmation(t *testing.T) {
	model := runningTestModel(t)
	model = press(t, model, "q")
	if !model.confirmingQuit || !strings.Contains(model.View(), "Cancel 1 active task") {
		t.Fatal("q did not request a visible confirmation")
	}
	model = press(t, model, "n")
	if model.confirmingQuit {
		t.Fatal("n did not dismiss confirmation")
	}
}

func TestConfirmationPrecedesGlobalKeysAndCanCancel(t *testing.T) {
	controller := &fakeController{}
	model := New([]runner.Task{{ID: "a", Name: "Alpha"}}, controller, make(chan runner.Event, 1))
	model = updateModel(t, model, runner.Event{TaskID: "a", Status: runner.Running})
	model = press(t, model, "q")
	model = press(t, model, "?")
	if model.showHelp || !model.confirmingQuit {
		t.Fatal("global help key bypassed confirmation")
	}
	model, cmd := pressWithCommand(t, model, "y")
	if controller.cancels != 1 || !model.canceled {
		t.Fatal("confirmed quit did not cancel the controller")
	}
	assertQuit(t, cmd)
}

func TestSecondInterruptForcesCancellation(t *testing.T) {
	controller := &fakeController{}
	model := New([]runner.Task{{ID: "a", Name: "Alpha"}}, controller, make(chan runner.Event, 1))
	model = updateModel(t, model, runner.Event{TaskID: "a", Status: runner.Running})
	model = press(t, model, "ctrl+c")
	model, cmd := pressWithCommand(t, model, "ctrl+c")
	if controller.cancels != 1 || !model.CanceledByUser() {
		t.Fatal("second interrupt did not force cancellation")
	}
	assertQuit(t, cmd)
}

func TestTerminateMsgCancelsImmediately(t *testing.T) {
	controller := &fakeController{}
	model := New([]runner.Task{{ID: "a", Name: "Alpha"}}, controller, make(chan runner.Event, 1))
	model, cmd := updateWithCommand(t, model, TerminateMsg{})
	if controller.cancels != 1 || model.CanceledByUser() {
		t.Fatal("termination did not cancel immediately or was marked as user cancellation")
	}
	assertQuit(t, cmd)
}

func TestQuitWithoutConfirmationAfterCompletionAndExitCode(t *testing.T) {
	model := newTestModel(t, []runner.Task{{ID: "a", Name: "Alpha"}, {ID: "b", Name: "Beta"}})
	model = updateModel(t, model, runner.Event{TaskID: "a", Status: runner.Succeeded})
	model = updateModel(t, model, runner.Event{TaskID: "b", Status: runner.Succeeded})
	if model.ExitCode() != 0 {
		t.Fatalf("all-success exit code = %d, want 0", model.ExitCode())
	}
	model, cmd := pressWithCommand(t, model, "q")
	if model.confirmingQuit {
		t.Fatal("completed tasks unexpectedly required quit confirmation")
	}
	assertQuit(t, cmd)
	model = updateModel(t, model, runner.Event{TaskID: "b", Status: runner.Failed})
	if model.ExitCode() != 1 {
		t.Fatalf("failed aggregate exit code = %d, want 1", model.ExitCode())
	}
}

func numberedLines(count int) string {
	var builder strings.Builder
	for i := range count {
		fmt.Fprintf(&builder, "line %02d\n", i)
	}
	return builder.String()
}
