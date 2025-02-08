if [ -n "$GOOGLE_CREDS_BASE64" ]; then
  echo "Decoding GOOGLE_CREDS_BASE64 into /app/credentials.json..."
  echo "$GOOGLE_CREDS_BASE64" | base64 -d > /app/credentials.json
fi

echo "Starting tradingview_apiservice..."
# Use 'exec' so that signals are forwarded properly to your Go app
exec /tradingview_apiservice