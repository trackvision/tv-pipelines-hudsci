package tasks

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestQueryShipmentEventsByCaptureID(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create mock: %v", err)
	}
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "sqlmock")
	ctx := context.Background()

	captureID := "test-capture-123"
	now := time.Now()

	// Mock rows that should be returned
	rows := sqlmock.NewRows([]string{"event_id", "event_body", "date_created"}).
		AddRow("event-1", `{"eventType":"ObjectEvent","action":"OBSERVE"}`, now).
		AddRow("event-2", `{"eventType":"ObjectEvent","action":"ADD"}`, now.Add(1*time.Minute))

	// Expect the complex CTE query with captureID parameter
	mock.ExpectQuery("WITH").
		WithArgs(captureID).
		WillReturnRows(rows)

	events, err := QueryShipmentEventsByCaptureID(ctx, sqlxDB, captureID)
	if err != nil {
		t.Fatalf("QueryShipmentEventsByCaptureID failed: %v", err)
	}

	if len(events) != 2 {
		t.Errorf("Expected 2 events, got %d", len(events))
	}

	if events[0].EventID != "event-1" {
		t.Errorf("Expected first event ID to be 'event-1', got '%s'", events[0].EventID)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %v", err)
	}
}

func TestQueryShipmentEventsByCaptureID_NoResults(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create mock: %v", err)
	}
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "sqlmock")
	ctx := context.Background()

	captureID := "nonexistent-capture"

	// Mock empty result
	rows := sqlmock.NewRows([]string{"event_id", "event_body", "date_created"})
	mock.ExpectQuery("WITH").
		WithArgs(captureID).
		WillReturnRows(rows)

	events, err := QueryShipmentEventsByCaptureID(ctx, sqlxDB, captureID)
	if err != nil {
		t.Fatalf("QueryShipmentEventsByCaptureID failed: %v", err)
	}

	if len(events) != 0 {
		t.Errorf("Expected 0 events, got %d", len(events))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %v", err)
	}
}

func TestGetShippingOperationByCaptureID(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create mock: %v", err)
	}
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "sqlmock")
	ctx := context.Background()

	captureID := "test-capture-123"
	now := time.Now()

	rows := sqlmock.NewRows([]string{"id", "capture_id", "status", "date_created", "date_updated"}).
		AddRow("op-1", captureID, "approved", now, now)

	mock.ExpectQuery("SELECT id, capture_id, status, date_created, date_updated").
		WithArgs(captureID).
		WillReturnRows(rows)

	result, err := GetShippingOperationByCaptureID(ctx, sqlxDB, captureID)
	if err != nil {
		t.Fatalf("GetShippingOperationByCaptureID failed: %v", err)
	}

	if result == nil {
		t.Fatal("Expected result, got nil")
	}

	if result["id"] != "op-1" {
		t.Errorf("Expected id 'op-1', got '%v'", result["id"])
	}

	if result["status"] != "approved" {
		t.Errorf("Expected status 'approved', got '%v'", result["status"])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %v", err)
	}
}

func TestGetShippingOperationByCaptureID_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create mock: %v", err)
	}
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "sqlmock")
	ctx := context.Background()

	captureID := "nonexistent-capture"

	rows := sqlmock.NewRows([]string{"id", "capture_id", "status", "date_created", "date_updated"})
	mock.ExpectQuery("SELECT id, capture_id, status, date_created, date_updated").
		WithArgs(captureID).
		WillReturnRows(rows)

	result, err := GetShippingOperationByCaptureID(ctx, sqlxDB, captureID)
	if err != sql.ErrNoRows {
		t.Fatalf("Expected sql.ErrNoRows, got: %v", err)
	}

	if result != nil {
		t.Errorf("Expected nil result, got: %v", result)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %v", err)
	}
}

func TestCheckEventExists(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create mock: %v", err)
	}
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "sqlmock")
	ctx := context.Background()

	tests := []struct {
		name      string
		eventID   string
		mockCount int
		expected  bool
	}{
		{"Event exists", "event-123", 1, true},
		{"Event does not exist", "nonexistent", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows := sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(tt.mockCount)
			mock.ExpectQuery("SELECT COUNT").
				WithArgs(tt.eventID).
				WillReturnRows(rows)

			exists, err := CheckEventExists(ctx, sqlxDB, tt.eventID)
			if err != nil {
				t.Fatalf("CheckEventExists failed: %v", err)
			}

			if exists != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, exists)
			}

			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("Unfulfilled expectations: %v", err)
			}
		})
	}
}
