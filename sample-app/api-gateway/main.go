package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/handiism/observability/sample-app/api-gateway/pb"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	meter metric.Meter
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
	otelHandler := otelslog.NewHandler("api-gateway")
	slog.SetDefault(slog.New(&MultiHandler{
		handlers: []slog.Handler{consoleHandler, otelHandler},
	}))

	// Create metrics
	requestCounter, _ := meter.Int64Counter("http.requests.total")
	errorCounter, _ := meter.Int64Counter("http.errors.total")

	// HTTP handler
	mux := http.NewServeMux()
	mux.HandleFunc("/api/orders", func(w http.ResponseWriter, r *http.Request) {
		ctx, span := otel.Tracer("api-gateway").Start(r.Context(), "handle-order-request")
		defer span.End()

		slog.InfoContext(ctx, "Received order request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
		)

		attrs := []attribute.KeyValue{
			attribute.String("method", r.Method),
			attribute.String("path", r.URL.Path),
		}

		orderID := fmt.Sprintf("order-%d", time.Now().UnixNano())
		span.SetAttributes(attribute.String("order.id", orderID))

		resp, err := callBackendService(ctx, orderID)
		if err != nil {
			slog.ErrorContext(ctx, "Failed to call backend",
				slog.String("error", err.Error()),
				slog.String("order_id", orderID),
			)
			errorCounter.Add(ctx, 1, metric.WithAttributes(attrs...))
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

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

	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
	})

	handler := otelhttp.NewHandler(mux, "api-gateway")

	srv := &http.Server{
		Addr:         ":8080",
		Handler:      handler,
		ReadTimeout:  time.Second,
		WriteTimeout: 10 * time.Second,
	}
	srvErr := make(chan error, 1)
	go func() {
		srvErr <- srv.ListenAndServe()
	}()

	slog.Info("API Gateway starting", slog.String("port", "8080"))

	select {
	case err = <-srvErr:
		return err
	case <-ctx.Done():
		stop()
	}

	return srv.Shutdown(context.Background())
}

func callBackendService(ctx context.Context, orderID string) (*BackendResponse, error) {
	backendProto := os.Getenv("BACKEND_PROTOCOL")
	if backendProto == "grpc" {
		grpcAddr := os.Getenv("BACKEND_GRPC_ADDR")
		if grpcAddr == "" {
			grpcAddr = "backend-service:50051"
		}

		conn, err := grpc.NewClient(grpcAddr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		)
		if err != nil {
			return nil, err
		}
		defer conn.Close()

		client := pb.NewOrderServiceClient(conn)
		resp, err := client.ProcessOrder(ctx, &pb.ProcessOrderRequest{OrderId: orderID})
		if err != nil {
			return nil, err
		}

		return &BackendResponse{
			OrderID: resp.OrderId,
			Status:  resp.Status,
			Message: resp.Message,
		}, nil
	}

	backendURL := os.Getenv("BACKEND_SERVICE_URL")
	if backendURL == "" {
		backendURL = "http://backend-service:8081"
	}

	ctx, span := otel.Tracer("api-gateway").Start(ctx, "call-backend-service")
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

type BackendResponse struct {
	OrderID string `json:"order_id"`
	Status  string `json:"status"`
	Message string `json:"message"`
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

