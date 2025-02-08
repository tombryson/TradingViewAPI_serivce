echo "$GOOGLE_CREDS_BASE64" | base64 -d > /app/credentials.json
./main.go