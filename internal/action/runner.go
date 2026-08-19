package action

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/coreos/go-systemd/v22/dbus"
	"github.com/moby/moby/client"

	"github.com/olivermarcusson/dashboard/internal/hub"
	"github.com/olivermarcusson/dashboard/internal/store"
)

const TopicJobs = "jobs"

// Job is one execution, live or historical.
type Job struct {
	ID         string  `json:"id"`
	ActionID   string  `json:"action_id"`
	Label      string  `json:"label"`
	Kind       string  `json:"kind"`
	Target     string  `json:"target"`
	Actor      string  `json:"actor"`
	Status     string  `json:"status"`
	Output     string  `json:"output"`
	Error      string  `json:"error,omitempty"`
	StartedAt  string  `json:"started_at"`
	FinishedAt *string `json:"finished_at"`
}

const (
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
)

// Runner executes actions against Docker and systemd.
type Runner struct {
	db     *store.DB
	hub    *hub.Hub
	docker *client.Client
}

func NewRunner(db *store.DB, h *hub.Hub, docker *client.Client) *Runner {
	return &Runner{db: db, hub: h, docker: docker}
}

// Run executes an action and records it as a job.
//
// It blocks until the action finishes: these are seconds-long operations, and
// a caller that wants to watch progress subscribes to the jobs topic.
func (r *Runner) Run(ctx context.Context, actionID, actor string) (*Job, error) {
	kind, verb, target, err := ParseID(actionID)
	if err != nil {
		return nil, err
	}

	specs := For(kind, target, "")
	label := actionID
	for _, s := range specs {
		if s.ID == actionID {
			label = s.Label
		}
	}

	job := &Job{
		ID:        store.NewID(),
		ActionID:  actionID,
		Label:     label,
		Kind:      string(kind),
		Target:    target,
		Actor:     actor,
		Status:    StatusRunning,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO jobs (id, action_id, label, kind, target, actor, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.ActionID, job.Label, job.Kind, job.Target, job.Actor, job.Status); err != nil {
		return nil, err
	}
	r.hub.Publish(TopicJobs, job)

	// The action outlives the request that started it: a browser navigating
	// away must not cancel a restart halfway through.
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Minute)
	defer cancel()

	output, err := r.execute(runCtx, kind, verb, target)

	job.Output = output
	job.Status = StatusSucceeded
	if err != nil {
		job.Status = StatusFailed
		job.Error = err.Error()
	}
	finished := time.Now().UTC().Format(time.RFC3339)
	job.FinishedAt = &finished

	if _, dberr := r.db.ExecContext(context.WithoutCancel(ctx),
		`UPDATE jobs SET status = ?, output = ?, error = ?, finished_at = ? WHERE id = ?`,
		job.Status, job.Output, job.Error, finished, job.ID); dberr != nil {
		return job, dberr
	}

	detail, _ := json.Marshal(map[string]string{"action": actionID, "status": job.Status})
	r.db.Audit(context.WithoutCancel(ctx), "action.run", actor, string(detail))
	r.hub.Publish(TopicJobs, job)

	return job, nil
}

func (r *Runner) execute(ctx context.Context, kind Kind, verb, target string) (string, error) {
	switch kind {
	case KindContainer:
		return r.container(ctx, verb, target)
	case KindStack:
		return r.stack(ctx, verb, target)
	case KindUnit:
		return r.unit(ctx, verb, target)
	}
	return "", fmt.Errorf("unknown action kind %q", kind)
}

func (r *Runner) container(ctx context.Context, verb, name string) (string, error) {
	if r.docker == nil {
		return "", fmt.Errorf("the Docker daemon is not reachable")
	}
	var err error
	switch verb {
	case "start":
		_, err = r.docker.ContainerStart(ctx, name, client.ContainerStartOptions{})
	case "stop":
		_, err = r.docker.ContainerStop(ctx, name, client.ContainerStopOptions{})
	case "restart":
		_, err = r.docker.ContainerRestart(ctx, name, client.ContainerRestartOptions{})
	default:
		return "", fmt.Errorf("%q is not something you can do to a container", verb)
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s %s: ok", verb, name), nil
}

// stack applies a verb to every container in a compose project. Failures are
// collected rather than aborting: stopping four of five containers and
// reporting which one refused is more useful than stopping at the first error.
func (r *Runner) stack(ctx context.Context, verb, project string) (string, error) {
	if r.docker == nil {
		return "", fmt.Errorf("the Docker daemon is not reachable")
	}
	list, err := r.docker.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: make(client.Filters).Add("label", "com.docker.compose.project="+project),
	})
	if err != nil {
		return "", err
	}
	if len(list.Items) == 0 {
		return "", fmt.Errorf("no containers belong to the %s stack", project)
	}

	var lines []string
	var failures int
	for _, c := range list.Items {
		name := project
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		out, err := r.container(ctx, verb, name)
		if err != nil {
			failures++
			lines = append(lines, fmt.Sprintf("%s %s: %v", verb, name, err))
			continue
		}
		lines = append(lines, out)
	}
	result := strings.Join(lines, "\n")
	if failures > 0 {
		return result, fmt.Errorf("%d of %d containers failed", failures, len(list.Items))
	}
	return result, nil
}

func (r *Runner) unit(ctx context.Context, verb, name string) (string, error) {
	if !strings.HasSuffix(name, ".service") {
		name += ".service"
	}
	conn, err := dbus.NewSystemdConnectionContext(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	done := make(chan string, 1)
	switch verb {
	case "start":
		_, err = conn.StartUnitContext(ctx, name, "replace", done)
	case "stop":
		_, err = conn.StopUnitContext(ctx, name, "replace", done)
	case "restart":
		_, err = conn.RestartUnitContext(ctx, name, "replace", done)
	default:
		return "", fmt.Errorf("%q is not something you can do to a service", verb)
	}
	if err != nil {
		return "", err
	}

	select {
	case result := <-done:
		if result != "done" {
			return "", fmt.Errorf("systemd reported %q", result)
		}
		return fmt.Sprintf("%s %s: done", verb, name), nil
	case <-ctx.Done():
		return "", fmt.Errorf("timed out waiting for systemd")
	}
}

// Recent lists job history, newest first.
func (r *Runner) Recent(ctx context.Context, limit int) ([]Job, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, action_id, label, kind, target, actor, status, output, error, started_at, finished_at
		   FROM jobs ORDER BY started_at DESC, rowid DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

// Get returns one job by id.
func (r *Runner) Get(ctx context.Context, id string) (*Job, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, action_id, label, kind, target, actor, status, output, error, started_at, finished_at
		   FROM jobs WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	jobs, err := scanJobs(rows)
	if err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		return nil, fmt.Errorf("no such job")
	}
	return &jobs[0], nil
}

func scanJobs(rows *sql.Rows) ([]Job, error) {
	out := []Job{}
	for rows.Next() {
		var j Job
		var finished sql.NullString
		if err := rows.Scan(&j.ID, &j.ActionID, &j.Label, &j.Kind, &j.Target, &j.Actor,
			&j.Status, &j.Output, &j.Error, &j.StartedAt, &finished); err != nil {
			return nil, err
		}
		if finished.Valid {
			j.FinishedAt = &finished.String
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
