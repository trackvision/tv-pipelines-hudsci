// +build ignore

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"cloud.google.com/go/logging/logadmin"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/types/known/structpb"
)

func main() {
	ctx := context.Background()

	projectID := os.Getenv("GCP_PROJECT_ID")
	serviceName := os.Getenv("CLOUD_RUN_SERVICE")

	if projectID == "" {
		projectID = "hudscidev-100"
	}
	if serviceName == "" {
		serviceName = "pipelines"
	}

	fmt.Printf("=== Log Query Diagnostic ===\n")
	fmt.Printf("Project ID: %s\n", projectID)
	fmt.Printf("Service Name: %s\n", serviceName)
	fmt.Println()

	client, err := logadmin.NewClient(ctx, projectID)
	if err != nil {
		fmt.Printf("ERROR: Failed to create logadmin client: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	since := 2 * time.Hour
	sinceTime := time.Now().Add(-since)

	// Test 1: Basic filter without pipeline requirement
	fmt.Println("=== Test 1: Basic filter (no pipeline requirement) ===")
	filter1 := fmt.Sprintf(
		`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND timestamp>="%s"`,
		serviceName,
		sinceTime.Format(time.RFC3339),
	)
	fmt.Printf("Filter: %s\n", filter1)
	count1 := countLogs(ctx, client, filter1, 50)
	fmt.Printf("Results: %d entries\n\n", count1)

	// Test 2: Filter with pipeline!="" (what the code uses)
	fmt.Println("=== Test 2: Filter with jsonPayload.pipeline!=\"\" ===")
	filter2 := fmt.Sprintf(
		`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND timestamp>="%s" AND jsonPayload.pipeline!=""`,
		serviceName,
		sinceTime.Format(time.RFC3339),
	)
	fmt.Printf("Filter: %s\n", filter2)
	count2 := countLogs(ctx, client, filter2, 50)
	fmt.Printf("Results: %d entries\n\n", count2)

	// Test 3: Filter with jsonPayload.pipeline:* (exists check)
	fmt.Println("=== Test 3: Filter with jsonPayload.pipeline:* ===")
	filter3 := fmt.Sprintf(
		`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND timestamp>="%s" AND jsonPayload.pipeline:*`,
		serviceName,
		sinceTime.Format(time.RFC3339),
	)
	fmt.Printf("Filter: %s\n", filter3)
	count3 := countLogs(ctx, client, filter3, 50)
	fmt.Printf("Results: %d entries\n\n", count3)

	// Test 4: Show actual log entries with pipeline field
	fmt.Println("=== Test 4: Sample log entries with pipeline field ===")
	filter4 := fmt.Sprintf(
		`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND jsonPayload.pipeline:*`,
		serviceName,
	)
	showSampleLogs(ctx, client, filter4, 5)
}

func countLogs(ctx context.Context, client *logadmin.Client, filter string, limit int) int {
	iter := client.Entries(ctx, logadmin.Filter(filter), logadmin.NewestFirst())
	count := 0
	for count < limit {
		_, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			fmt.Printf("  Error iterating: %v\n", err)
			break
		}
		count++
	}
	return count
}

func showSampleLogs(ctx context.Context, client *logadmin.Client, filter string, limit int) {
	iter := client.Entries(ctx, logadmin.Filter(filter), logadmin.NewestFirst())
	count := 0
	for count < limit {
		entry, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			fmt.Printf("  Error: %v\n", err)
			break
		}
		count++

		fmt.Printf("\n--- Entry %d ---\n", count)
		fmt.Printf("Timestamp: %s\n", entry.Timestamp.Format(time.RFC3339))
		fmt.Printf("Severity: %s\n", entry.Severity)
		fmt.Printf("LogName: %s\n", entry.LogName)

		switch p := entry.Payload.(type) {
		case *structpb.Struct:
			fields := p.GetFields()
			fmt.Printf("Payload type: structpb.Struct\n")
			fmt.Printf("Fields: ")
			for k := range fields {
				fmt.Printf("%s, ", k)
			}
			fmt.Println()
			if msg := fields["msg"]; msg != nil {
				fmt.Printf("  msg: %s\n", msg.GetStringValue())
			}
			if pipeline := fields["pipeline"]; pipeline != nil {
				fmt.Printf("  pipeline: %s\n", pipeline.GetStringValue())
			}
		case map[string]interface{}:
			fmt.Printf("Payload type: map[string]interface{}\n")
			for k, v := range p {
				fmt.Printf("  %s: %v\n", k, v)
			}
		case string:
			fmt.Printf("Payload type: string\n")
			fmt.Printf("  %s\n", p)
		default:
			fmt.Printf("Payload type: %T\n", p)
		}
	}
}
