#!/bin/sh

if [ -n "$GOOGLE_CREDS_BASE64" ]; then
  echo "Decoding GOOGLE_CREDS_BASE64 into credentials.json..."
  echo "$GOOGLE_CREDS_BASE64" | base64 -d > credentials.json
fi

echo "Starting tradingview_apiservice..."
exec /app/tradingview_apiservice
