package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func get(t *testing.T, url string) (int, error) {
	t.Helper()
	c := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 5 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

func TestGracefulShutdownSequence(t *testing.T) {
	ready := &Readiness{}
	started := make(chan struct{})
	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready.Ready() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
	})

	var jobStopped atomic.Bool
	ln := listen(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Listener: ln, Handler: mux, Readiness: ready,
			ShutdownDelay: 600 * time.Millisecond, ShutdownTimeout: 5 * time.Second,
			Jobs:   []func(context.Context){func(c context.Context) { <-c.Done(); jobStopped.Store(true) }},
			Logger: quietLogger(),
		})
	}()
	base := "http://" + ln.Addr().String()

	if code, err := get(t, base+"/readyz"); err != nil || code != 200 {
		t.Fatalf("readyz before shutdown = %d, %v", code, err)
	}

	slowDone := make(chan int, 1)
	go func() {
		code, err := get(t, base+"/slow")
		if err != nil {
			code = -1
		}
		slowDone <- code
	}()
	<-started

	cancel() // simulates SIGTERM

	// During SHUTDOWN_DELAY the listener still accepts, but readiness is 503.
	deadline := time.Now().Add(3 * time.Second)
	for {
		code, err := get(t, base+"/readyz")
		if err == nil && code == http.StatusServiceUnavailable {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("readyz never turned 503 (last %d, %v)", code, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	select {
	case err := <-done:
		t.Fatalf("Run returned before the in-flight request finished: %v", err)
	default:
	}

	close(release)
	if code := <-slowDone; code != http.StatusOK {
		t.Fatalf("in-flight request = %d, want 200", code)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
	if !jobStopped.Load() {
		t.Error("background job was not stopped")
	}
}

func TestMetricsListener(t *testing.T) {
	metrics := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mainLn, metricsLn := listen(t), listen(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Listener: mainLn, MetricsListener: metricsLn,
			Handler: http.NotFoundHandler(), MetricsHandler: metrics,
			ShutdownTimeout: time.Second, Logger: quietLogger(),
		})
	}()
	if code, err := get(t, "http://"+metricsLn.Addr().String()+"/"); err != nil || code != 200 {
		t.Fatalf("metrics listener = %d, %v", code, err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run = %v", err)
	}
	if _, err := get(t, "http://"+metricsLn.Addr().String()+"/"); err == nil {
		t.Error("metrics listener still accepting after shutdown")
	}
}

func TestEmptyMetricsAddrDisablesListener(t *testing.T) {
	// Reserve a port, release it, and make sure nothing binds there.
	probe := listen(t)
	metricsAddr := probe.Addr().String()
	_ = probe.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	ln := listen(t)
	go func() {
		done <- Run(ctx, Options{Listener: ln, MetricsAddr: "", Handler: http.NotFoundHandler(),
			ShutdownTimeout: time.Second, Logger: quietLogger()})
	}()
	if code, err := get(t, "http://"+ln.Addr().String()+"/"); err != nil || code != 404 {
		t.Fatalf("main listener = %d, %v", code, err)
	}
	if conn, err := net.DialTimeout("tcp", metricsAddr, 200*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Error("something is listening on the metrics address although METRICS_ADDR is empty")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestListenErrorIsReported(t *testing.T) {
	ln := listen(t)
	defer ln.Close()
	err := Run(context.Background(), Options{Addr: ln.Addr().String(), Handler: http.NotFoundHandler(), Logger: quietLogger()})
	if err == nil {
		t.Fatal("expected an error when the address is already in use")
	}
}
