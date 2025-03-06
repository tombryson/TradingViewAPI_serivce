package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Signal represents an individual signal within the alert
type Signal struct {
    Indicator string `json:"indicator"`
    Signal    string `json:"signal"`
}

// TradingViewAlert represents the new JSON structure with multiple signals
type TradingViewAlert struct {
    Ticker  string   `json:"ticker"`
    Signals []Signal `json:"signals"`
}

// readCreds reads the credentials from a file.
func readCreds() ([]byte, error) {
	return ioutil.ReadFile("credentials.json")
}

func initDB() *sql.DB {
    db, err := sql.Open("sqlite3", "/data/stockmomentum.db")
    if err != nil {
        log.Fatal(err)
    }

    query := `
    CREATE TABLE IF NOT EXISTS securities (
        ticker TEXT PRIMARY KEY,
        sma_strategy TEXT DEFAULT '',
        occ TEXT DEFAULT '',
        adaptive_supertrend TEXT DEFAULT '',
        range_filter_daily TEXT DEFAULT '',
        range_filter_weekly TEXT DEFAULT '',
        shinohara_intensity_ratio TEXT DEFAULT '',
        oscillator_daily TEXT DEFAULT '',
        oscillator_weekly TEXT DEFAULT '',
        oscillator_color TEXT DEFAULT '',
        date_updated DATETIME DEFAULT CURRENT_TIMESTAMP
    );`
    _, err = db.Exec(query)
    if err != nil {
        log.Fatal(err)
    }

    return db
}

// handleWebhook handles both GET and POST methods.
func handleWebhook(db *sql.DB) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        switch r.Method {
        case http.MethodGet:
            // Unchanged GET handling
            rows, err := db.Query("SELECT * FROM securities")
            if err != nil {
                http.Error(w, "Error querying database", http.StatusInternalServerError)
                return
            }
            defer rows.Close()

            cols, err := rows.Columns()
            if err != nil {
                http.Error(w, "Error retrieving columns", http.StatusInternalServerError)
                return
            }

            var result []map[string]interface{}
            for rows.Next() {
                columns := make([]interface{}, len(cols))
                columnPointers := make([]interface{}, len(cols))
                for i := range columns {
                    columnPointers[i] = &columns[i]
                }

                if err := rows.Scan(columnPointers...); err != nil {
                    http.Error(w, "Error scanning row", http.StatusInternalServerError)
                    return
                }
                m := make(map[string]interface{})
                for i, colName := range cols {
                    val := columnPointers[i].(*interface{})
                    switch v := (*val).(type) {
                    case []byte:
                        m[colName] = string(v)
                    default:
                        m[colName] = v
                    }
                }
                result = append(result, m)
            }

            w.Header().Set("Content-Type", "application/json")
            json.NewEncoder(w).Encode(result)
            return

        case http.MethodPost:
            var alert TradingViewAlert
            if err := json.NewDecoder(r.Body).Decode(&alert); err != nil {
                log.Printf("JSON decoding error: %v", err)
                http.Error(w, "Invalid payload", http.StatusBadRequest)
                return
            }
            log.Printf("Received alert: %+v", alert)

            // Update database with all signals
            if err := updateIndicators(db, alert); err != nil {
                log.Printf("Failed to update database for alert %+v: %v", alert, err)
                http.Error(w, fmt.Sprintf("Failed to update database: %v", err), http.StatusInternalServerError)
                return
            }

            log.Println("Webhook processed successfully")
            w.WriteHeader(http.StatusOK)
            fmt.Fprint(w, "Webhook processed successfully")

        default:
            http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
        }
    }
}

func updateIndicators(db *sql.DB, alert TradingViewAlert) error {
    allowedIndicators := map[string]bool{
        "sma_strategy":              true,
        "occ":                       true,
        "adaptive_supertrend":       true,
        "range_filter_daily":        true,
        "range_filter_weekly":       true,
        "shinohara_intensity_ratio": true,
        "oscillator_daily":          true,
        "oscillator_weekly":         true,
        "oscillator_color":          true,
    }

    // Begin a transaction to ensure atomic updates
    tx, err := db.Begin()
    if err != nil {
        return fmt.Errorf("failed to begin transaction: %v", err)
    }
    defer tx.Rollback() // Roll back if commit fails

    // Prepare the base query with placeholders for each indicator
    query := `
    INSERT INTO securities (
        ticker,
        sma_strategy,
        occ,
        adaptive_supertrend,
        range_filter_daily,
        range_filter_weekly,
        shinohara_intensity_ratio,
        oscillator_daily,
        oscillator_weekly,
        oscillator_color,
        date_updated
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    ON CONFLICT(ticker) DO UPDATE SET
        sma_strategy = COALESCE(excluded.sma_strategy, sma_strategy),
        occ = COALESCE(excluded.occ, occ),
        adaptive_supertrend = COALESCE(excluded.adaptive_supertrend, adaptive_supertrend),
        range_filter_daily = COALESCE(excluded.range_filter_daily, range_filter_daily),
        range_filter_weekly = COALESCE(excluded.range_filter_weekly, range_filter_weekly),
        shinohara_intensity_ratio = COALESCE(excluded.shinohara_intensity_ratio, shinohara_intensity_ratio),
        oscillator_daily = COALESCE(excluded.oscillator_daily, oscillator_daily),
        oscillator_weekly = COALESCE(excluded.oscillator_weekly, oscillator_weekly),
        oscillator_color = COALESCE(excluded.oscillator_color, oscillator_color),
        date_updated = excluded.date_updated;`

    // Default values for all indicator columns
    values := map[string]string{
        "sma_strategy":              "",
        "occ":                       "",
        "adaptive_supertrend":       "",
        "range_filter_daily":        "",
        "range_filter_weekly":       "",
        "shinohara_intensity_ratio": "",
        "oscillator_daily":          "",
        "oscillator_weekly":         "",
        "oscillator_color":          "",
    }

    // Update values based on received signals
    for _, signal := range alert.Signals {
        if !allowedIndicators[signal.Indicator] {
            log.Printf("Invalid indicator: %s", signal.Indicator)
            continue // Skip invalid indicators
        }
        values[signal.Indicator] = signal.Signal
    }

    now := time.Now().Format("2006-01-02 15:04:05")
    log.Printf("Executing query for ticker %s with values: %+v", alert.Ticker, values)

    // Execute the query with all values
    _, err = tx.Exec(query,
        alert.Ticker,
        values["sma_strategy"],
        values["occ"],
        values["adaptive_supertrend"],
        values["range_filter_daily"],
        values["range_filter_weekly"],
        values["shinohara_intensity_ratio"],
        values["oscillator_daily"],
        values["oscillator_weekly"],
        values["oscillator_color"],
        now,
    )
    if err != nil {
        return fmt.Errorf("db.Exec error: %v", err)
    }

    // Commit the transaction
    if err := tx.Commit(); err != nil {
        return fmt.Errorf("failed to commit transaction: %v", err)
    }

    log.Printf("Successfully updated ticker %s with %d signals", alert.Ticker, len(alert.Signals))
    return nil
}

func deleteTicker(db *sql.DB, ticker string) error {
	query := `
	DELETE FROM securities
	WHERE ticker = ?`
	_, err := db.Exec(query, ticker)
	if err != nil {
		log.Printf("db.Exec error: %v", ticker, err)
		return err
	}
	return nil
}


func handleDelete(db *sql.DB) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        ticker := r.URL.Query().Get("ticker")
        if ticker == "" {
            http.Error(w, "Missing ticker query parameter", http.StatusBadRequest)
            return
        }
        err := deleteTicker(db, ticker)
        if err != nil {
            http.Error(w, fmt.Sprintf("Error deleting ticker %s: %v", ticker, err), http.StatusInternalServerError)
            return
        }
        fmt.Fprintf(w, "Ticker %s deleted successfully", ticker)
    }
}

func main() {
	db := initDB()
	defer db.Close()

	http.HandleFunc("/webhook", handleWebhook(db))
	http.HandleFunc("/delete", handleDelete(db))

	port := "8090"
	log.Printf("Server started and listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
