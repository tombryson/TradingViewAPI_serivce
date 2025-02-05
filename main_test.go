package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// We want to override the real updateGoogleSheet function during tests.
// In main.go, add a package-level variable:
//   var updateGoogleSheetFn = updateGoogleSheet
// and change handleWebhook so that it calls updateGoogleSheetFn instead of updateGoogleSheet.
// (If you haven’t done this yet, update main.go accordingly.)
//
// For the tests, we override that variable with a dummy function:
func dummyUpdateGoogleSheet(db *sql.DB, ticker string) error {
	// In tests, we simply log or do nothing.
	return nil
}

// testDB creates a temporary SQLite database for testing.
func testDB(t *testing.T) *sql.DB {
	tmpDB := "test_stockmomentum.db"
	db, err := sql.Open("sqlite3", tmpDB)
	if err != nil {
		t.Fatalf("Error opening test database: %v", err)
	}

	query := `
	CREATE TABLE IF NOT EXISTS securities (
		ticker TEXT PRIMARY KEY,
		sma_strategy INTEGER DEFAULT 0,
		occ INTEGER DEFAULT 0,
		adaptive_supertrend INTEGER DEFAULT 0,
		range_filter INTEGER DEFAULT 0,
		pmax INTEGER DEFAULT 0,
		shinohara_intensity_ratio INTEGER DEFAULT 0,
		oscillators INTEGER DEFAULT 0,
		momentum INTEGER DEFAULT 0
	);`
	if _, err := db.Exec(query); err != nil {
		t.Fatalf("Error creating table in test database: %v", err)
	}

	t.Cleanup(func() {
		db.Close()
		os.Remove(tmpDB)
	})

	return db
}

// TestWebhookHandler sends several simulated webhook calls and checks the response.
func TestWebhookHandler(t *testing.T) {
	// Override the Sheets update function to avoid live calls.
	updateGoogleSheetFn = dummyUpdateGoogleSheet

	db := testDB(t)
	handler := handleWebhook(db)

	// Define several test alerts for different tickers/indicators.
	testAlerts := []TradingViewAlert{
		{Ticker: "AAPL", Indicator: "sma_strategy", Signal: 2, Comment: "Buy signal"},
		{Ticker: "GOOG", Indicator: "occ", Signal: 1, Comment: "Neutral signal"},
		{Ticker: "MSFT", Indicator: "pmax", Signal: 0, Comment: "Sell signal"},
	}

	for _, alert := range testAlerts {
		t.Run(alert.Ticker+"_"+alert.Indicator, func(t *testing.T) {
			jsonBytes, err := json.Marshal(alert)
			if err != nil {
				t.Fatalf("Failed to marshal alert: %v", err)
			}
			req := httptest.NewRequest("POST", "/webhook", bytes.NewReader(jsonBytes))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()

			handler(rr, req)

			if rr.Code != http.StatusOK {
				t.Errorf("Expected status 200 OK, got %d", rr.Code)
			}
			expected := "Webhook processed successfully"
			if rr.Body.String() != expected {
				t.Errorf("Unexpected response body: got %q, want %q", rr.Body.String(), expected)
			}
		})
	}
}

// TestMultipleWebhookCalls simulates multiple webhook calls for one ticker.
func TestMultipleWebhookCalls(t *testing.T) {
	updateGoogleSheetFn = dummyUpdateGoogleSheet
	db := testDB(t)
	handler := handleWebhook(db)

	// Simulate 10 calls for the ticker "ASX: Meeka Metals Limited" with various indicators.
	alerts := []TradingViewAlert{
		{Ticker: "ASX: Meeka Metals Limited", Indicator: "sma_strategy", Signal: 2, Comment: "Call 1"},
		{Ticker: "ASX: Meeka Metals Limited", Indicator: "occ", Signal: 2, Comment: "Call 2"},
		{Ticker: "ASX: Meeka Metals Limited", Indicator: "adaptive_supertrend", Signal: 2, Comment: "Call 3"},
		{Ticker: "ASX: Meeka Metals Limited", Indicator: "range_filter", Signal: 2, Comment: "Call 4"},
		{Ticker: "ASX: Meeka Metals Limited", Indicator: "pmax", Signal: 2, Comment: "Call 5"},
		{Ticker: "ASX: Meeka Metals Limited", Indicator: "shinohara_intensity_ratio", Signal: 2, Comment: "Call 6"},
		{Ticker: "ASX: Meeka Metals Limited", Indicator: "oscillators", Signal: 1, Comment: "Call 7"},
		{Ticker: "ASX: Meeka Metals Limited", Indicator: "sma_strategy", Signal: 2, Comment: "Call 8"},
		{Ticker: "ASX: Meeka Metals Limited", Indicator: "occ", Signal: 2, Comment: "Call 9"},
		{Ticker: "ASX: Meeka Metals Limited", Indicator: "pmax", Signal: 2, Comment: "Call 10"},
	}

	// Send each simulated webhook call.
	for i, alert := range alerts {
		t.Run(fmt.Sprintf("Call_%d", i+1), func(t *testing.T) {
			jsonBytes, err := json.Marshal(alert)
			if err != nil {
				t.Fatalf("Failed to marshal alert: %v", err)
			}
			req := httptest.NewRequest("POST", "/webhook", bytes.NewReader(jsonBytes))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			handler(rr, req)
			if rr.Code != http.StatusOK {
				t.Errorf("Expected status 200 OK, got %d", rr.Code)
			}
		})
	}

	// After all calls, query the database for "ASX: Meeka Metals Limited".
	row := db.QueryRow(`SELECT sma_strategy, occ, adaptive_supertrend, range_filter, pmax, shinohara_intensity_ratio, oscillators, momentum 
	                     FROM securities WHERE ticker = ?`, "ASX: Meeka Metals Limited")
	var sma, occ, adaptive, rangeFilter, pmax, shinohara, oscillators, momentum int
	if err := row.Scan(&sma, &occ, &adaptive, &rangeFilter, &pmax, &shinohara, &oscillators, &momentum); err != nil {
		t.Fatalf("Failed to scan row: %v", err)
	}
	t.Logf("Final values for ASX: Meeka Metals Limited: sma_strategy=%d, occ=%d, adaptive_supertrend=%d, range_filter=%d, pmax=%d, shinohara_intensity_ratio=%d, oscillators=%d, momentum=%d",
		sma, occ, adaptive, rangeFilter, pmax, shinohara, oscillators, momentum)
}
