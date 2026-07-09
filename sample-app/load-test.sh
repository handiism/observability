#!/bin/bash

# HTTP Load Test for Sample App
# Sends concurrent requests to api-gateway

API_URL="http://localhost:8080/api/orders"
NUM_REQUESTS=${1:-100}
CONCURRENCY=${2:-10}

echo "Starting load test..."
echo "Target: $API_URL"
echo "Requests: $NUM_REQUESTS"
echo "Concurrency: $CONCURRENCY"
echo ""

# Function to send a request
send_request() {
    curl -s -X POST "$API_URL" > /dev/null 2>&1
}

# Run load test
start_time=$(date +%s)
for i in $(seq 1 $NUM_REQUESTS); do
    send_request &
    if (( i % CONCURRENCY == 0 )); then
        wait
    fi
done
wait
end_time=$(date +%s)

# Calculate statistics
duration=$((end_time - start_time))
rps=$(echo "scale=2; $NUM_REQUESTS / $duration" | bc)

echo ""
echo "Load test completed!"
echo "Duration: ${duration}s"
echo "Total requests: $NUM_REQUESTS"
echo "Requests per second: $rps"
echo ""
echo "Check SigNoz UI for traces and metrics: https://signoz.example.com"
