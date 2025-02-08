#!/bin/sh

if [ -n "$GOOGLE_CREDS_BASE64" ]; then
  echo "Decoding GOOGLE_CREDS_BASE64 into /app/credentials.json..."
  echo "$GOOGLE_CREDS_BASE64" | base64 -d > /app/credentials.json
fi

echo "Starting tradingview_apiservice..."
exec /tradingview_apiservice