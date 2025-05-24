package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	_ "github.com/mattn/go-sqlite3"
)

// NullableFloat64 is a custom type to handle flexible JSON parsing for float64
type NullableFloat64 struct {
	*float64
}

func (nf *NullableFloat64) UnmarshalJSON(data []byte) error {
	if string(data) == "null" || string(data) == `""` {
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
	AnalystPriceTarget NullableFloat64 `json:"analystPriceTarget"`
}

func initDB() *sql.DB {
	db, err := sql.Open("sqlite3", "/data/stockmomentum.db")
	if err != nil {
		log.Fatal(err)
	}

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

	return db
}

// handleWebhook handles GET and POST methods
func handleWebhook(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			rows, err := db.Query("SELECT ticker, signal, analyst_price_target, date_updated FROM securities")
			if err != nil {
				http.Error(w, "Error querying database", http.StatusInternalServerError)
				return
			}
			defer rows.Close()

			var result []map[string]interface{}
			for rows.Next() {
				var ticker, signal string
				var priceTarget sql.NullFloat64
				var dateUpdated string
				if err := rows.Scan(&ticker, &signal, &priceTarget, &dateUpdated); err != nil {
					http.Error(w, "Error scanning row", http.StatusInternalServerError)
					return
				}
				var priceTargetVal interface{}
				if priceTarget.Valid {
					priceTargetVal = priceTarget.Float64
				} else {
					priceTargetVal = nil
				}
				m := map[string]interface{}{
					"ticker":               ticker,
					"signal":               signal,
					"analyst_price_target": priceTargetVal,
					"date_updated":         dateUpdated,
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

			// Convert NullableFloat64 to sql.NullFloat64
			var priceTarget sql.NullFloat64
			if alert.AnalystPriceTarget.float64 != nil {
				priceTarget.Float64 = *alert.AnalystPriceTarget.float64
				priceTarget.Valid = true
			}

			query := `
			INSERT INTO securities (ticker, signal, analyst_price_target, date_updated)
			VALUES (?, ?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(ticker) DO UPDATE SET
				signal = excluded.signal,
				analyst_price_target = excluded.analyst_price_target,
				date_updated = CURRENT_TIMESTAMP;`
			_, err := db.Exec(query, alert.Ticker, alert.Signal, priceTarget)
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