package pipelines

import (
	"context"
	"fmt"
	"time"

	"github.com/fieldryand/goflow/v2"
	"github.com/trackvision/tv-shared-go/logger"
	"go.uber.org/zap"
)

// ContextKey is a type for context keys used by the pipelines package.
type ContextKey string

// SkipStepsKey is the context key for skip steps.
const SkipStepsKey ContextKey = "skip_steps"

// Flow provides a fluent API for building and running pipelines.
type Flow struct {
	job       *goflow.Job
	taskOrder []string
	tasks     map[string]*goflow.Task
	name      string
}

// NewFlow creates a new pipeline flow.
func NewFlow(name string) *Flow {
	return &Flow{
		job: &goflow.Job{
			Name:     name,
			Schedule: "@manual",
			Active:   true,
		},
		tasks: make(map[string]*goflow.Task),
		name:  name,
	}
}

// AddTask adds a task to the flow. Dependencies are specified by name.
// Example: flow.AddTask("process", processFunc, "fetch1", "fetch2")
func (f *Flow) AddTask(name string, fn func() error, deps ...string) *Flow {
	task := &goflow.Task{
		Name:       name,
		Operator:   taskFunc(fn),
		Retries:    2,
		RetryDelay: goflow.ConstantDelay{Period: 5},
	}

	f.job.Add(task)
	f.tasks[name] = task
	f.taskOrder = append(f.taskOrder, name)

	// Set up dependencies
	for _, dep := range deps {
		if depTask, ok := f.tasks[dep]; ok {
			f.job.SetDownstream(depTask, task)
		}
	}

	return f
}

// Run executes the pipeline synchronously with comprehensive logging.
func (f *Flow) Run(ctx context.Context) error {
	startTime := time.Now()

	// Build task name list for logging
	taskNames := make([]string, 0, len(f.taskOrder))
	for _, name := range f.taskOrder {
		taskNames = append(taskNames, name)
	}

	// Get skip steps from context
	skipSteps := getSkipStepsFromContext(ctx)

	logger.Info("flow started",
		zap.String("pipeline", f.name),
		zap.Int("task_count", len(f.taskOrder)),
		zap.Strings("steps", taskNames),
		zap.Int("skip_count", len(skipSteps)))

	completedCount := 0
	skippedCount := 0

	for _, name := range f.taskOrder {
		task := f.tasks[name]

		if err := ctx.Err(); err != nil {
			return fmt.Errorf("cancelled before %s: %w", name, err)
		}

		// Check if this step should be skipped
		if skipSteps[name] {
			logger.Info("step skipped",
				zap.String("pipeline", f.name),
				zap.String("step", name))
			skippedCount++
			continue
		}

		if err := f.runTaskWithLogging(ctx, task); err != nil {
			return err
		}
		completedCount++
	}

	logger.Info("flow completed",
		zap.String("pipeline", f.name),
		zap.Duration("duration", time.Since(startTime)),
		zap.Int("steps_completed", completedCount),
		zap.Int("steps_skipped", skippedCount))

	return nil
}

// runTaskWithLogging executes a single task with detailed logging
func (f *Flow) runTaskWithLogging(ctx context.Context, t *goflow.Task) error {
	taskStart := time.Now()

	logger.Info("step started",
		zap.String("pipeline", f.name),
		zap.String("step", t.Name))

	if err := runWithRetry(ctx, t); err != nil {
		logger.Error("step failed",
			zap.String("pipeline", f.name),
			zap.String("step", t.Name),
			zap.Error(err),
			zap.Duration("duration", time.Since(taskStart)))
		return err
	}

	logger.Info("step completed",
		zap.String("pipeline", f.name),
		zap.String("step", t.Name),
		zap.Duration("duration", time.Since(taskStart)))

	return nil
}

// Job returns the underlying goflow Job for visualization.
func (f *Flow) Job() *goflow.Job {
	return f.job
}

// getSkipStepsFromContext extracts the skip steps set from context.
func getSkipStepsFromContext(ctx context.Context) map[string]bool {
	m := make(map[string]bool)
	if steps, ok := ctx.Value(SkipStepsKey).([]string); ok {
		for _, s := range steps {
			m[s] = true
		}
	}
	return m
}

// taskFunc wraps a simple function as a goflow Operator
type taskFunc func() error

func (fn taskFunc) Run() (any, error) {
	return nil, fn()
}

func runWithRetry(ctx context.Context, t *goflow.Task) error {
	maxAttempts := max(t.Retries+1, 1)
	retryDelay := 5 * time.Second
	if delay, ok := t.RetryDelay.(goflow.ConstantDelay); ok {
		retryDelay = time.Duration(delay.Period) * time.Second
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%s cancelled: %w", t.Name, err)
		}

		if attempt > 1 {
			logger.Info("Retrying task", zap.String("task", t.Name), zap.Int("attempt", attempt))
			time.Sleep(retryDelay)
		}

		if _, err := t.Operator.Run(); err != nil {
			lastErr = err
			logger.Warn("Task failed", zap.String("task", t.Name), zap.Error(err))
			continue
		}
		return nil
	}

	return fmt.Errorf("%s failed after %d attempts: %w", t.Name, maxAttempts, lastErr)
}
