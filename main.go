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
	Signal             string         `json:"signal"`
	SignalStrength     int            `json:"signalStrength"`
	VWMAPosition       string         `json:"vwmaPosition"`
	AnalystPriceTarget NullableFloat64 `json:"analystPriceTarget"`
}

func initDB() *sql.DB {
	db, err := sql.Open("sqlite3", "/data/stockmomentum.db")
	if err != nil {
		log.Fatal(err)
	}

	// Create the securities table if it doesn't exist
	query := `
	CREATE TABLE IF NOT EXISTS securities (
		ticker TEXT PRIMARY KEY,
		signal TEXT,
		analyst_price_target REAL,
		date_updated DATETIME DEFAULT CURRENT_TIMESTAMP
	);`
	_, err = db.Exec(query)
	if err != nil {
		log.Fatal(err)
	}

	// Add new columns if they don't exist
	alterQueries := []string{
		`ALTER TABLE securities ADD COLUMN signal_strength INTEGER DEFAULT 0;`,
		`ALTER TABLE securities ADD COLUMN vwma_position TEXT DEFAULT '';`,
		`ALTER TABLE securities ADD COLUMN signal_date DATETIME;`,
	}
	for _, alterQuery := range alterQueries {
		_, err := db.Exec(alterQuery)
		if err != nil {
			// SQLite doesn't error if column already exists, but log other errors
			if err.Error() != "duplicate column name" {
				log.Printf("Warning: Failed to execute alter query: %v", err)
			}
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
				if err := rows.Scan(&ticker, &signal, &signalStrength, &vwmaPosition, &priceTarget, &dateUpdated, &signalDate); err != nil {
					http.Error(w, "Error scanning row", http.StatusInternalServerError)
					return
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
			if alert.Ticker == "" || alert.Signal == "" {
				http.Error(w, "Missing ticker or signal", http.StatusBadRequest)
				return
			}

			// Get current signal from database
			var currentSignal string
			err := db.QueryRow("SELECT signal FROM securities WHERE ticker = ?", alert.Ticker).Scan(&currentSignal)
			if err != nil && err != sql.ErrNoRows {
				log.Printf("Error querying current signal for ticker %s: %v", alert.Ticker, err)
				http.Error(w, "Error querying database", http.StatusInternalServerError)
				return
			}

			// Determine if signal has changed
			now := time.Now().UTC()
			var signalDate interface{}
			if currentSignal != alert.Signal {
				signalDate = now
			} else {
				// Retrieve existing signal_date if signal hasn't changed
				var existingSignalDate sql.NullTime
				err := db.QueryRow("SELECT signal_date FROM securities WHERE ticker = ?", alert.Ticker).Scan(&existingSignalDate)
				if err != nil && err != sql.ErrNoRows {
					log.Printf("Error querying signal_date for ticker %s: %v", alert.Ticker, err)
					http.Error(w, "Error querying database", http.StatusInternalServerError)
					return
				}
				if existingSignalDate.Valid {
					signalDate = existingSignalDate.Time
				} else {
					signalDate = nil
				}
			}

			// Convert NullableFloat64 to sql.NullFloat64
			var priceTarget sql.NullFloat64
			if alert.AnalystPriceTarget.float64 != nil {
				priceTarget.Float64 = *alert.AnalystPriceTarget.float64
				priceTarget.Valid = true
			}

			// Update database
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