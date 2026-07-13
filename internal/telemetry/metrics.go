// Package telemetry exposes bounded operational metrics.
package telemetry

import (
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/codes"
)

type Metrics struct {
	oapRPC      *prometheus.CounterVec
	armsRPC     *prometheus.CounterVec
	inflight    *prometheus.GaugeVec
	oapDuration *prometheus.HistogramVec
}

func New(registerer prometheus.Registerer) *Metrics {
	m := &Metrics{
		oapRPC: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "skywalking_mirror_oap_rpc_total",
			Help: "OAP RPC terminal results.",
		}, []string{"method", "code"}),
		armsRPC: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "skywalking_mirror_arms_rpc_total",
			Help: "ARMS mirror terminal results.",
		}, []string{"method", "result"}),
		inflight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "skywalking_mirror_inflight",
			Help: "In-flight upstream operations.",
		}, []string{"target"}),
		oapDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "skywalking_mirror_oap_duration_seconds",
			Help:    "OAP RPC latency in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method"}),
	}
	registerer.MustRegister(m.oapRPC, m.armsRPC, m.inflight, m.oapDuration)
	m.inflight.WithLabelValues("oap").Set(0)
	m.inflight.WithLabelValues("arms").Set(0)
	return m
}

func (m *Metrics) ObserveOAP(method string, code codes.Code, seconds float64) {
	m.oapRPC.WithLabelValues(method, code.String()).Inc()
	m.oapDuration.WithLabelValues(method).Observe(seconds)
}

func (m *Metrics) ObserveARMS(method, result string) {
	m.armsRPC.WithLabelValues(method, result).Inc()
}

func (m *Metrics) IncInflight(target string) { m.inflight.WithLabelValues(target).Inc() }
func (m *Metrics) DecInflight(target string) { m.inflight.WithLabelValues(target).Dec() }
