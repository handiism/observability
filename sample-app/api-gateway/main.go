package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

var (
	tracer trace.Tracer
	meter  metric.Meter
)

func main() {
	// Initialize OpenTelemetry
	ctx := context.Background()
	shutdown, err := initOTel(ctx)
	if err != nil {
		slog.Error("Failed to initialize OTel", "error", err)
		os.Exit(1)
	}
	defer shutdown(ctx)

	// Configure slog with OTel bridge
	slog.SetDefault(otelslog.NewLogger("api-gateway"))

	// Create metrics
	requestCounter, _ := meter.Int64Counter("http.requests.total")
	errorCounter, _ := meter.Int64Counter("http.errors.total")

	// HTTP handler
	http.HandleFunc("/api/orders", func(w http.ResponseWriter, r *http.Request) {
		ctx, span := tracer.Start(r.Context(), "handle-order-request")
		defer span.End()

		// Log request with trace context
		slog.InfoContext(ctx, "Received order request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
		)

		// Create metric attributes
		attrs := []attribute.KeyValue{
			attribute.String("method", r.Method),
			attribute.String("path", r.URL.Path),
		}

		// Simulate order processing
		orderID := fmt.Sprintf("order-%d", time.Now().UnixNano())
		span.SetAttributes(attribute.String("order.id", orderID))

		// Call backend service
		resp, err := callBackendService(ctx, orderID)
		if err != nil {
			slog.ErrorContext(ctx, "Failed to call backend",
				slog.String("error", err.Error()),
				slog.String("order_id", orderID),
			)
			span.SetStatus(2, err.Error())
			errorCounter.Add(ctx, 1, metric.WithAttributes(attrs...))
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Log response
		slog.InfoContext(ctx, "Order processed",
			slog.String("order_id", orderID),
			slog.String("status", resp.Status),
		)

		requestCounter.Add(ctx, 1, metric.WithAttributes(attrs...))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"order_id": orderID,
			"status":   resp.Status,
			"message":  "Order processed successfully",
		})
	})

	http.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
	})

	// Wrap handler with OTel instrumentation
	handler := otelhttp.NewHandler(http.DefaultServeMux, "api-gateway")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	slog.Info("API Gateway starting", slog.String("port", port))
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		slog.Error("Server failed", "error", err)
		os.Exit(1)
	}
}

func initOTel(ctx context.Context) (func(context.Context), error) {
	// Create OTLP trace exporter
	traceExporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}

	// Create OTLP metric exporter
	metricExporter, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create metric exporter: %w", err)
	}

	// Create resource
	res, _ := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName("api-gateway"),
			semconv.ServiceVersion("1.0.0"),
			attribute.String("environment", "development"),
		),
	)

	// Create trace provider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	tracer = tp.Tracer("api-gateway")

	// Create metric provider
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)
	meter = mp.Meter("api-gateway")

	// Shutdown function
	shutdown := func(ctx context.Context) {
		tp.Shutdown(ctx)
		mp.Shutdown(ctx)
	}

	return shutdown, nil
}

type BackendResponse struct {
	OrderID string `json:"order_id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

func callBackendService(ctx context.Context, orderID string) (*BackendResponse, error) {
	backendURL := os.Getenv("BACKEND_SERVICE_URL")
	if backendURL == "" {
		backendURL = "http://backend-service:8081"
	}

	ctx, span := tracer.Start(ctx, "call-backend-service")
	defer span.End()

	req, err := http.NewRequestWithContext(ctx, "POST", backendURL+"/process", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Order-ID", orderID)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result BackendResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}
