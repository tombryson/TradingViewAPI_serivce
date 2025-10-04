package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// NullableFloat64 is a custom type to handle flexible JSON parsing for float64
type NullableFloat64 struct {
	*float64
}

func (nf *NullableFloat64) UnmarshalJSON(data []byte) error {
	if string(data) == `"false"` || string(data) == "null" || string(data) == `""` {
		nf.float64 = nil
		return nil
	}
	var val float64
	if err := json.Unmarshal(data, &val); err != nil {
		log.Printf("Invalid analystPriceTarget value %s, treating as null: %v", string(data), err)
		nf.float64 = nil
		return nil
	}
	nf.float64 = &val
	return nil
}

// TradingViewAlert represents the JSON structure from TradingView alerts
type TradingViewAlert struct {
	Ticker             string         `json:"ticker"`
	Signal             string         `json:"signal,omitempty"`
	Event              string         `json:"event,omitempty"`
	SignalStrength     int            `json:"signalStrength"`
	VWMAPosition       string         `json:"vwmaPosition"`
	AnalystPriceTarget NullableFloat64 `json:"analystPriceTarget"`
}

func initDB() *sql.DB {
	db, err := sql.Open("sqlite3", "/data/stockmomentum.db")
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}

	// Create the full securities table with all columns
	query := `
	CREATE TABLE IF NOT EXISTS securities (
		ticker TEXT PRIMARY KEY,
		signal TEXT,
		signal_strength INTEGER DEFAULT 0,
		vwma_position TEXT DEFAULT '',
		analyst_price_target REAL,
		date_updated DATETIME DEFAULT CURRENT_TIMESTAMP,
		signal_date DATETIME
	);`
	_, err = db.Exec(query)
	if err != nil {
		log.Fatalf("Error creating securities table: %v", err)
	}

	// Verify table structure
	rows, err := db.Query("PRAGMA table_info(securities);")
	if err != nil {
		log.Fatalf("Error querying table info: %v", err)
	}
	defer rows.Close()

	expectedColumns := []string{
		"ticker",
		"signal",
		"signal_strength",
		"vwma_position",
		"analyst_price_target",
		"date_updated",
		"signal_date",
	}
	foundColumns := make([]string, 0)
	for rows.Next() {
		var cid int
		var name, typeStr string
		var notnull, pk int
		var dflt_value sql.NullString
		if err := rows.Scan(&cid, &name, &typeStr, &notnull, &dflt_value, &pk); err != nil {
			log.Printf("Error scanning table info: %v", err)
			continue
		}
		foundColumns = append(foundColumns, name)
		log.Printf("Found column %s: %s", name, typeStr)
	}

	// Validate schema
	if len(foundColumns) != len(expectedColumns) {
		log.Fatalf("Schema mismatch: expected %d columns (%v), found %d columns (%v)",
			len(expectedColumns), expectedColumns, len(foundColumns), foundColumns)
	}
	for i, col := range expectedColumns {
		if i >= len(foundColumns) || foundColumns[i] != col {
			log.Fatalf("Schema mismatch: expected column %s at position %d, found %s",
				col, i, foundColumns[i])
		}
	}

	return db
}

// handleWebhook handles GET and POST methods
func handleWebhook(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			rows, err := db.Query("SELECT ticker, signal, signal_strength, vwma_position, analyst_price_target, date_updated, signal_date FROM securities")
			if err != nil {
				log.Printf("Error querying database: %v", err)
				http.Error(w, "Error querying database", http.StatusInternalServerError)
				return
			}
			defer rows.Close()

			var result []map[string]interface{}
			for rows.Next() {
				var ticker, signal, vwmaPosition string
				var signalStrength int
				var priceTarget sql.NullFloat64
				var dateUpdated, signalDate sql.NullTime
				err := rows.Scan(&ticker, &signal, &signalStrength, &vwmaPosition, &priceTarget, &dateUpdated, &signalDate)
				if err != nil {
					log.Printf("Error scanning row for ticker %s: %v (skipping row)", ticker, err)
					continue // Skip problematic row and continue with next
				}
				m := map[string]interface{}{
					"ticker":               ticker,
					"signal":               signal,
					"signalStrength":       signalStrength,
					"vwmaPosition":         vwmaPosition,
					"analyst_price_target": nil,
					"date_updated":         nil,
					"signalDate":           nil,
				}
				if priceTarget.Valid {
					m["analyst_price_target"] = priceTarget.Float64
				}
				if dateUpdated.Valid {
					m["date_updated"] = dateUpdated.Time.Format(time.RFC3339)
				}
				if signalDate.Valid {
					m["signalDate"] = signalDate.Time.Format(time.RFC3339)
				}
				result = append(result, m)
			}

			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(result); err != nil {
				log.Printf("Error encoding JSON: %v", err)
				http.Error(w, "Error encoding JSON", http.StatusInternalServerError)
				return
			}

		case http.MethodPost:
			var alert TradingViewAlert
			if err := json.NewDecoder(r.Body).Decode(&alert); err != nil {
				log.Printf("JSON decoding error: %v", err)
				http.Error(w, "Invalid payload", http.StatusBadRequest)
				return
			}
			log.Printf("Received alert: %+v", alert)

			// Validate required fields
			if alert.Ticker == "" {
				http.Error(w, "Missing ticker", http.StatusBadRequest)
				return
			}

			// Convert NullableFloat64 to sql.NullFloat64
			var priceTarget sql.NullFloat64
			if alert.AnalystPriceTarget.float64 != nil {
				priceTarget.Float64 = *alert.AnalystPriceTarget.float64
				priceTarget.Valid = true
			}

			if alert.Event == "price_target_change" || alert.Signal == "" {
				// Handle VWMA-only or price target change update
				query := `
				INSERT INTO securities (ticker, signal_strength, vwma_position, analyst_price_target, date_updated)
				VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
				ON CONFLICT(ticker) DO UPDATE SET
					signal_strength = excluded.signal_strength,
					vwma_position = excluded.vwma_position,
					analyst_price_target = excluded.analyst_price_target,
					date_updated = CURRENT_TIMESTAMP;`
				_, err := db.Exec(query, alert.Ticker, alert.SignalStrength, alert.VWMAPosition, priceTarget)
				if err != nil {
					log.Printf("Failed to update database for VWMA/price target alert %+v: %v", alert, err)
					http.Error(w, fmt.Sprintf("Failed to update database: %v", err), http.StatusInternalServerError)
					return
				}
			} else {
				// Handle buy/sell signals
				if alert.Signal != "buy" && alert.Signal != "sell" {
					log.Printf("Invalid signal for ticker %s: %s", alert.Ticker, alert.Signal)
					http.Error(w, "Invalid signal (must be 'buy' or 'sell')", http.StatusBadRequest)
					return
				}

				// Get current signal and signal_date from database
				var currentSignal sql.NullString
				var existingSignalDate sql.NullTime
				err := db.QueryRow("SELECT signal, signal_date FROM securities WHERE ticker = ?", alert.Ticker).Scan(&currentSignal, &existingSignalDate)
				if err != nil && err != sql.ErrNoRows {
					log.Printf("Error querying current signal and signal_date for ticker %s: %v", alert.Ticker, err)
					http.Error(w, "Error querying database", http.StatusInternalServerError)
					return
				}

				// Determine signal_date based on signal change
				var signalDate interface{}
				if !currentSignal.Valid || currentSignal.String != alert.Signal {
					// Signal is new or has changed, set signal_date to now
					signalDate = time.Now().UTC()
				} else {
					// Signal is the same, preserve existing signal_date or set to NULL
					if existingSignalDate.Valid {
						signalDate = existingSignalDate.Time
					} else {
						signalDate = nil
					}
				}

				// Update database for buy/sell signals
				query := `
				INSERT INTO securities (ticker, signal, signal_strength, vwma_position, analyst_price_target, date_updated, signal_date)
				VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP, ?)
				ON CONFLICT(ticker) DO UPDATE SET
					signal = excluded.signal,
					signal_strength = excluded.signal_strength,
					vwma_position = excluded.vwma_position,
					analyst_price_target = excluded.analyst_price_target,
					date_updated = CURRENT_TIMESTAMP,
					signal_date = excluded.signal_date;`
				_, err = db.Exec(query, alert.Ticker, alert.Signal, alert.SignalStrength, alert.VWMAPosition, priceTarget, signalDate)
				if err != nil {
					log.Printf("Failed to update database for alert %+v: %v", alert, err)
					http.Error(w, fmt.Sprintf("Failed to update database: %v", err), http.StatusInternalServerError)
					return
				}
			}

			log.Println("Webhook processed successfully")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "Webhook processed successfully")

		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func deleteTicker(db *sql.DB, ticker string) error {
	query := `DELETE FROM securities WHERE ticker = ?`
	_, err := db.Exec(query, ticker)
	if err != nil {
		log.Printf("Error deleting ticker %s: %v", ticker, err)
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
		if err := deleteTicker(db, ticker); err != nil {
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