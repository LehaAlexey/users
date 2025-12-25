package bootstrap

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/LehaAlexey/Users/config"
	"github.com/LehaAlexey/Users/internal/api/grpcserver"
	"github.com/LehaAlexey/Users/internal/api/httpapi"
	"github.com/LehaAlexey/Users/internal/kafka"
	"github.com/LehaAlexey/Users/internal/scheduler"
	"github.com/LehaAlexey/Users/internal/services/userservice"
	"github.com/LehaAlexey/Users/internal/storage/pgstorage"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
)

type App struct {
	server    HTTPServerRunner
	scheduler SchedulerRunner
	grpcServer GRPCServerRunner
}

func InitApp(configuration *config.Config) (*App, error) {
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		configuration.Database.Username,
		configuration.Database.Password,
		configuration.Database.Host,
		configuration.Database.Port,
		configuration.Database.DBName,
		configuration.Database.SSLMode,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pgx pool: %w", err)
	}

	storage := pgstorage.New(pool)
	service := userservice.New(storage, configuration.Scheduler.DefaultIntervalSeconds)
	handler := httpapi.New(service)

	router := chi.NewRouter()
	router.Mount("/", handler.Routes())
	mountSwagger(router, configuration)
	server := NewHTTPServer(configuration.HTTP.Addr, router)

	grpcSrv := grpc.NewServer()
	grpcHandler := grpcserver.New(service)
	grpcServer := NewGRPCServer(configuration.GRPC.Addr, grpcSrv, grpcHandler)

	kafkaBrokers := []string{fmt.Sprintf("%s:%d", configuration.Kafka.Host, configuration.Kafka.Port)}
	writer := kafka.NewWriter(kafkaBrokers, configuration.Kafka.ParseRequestedTopic)
	sched := scheduler.New(storage, writer, time.Duration(configuration.Scheduler.TickSeconds)*time.Second, configuration.Scheduler.DefaultIntervalSeconds, configuration.Scheduler.MaxBatch)

	return &App{server: server, scheduler: sched, grpcServer: grpcServer}, nil
}

type HTTPServerRunner interface {
	Run(ctx context.Context) error
}

type SchedulerRunner interface {
	Run(ctx context.Context) error
}

type GRPCServerRunner interface {
	Run(ctx context.Context) error
}

func mountSwagger(router chi.Router, configuration *config.Config) {
	if configuration == nil || !configuration.Swagger.Enabled {
		return
	}

	path := strings.TrimSpace(configuration.Swagger.Path)
	if path == "" {
		path = "/swagger"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	specPath := strings.TrimSpace(configuration.Swagger.SpecPath)
	if specPath == "" {
		specPath = "api/swagger/swagger.json"
	}

	router.Get(path, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		http.ServeFile(w, r, specPath)
	})
}
