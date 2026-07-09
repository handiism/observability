package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	_ "modernc.org/sqlite"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/handiism/observability/sample-app/backend-service/pb"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
)

var (
	meter              metric.Meter
	db                 *sql.DB
	processCounter     metric.Int64Counter
	processingDuration metric.Float64Histogram
)

func main() {
	if err := run(); err != nil {
		slog.Error("Failed to start", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	otelShutdown, err := setupOTelSDK(ctx)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, otelShutdown(context.Background()))
	}()

	// Configure slog with OTel bridge and stdout console logging
	consoleHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	otelHandler := otelslog.NewHandler("backend-service")
	slog.SetDefault(slog.New(&MultiHandler{
		handlers: []slog.Handler{consoleHandler, otelHandler},
	}))

	if err := initDB(); err != nil {
		return err
	}
	defer db.Close()

	processCounter, err = meter.Int64Counter("backend.orders.processed")
	if err != nil {
		return err
	}
	processingDuration, err = meter.Float64Histogram("backend.orders.duration")
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/process", func(w http.ResponseWriter, r *http.Request) {
		ctx, span := otel.Tracer("backend-service").Start(r.Context(), "process-order")
		defer span.End()

		orderID := r.Header.Get("X-Order-ID")
		if orderID == "" {
			http.Error(w, "Missing X-Order-ID header", http.StatusBadRequest)
			return
		}

		slog.InfoContext(ctx, "Processing order",
			slog.String("order_id", orderID),
		)

		span.SetAttributes(attribute.String("order.id", orderID))

		start := time.Now()
		time.Sleep(time.Duration(100+time.Now().UnixNano()%200) * time.Millisecond)
		duration := time.Since(start).Seconds()

		if err := storeOrder(ctx, orderID, "processed"); err != nil {
			slog.ErrorContext(ctx, "Failed to store order",
				slog.String("error", err.Error()),
				slog.String("order_id", orderID),
			)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		attrs := []attribute.KeyValue{
			attribute.String("order.id", orderID),
			attribute.String("status", "processed"),
		}
		processCounter.Add(ctx, 1, metric.WithAttributes(attrs...))
		processingDuration.Record(ctx, duration, metric.WithAttributes(attrs...))

		slog.InfoContext(ctx, "Order processed successfully",
			slog.String("order_id", orderID),
			slog.Float64("duration_seconds", duration),
		)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"order_id": orderID,
			"status":   "processed",
			"message":  "Order processed successfully",
			"duration": fmt.Sprintf("%.3fs", duration),
		})
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
	})

	handler := otelhttp.NewHandler(mux, "backend-service")

	// Setup and start gRPC server
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		return err
	}
	grpcServer := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
	)
	pb.RegisterOrderServiceServer(grpcServer, &orderServer{})

	grpcErr := make(chan error, 1)
	go func() {
		slog.Info("gRPC server starting", slog.String("port", "50051"))
		grpcErr <- grpcServer.Serve(lis)
	}()

	srv := &http.Server{
		Addr:         ":8081",
		Handler:      handler,
		ReadTimeout:  time.Second,
		WriteTimeout: 10 * time.Second,
	}
	srvErr := make(chan error, 1)
	go func() {
		srvErr <- srv.ListenAndServe()
	}()

	slog.Info("Backend service starting", slog.String("port", "8081"))

	select {
	case err = <-srvErr:
		grpcServer.GracefulStop()
		return err
	case err = <-grpcErr:
		srv.Shutdown(context.Background())
		return err
	case <-ctx.Done():
		stop()
	}

	grpcServer.GracefulStop()
	return srv.Shutdown(context.Background())
}

func initDB() error {
	var err error
	db, err = sql.Open("sqlite", "./orders.db")
	if err != nil {
		return err
	}

	// Optimize SQLite for concurrency
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		return err
	}
	if _, err := db.Exec("PRAGMA busy_timeout=5000;"); err != nil {
		return err
	}

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

	slog.Info("Database initialized successfully")
	return nil
}

func storeOrder(ctx context.Context, orderID, status string) error {
	ctx, span := otel.Tracer("backend-service").Start(ctx, "store-order-in-db")
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

	slog.InfoContext(ctx, "Order stored in database",
		slog.String("order_id", orderID),
		slog.String("status", status),
	)

	return nil
}

type MultiHandler struct {
	handlers []slog.Handler
}

func (m *MultiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (m *MultiHandler) Handle(ctx context.Context, r slog.Record) error {
	var err error
	for _, h := range m.handlers {
		if h.Enabled(ctx, r.Level) {
			err = errors.Join(err, h.Handle(ctx, r.Clone()))
		}
	}
	return err
}

func (m *MultiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		next[i] = h.WithAttrs(attrs)
	}
	return &MultiHandler{handlers: next}
}

func (m *MultiHandler) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		next[i] = h.WithGroup(name)
	}
	return &MultiHandler{handlers: next}
}

type orderServer struct {
	pb.UnimplementedOrderServiceServer
}

func (s *orderServer) ProcessOrder(ctx context.Context, req *pb.ProcessOrderRequest) (*pb.ProcessOrderResponse, error) {
	orderID := req.GetOrderId()
	if orderID == "" {
		return nil, errors.New("missing order ID")
	}

	slog.InfoContext(ctx, "Processing order via gRPC",
		slog.String("order_id", orderID),
	)

	start := time.Now()
	time.Sleep(time.Duration(100+time.Now().UnixNano()%200) * time.Millisecond)
	duration := time.Since(start).Seconds()

	if err := storeOrder(ctx, orderID, "processed"); err != nil {
		slog.ErrorContext(ctx, "Failed to store order via gRPC",
			slog.String("error", err.Error()),
			slog.String("order_id", orderID),
		)
		return nil, err
	}

	attrs := []attribute.KeyValue{
		attribute.String("order.id", orderID),
		attribute.String("status", "processed"),
	}
	processCounter.Add(ctx, 1, metric.WithAttributes(attrs...))
	processingDuration.Record(ctx, duration, metric.WithAttributes(attrs...))

	slog.InfoContext(ctx, "Order processed successfully via gRPC",
		slog.String("order_id", orderID),
		slog.Float64("duration_seconds", duration),
	)

	return &pb.ProcessOrderResponse{
		OrderId:  orderID,
		Status:   "processed",
		Message:  "Order processed successfully",
		Duration: fmt.Sprintf("%.3fs", duration),
	}, nil
}

