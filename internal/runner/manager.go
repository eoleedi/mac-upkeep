package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

const eventBufferSize = 64

type Manager struct {
	tasks    []Task
	limit    int
	executor Executor
	lookup   LookupFunc

	commands chan any
	events   chan Event
	done     chan struct{}

	startOnce sync.Once
	cancel    context.CancelFunc
}

type taskRun struct {
	task   Task
	status Status
	result Result
}

type completion struct {
	id     string
	result Result
}

type retryRequest struct {
	id       string
	response chan error
}

func NewManager(tasks []Task, limit int, executor Executor, lookup LookupFunc) *Manager {
	return &Manager{
		tasks:    append([]Task(nil), tasks...),
		limit:    limit,
		executor: executor,
		lookup:   lookup,
		commands: make(chan any),
		events:   make(chan Event, eventBufferSize),
		done:     make(chan struct{}),
	}
}

func (m *Manager) Start(parent context.Context) <-chan Event {
	m.startOnce.Do(func() {
		ctx, cancel := context.WithCancel(parent)
		m.cancel = cancel
		go m.coordinate(ctx)
	})
	return m.events
}

func (m *Manager) Retry(id string) error {
	request := retryRequest{id: id, response: make(chan error, 1)}
	select {
	case m.commands <- request:
	case <-m.done:
		return errors.New("manager stopped")
	}

	select {
	case err := <-request.response:
		return err
	case <-m.done:
		return errors.New("manager stopped")
	}
}

func (m *Manager) Cancel() {
	m.Start(context.Background())
	m.cancel()
}

func (m *Manager) Wait() {
	<-m.done
}

func Aggregate(states map[string]Status) int {
	if len(states) == 0 {
		return 1
	}
	for _, status := range states {
		if status != Succeeded {
			return 1
		}
	}
	return 0
}

func (m *Manager) coordinate(ctx context.Context) {
	runs := make(map[string]*taskRun, len(m.tasks))
	queue := make([]*taskRun, 0, len(m.tasks))
	for _, task := range m.tasks {
		run := &taskRun{task: task}
		runs[task.ID] = run
		if err := m.checkRequirements(task); err != nil {
			run.status = Unavailable
			run.result.Err = err
			m.emit(Event{TaskID: task.ID, Status: Unavailable, Err: err})
			continue
		}
		run.status = Queued
		queue = append(queue, run)
		m.emit(Event{TaskID: task.ID, Status: Queued})
	}

	active := 0
	canceled := false
	startAvailable := func() {
		for !canceled && ctx.Err() == nil && active < m.limit && len(queue) > 0 {
			run := queue[0]
			queue = queue[1:]
			run.status = Running
			active++
			m.emit(Event{TaskID: run.task.ID, Status: Running})
			go m.execute(ctx, run.task)
		}
	}
	startAvailable()

	ctxDone := ctx.Done()
	for {
		select {
		case message := <-m.commands:
			switch command := message.(type) {
			case completion:
				run := runs[command.id]
				run.result = command.result
				run.status = terminalStatus(ctx, command.result)
				m.emit(Event{
					TaskID:   command.id,
					Status:   run.status,
					ExitCode: command.result.ExitCode,
					Err:      command.result.Err,
				})
				active--
				startAvailable()
			case retryRequest:
				if canceled || ctx.Err() != nil {
					command.response <- errors.New("manager canceled")
					continue
				}
				run, ok := runs[command.id]
				if !ok {
					command.response <- fmt.Errorf("unknown task %q", command.id)
					continue
				}
				if run.status != Failed {
					command.response <- fmt.Errorf("task %q is %s", command.id, run.status)
					continue
				}
				run.result = Result{}
				if err := m.checkRequirements(run.task); err != nil {
					run.status = Unavailable
					run.result.Err = err
					m.emit(Event{TaskID: run.task.ID, Status: Unavailable, Err: err})
					command.response <- nil
					continue
				}
				run.status = Queued
				queue = append(queue, run)
				m.emit(Event{TaskID: run.task.ID, Status: Queued})
				startAvailable()
				command.response <- nil
			}
		case <-ctxDone:
			canceled = true
			ctxDone = nil
			for _, run := range queue {
				run.status = Canceled
				run.result = Result{Err: ctx.Err()}
				m.emit(Event{TaskID: run.task.ID, Status: Canceled, Err: ctx.Err()})
			}
			queue = nil
		}

		if canceled && active == 0 {
			close(m.events)
			close(m.done)
			return
		}
	}
}

func (m *Manager) execute(ctx context.Context, task Task) {
	result := m.executor.Run(ctx, task, eventWriter{taskID: task.ID, events: m.events})
	m.commands <- completion{id: task.ID, result: result}
}

func (m *Manager) checkRequirements(task Task) error {
	for _, requirement := range task.Requires {
		if _, err := m.lookup(requirement); err != nil {
			return fmt.Errorf("requirement %q: %w", requirement, err)
		}
	}
	return nil
}

func (m *Manager) emit(event Event) {
	event.At = time.Now()
	m.events <- event
}

func terminalStatus(ctx context.Context, result Result) Status {
	if ctx.Err() != nil || errors.Is(result.Err, context.Canceled) {
		return Canceled
	}
	if result.Err != nil || result.ExitCode != 0 {
		return Failed
	}
	return Succeeded
}

type eventWriter struct {
	taskID string
	events chan<- Event
}

func (w eventWriter) Write(p []byte) (int, error) {
	w.events <- Event{TaskID: w.taskID, Output: string(p), At: time.Now()}
	return len(p), nil
}

var _ io.Writer = eventWriter{}
