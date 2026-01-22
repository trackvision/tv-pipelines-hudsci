package pipelines

import (
	"context"
	"errors"
	"testing"
)

func TestFlowBasic(t *testing.T) {
	executed := []string{}

	flow := NewFlow("test")
	flow.AddTask("task1", func() error {
		executed = append(executed, "task1")
		return nil
	})
	flow.AddTask("task2", func() error {
		executed = append(executed, "task2")
		return nil
	}, "task1")

	if err := flow.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(executed) != 2 {
		t.Errorf("Expected 2 tasks executed, got %d", len(executed))
	}
	if executed[0] != "task1" {
		t.Errorf("Expected task1 first, got %s", executed[0])
	}
	if executed[1] != "task2" {
		t.Errorf("Expected task2 second, got %s", executed[1])
	}
}

func TestFlowError(t *testing.T) {
	expectedErr := errors.New("task failed")

	flow := NewFlow("test")
	flow.AddTask("task1", func() error {
		return expectedErr
	})
	flow.AddTask("task2", func() error {
		t.Error("task2 should not execute after task1 fails")
		return nil
	}, "task1")

	err := flow.Run(context.Background())
	if err == nil {
		t.Fatal("Run() expected error")
	}
}

func TestFlowContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	flow := NewFlow("test")
	flow.AddTask("task1", func() error {
		return nil
	})

	err := flow.Run(ctx)
	if err == nil {
		t.Fatal("Run() expected error for cancelled context")
	}
}

func TestFlowSkipSteps(t *testing.T) {
	executed := []string{}

	flow := NewFlow("test")
	flow.AddTask("task1", func() error {
		executed = append(executed, "task1")
		return nil
	})
	flow.AddTask("task2", func() error {
		executed = append(executed, "task2")
		return nil
	}, "task1")
	flow.AddTask("task3", func() error {
		executed = append(executed, "task3")
		return nil
	}, "task2")

	// Skip task2
	ctx := context.WithValue(context.Background(), SkipStepsKey, []string{"task2"})

	if err := flow.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// Should execute task1 and task3, but skip task2
	if len(executed) != 2 {
		t.Errorf("Expected 2 tasks executed, got %d: %v", len(executed), executed)
	}
	if executed[0] != "task1" {
		t.Errorf("Expected task1 first, got %s", executed[0])
	}
	if executed[1] != "task3" {
		t.Errorf("Expected task3 second, got %s", executed[1])
	}
}

func TestFlowSkipMultipleSteps(t *testing.T) {
	executed := []string{}

	flow := NewFlow("test")
	flow.AddTask("task1", func() error {
		executed = append(executed, "task1")
		return nil
	})
	flow.AddTask("task2", func() error {
		executed = append(executed, "task2")
		return nil
	}, "task1")
	flow.AddTask("task3", func() error {
		executed = append(executed, "task3")
		return nil
	}, "task2")
	flow.AddTask("task4", func() error {
		executed = append(executed, "task4")
		return nil
	}, "task3")

	// Skip task2 and task3
	ctx := context.WithValue(context.Background(), SkipStepsKey, []string{"task2", "task3"})

	if err := flow.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// Should execute only task1 and task4
	if len(executed) != 2 {
		t.Errorf("Expected 2 tasks executed, got %d: %v", len(executed), executed)
	}
	if executed[0] != "task1" {
		t.Errorf("Expected task1 first, got %s", executed[0])
	}
	if executed[1] != "task4" {
		t.Errorf("Expected task4 second, got %s", executed[1])
	}
}

func TestFlowNoSkipSteps(t *testing.T) {
	executed := []string{}

	flow := NewFlow("test")
	flow.AddTask("task1", func() error {
		executed = append(executed, "task1")
		return nil
	})
	flow.AddTask("task2", func() error {
		executed = append(executed, "task2")
		return nil
	}, "task1")

	// No skip steps in context - all tasks should run
	if err := flow.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(executed) != 2 {
		t.Errorf("Expected 2 tasks executed, got %d: %v", len(executed), executed)
	}
}
