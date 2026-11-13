package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"fleet-log-policy/internal/fleetlog/handler"
	"fleet-log-policy/internal/fleetlog/repo"
	"fleet-log-policy/internal/fleetlog/service"
	"fleet-log-policy/internal/gateway/router"
	"fleet-log-policy/pkg/config"
	"fleet-log-policy/pkg/database"
	"fleet-log-policy/pkg/logger"
	"fleet-log-policy/pkg/observability"
)

func main() {
	// 构建依赖前先加载运行时配置。
	if err := config.InitGlobal("configs/config.yaml"); err != nil {
		panic(err)
	}
	cfg := config.MustGlobal()
	gatewayServiceName := cfg.ServiceName("gateway")
	cfg.Log.Service = gatewayServiceName
	cfg.Log = logger.WithDailyFileName(cfg.Log, time.Now())

	log, err := logger.New(cfg.Log)
	if err != nil {
		panic(err)
	}
	defer log.Sync()
	observability.Register(gatewayServiceName, log)
	config.PrintSourceNotice(cfg, os.Stdout)

	engine := router.New(log, cfg.Middleware)

	// 题目 09：默认连接主库（configs/config.yaml 的 database.dsn → 本地 MySQL questions 库）；
	// 显式设置 FLEETLOG_DB_PATH 时改用 SQLite，供单元测试与隔离核查使用。
	store, err := openStore(cfg, log)
	if err != nil {
		log.Fatal("fleetlog database open failed", zap.Error(err))
	}
	if err := store.Seed(); err != nil {
		log.Fatal("fleetlog seed failed", zap.Error(err))
	}
	svc := service.New(store, timeoutSeconds())
	h := handler.New(svc)
	router.RegisterFleetLog(engine, h)
	engine.Static("/web", "./web")
	engine.GET("/", func(c *gin.Context) { c.Redirect(302, "/web/index.html") })

	// 教学时钟：后台每秒扫描一次超时，保证 60 秒（可配置）阈值可演示。
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if err := svc.Tick(); err != nil {
				log.Error("fleetlog tick failed", zap.Error(err))
			}
		}
	}()

	addr := fmt.Sprintf("%s:%d", cfg.HTTP.Host, httpPort(cfg.HTTP.Port))
	server := &http.Server{
		Addr:         addr,
		Handler:      engine,
		ReadTimeout:  time.Duration(cfg.HTTP.ReadTimeoutSeconds) * time.Second,
		WriteTimeout: time.Duration(cfg.HTTP.WriteTimeoutSeconds) * time.Second,
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		printStartupFailure(addr, err)
		log.Fatal("gateway listen failed", zap.String("addr", addr), zap.Error(err))
	}
	printStartupSummary(cfg, addr)

	go func() {
		log.Info("gateway listening", zap.String("addr", addr))
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Fatal("gateway stopped unexpectedly", zap.Error(err))
		}
	}()

	waitForShutdown(server, log)
}

// openStore 选择存储后端：显式设置 FLEETLOG_DB_PATH 时用 SQLite（测试/隔离核查），
// 否则用主库（默认本地 MySQL questions 库，DSN 取自配置文件的 database.dsn / mysql.dsn）。
func openStore(cfg *config.Config, log *zap.Logger) (*repo.Store, error) {
	if p := strings.TrimSpace(os.Getenv("FLEETLOG_DB_PATH")); p != "" {
		log.Info("fleetlog storage", zap.String("driver", "sqlite"), zap.String("dsn", p))
		return repo.Open(p)
	}
	if dsn := strings.TrimSpace(os.Getenv("FLEETLOG_DB_DSN")); dsn != "" {
		log.Info("fleetlog storage", zap.String("driver", "mysql"), zap.String("source", "FLEETLOG_DB_DSN"))
		return repo.OpenMySQL(dsn)
	}
	driver := strings.ToLower(strings.TrimSpace(cfg.Database.Driver))
	if driver == "" {
		driver = "mysql"
	}
	if driver == "mysql" {
		dsn := strings.TrimSpace(cfg.Database.DSN)
		if dsn == "" {
			dsn = strings.TrimSpace(cfg.MySQL.DSN)
		}
		log.Info("fleetlog storage", zap.String("driver", "mysql"), zap.String("database", "questions"))
		return repo.OpenMySQL(dsn)
	}
	db, err := database.Open(cfg.Database, cfg.MySQL, cfg.PostgreSQL, log)
	if err != nil {
		return nil, err
	}
	return repo.New(db)
}

// httpPort 返回监听端口；可用 FLEETLOG_HTTP_PORT 覆盖（部署脚本用于避开本机已占用端口）。
func httpPort(fallback int) int {
	if v := strings.TrimSpace(os.Getenv("FLEETLOG_HTTP_PORT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n < 65536 {
			return n
		}
	}
	return fallback
}

// timeoutSeconds 返回教学超时阈值（题面 60 秒；演示可用 FLEETLOG_TIMEOUT_SECONDS 缩短并须注明）。
func timeoutSeconds() int {
	if v := strings.TrimSpace(os.Getenv("FLEETLOG_TIMEOUT_SECONDS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 60
}

func printStartupFailure(addr string, err error) {
	fmt.Fprintf(os.Stderr, "\n[Gateway Start Failed]\n")
	fmt.Fprintf(os.Stderr, "  listen: %s\n", addr)
	fmt.Fprintf(os.Stderr, "  error: %v\n\n", err)
}

func printStartupSummary(cfg *config.Config, addr string) {
	host := cfg.HTTP.Host
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	baseURL := fmt.Sprintf("http://%s:%d", host, cfg.HTTP.Port)
	fmt.Fprintf(os.Stdout, "\n[Gateway Started]\n")
	fmt.Fprintf(os.Stdout, "  service: %s\n", cfg.ServiceName("gateway"))
	fmt.Fprintf(os.Stdout, "  env: %s\n", cfg.App.Env)
	fmt.Fprintf(os.Stdout, "  listen: %s\n", addr)
	fmt.Fprintf(os.Stdout, "  http: %s\n", baseURL)
	fmt.Fprintf(os.Stdout, "  health: %s/healthz\n", baseURL)
	fmt.Fprintf(os.Stdout, "  api: %s/api/v1\n\n", baseURL)
}

func waitForShutdown(server *http.Server, log *zap.Logger) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	log.Info("gateway shutting down")
	if err := server.Shutdown(ctx); err != nil {
		log.Error("gateway shutdown failed", zap.Error(err))
	}
}
