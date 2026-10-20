package observability

import (
	"net/http"
	"runtime"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics owns a private Prometheus registry with all application metrics. It
// implements auth.Recorder.
type Metrics struct {
	reg      *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	inFlight prometheus.Gauge
	logins   *prometheus.CounterVec
	signups  *prometheus.CounterVec
}

// NewMetrics registers all metrics. version and commit label the build_info gauge.
func NewMetrics(version, commit string) *Metrics {
	m := &Metrics{
		reg: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total", Help: "HTTP requests by method, route pattern and status code.",
		}, []string{"method", "route", "code"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "http_request_duration_seconds", Help: "HTTP request latency by method and route pattern.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "http_requests_in_flight", Help: "HTTP requests currently being served.",
		}),
		logins: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "auth_login_attempts_total", Help: "Login attempts by result.",
		}, []string{"result"}),
		signups: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "auth_registrations_total", Help: "Registrations by result.",
		}, []string{"result"}),
	}
	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cloud_notes_build_info", Help: "Build information; the value is always 1.",
	}, []string{"version", "commit", "go_version"})
	buildInfo.WithLabelValues(version, commit, runtime.Version()).Set(1)

	m.reg.MustRegister(m.requests, m.duration, m.inFlight, m.logins, m.signups, buildInfo,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	// Pre-create the documented label values so rates work from the first scrape.
	for _, r := range []string{"success", "invalid_credentials", "locked", "rate_limited"} {
		m.logins.WithLabelValues(r)
	}
	return m
}

// Handler serves GET /metrics (Prometheus text format) for the metrics listener.
func (m *Metrics) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{}))
	return mux
}

// LoginAttempt implements auth.Recorder.
func (m *Metrics) LoginAttempt(result string) { m.logins.WithLabelValues(result).Inc() }

// Registration implements auth.Recorder.
func (m *Metrics) Registration(result string) { m.signups.WithLabelValues(result).Inc() }

// Middleware records request count, latency and in-flight requests, labelled by
// chi route pattern. Register it with Router.Use, inside WithRequestID.
func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.inFlight.Inc()
		defer m.inFlight.Dec()
		start := time.Now()
		sw := wrap(w)
		next.ServeHTTP(sw, r)
		route := routePattern(r)
		m.requests.WithLabelValues(r.Method, route, strconv.Itoa(sw.status)).Inc()
		m.duration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
	})
}

// RegisterPool exposes pgxpool statistics (read lazily on every scrape).
func (m *Metrics) RegisterPool(stat func() *pgxpool.Stat) {
	m.reg.MustRegister(&poolCollector{stat: stat})
}

type poolCollector struct{ stat func() *pgxpool.Stat }

var (
	descAcquired = prometheus.NewDesc("pgxpool_acquired_conns", "Connections currently acquired.", nil, nil)
	descIdle     = prometheus.NewDesc("pgxpool_idle_conns", "Idle connections.", nil, nil)
	descTotal    = prometheus.NewDesc("pgxpool_total_conns", "Total connections (idle + acquired + constructing).", nil, nil)
	descMax      = prometheus.NewDesc("pgxpool_max_conns", "Maximum pool size.", nil, nil)
	descAcqCount = prometheus.NewDesc("pgxpool_acquire_total", "Successful connection acquires.", nil, nil)
	descAcqDur   = prometheus.NewDesc("pgxpool_acquire_duration_seconds_total", "Total time spent acquiring connections.", nil, nil)
	descEmpty    = prometheus.NewDesc("pgxpool_empty_acquire_total", "Acquires that had to wait because the pool was empty.", nil, nil)
	descCanceled = prometheus.NewDesc("pgxpool_canceled_acquire_total", "Acquires canceled by their context.", nil, nil)
)

func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{descAcquired, descIdle, descTotal, descMax, descAcqCount, descAcqDur, descEmpty, descCanceled} {
		ch <- d
	}
}

func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.stat()
	g, ctr := prometheus.GaugeValue, prometheus.CounterValue
	ch <- prometheus.MustNewConstMetric(descAcquired, g, float64(s.AcquiredConns()))
	ch <- prometheus.MustNewConstMetric(descIdle, g, float64(s.IdleConns()))
	ch <- prometheus.MustNewConstMetric(descTotal, g, float64(s.TotalConns()))
	ch <- prometheus.MustNewConstMetric(descMax, g, float64(s.MaxConns()))
	ch <- prometheus.MustNewConstMetric(descAcqCount, ctr, float64(s.AcquireCount()))
	ch <- prometheus.MustNewConstMetric(descAcqDur, ctr, s.AcquireDuration().Seconds())
	ch <- prometheus.MustNewConstMetric(descEmpty, ctr, float64(s.EmptyAcquireCount()))
	ch <- prometheus.MustNewConstMetric(descCanceled, ctr, float64(s.CanceledAcquireCount()))
}
