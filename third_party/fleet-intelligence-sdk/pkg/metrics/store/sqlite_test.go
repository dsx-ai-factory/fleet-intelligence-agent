// Copyright 2024 Lepton AI Inc
// Source: https://github.com/leptonai/gpud

package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmetadata "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metadata"
	pkgmetrics "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics"
	pkgsqlite "github.com/NVIDIA/fleet-intelligence-sdk/pkg/sqlite"
)

func TestSQLiteNewStore(t *testing.T) {
	// Setup test database
	dbRW, dbRO, cleanup := pkgsqlite.OpenTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Test creating a new store
	store, err := NewSQLiteStore(ctx, dbRW, dbRO, "test_metrics")
	require.NoError(t, err)
	require.NotNil(t, store)

	// Test with empty table name
	_, err = NewSQLiteStore(ctx, dbRW, dbRO, "")
	assert.Equal(t, ErrEmptyTableName, err)
}

func TestSQLiteLateSamplesAndRepeatedReceipts(t *testing.T) {
	ctx := context.Background()
	dbRW, dbRO, cleanup := pkgsqlite.OpenTestDB(t)
	defer cleanup()
	store, err := NewSQLiteStore(ctx, dbRW, dbRO, "late_samples")
	require.NoError(t, err)
	cutoff := time.Now().Add(-time.Minute)
	m := pkgmetrics.Metric{
		UnixMilliseconds: cutoff.Add(-time.Hour).UnixMilli(),
		Component:        "dcgm-nvlink", Name: "counter", Type: pkgmetrics.MetricTypeCounter,
		Labels: map[string]string{"gpu": "0"}, Value: 42,
	}
	failedCtx, cancel := context.WithCancel(ctx)
	cancel()
	require.Error(t, store.Record(failedCtx, m))
	rows, err := store.Read(ctx)
	require.NoError(t, err)
	require.Empty(t, rows, "a failed write must not consume the source sample")
	require.NoError(t, store.Record(ctx, m, m))
	// Use a distinct receipt time so this assertion cannot pass accidentally
	// when several writes happen within one wall-clock millisecond.
	firstReceipt := time.Now().Add(-30 * time.Second).UnixMilli()
	_, err = dbRW.ExecContext(ctx, "UPDATE late_samples SET recorded_at = ?", firstReceipt)
	require.NoError(t, err)
	// Reopening the store models an agent restart: deduplication must persist.
	store, err = NewSQLiteStore(ctx, dbRW, dbRO, "late_samples")
	require.NoError(t, err)
	m.Value = 43 // Preserve the existing update-on-conflict contract.
	require.NoError(t, store.Record(ctx, m))
	var repeatedReceipt int64
	require.NoError(t, dbRO.QueryRowContext(ctx, "SELECT recorded_at FROM late_samples").Scan(&repeatedReceipt))
	require.Equal(t, firstReceipt, repeatedReceipt)
	rows, err = store.Read(ctx, pkgmetrics.WithRecordedSince(cutoff), pkgmetrics.WithComponents("dcgm-nvlink"))
	require.NoError(t, err)
	require.Equal(t, pkgmetrics.Metrics{m}, rows, "late measurements must remain exportable")
	rows, err = store.Read(ctx, pkgmetrics.WithSince(cutoff))
	require.NoError(t, err)
	require.Empty(t, rows, "measurement-time API queries retain their meaning")
	rows, err = store.Read(ctx, pkgmetrics.WithRecordedSince(cutoff), pkgmetrics.WithSince(cutoff))
	require.NoError(t, err)
	require.Empty(t, rows)
	rows, err = store.Read(ctx, pkgmetrics.WithRecordedSince(time.UnixMilli(firstReceipt+1)))
	require.NoError(t, err)
	require.Empty(t, rows, "duplicates must not renew the export lookback")
	count, err := store.Purge(ctx, cutoff)
	require.NoError(t, err)
	require.Zero(t, count, "a late sample must survive long enough to export")
	// A new measurement with the same value remains a distinct point.
	m.UnixMilliseconds += 60_000
	require.NoError(t, store.Record(ctx, m))
	rows, err = store.Read(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	// Repeated reads did not extend retention for the first sample.
	count, err = store.Purge(ctx, time.Now().Add(time.Millisecond))
	require.NoError(t, err)
	require.Equal(t, 2, count)
}

func TestSQLiteReceiptMigrationPreservesLegacyAndRollbackWrites(t *testing.T) {
	ctx := context.Background()
	dbRW, dbRO, cleanup := pkgsqlite.OpenTestDB(t)
	defer cleanup()
	_, err := dbRW.ExecContext(ctx, `CREATE TABLE legacy (
		unix_milliseconds INTEGER NOT NULL, component_name TEXT NOT NULL,
		metric_name TEXT NOT NULL, metric_labels TEXT, metric_value REAL NOT NULL,
		PRIMARY KEY (unix_milliseconds, component_name, metric_name, metric_labels)
	) WITHOUT ROWID`)
	require.NoError(t, err)
	oldTime := time.Now().Add(-time.Hour).UnixMilli()
	_, err = dbRW.ExecContext(ctx, "INSERT INTO legacy VALUES (?, 'cpu', 'usage', '', 10)", oldTime)
	require.NoError(t, err)
	store, err := NewSQLiteStore(ctx, dbRW, dbRO, "legacy")
	require.NoError(t, err)
	rows, err := store.Read(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, pkgmetrics.MetricTypeGauge, rows[0].Type)
	// A duplicate legacy sample must retain its original effective receipt time.
	require.NoError(t, store.Record(ctx, rows[0]))
	// Simulate a previous binary writing its explicit six-column INSERT.
	newTime := time.Now().UnixMilli()
	_, err = dbRW.ExecContext(ctx, `INSERT OR REPLACE INTO legacy
		(unix_milliseconds, component_name, metric_name, metric_labels, metric_value, metric_type)
		VALUES (?, 'cpu', 'usage', '', 20, 'gauge')`, newTime)
	require.NoError(t, err)
	store, err = NewSQLiteStore(ctx, dbRW, dbRO, "legacy")
	require.NoError(t, err)
	rows, err = store.Read(ctx, pkgmetrics.WithRecordedSince(time.UnixMilli(newTime)))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, newTime, rows[0].UnixMilliseconds)
	count, err := store.Purge(ctx, time.UnixMilli(newTime))
	require.NoError(t, err)
	require.Equal(t, 1, count, "legacy retention falls back to measurement time")
	var plan string
	var id, parent, unused int
	require.NoError(t, dbRO.QueryRowContext(ctx, `EXPLAIN QUERY PLAN SELECT * FROM legacy
		WHERE COALESCE(recorded_at, unix_milliseconds) >= ?`, newTime).Scan(&id, &parent, &unused, &plan))
	require.Contains(t, plan, "legacy_recorded_at", "receipt-time lookbacks must be indexed")
}

func TestSQLiteReceiptMigrationRollsBackOnFailure(t *testing.T) {
	ctx := context.Background()
	dbRW, _, cleanup := pkgsqlite.OpenTestDB(t)
	defer cleanup()
	_, err := dbRW.ExecContext(ctx, `CREATE TABLE legacy (unix_milliseconds INTEGER);
		CREATE TABLE legacy_recorded_at (collision INTEGER);`)
	require.NoError(t, err)
	require.Error(t, ensureMetricColumns(ctx, dbRW, "legacy"))
	var addedColumns int
	require.NoError(t, dbRW.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('legacy')
		WHERE name IN ('metric_type', 'recorded_at')`).Scan(&addedColumns))
	require.Zero(t, addedColumns, "a failed index migration must not leave partial columns")
	_, err = dbRW.ExecContext(ctx, "DROP TABLE legacy_recorded_at")
	require.NoError(t, err)
	require.NoError(t, ensureMetricColumns(ctx, dbRW, "legacy"), "migration must be retryable")
}

func TestSQLiteNewStore_AddsMetricTypeColumnToExistingTable(t *testing.T) {
	dbRW, dbRO, cleanup := pkgsqlite.OpenTestDB(t)
	defer cleanup()

	ctx := context.Background()
	table := "test_metrics_legacy"
	_, err := dbRW.ExecContext(ctx, fmt.Sprintf(`
CREATE TABLE %s (
	%s INTEGER NOT NULL,
	%s TEXT NOT NULL,
	%s TEXT NOT NULL,
	%s TEXT,
	%s REAL NOT NULL,
	PRIMARY KEY (%s, %s, %s, %s)
) WITHOUT ROWID;`,
		table,
		columnUnixMilliseconds, columnComponentName, columnMetricName, columnMetricLabels, columnMetricValue,
		columnUnixMilliseconds, columnComponentName, columnMetricName, columnMetricLabels,
	))
	require.NoError(t, err)

	store, err := NewSQLiteStore(ctx, dbRW, dbRO, table)
	require.NoError(t, err)

	err = store.Record(ctx, pkgmetrics.Metric{
		UnixMilliseconds: time.Now().UnixMilli(),
		Component:        "test-component",
		Name:             "counter_metric",
		Type:             pkgmetrics.MetricTypeCounter,
		Value:            10,
	})
	require.NoError(t, err)

	results, err := store.Read(ctx)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, pkgmetrics.MetricTypeCounter, results[0].Type)
}

func TestEnsureMetricColumnsRejectsInvalidTableName(t *testing.T) {
	dbRW, _, cleanup := pkgsqlite.OpenTestDB(t)
	defer cleanup()

	ctx := context.Background()
	err := ensureMetricColumns(ctx, dbRW, `test_metrics; DROP TABLE test_metrics;`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid sqlite identifier")
}

func TestSQLiteStore_Record(t *testing.T) {
	// Setup test database
	dbRW, dbRO, cleanup := pkgsqlite.OpenTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create a new store
	store, err := NewSQLiteStore(ctx, dbRW, dbRO, "test_metrics")
	require.NoError(t, err)

	// Create test metrics
	now := time.Now().UnixMilli()
	metrics := []pkgmetrics.Metric{
		{
			UnixMilliseconds: now,
			Component:        "test-component",
			Name:             "metric1",
			Type:             pkgmetrics.MetricTypeCounter,
			Value:            42.0,
		},
		{
			UnixMilliseconds: now,
			Component:        "test-component",
			Name:             "metric2",
			Labels:           map[string]string{"gpu": "gpu0"},
			Value:            123.45,
		},
	}

	// Record metrics
	for _, m := range metrics {
		err := store.Record(ctx, m)
		require.NoError(t, err)
	}

	// Test record with empty component name
	err = store.Record(ctx, pkgmetrics.Metric{
		UnixMilliseconds: now,
		Name:             "metric3",
		Value:            789.0,
	})
	assert.Error(t, err)

	// Test record with empty metric name
	err = store.Record(ctx, pkgmetrics.Metric{
		UnixMilliseconds: now,
		Component:        "test-component",
		Value:            789.0,
	})
	assert.Error(t, err)

	// Test updating existing metric
	updatedMetric := pkgmetrics.Metric{
		UnixMilliseconds: now,
		Component:        "test-component",
		Name:             "metric1",
		Type:             pkgmetrics.MetricTypeCounter,
		Value:            99.9,
	}
	err = store.Record(ctx, updatedMetric)
	require.NoError(t, err)

	// Verify the update worked by reading it back
	results, err := store.Read(ctx, pkgmetrics.WithSince(time.Unix(0, 0)))
	require.NoError(t, err)
	found := false
	for _, m := range results {
		if m.Component == "test-component" && m.Name == "metric1" {
			assert.Equal(t, 99.9, m.Value)
			assert.Equal(t, pkgmetrics.MetricTypeCounter, m.Type)
			found = true
		}
		if m.Component == "test-component" && m.Name == "metric2" {
			assert.Equal(t, pkgmetrics.MetricTypeGauge, m.Type)
		}
	}
	assert.True(t, found, "Updated metric not found in results")
}

func TestSQLiteStore_Read(t *testing.T) {
	// Setup test database
	dbRW, dbRO, cleanup := pkgsqlite.OpenTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create a new store
	store, err := NewSQLiteStore(ctx, dbRW, dbRO, "test_metrics")
	require.NoError(t, err)

	// Generate timestamps for testing
	now := time.Now()
	timestamp1 := now.Add(-2 * time.Hour).UnixMilli()
	timestamp2 := now.Add(-1 * time.Hour).UnixMilli()
	timestamp3 := now.UnixMilli()

	// Create and record test metrics
	metrics := []pkgmetrics.Metric{
		{
			UnixMilliseconds: timestamp1,
			Component:        "component1",
			Name:             "metric1",
			Value:            10.0,
		},
		{
			UnixMilliseconds: timestamp2,
			Component:        "component1",
			Name:             "metric1",
			Value:            20.0,
		},
		{
			UnixMilliseconds: timestamp3,
			Component:        "component1",
			Name:             "metric1",
			Value:            30.0,
		},
		{
			UnixMilliseconds: timestamp2,
			Component:        "component1",
			Name:             "metric2",
			Value:            100.0,
			Labels:           map[string]string{"label": "label1"},
		},
		{
			UnixMilliseconds: timestamp3,
			Component:        "component1",
			Name:             "metric2",
			Value:            200.0,
			Labels:           map[string]string{"label": "label1"},
		},
		{
			UnixMilliseconds: timestamp3,
			Component:        "component2",
			Name:             "metric3",
			Value:            300.0,
		},
	}

	for _, m := range metrics {
		err := store.Record(ctx, m)
		require.NoError(t, err)
	}

	// Test reading all metrics
	rs, err := store.Read(ctx)
	require.NoError(t, err)
	assert.Len(t, rs, 6)

	// Test reading metrics since a specific time
	rs, err = store.Read(ctx, pkgmetrics.WithSince(now.Add(-30*time.Minute)))
	require.NoError(t, err)
	assert.Len(t, rs, 3)

	// Test reading metrics since a specific time that should include some older entries
	rs, err = store.Read(ctx, pkgmetrics.WithSince(now.Add(-90*time.Minute)))
	require.NoError(t, err)
	assert.Len(t, rs, 5)

	// Test reading metrics filtered by component
	rs, err = store.Read(ctx, pkgmetrics.WithComponents("component1"))
	require.NoError(t, err)
	assert.Len(t, rs, 5)
	for _, m := range rs {
		assert.Equal(t, "component1", m.Component)
	}

	// Test reading metrics with multiple filters (component and since)
	rs, err = store.Read(ctx, pkgmetrics.WithComponents("component1"), pkgmetrics.WithSince(now.Add(-30*time.Minute)))
	require.NoError(t, err)
	assert.Len(t, rs, 2)
	for _, m := range rs {
		assert.Equal(t, "component1", m.Component)
		assert.GreaterOrEqual(t, m.UnixMilliseconds, now.Add(-30*time.Minute).UnixMilli())
	}

	// Test reading metrics for non-existent component
	rs, err = store.Read(ctx, pkgmetrics.WithComponents("nonexistent"))
	require.NoError(t, err)
	assert.Empty(t, rs)

	// Verify sorting order is by timestamp (ascending)
	if len(rs) >= 3 {
		assert.LessOrEqual(t, rs[0].UnixMilliseconds, rs[1].UnixMilliseconds)
		assert.LessOrEqual(t, rs[1].UnixMilliseconds, rs[2].UnixMilliseconds)
	}
}

func TestSQLiteStore_ReadEmpty(t *testing.T) {
	// Setup test database
	dbRW, dbRO, cleanup := pkgsqlite.OpenTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create a new store with a unique table name
	store, err := NewSQLiteStore(ctx, dbRW, dbRO, "empty_metrics")
	require.NoError(t, err)

	// Reading from an empty store should return empty results, not an error
	rs, err := store.Read(ctx)
	require.NoError(t, err)
	assert.Empty(t, rs)
}

func TestSQLiteStore_Purge(t *testing.T) {
	// Setup test database
	dbRW, dbRO, cleanup := pkgsqlite.OpenTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create a new store
	tableName := "purge_test_metrics"
	store, err := NewSQLiteStore(ctx, dbRW, dbRO, tableName)
	require.NoError(t, err)

	// Generate timestamps for testing
	now := time.Now()
	timestamp1 := now.Add(-3 * time.Hour).UnixMilli()
	timestamp2 := now.Add(-2 * time.Hour).UnixMilli()
	timestamp3 := now.Add(-1 * time.Hour).UnixMilli()
	timestamp4 := now.UnixMilli()

	// Create and record test metrics
	metrics := []pkgmetrics.Metric{
		{
			UnixMilliseconds: timestamp1,
			Component:        "component1",
			Name:             "metric1",
			Value:            10.0,
		},
		{
			UnixMilliseconds: timestamp2,
			Component:        "component1",
			Name:             "metric2",
			Value:            20.0,
		},
		{
			UnixMilliseconds: timestamp3,
			Component:        "component2",
			Name:             "metric3",
			Value:            30.0,
		},
		{
			UnixMilliseconds: timestamp4,
			Component:        "component2",
			Name:             "metric4",
			Value:            40.0,
		},
	}

	for _, m := range metrics {
		err := store.Record(ctx, m)
		require.NoError(t, err)
	}

	// Model rows first recorded at their historical measurement times.
	_, err = dbRW.ExecContext(ctx, "UPDATE "+tableName+" SET recorded_at = unix_milliseconds")
	require.NoError(t, err)

	// Verify all records exist
	rs, err := store.Read(ctx)
	require.NoError(t, err)
	assert.Len(t, rs, 4)

	// Purge records older than 2.5 hours
	affected, err := purge(ctx, dbRW, tableName, now.Add(-150*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, 1, affected)

	// Verify purged records are gone
	rs, err = store.Read(ctx)
	require.NoError(t, err)
	assert.Len(t, rs, 3)

	// Purge records older than 30 minutes
	affected, err = purge(ctx, dbRW, tableName, now.Add(-30*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, 2, affected)

	// Verify only the most recent records remain
	rs, err = store.Read(ctx)
	require.NoError(t, err)
	assert.Len(t, rs, 1)
	assert.Equal(t, timestamp4, rs[0].UnixMilliseconds)
}

func TestSQLiteInsertAndReadLast(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbRW, dbRO, cleanup := pkgsqlite.OpenTestDB(t)
	defer cleanup()

	tableName := "test_metrics"

	err := CreateTable(ctx, dbRW, tableName)
	require.NoError(t, err, "failed to create table")

	// Insert test metrics with different timestamps
	now := time.Now()
	metrics := []pkgmetrics.Metric{
		{
			UnixMilliseconds: now.Add(-2 * time.Hour).UnixMilli(),
			Component:        "component1",
			Name:             "metric1",
			Value:            10.0,
		},
		{
			UnixMilliseconds: now.Add(-1 * time.Hour).UnixMilli(),
			Component:        "component1",
			Name:             "metric1",
			Value:            20.0,
		},
		{
			UnixMilliseconds: now.UnixMilli(),
			Component:        "component1",
			Name:             "metric1",
			Value:            30.0,
		},
	}

	// Insert the metrics
	for _, m := range metrics {
		err := insert(ctx, dbRW, tableName, m)
		require.NoError(t, err)
	}

	// Read metrics and verify they're in order
	results, err := read(ctx, dbRO, tableName)
	require.NoError(t, err)
	require.Len(t, results, 3)

	// Verify ascending order
	assert.Equal(t, float64(10.0), results[0].Value)
	assert.Equal(t, float64(20.0), results[1].Value)
	assert.Equal(t, float64(30.0), results[2].Value)
}

func TestSQLiteCreateTable(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbRW, _, cleanup := pkgsqlite.OpenTestDB(t)
	defer cleanup()

	// Test successful table creation
	err := CreateTable(ctx, dbRW, "test_metrics")
	require.NoError(t, err)

	// Test with empty table name
	err = CreateTable(ctx, dbRW, "")
	assert.Equal(t, ErrEmptyTableName, err)
}

func TestSQLiteInsert(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbRW, _, cleanup := pkgsqlite.OpenTestDB(t)
	defer cleanup()

	tableName := "test_metrics"
	err := CreateTable(ctx, dbRW, tableName)
	require.NoError(t, err)

	// Create a test metric
	now := time.Now().UnixMilli()
	metric := pkgmetrics.Metric{
		UnixMilliseconds: now,
		Component:        "test-component",
		Name:             "test-metric",
		Value:            42.0,
		Labels:           map[string]string{"label": "test-label"},
	}

	// Test successful insert
	err = insert(ctx, dbRW, tableName, metric)
	require.NoError(t, err)

	// Test invalid inputs
	// Empty table name
	err = insert(ctx, dbRW, "", metric)
	assert.Equal(t, ErrEmptyTableName, err)

	// Empty component name
	invalidMetric := metric
	invalidMetric.Component = ""
	err = insert(ctx, dbRW, tableName, invalidMetric)
	assert.Equal(t, ErrEmptyComponentName, err)

	// Empty metric name
	invalidMetric = metric
	invalidMetric.Name = ""
	err = insert(ctx, dbRW, tableName, invalidMetric)
	assert.Equal(t, ErrEmptyMetricName, err)

	// Test replace functionality
	updatedMetric := metric
	updatedMetric.Value = 100.0
	err = insert(ctx, dbRW, tableName, updatedMetric)
	require.NoError(t, err)
}

func TestSQLiteRead(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbRW, dbRO, cleanup := pkgsqlite.OpenTestDB(t)
	defer cleanup()

	tableName := "test_metrics"
	err := CreateTable(ctx, dbRW, tableName)
	require.NoError(t, err)

	// Create test metrics
	now := time.Now()
	oldTimestamp := now.Add(-1 * time.Hour).UnixMilli()
	currentTimestamp := now.UnixMilli()

	metrics := []pkgmetrics.Metric{
		{
			UnixMilliseconds: oldTimestamp,
			Component:        "component1",
			Name:             "metric1",
			Value:            10.0,
		},
		{
			UnixMilliseconds: currentTimestamp,
			Component:        "component1",
			Name:             "metric1",
			Value:            20.0,
		},
		{
			UnixMilliseconds: currentTimestamp,
			Component:        "component2",
			Name:             "metric2",
			Value:            30.0,
			Labels:           map[string]string{"label": "label1"},
		},
	}

	// Insert test metrics
	for _, m := range metrics {
		err := insert(ctx, dbRW, tableName, m)
		require.NoError(t, err)
	}

	// Test reading all metrics
	results, err := read(ctx, dbRO, tableName)
	require.NoError(t, err)
	assert.Len(t, results, 3)

	// Test reading with timestamp filter
	results, err = read(ctx, dbRO, tableName, pkgmetrics.WithSince(now.Add(-30*time.Minute)))
	require.NoError(t, err)
	assert.Len(t, results, 2)

	// Test empty table name
	results, err = read(ctx, dbRO, "")
	assert.Equal(t, ErrEmptyTableName, err)
	assert.Nil(t, results)
}

func TestSQLitePurge(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbRW, _, cleanup := pkgsqlite.OpenTestDB(t)
	defer cleanup()

	tableName := "test_metrics"
	err := CreateTable(ctx, dbRW, tableName)
	require.NoError(t, err)

	// Create test metrics with different timestamps
	now := time.Now()
	oldTimestamp1 := now.Add(-2 * time.Hour).UnixMilli()
	oldTimestamp2 := now.Add(-1 * time.Hour).UnixMilli()
	currentTimestamp := now.UnixMilli()

	metrics := []pkgmetrics.Metric{
		{
			UnixMilliseconds: oldTimestamp1,
			Component:        "component1",
			Name:             "metric1",
			Value:            10.0,
		},
		{
			UnixMilliseconds: oldTimestamp2,
			Component:        "component1",
			Name:             "metric2",
			Value:            20.0,
		},
		{
			UnixMilliseconds: currentTimestamp,
			Component:        "component2",
			Name:             "metric3",
			Value:            30.0,
		},
	}

	// Insert test metrics
	for _, m := range metrics {
		err := insert(ctx, dbRW, tableName, m)
		require.NoError(t, err)
	}

	// Model rows first recorded at their historical measurement times.
	_, err = dbRW.ExecContext(ctx, "UPDATE "+tableName+" SET recorded_at = unix_milliseconds")
	require.NoError(t, err)

	// Test purging metrics older than 90 minutes
	affected, err := purge(ctx, dbRW, tableName, now.Add(-90*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, 1, affected)

	// Test purging metrics older than 30 minutes
	affected, err = purge(ctx, dbRW, tableName, now.Add(-30*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, 1, affected)

	// Test purging with empty table name
	affected, err = purge(ctx, dbRW, "", time.Time{})
	assert.Equal(t, ErrEmptyTableName, err)
	assert.Equal(t, 0, affected)
}

// Test reading from a non-existent table to check error handling
func TestSQLiteReadNonExistentTable(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, dbRO, cleanup := pkgsqlite.OpenTestDB(t)
	defer cleanup()

	// Try reading from a table that doesn't exist
	results, err := read(ctx, dbRO, "nonexistent_table")
	require.Error(t, err)
	assert.Nil(t, results)
}

// Test null label handling in Read function
func TestSQLiteReadNullLabel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbRW, dbRO, cleanup := pkgsqlite.OpenTestDB(t)
	defer cleanup()

	tableName := "test_metrics_null_label"
	err := CreateTable(ctx, dbRW, tableName)
	require.NoError(t, err)

	// Create test metrics with and without labels
	now := time.Now()
	metrics := []pkgmetrics.Metric{
		{
			UnixMilliseconds: now.UnixMilli(),
			Component:        "component1",
			Name:             "metric1",
			Value:            10.0,
			// Empty labels
		},
		{
			UnixMilliseconds: now.UnixMilli(),
			Component:        "component2",
			Name:             "metric2",
			Value:            20.0,
			Labels:           map[string]string{"label": "label2"},
		},
	}

	// Insert test metrics
	for _, m := range metrics {
		err := insert(ctx, dbRW, tableName, m)
		require.NoError(t, err)
	}

	// Read the metrics back
	results, err := read(ctx, dbRO, tableName)
	require.NoError(t, err)
	require.Len(t, results, 2)

	// Verify labels are preserved correctly
	for _, result := range results {
		switch result.Component {
		case "component1":
			assert.Empty(t, result.Labels)
		case "component2":
			assert.Equal(t, "label2", result.Labels["label"])
		}
	}
}

// Test invalid database operations
func TestSQLiteInvalidDatabaseOperations(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a db connection and immediately close it to simulate errors
	dbRW, _, cleanup := pkgsqlite.OpenTestDB(t)
	cleanup() // Close the DB immediately

	// Operations on closed DB should error
	err := CreateTable(ctx, dbRW, "test_metrics")
	assert.Error(t, err)

	metric := pkgmetrics.Metric{
		UnixMilliseconds: time.Now().UnixMilli(),
		Component:        "test-component",
		Name:             "test-metric",
		Value:            42.0,
	}

	err = insert(ctx, dbRW, "test_metrics", metric)
	assert.Error(t, err)

	affected, err := purge(ctx, dbRW, "test_metrics", time.Now())
	assert.Error(t, err)
	assert.Equal(t, 0, affected)
}

// TestBatchInsert tests the batch insertion of multiple metrics at once
func TestSQLiteBatchInsert(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbRW, dbRO, cleanup := pkgsqlite.OpenTestDB(t)
	defer cleanup()

	tableName := "test_batch_metrics"
	err := CreateTable(ctx, dbRW, tableName)
	require.NoError(t, err, "failed to create table")

	// Create a batch of test metrics
	now := time.Now()
	baseTime := now.UnixMilli()
	batchMetrics := []pkgmetrics.Metric{
		{
			UnixMilliseconds: baseTime,
			Component:        "component1",
			Name:             "metric1",
			Value:            10.5,
			Labels:           map[string]string{"label": "GPU-0"},
		},
		{
			UnixMilliseconds: baseTime,
			Component:        "component1",
			Name:             "metric2",
			Value:            20.7,
			Labels:           map[string]string{"label": "GPU-0"},
		},
		{
			UnixMilliseconds: baseTime,
			Component:        "component2",
			Name:             "metric1",
			Value:            30.2,
			Labels:           map[string]string{"label": "GPU-1"},
		},
		{
			UnixMilliseconds: baseTime + 100,
			Component:        "component2",
			Name:             "metric2",
			Value:            40.9,
			Labels:           map[string]string{"label": "GPU-1"},
		},
		{
			UnixMilliseconds: baseTime + 200,
			Component:        "component3",
			Name:             "metric3",
			Value:            50.1,
		},
	}

	// Test batch insert
	err = insert(ctx, dbRW, tableName, batchMetrics...)
	require.NoError(t, err, "batch insert failed")

	// Read all metrics back and verify
	results, err := read(ctx, dbRO, tableName)
	require.NoError(t, err)
	require.Len(t, results, len(batchMetrics), "should have same number of metrics as inserted")

	// Create a map of expected values for easier verification
	expectedMap := make(map[string]float64)
	for _, m := range batchMetrics {
		key := keyFromMetric(m)
		expectedMap[key] = m.Value
	}

	// Verify all metrics were inserted correctly
	for _, m := range results {
		key := keyFromMetric(m)
		expectedValue, exists := expectedMap[key]
		assert.True(t, exists, "metric should exist: %s", key)
		assert.Equal(t, expectedValue, m.Value, "metric value should match for %s", key)
	}

	// Test empty batch insert - should be a no-op
	err = insert(ctx, dbRW, tableName)
	require.NoError(t, err, "empty batch insert should succeed")

	// Test batch with validation error
	invalidBatch := []pkgmetrics.Metric{
		{
			UnixMilliseconds: baseTime + 300,
			Component:        "component4",
			Name:             "metric4",
			Value:            60.3,
		},
		{
			UnixMilliseconds: baseTime + 400,
			Component:        "", // Invalid: empty component
			Name:             "metric5",
			Value:            70.4,
		},
	}

	err = insert(ctx, dbRW, tableName, invalidBatch...)
	assert.Equal(t, ErrEmptyComponentName, err, "should fail on empty component name")

	// Ensure first batch is still intact by reading again
	results, err = read(ctx, dbRO, tableName)
	require.NoError(t, err)
	require.Len(t, results, len(batchMetrics), "should still have original metrics")

	// Test batch update (replace) by inserting metrics with same keys but different values
	updatedBatch := []pkgmetrics.Metric{
		{
			UnixMilliseconds: baseTime,
			Component:        "component1",
			Name:             "metric1",
			Value:            100.5, // Updated value
			Labels:           map[string]string{"label": "GPU-0"},
		},
		{
			UnixMilliseconds: baseTime,
			Component:        "component1",
			Name:             "metric2",
			Value:            200.7, // Updated value
			Labels:           map[string]string{"label": "GPU-0"},
		},
	}

	err = insert(ctx, dbRW, tableName, updatedBatch...)
	require.NoError(t, err, "update batch insert failed")

	// Verify updates were applied
	results, err = read(ctx, dbRO, tableName)
	require.NoError(t, err)
	require.Len(t, results, len(batchMetrics), "should have same number of metrics as before")

	// Check updated values
	for _, m := range results {
		if m.Component == "component1" && m.Name == "metric1" && m.Labels["label"] == "GPU-0" {
			assert.Equal(t, 100.5, m.Value, "value should be updated")
		} else if m.Component == "component1" && m.Name == "metric2" && m.Labels["label"] == "GPU-0" {
			assert.Equal(t, 200.7, m.Value, "value should be updated")
		}
	}
}

// Helper function to create a unique key from a metric for testing
func keyFromMetric(m pkgmetrics.Metric) string {
	labelKey := ""
	labelValue := ""

	// If there are labels, get the first one for the key
	if len(m.Labels) > 0 {
		// Since we're usually using a single label in tests
		for k, v := range m.Labels {
			labelKey = k
			labelValue = v
			break
		}
	}

	return fmt.Sprintf("%d:%s:%s:%s:%s", m.UnixMilliseconds, m.Component, m.Name, labelKey, labelValue)
}

// TestSQLiteStore_PurgeMethod tests the Purge method of the sqliteStore struct
func TestSQLiteStore_PurgeMethod(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup test database
	dbRW, dbRO, cleanup := pkgsqlite.OpenTestDB(t)
	defer cleanup()

	// Create a new store
	tableName := "purge_method_test"
	store, err := NewSQLiteStore(ctx, dbRW, dbRO, tableName)
	require.NoError(t, err)

	// Generate timestamps for testing
	now := time.Now()
	oldTimestamp1 := now.Add(-3 * time.Hour).UnixMilli()
	oldTimestamp2 := now.Add(-2 * time.Hour).UnixMilli()
	recentTimestamp := now.Add(-30 * time.Minute).UnixMilli()
	currentTimestamp := now.UnixMilli()

	// Create and record test metrics
	metrics := []pkgmetrics.Metric{
		{
			UnixMilliseconds: oldTimestamp1,
			Component:        "component1",
			Name:             "metric1",
			Value:            10.0,
		},
		{
			UnixMilliseconds: oldTimestamp2,
			Component:        "component1",
			Name:             "metric2",
			Value:            20.0,
		},
		{
			UnixMilliseconds: recentTimestamp,
			Component:        "component2",
			Name:             "metric3",
			Value:            30.0,
		},
		{
			UnixMilliseconds: currentTimestamp,
			Component:        "component2",
			Name:             "metric4",
			Value:            40.0,
		},
	}

	// Insert all metrics
	for _, m := range metrics {
		err := store.Record(ctx, m)
		require.NoError(t, err)
	}

	// Model rows first recorded at their historical measurement times.
	_, err = dbRW.ExecContext(ctx, "UPDATE "+tableName+" SET recorded_at = unix_milliseconds")
	require.NoError(t, err)

	// Verify all records exist
	rs, err := store.Read(ctx)
	require.NoError(t, err)
	assert.Len(t, rs, 4)

	// Purge records older than 2.5 hours using the Store's Purge method
	affected, err := store.Purge(ctx, now.Add(-150*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, 1, affected)

	// Verify purged records are gone
	rs, err = store.Read(ctx)
	require.NoError(t, err)
	assert.Len(t, rs, 3)

	// Purge records older than 1 hour
	affected, err = store.Purge(ctx, now.Add(-60*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, 1, affected)

	// Verify only the two most recent records remain
	rs, err = store.Read(ctx)
	require.NoError(t, err)
	assert.Len(t, rs, 2)

	// Purge records with an empty table name (shouldn't happen in practice)
	sqlStore := store.(*sqliteStore)
	sqlStore.table = ""
	_, err = sqlStore.Purge(ctx, now)
	assert.Equal(t, ErrEmptyTableName, err)
}

// TestSQLiteReadEdgeCases tests additional edge cases for the read function
func TestSQLiteReadEdgeCases(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbRW, dbRO, cleanup := pkgsqlite.OpenTestDB(t)
	defer cleanup()

	tableName := "test_read_edge_cases"
	err := CreateTable(ctx, dbRW, tableName)
	require.NoError(t, err)

	now := time.Now()
	currentTimestamp := now.UnixMilli()

	// Test component filter with multiple components
	metrics := []pkgmetrics.Metric{
		{
			UnixMilliseconds: currentTimestamp,
			Component:        "component1",
			Name:             "metric1",
			Value:            10.0,
		},
		{
			UnixMilliseconds: currentTimestamp,
			Component:        "component2",
			Name:             "metric1",
			Value:            20.0,
		},
		{
			UnixMilliseconds: currentTimestamp,
			Component:        "component3",
			Name:             "metric1",
			Value:            30.0,
		},
	}

	// Insert metrics
	for _, m := range metrics {
		err := insert(ctx, dbRW, tableName, m)
		require.NoError(t, err)
	}

	// Test filtering by multiple components
	selectedComponents := map[string]struct{}{
		"component1": {},
		"component3": {},
	}
	results, err := read(ctx, dbRO, tableName, pkgmetrics.WithComponents("component1", "component3"))
	require.NoError(t, err)
	require.Len(t, results, 2)

	// Verify only the requested components are returned
	for _, r := range results {
		_, exists := selectedComponents[r.Component]
		assert.True(t, exists, "Unexpected component: %s", r.Component)
	}

	// Test applying both component filter and since filter
	// First insert data with different timestamps
	oldMetric := pkgmetrics.Metric{
		UnixMilliseconds: now.Add(-2 * time.Hour).UnixMilli(),
		Component:        "component1",
		Name:             "metric2",
		Value:            40.0,
	}
	err = insert(ctx, dbRW, tableName, oldMetric)
	require.NoError(t, err)

	// Read with both filters
	results, err = read(ctx, dbRO, tableName,
		pkgmetrics.WithComponents("component1"),
		pkgmetrics.WithSince(now.Add(-1*time.Hour)))
	require.NoError(t, err)

	// Should only include recent component1 metrics
	assert.Len(t, results, 1)
	assert.Equal(t, "component1", results[0].Component)
	assert.Equal(t, currentTimestamp, results[0].UnixMilliseconds)

	// Test query error case by using a table that doesn't exist
	_, err = read(ctx, dbRO, "nonexistent_table_"+tableName, pkgmetrics.WithComponents("component1"))
	assert.Error(t, err)
}

// TestSQLiteReadScanError is a test specifically to check error handling during row scanning
// Note: This is a bit tricky to test since we can't easily inject errors into sql.Rows.Scan in unit tests
// This test demonstrates that we're properly handling scanning of different data types
func TestSQLiteReadScanHandling(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbRW, dbRO, cleanup := pkgsqlite.OpenTestDB(t)
	defer cleanup()

	tableName := "test_read_scan"
	err := CreateTable(ctx, dbRW, tableName)
	require.NoError(t, err)

	// Create a metric with extreme value to ensure proper handling of large numbers
	now := time.Now()
	extremeMetric := pkgmetrics.Metric{
		UnixMilliseconds: now.UnixMilli(),
		Component:        "extreme-component",
		Name:             "extreme-metric",
		Value:            1e308, // Very large value, close to max float64
		Labels:           map[string]string{"label": "extreme-value"},
	}

	// Insert the metric
	err = insert(ctx, dbRW, tableName, extremeMetric)
	require.NoError(t, err)

	// Read it back to ensure it was properly stored and can be scanned
	results, err := read(ctx, dbRO, tableName)
	require.NoError(t, err)
	require.Len(t, results, 1)

	// Verify the extreme value was properly stored and retrieved
	assert.Equal(t, extremeMetric.Value, results[0].Value)
	assert.Equal(t, extremeMetric.Component, results[0].Component)
	assert.Equal(t, extremeMetric.Name, results[0].Name)
	assert.Equal(t, extremeMetric.Labels["label"], results[0].Labels["label"])

	// Test handling of NULL values
	// Create a special query that'll result in NULL for some fields to test null handling
	// This is redundant with TestSQLiteReadNullLabel but provides additional coverage
	nullMetric := pkgmetrics.Metric{
		UnixMilliseconds: now.Add(1 * time.Minute).UnixMilli(),
		Component:        "null-component",
		Name:             "null-metric",
		Value:            42.0,
		// Deliberately not setting Labels
	}

	err = insert(ctx, dbRW, tableName, nullMetric)
	require.NoError(t, err)

	// Read back and verify
	results, err = read(ctx, dbRO, tableName, pkgmetrics.WithComponents("null-component"))
	require.NoError(t, err)
	require.Len(t, results, 1)

	assert.Equal(t, nullMetric.Value, results[0].Value)
	assert.Equal(t, nullMetric.Component, results[0].Component)
	assert.Equal(t, nullMetric.Name, results[0].Name)
	assert.Empty(t, results[0].Labels)
}

func TestSQLiteReadWithComponentsTreatsSQLMetacharactersAsData(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbRW, dbRO, cleanup := pkgsqlite.OpenTestDB(t)
	defer cleanup()

	tableName := "test_read_sql_metacharacters"
	err := CreateTable(ctx, dbRW, tableName)
	require.NoError(t, err)

	err = pkgmetadata.CreateTableMetadata(ctx, dbRW)
	require.NoError(t, err)
	err = pkgmetadata.SetMetadata(ctx, dbRW, "sak_token", "top-secret")
	require.NoError(t, err)

	payload := "x') UNION ALL SELECT 0,key,value,NULL,0 FROM gpud_metadata--"
	expected := pkgmetrics.Metric{
		UnixMilliseconds: time.Now().UnixMilli(),
		Component:        payload,
		Name:             "metric1",
		Value:            42.0,
	}

	err = insert(ctx, dbRW, tableName, expected)
	require.NoError(t, err)

	results, err := read(ctx, dbRO, tableName, pkgmetrics.WithComponents(payload))
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, expected.UnixMilliseconds, results[0].UnixMilliseconds)
	assert.Equal(t, expected.Component, results[0].Component)
	assert.Equal(t, expected.Name, results[0].Name)
	assert.Equal(t, expected.Value, results[0].Value)
}
