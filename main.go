package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

type TradingViewAlert struct {
	Ticker string `json:"ticker"`
	Indicator string `json:"indicator"`
	Signal int `json:"signal"`
	Comment   string  `json:"comment"`
}

func initDB() *sql.DB {
	db, err := sql.Open("sqlite3", "./stockmomentum.db")
	if err != nil {
		log.Fatal(err)
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
	_, err = db.Exec(query)
	if err != nil {
		log.Fatal(err)
	}

	return db
}

func handleWebhook(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var alert TradingViewAlert
		if err := json.NewDecoder(r.Body).Decode(&alert); err != nil {
			http.Error(w, "Invalid payload", http.StatusBadRequest)
			return
		}

		if err := updateIndicator(db, alert); err != nil {
			http.Error(w, "Failed to update database", http.StatusInternalServerError)
			return
		}

		if err := updateGoogleSheet(db, alert.Ticker); err != nil {
			http.Error(w, "Failed to update Google Sheet", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "Webhook processed successfully")
	}
}

func updateIndicator(db *sql.DB, alert TradingViewAlert) error {
	var allowedIndicators = map[string]bool{
		"sma_strategy":              true,
		"occ":                       true,
		"adaptive_supertrend":       true,
		"range_filter":              true,
		"pmax":                      true,
		"shinohara_intensity_ratio": true,
		"oscillators":               true,
	}
	
	if !allowedIndicators[alert.Indicator] {
		return fmt.Errorf("invalid indicator: %s", alert.Indicator)
	}
	
	query := fmt.Sprintf(`
	INSERT INTO securities (ticker, %s, momentum)
	VALUES (?, ?, ?)
	ON CONFLICT(ticker) DO UPDATE SET
		%s = ?,
		momentum = momentum + ?;`, alert.Indicator, alert.Indicator)

	_, err := db.Exec(query, alert.Ticker, alert.Signal, alert.Signal, alert.Signal, alert.Signal)
	return err
}

func updateGoogleSheet(db *sql.DB, ticker string) error {
	var momentum int
	row := db.QueryRow("SELECT momentum FROM securities WHERE ticker = ?", ticker)
	if err := row.Scan(&momentum); err != nil {
		return err
	}

	ctx := context.Background()
	creds, err := google.CredentialsFromJSON(ctx, []byte(credentialsJSON), sheets.SpreadsheetsScope)
	if err != nil {
		return err
	}

	client, err := sheets.NewService(ctx, option.WithCredentials(creds))
	if err != nil {
		return err
	}

	spreadsheetID := "1A2B3C4D5E6F7G8H9I0J"
	range_ := fmt.Sprintf("Sheet1!A2:D2") // Adjust range as needed
	values := []interface{}{ticker, momentum} // Add other indicators as needed

	_, err = client.Spreadsheets.Values.Update(spreadsheetID, range_, &sheets.ValueRange{
		Values: [][]interface{}{values},
	}).ValueInputOption("USER_ENTERED").Do()

	return err
}

func readCreds() {
	credBytes, err := os.ReadFile("/.credentials.json")
	if err != nil {
		return err
	}
	creds, err != google.CredentialsFromJSON(ctx, credsBytes, sheets.SpreadsheetsScope)
	if err != nil {
		return err
	}
}

func main() {
	db := initDB()
	defer db.Close()

	http.HandleFunc("/webhook", handleWebhook(db))

	log.Println("Server started on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}