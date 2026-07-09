package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	_ "modernc.org/sqlite"

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
	"go.uber.org/zap"
)

var (
	tracer       trace.Tracer
	meter        metric.Meter
	db           *sql.DB
	logger       *zap.Logger
)

func main() {
	// Initialize OpenTelemetry
	ctx := context.Background()
	shutdown, err := initOTel(ctx)
	if err != nil {
		log.Fatalf("Failed to initialize OTel: %v", err)
	}
	defer shutdown(ctx)

	// Initialize logger
	logger, _ = zap.NewProduction()
	defer logger.Sync()

	// Initialize SQLite database
	if err := initDB(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Create metrics
	processCounter, _ := meter.Int64Counter("backend.orders.processed")
	processingDuration, _ := meter.Float64Histogram("backend.orders.duration")

	// HTTP handler
	http.HandleFunc("/process", func(w http.ResponseWriter, r *http.Request) {
		_, span := tracer.Start(r.Context(), "process-order")
		defer span.End()

		orderID := r.Header.Get("X-Order-ID")
		if orderID == "" {
			http.Error(w, "Missing X-Order-ID header", http.StatusBadRequest)
			return
		}

		// Log request
		logger.Info("Processing order",
			zap.String("order_id", orderID),
		)

		span.SetAttributes(attribute.String("order.id", orderID))

		// Simulate processing time
		start := time.Now()
		time.Sleep(time.Duration(100+time.Now().UnixNano()%200) * time.Millisecond)
		duration := time.Since(start).Seconds()

		// Store order in database
		if err := storeOrder(orderID, "processed"); err != nil {
			logger.Error("Failed to store order", zap.Error(err))
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Record metrics
		attrs := []attribute.KeyValue{
			attribute.String("order.id", orderID),
			attribute.String("status", "processed"),
		}
		processCounter.Add(ctx, 1, metric.WithAttributes(attrs...))
		processingDuration.Record(ctx, duration, metric.WithAttributes(attrs...))

		// Log success
		logger.Info("Order processed successfully",
			zap.String("order_id", orderID),
			zap.Float64("duration_seconds", duration),
		)

		span.SetAttributes(
			attribute.Float64("order.duration_seconds", duration),
			attribute.String("order.status", "processed"),
		)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"order_id": orderID,
			"status":   "processed",
			"message":  "Order processed successfully",
			"duration": fmt.Sprintf("%.3fs", duration),
		})
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
	})

	// Wrap handler with OTel instrumentation
	handler := otelhttp.NewHandler(http.DefaultServeMux, "backend-service")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	log.Printf("Backend service starting on port %s", port)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Fatalf("Server failed: %v", err)
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
			semconv.ServiceName("backend-service"),
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
	tracer = tp.Tracer("backend-service")

	// Create metric provider
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)
	meter = mp.Meter("backend-service")

	// Shutdown function
	shutdown := func(ctx context.Context) {
		tp.Shutdown(ctx)
		mp.Shutdown(ctx)
	}

	return shutdown, nil
}

func initDB() error {
	var err error
	db, err = sql.Open("sqlite", "./orders.db")
	if err != nil {
		return err
	}

	// Create orders table
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS orders (
		id TEXT PRIMARY KEY,
		status TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	`
	if _, err := db.Exec(createTableSQL); err != nil {
		return err
	}

	logger.Info("Database initialized successfully")
	return nil
}

func storeOrder(orderID, status string) error {
	_, span := tracer.Start(context.Background(), "store-order-in-db")
	defer span.End()

	span.SetAttributes(
		attribute.String("db.system", "sqlite"),
		attribute.String("db.operation", "insert"),
		attribute.String("db.sql.table", "orders"),
	)

	_, err := db.Exec(
		"INSERT OR REPLACE INTO orders (id, status, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)",
		orderID, status,
	)
	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.String("error.message", err.Error()))
		return err
	}

	logger.Info("Order stored in database",
		zap.String("order_id", orderID),
		zap.String("status", status),
	)

	return nil
}
