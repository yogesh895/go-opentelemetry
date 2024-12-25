package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "github.com/gin-gonic/gin"
    "go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/attribute"
    "go.opentelemetry.io/otel/codes"
    "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
    "go.opentelemetry.io/otel/metric"
    sdkmetric "go.opentelemetry.io/otel/sdk/metric"
    "go.opentelemetry.io/otel/sdk/resource"
    sdktrace "go.opentelemetry.io/otel/sdk/trace"
    semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
    "go.opentelemetry.io/otel/trace"
    "go.uber.org/zap"
    "go.uber.org/zap/zapcore"
    "google.golang.org/grpc"
)

// Cart represents a user's shopping cart
type Cart struct {
    UserID    string  `json:"user_id"`
    Items     []Item  `json:"items"`
    Total     float64 `json:"total"`
}

type Item struct {
    ID       string  `json:"id"`
    Name     string  `json:"name"`
    Price    float64 `json:"price"`
    Quantity int     `json:"quantity"`
}

var (
    // Metrics instruments
    requestCounter   metric.Int64Counter
    requestLatency  metric.Float64Histogram
    cartItemsGauge  metric.Int64ObservableGauge
    cartItems       = make(map[string]int64)
    
    // Global logger
    logger *zap.Logger
    
    // In-memory storage
    carts = make(map[string]*Cart)
)

// Opentelemetry Initiator
func initOpenTelemetry(ctx context.Context) (func(), error) {
    res, err := resource.New(ctx,
        resource.WithAttributes(
            semconv.ServiceNameKey.String("ecommerce-service"),
            semconv.ServiceVersionKey.String("1.0.0"),
        ),
    )
    if err != nil {
        return nil, fmt.Errorf("failed to create resource: %w", err)
    }

    tracerProvider, err := initTracer(ctx, res)
    if err != nil {
        return nil, fmt.Errorf("failed to init tracer: %w", err)
    }

    meterProvider, err := initMetrics(ctx, res)
    if err != nil {
        return nil, fmt.Errorf("failed to init metrics: %w", err)
    }

    logger, err = initLogging()
    if err != nil {
        return nil, fmt.Errorf("failed to init logging: %w", err)
    }

    cleanup := func() {
        ctx := context.Background()
        if err := tracerProvider.Shutdown(ctx); err != nil {
            log.Printf("Error shutting down tracer provider: %v", err)
        }
        if err := meterProvider.Shutdown(ctx); err != nil {
            log.Printf("Error shutting down meter provider: %v", err)
        }
        if err := logger.Sync(); err != nil {
            log.Printf("Error syncing logger: %v", err)
        }
    }

    return cleanup, nil
}

//Trace Initiator Opentelemetry
func initTracer(ctx context.Context, res *resource.Resource) (*sdktrace.TracerProvider, error) {
    traceExporter, err := otlptracegrpc.New(
        ctx,
        otlptracegrpc.WithInsecure(),
        otlptracegrpc.WithEndpoint("localhost:4317"),
    )
    if err != nil {
        return nil, fmt.Errorf("failed to create trace exporter: %w", err)
    }

    tracerProvider := sdktrace.NewTracerProvider(
        sdktrace.WithResource(res),
        sdktrace.WithBatcher(traceExporter),
        sdktrace.WithSampler(sdktrace.AlwaysSample()),
    )
    otel.SetTracerProvider(tracerProvider)

    return tracerProvider, nil
}
// Metric Initiator Opentelemetry
func initMetrics(ctx context.Context, res *resource.Resource) (*sdkmetric.MeterProvider, error) {
    metricExporter, err := otlpmetricgrpc.New(
        ctx,
        otlpmetricgrpc.WithInsecure(),
        otlpmetricgrpc.WithEndpoint("localhost:4317"),
        otlpmetricgrpc.WithDialOption(grpc.WithBlock()),
    )
    if err != nil {
        return nil, fmt.Errorf("failed to create metric exporter: %w", err)
    }

    meterProvider := sdkmetric.NewMeterProvider(
        sdkmetric.WithResource(res),
        sdkmetric.WithReader(
            sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(10*time.Second)),
        ),
    )
    otel.SetMeterProvider(meterProvider)

    meter := meterProvider.Meter("ecommerce-metrics")
    var err1, err2 error
    
    requestCounter, err1 = meter.Int64Counter(
        "request_count",
        metric.WithDescription("Number of requests processed"),
    )
    
    requestLatency, err2 = meter.Float64Histogram(
        "request_latency",
        metric.WithDescription("Latency of requests"),
        metric.WithUnit("ms"),
    )

    if err1 != nil || err2 != nil {
        return nil, fmt.Errorf("failed to create metrics: %v, %v", err1, err2)
    }

    var err3 error
    cartItemsGauge, err3 = meter.Int64ObservableGauge(
        "cart_items",
        metric.WithDescription("Number of items in cart"),
        metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
            for userID, count := range cartItems {
                o.Observe(count, metric.WithAttributes(attribute.String("user_id", userID)))
            }
            return nil
        }),
    )
    if err3 != nil {
        return nil, fmt.Errorf("failed to create gauge: %v", err3)
    }

    return meterProvider, nil
}

func initLogging() (*zap.Logger, error) {
    config := zap.NewProductionConfig()
    config.EncoderConfig.TimeKey = "timestamp"
    config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

    logger, err := config.Build()
    if err != nil {
        return nil, fmt.Errorf("failed to create logger: %w", err)
    }

    return logger, nil
}

func updateCartItemsCount(userID string, delta int64) {
    if current, exists := cartItems[userID]; exists {
        cartItems[userID] = current + delta
    } else {
        cartItems[userID] = delta
    }
    if cartItems[userID] == 0 {
        delete(cartItems, userID)
    }
}


func addToCartHandler(c *gin.Context) {
    ctx := c.Request.Context()
    span := trace.SpanFromContext(ctx)
    start := time.Now()
    var err error
    
    defer func() {
        duration := float64(time.Since(start).Milliseconds())
        endpoint := "/cart/add"
        
        requestCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("endpoint", endpoint)))
        requestLatency.Record(ctx, duration, metric.WithAttributes(attribute.String("endpoint", endpoint)))
        
        if err != nil {
            span.SetStatus(codes.Error, err.Error())
            logger.Error("Error processing add to cart request",
                zap.Error(err),
                zap.String("endpoint", endpoint),
            )
        }
    }()

    var item Item
    if err = c.BindJSON(&item); err != nil {
        c.JSON(400, gin.H{"error": "invalid request body"})
        return
    }

    userID := c.GetHeader("User-ID")
    if userID == "" {
        err = fmt.Errorf("user ID not provided")
        c.JSON(400, gin.H{"error": err.Error()})
        return
    }

    span.SetAttributes(
        attribute.String("user_id", userID),
        attribute.String("item_id", item.ID),
    )

    cart, exists := carts[userID]
    if !exists {
        cart = &Cart{UserID: userID, Items: []Item{}}
        carts[userID] = cart
    }

    cart.Items = append(cart.Items, item)
    cart.Total += item.Price * float64(item.Quantity)

    // Update cart items count
    updateCartItemsCount(userID, int64(item.Quantity))

    logger.Info("Item added to cart",
        zap.String("user_id", userID),
        zap.String("item_id", item.ID),
        zap.Int("quantity", item.Quantity),
    )

    c.JSON(200, cart)
}


func removeFromCartHandler(c *gin.Context) {
    ctx := c.Request.Context()
    span := trace.SpanFromContext(ctx)
    start := time.Now()
    var err error
    
    defer func() {
        duration := float64(time.Since(start).Milliseconds())
        endpoint := "/cart/remove"
        
        requestCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("endpoint", endpoint)))
        requestLatency.Record(ctx, duration, metric.WithAttributes(attribute.String("endpoint", endpoint)))
        
        if err != nil {
            span.SetStatus(codes.Error, err.Error())
            logger.Error("Error processing remove from cart request",
                zap.Error(err),
                zap.String("endpoint", endpoint),
            )
        }
    }()

    var item Item
    if err = c.BindJSON(&item); err != nil {
        c.JSON(400, gin.H{"error": "invalid request body"})
        return
    }

    userID := c.GetHeader("User-ID")
    if userID == "" {
        err = fmt.Errorf("user ID not provided")
        c.JSON(400, gin.H{"error": err.Error()})
        return
    }

    span.SetAttributes(
        attribute.String("user_id", userID),
        attribute.String("item_id", item.ID),
    )

    cart, exists := carts[userID]
    if !exists {
        err = fmt.Errorf("cart not found")
        c.JSON(404, gin.H{"error": err.Error()})
        return
    }

    found := false
    for i, cartItem := range cart.Items {
        if cartItem.ID == item.ID {
            // Update cart items count
            updateCartItemsCount(userID, -int64(cartItem.Quantity))
            
            cart.Total -= cartItem.Price * float64(cartItem.Quantity)
            cart.Items = append(cart.Items[:i], cart.Items[i+1:]...)
            found = true
            
            logger.Info("Item removed from cart",
                zap.String("user_id", userID),
                zap.String("item_id", item.ID),
                zap.Int("quantity", cartItem.Quantity),
            )
            break
        }
    }

    if !found {
        err = fmt.Errorf("item not found in cart")
        c.JSON(404, gin.H{"error": err.Error()})
        return
    }

    c.JSON(200, cart)
}

func viewCartHandler(c *gin.Context) {
    ctx := c.Request.Context()
    span := trace.SpanFromContext(ctx)
    start := time.Now()
    var err error
    
    defer func() {
        duration := float64(time.Since(start).Milliseconds())
        endpoint := "/cart/view"
        
        requestCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("endpoint", endpoint)))
        requestLatency.Record(ctx, duration, metric.WithAttributes(attribute.String("endpoint", endpoint)))
        
        if err != nil {
            span.SetStatus(codes.Error, err.Error())
            logger.Error("Error processing view cart request",
                zap.Error(err),
                zap.String("endpoint", endpoint),
            )
        }
    }()

    userID := c.GetHeader("User-ID")
    if userID == "" {
        err = fmt.Errorf("user ID not provided")
        c.JSON(400, gin.H{"error": err.Error()})
        return
    }

    span.SetAttributes(attribute.String("user_id", userID))

    cart, exists := carts[userID]
    if !exists {
        err = fmt.Errorf("cart not found")
        c.JSON(404, gin.H{"error": err.Error()})
        return
    }

    logger.Info("Cart viewed",
        zap.String("user_id", userID),
        zap.Int("item_count", len(cart.Items)),
        zap.Float64("cart_total", cart.Total),
    )

    c.JSON(200, cart)
}

func main() {
    ctx := context.Background()

    cleanup, err := initOpenTelemetry(ctx)
    if err != nil {
        log.Fatalf("Failed to initialize OpenTelemetry: %v", err)
    }
    defer cleanup()

    r := gin.New()
    r.Use(otelgin.Middleware("ecommerce-service"))
    r.Use(gin.Recovery())

    r.POST("/cart/add", addToCartHandler)
    r.POST("/cart/remove", removeFromCartHandler)
    r.GET("/cart/view", viewCartHandler)

    logger.Info("Server starting on :8080")
    if err := r.Run(":8080"); err != nil {
        logger.Fatal("Failed to start server", zap.Error(err))
    }
}
