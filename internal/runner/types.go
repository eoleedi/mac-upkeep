package runner

import (
	"context"
	"io"
	"time"
)

type Status string

const (
	Queued      Status = "queued"
	Running     Status = "running"
	Succeeded   Status = "succeeded"
	Failed      Status = "failed"
	Canceled    Status = "canceled"
	Unavailable Status = "unavailable"
)

type Task struct {
	ID       string
	Name     string
	Command  string
	Requires []string
}

type Result struct {
	ExitCode int
	Err      error
}

type Executor interface {
	Run(ctx context.Context, task Task, output io.Writer) Result
}

type LookupFunc func(name string) (string, error)

type Event struct {
	TaskID   string
	Status   Status
	Output   string
	ExitCode int
	Err      error
	At       time.Time
}
