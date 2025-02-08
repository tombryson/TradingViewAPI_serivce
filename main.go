package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

// TradingViewAlert holds the incoming webhook payload.
type TradingViewAlert struct {
	Ticker    string `json:"ticker"`
	Indicator string `json:"indicator"`
	Signal    string `json:"signal"`
	Comment   string `json:"comment"`
}

// Package-level variable so that the Sheets update function can be overridden in tests.
var updateGoogleSheetFn = updateGoogleSheet

// readCreds reads the credentials from a file.
func readCreds() ([]byte, error) {
	return ioutil.ReadFile("credentials.json")
}

// initDB initializes the SQLite database with a table for our securities.
func initDB() *sql.DB {
	db, err := sql.Open("sqlite3", "/data/stockmomentum.db")
	if err != nil {
		log.Fatal(err)
	}

	// Added a date_updated column for record keeping.
	query := `
	CREATE TABLE IF NOT EXISTS securities (
		ticker TEXT PRIMARY KEY,
		sma_strategy TEXT DEFAULT '',
		occ TEXT DEFAULT '',
		adaptive_supertrend TEXT DEFAULT '',
		range_filter_daily TEXT DEFAULT '',
		range_filter_weekly TEXT DEFAULT '',
		pmax TEXT DEFAULT '',
		shinohara_intensity_ratio TEXT DEFAULT '',
		oscillators_daily_weekly TEXT DEFAULT '',
		monthly_oscillator TEXT DEFAULT '',
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
			// For GET requests, return the contents of the securities table.
			rows, err := db.Query("SELECT * FROM securities")
			if err != nil {
				http.Error(w, "Error querying database", http.StatusInternalServerError)
				return
			}
			defer rows.Close()

			// Get column names.
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
				// Construct a map for the row.
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

			// Update database
			if err := updateIndicator(db, alert); err != nil {
				log.Printf("Failed to update database for alert %+v: %v", alert, err)
				http.Error(w, fmt.Sprintf("Failed to update database: %v", err), http.StatusInternalServerError)
				return
			}

			// Update Google Sheet
			if err := updateGoogleSheetFn(db, alert.Ticker); err != nil {
				log.Printf("Failed to update Google Sheet for ticker %s: %v", alert.Ticker, err)
				http.Error(w, fmt.Sprintf("Failed to update Google Sheet: %v", err), http.StatusInternalServerError)
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

func updateIndicator(db *sql.DB, alert TradingViewAlert) error {
	allowedIndicators := map[string]bool{
		"sma_strategy":              true,
		"occ":                       true,
		"adaptive_supertrend":       true,
		"range_filter_daily":        true,
		"range_filter_weekly":       true,
		"pmax":                      true,
		"shinohara_intensity_ratio": true,
		"oscillators_daily_weekly":  true,
		"monthly_oscillator":        true,
	}

	if !allowedIndicators[alert.Indicator] {
		errMsg := fmt.Sprintf("invalid indicator: %s", alert.Indicator)
		log.Println(errMsg)
		return fmt.Errorf(errMsg)
	}

	// Build an UPSERT query that sets the appropriate indicator column.
	query := fmt.Sprintf(`
	INSERT INTO securities (ticker, %s, date_updated)
	VALUES (?, ?, ?)
	ON CONFLICT(ticker) DO UPDATE SET
		%s = ?,
		date_updated = ?;`, alert.Indicator, alert.Indicator)

	now := time.Now().Format("2006-01-02 15:04:05")
	log.Printf("Executing query: %s", query)
	log.Printf("Parameters: ticker=%s, signal=%s, now=%s", alert.Ticker, alert.Signal, now)

	result, err := db.Exec(query, alert.Ticker, alert.Signal, now, alert.Signal, now)
	if err != nil {
		log.Printf("db.Exec error: %v", err)
		return err
	}
	// Optional: you can log the number of affected rows.
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		log.Printf("Error getting affected rows: %v", err)
	} else {
		log.Printf("Rows affected: %d", rowsAffected)
	}
	return nil
}

// updateGoogleSheet retrieves indicator values for a ticker and updates the Google Sheet.
func updateGoogleSheet(db *sql.DB, ticker string) error {
	var (sma_strategy, occ, adaptive_supertrend, range_filter_daily, range_filter_weekly, pmax, shinohara_intensity_ratio, oscillators_daily_weekly, monthly_oscillator, date_updated string)
	query := `
		SELECT ticker, sma_strategy, occ, adaptive_supertrend, range_filter_daily, range_filter_weekly, pmax, shinohara_intensity_ratio, oscillators_daily_weekly, monthly_oscillator, date_updated
		FROM securities
		WHERE ticker = ?`
	row := db.QueryRow(query, ticker)
	if err := row.Scan(&ticker, &sma_strategy, &occ, &adaptive_supertrend, &range_filter_daily, &range_filter_weekly, &pmax, &shinohara_intensity_ratio, &oscillators_daily_weekly, &monthly_oscillator, &date_updated); err != nil {
		log.Printf("Error scanning data for ticker %s: %v", ticker, err)
		return err
	}

	rowData := []interface{}{ticker, sma_strategy, occ, adaptive_supertrend, range_filter_daily, range_filter_weekly, pmax, shinohara_intensity_ratio, oscillators_daily_weekly, monthly_oscillator, date_updated}

	ctx := context.Background()

	log.Println("Reading credentials from file...")
	credBytes, err := readCreds()
	if err != nil {
		log.Printf("Error reading credentials: %v", err)
		return err
	}

	log.Println("Parsing credentials JSON...")
	creds, err := google.CredentialsFromJSON(ctx, credBytes, sheets.SpreadsheetsScope)
	if err != nil {
		log.Printf("Error parsing credentials JSON: %v", err)
		return err
	}

	log.Println("Creating Google Sheets client...")
	client, err := sheets.NewService(ctx, option.WithCredentials(creds))
	if err != nil {
		log.Printf("Error creating Sheets service: %v", err)
		return err
	}

	spreadsheetID := "1wiAQ8n3aLlKpCeWaN9x63s5MeLGsvBO52YP7sdBICps"
	log.Printf("Using spreadsheet ID: %s", spreadsheetID)

	getRange := "Sheet2!A2:A"
	resp, err := client.Spreadsheets.Values.Get(spreadsheetID, getRange).Do()
	if err != nil {
		log.Printf("Error retrieving sheet data: %v", err)
		return err
	}
	rowIndex := -1
	if resp.Values != nil {
		for i, r := range resp.Values {
			if len(r) > 0 && fmt.Sprintf("%v", r[0]) == ticker {
				rowIndex = i + 2
				break
			}
		}
	}

	if rowIndex == -1 {
		log.Printf("Ticker %s not found in sheet. Appending new row.", ticker)
		_, err = client.Spreadsheets.Values.Append(spreadsheetID, "Sheet2!A2:K2", &sheets.ValueRange{
			Values: [][]interface{}{rowData},
		}).ValueInputOption("USER_ENTERED").InsertDataOption("INSERT_ROWS").Do()
		if err != nil {
			log.Printf("Error appending new row: %v", err)
		}
		return err
	} else {
		updateRange := fmt.Sprintf("Sheet2!A%d:K%d", rowIndex, rowIndex)
		log.Printf("Updating row %d for ticker %s with data: %v", rowIndex, ticker, rowData)
		_, err = client.Spreadsheets.Values.Update(spreadsheetID, updateRange, &sheets.ValueRange{
			Values: [][]interface{}{rowData},
		}).ValueInputOption("USER_ENTERED").Do()
		if err != nil {
			log.Printf("Error updating row %d: %v", rowIndex, err)
		}
		return err
	}
}

func main() {
	db := initDB()
	defer db.Close()

	http.HandleFunc("/webhook", handleWebhook(db))

	port := "8090"
	log.Printf("Server started and listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
