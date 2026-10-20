// Package server runs the main and metrics HTTP servers and implements the
// graceful shutdown sequence (contract §5.2).
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Server timeouts (contract §5.2).
const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 120 * time.Second
)

// Readiness is the shared flag behind /readyz: it turns "not ready" as soon as
// shutdown starts so load balancers stop sending traffic.
type Readiness struct{ shuttingDown atomic.Bool }

// Ready reports whether the process accepts new traffic.
func (r *Readiness) Ready() bool { return !r.shuttingDown.Load() }

// BeginShutdown marks the process as not ready.
func (r *Readiness) BeginShutdown() { r.shuttingDown.Store(true) }

// Options configure Run.
type Options struct {
	// Addr / MetricsAddr are listen addresses, used when the matching
	// listener is nil. An empty MetricsAddr (and nil MetricsListener)
	// disables the metrics server.
	Addr        string
	MetricsAddr string
	// Listener / MetricsListener let tests (and socket activation) supply
	// pre-bound listeners.
	Listener        net.Listener
	MetricsListener net.Listener

	Handler        http.Handler
	MetricsHandler http.Handler // may be nil when metrics are disabled

	Readiness       *Readiness // required
	ShutdownDelay   time.Duration
	ShutdownTimeout time.Duration

	// Jobs are background loops; each receives a context cancelled during
	// shutdown (after the main server has drained) and must return promptly.
	Jobs []func(ctx context.Context)

	Logger *slog.Logger // optional
}

// Run serves until ctx is cancelled or a server fails, then shuts down:
// not-ready → wait ShutdownDelay → drain main server (ShutdownTimeout) → stop
// background jobs → close metrics server. It returns nil after a clean
// shutdown triggered by ctx.
func Run(ctx context.Context, o Options) error {
	log := o.Logger
	if log == nil {
		log = slog.Default()
	}
	if o.Readiness == nil {
		o.Readiness = &Readiness{}
	}

	mainLn := o.Listener
	if mainLn == nil {
		ln, err := net.Listen("tcp", o.Addr)
		if err != nil {
			return fmt.Errorf("listen on %q: %w", o.Addr, err)
		}
		mainLn = ln
	}
	metricsLn := o.MetricsListener
	if metricsLn == nil && o.MetricsAddr != "" {
		ln, err := net.Listen("tcp", o.MetricsAddr)
		if err != nil {
			_ = mainLn.Close()
			return fmt.Errorf("listen metrics on %q: %w", o.MetricsAddr, err)
		}
		metricsLn = ln
	}

	mainSrv := newHTTPServer(o.Handler)
	var metricsSrv *http.Server
	if metricsLn != nil {
		metricsSrv = newHTTPServer(o.MetricsHandler)
	}

	errCh := make(chan error, 2)
	serve := func(name string, srv *http.Server, ln net.Listener) {
		log.Info("listening", "server", name, "addr", ln.Addr().String())
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("%s server: %w", name, err)
		}
	}
	go serve("main", mainSrv, mainLn)
	if metricsSrv != nil {
		go serve("metrics", metricsSrv, metricsLn)
	}

	jobCtx, stopJobs := context.WithCancel(context.WithoutCancel(ctx))
	defer stopJobs()
	var jobs sync.WaitGroup
	for _, job := range o.Jobs {
		jobs.Go(func() { job(jobCtx) })
	}

	var runErr error
	select {
	case <-ctx.Done():
		log.Info("shutdown signal received")
	case runErr = <-errCh:
		log.Error("server failed, shutting down", "error", runErr)
	}

	o.Readiness.BeginShutdown()
	log.Info("shutdown: not ready", "delay", o.ShutdownDelay.String())
	if o.ShutdownDelay > 0 {
		time.Sleep(o.ShutdownDelay)
	}

	drainCtx, cancel := context.WithTimeout(context.Background(), o.ShutdownTimeout)
	defer cancel()
	if err := mainSrv.Shutdown(drainCtx); err != nil {
		log.Error("shutdown: draining main server", "error", err)
		_ = mainSrv.Close()
		runErr = errors.Join(runErr, fmt.Errorf("drain main server: %w", err))
	} else {
		log.Info("shutdown: main server drained")
	}

	stopJobs()
	jobs.Wait()
	log.Info("shutdown: background jobs stopped")

	if metricsSrv != nil {
		mctx, mcancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer mcancel()
		if err := metricsSrv.Shutdown(mctx); err != nil {
			_ = metricsSrv.Close()
		}
		log.Info("shutdown: metrics server closed")
	}
	return runErr
}

func newHTTPServer(h http.Handler) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
}
