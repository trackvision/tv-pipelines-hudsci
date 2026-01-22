package tasks

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/trackvision/tv-pipelines-hudsci/configs"
	"github.com/trackvision/tv-shared-go/logger"
	"go.uber.org/zap"

	_ "github.com/go-sql-driver/mysql"
)

// EventRow represents a raw event row from TiDB
type EventRow struct {
	EventID     string    `db:"event_id"`
	EventBody   string    `db:"event_body"`
	DateCreated time.Time `db:"date_created"`
}

// ConnectTiDB creates a new TiDB database connection
func ConnectTiDB(cfg *configs.Config) (*sqlx.DB, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&charset=utf8mb4",
		cfg.DBUser,
		cfg.DBPassword,
		cfg.DBHost,
		cfg.DBPort,
		cfg.DBName,
	)

	if cfg.DBSSL {
		dsn += "&tls=true"
	}

	logger.Info("Connecting to TiDB",
		zap.String("host", cfg.DBHost),
		zap.String("port", cfg.DBPort),
		zap.String("database", cfg.DBName),
	)

	db, err := sqlx.Connect("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("connecting to TiDB: %w", err)
	}

	// Set connection pool settings
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	logger.Info("TiDB connection established")
	return db, nil
}

// QueryShipmentEventsByCaptureID queries all EPCIS events related to a capture_id
// including shipping events, commissioning events, aggregation events, and child commissioning events.
//
// This query handles both direct shipping (item -> ship) and hierarchical shipping (item -> pack -> ship).
func QueryShipmentEventsByCaptureID(ctx context.Context, db *sqlx.DB, captureID string) ([]EventRow, error) {
	logger.Info("Querying shipment events", zap.String("capture_id", captureID))

	// Hierarchical CTE query from the migration plan
	query := `
WITH
top_level_events AS (
    -- Get shipping events from this capture
    SELECT event_id
    FROM epcis_events_raw
    WHERE capture_id = ?
),
top_level_keys AS (
    -- Get EPC join keys from shipping events (e.g., the SSCC being shipped)
    SELECT epc_join_key
    FROM epc_events
    WHERE event_id IN (SELECT event_id FROM top_level_events)
),
top_level_commissioning_events AS (
    -- Find commissioning events for top-level EPCs (direct shipping scenario)
    SELECT c.event_id
    FROM epc_events c
    WHERE c.biz_step = 'commissioning'
      AND c.epc_rel_type NOT IN ('inputQuantityList', 'inputEPCList')
      AND c.epc_join_key IN (SELECT epc_join_key FROM top_level_keys)
),
aggregation_events AS (
    -- Find aggregation/packing events where top-level EPCs are PARENTS
    -- This finds the packing event that put items into the shipped container
    SELECT c.child_epc_join_key,
           c.start_event_id AS aggregation_event_id,
           c.start_time AS aggregation_event_time
    FROM epc_events e
    LEFT JOIN view_aggregation_children c ON c.parent_epc_join_key = e.epc_join_key
    WHERE e.event_id IN (SELECT event_id FROM top_level_events)
),
child_commissioning_events AS (
    -- Find commissioning events for items packed into the container
    SELECT cc.event_id
    FROM epc_events cc
    WHERE cc.biz_step = 'commissioning'
      AND cc.epc_rel_type NOT IN ('inputQuantityList', 'inputEPCList')
      AND cc.epc_join_key IN (SELECT child_epc_join_key FROM aggregation_events)
)
-- Combine all event sources and get full event data
SELECT DISTINCT r.event_id, r.event_body, r.date_created
FROM (
    SELECT event_id FROM top_level_events
    UNION
    SELECT event_id FROM top_level_commissioning_events
    UNION
    SELECT aggregation_event_id AS event_id FROM aggregation_events WHERE aggregation_event_id IS NOT NULL
    UNION
    SELECT event_id FROM child_commissioning_events
) ev
INNER JOIN epcis_events_raw r ON r.event_id = ev.event_id
ORDER BY r.date_created ASC
LIMIT 1000`

	var events []EventRow
	err := db.SelectContext(ctx, &events, query, captureID)
	if err != nil {
		return nil, fmt.Errorf("querying shipment events: %w", err)
	}

	logger.Info("Found events",
		zap.String("capture_id", captureID),
		zap.Int("count", len(events)),
	)

	return events, nil
}

// GetShippingOperationByCaptureID fetches a shipping operation record by capture_id
func GetShippingOperationByCaptureID(ctx context.Context, db *sqlx.DB, captureID string) (map[string]interface{}, error) {
	query := `
		SELECT id, capture_id, status, date_created, date_updated
		FROM shipping_scanning_operation
		WHERE capture_id = ?
		LIMIT 1`

	var result map[string]interface{}
	rows, err := db.QueryxContext(ctx, query, captureID)
	if err != nil {
		return nil, fmt.Errorf("querying shipping operation: %w", err)
	}
	defer rows.Close()

	if rows.Next() {
		result = make(map[string]interface{})
		if err := rows.MapScan(result); err != nil {
			return nil, fmt.Errorf("scanning result: %w", err)
		}
		return result, nil
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating rows: %w", err)
	}

	return nil, sql.ErrNoRows
}

// CheckEventExists checks if an event_id exists in epcis_events_raw
func CheckEventExists(ctx context.Context, db *sqlx.DB, eventID string) (bool, error) {
	var count int
	query := `SELECT COUNT(*) FROM epcis_events_raw WHERE event_id = ?`
	err := db.GetContext(ctx, &count, query, eventID)
	if err != nil {
		return false, fmt.Errorf("checking event existence: %w", err)
	}
	return count > 0, nil
}
