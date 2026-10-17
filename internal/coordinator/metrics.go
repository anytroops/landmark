package coordinator

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/status"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
)

type metrics struct {
	identify  *prometheus.HistogramVec // by code, partial
	responded prometheus.Histogram     // partitions answering per query
	attempts  *prometheus.CounterVec   // by partition, replica, outcome
	retries   prometheus.Counter
	ejections *prometheus.CounterVec // by replica
	stopped   prometheus.Counter     // query landmarks dropped by the stop list (D30)
}

func newMetrics(reg prometheus.Registerer, c *Coordinator) (*metrics, error) {
	m := &metrics{
		identify: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "landmark_coordinator_identify_seconds", Help: "End-to-end Identify latency.",
			Buckets: prometheus.ExponentialBuckets(0.0005, 2, 14),
		}, []string{"code", "partial"}),
		responded: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "landmark_coordinator_partitions_responded_fraction", Help: "Fraction of partitions that answered.",
			Buckets: []float64{0, 0.25, 0.5, 0.75, 0.99, 1},
		}),
		attempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "landmark_coordinator_shard_attempts_total", Help: "Shard Search attempts by outcome.",
		}, []string{"partition", "replica", "outcome"}),
		retries: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "landmark_coordinator_retries_total", Help: "Second attempts on another replica.",
		}),
		ejections: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "landmark_coordinator_ejections_total", Help: "Replicas ejected after consecutive failures.",
		}, []string{"replica"}),
		stopped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "landmark_coordinator_landmarks_stopped_total", Help: "Query landmarks dropped by the stop list.",
		}),
	}
	collectors := []prometheus.Collector{m.identify, m.responded, m.attempts, m.retries, m.ejections, m.stopped}
	for _, reps := range c.parts {
		for _, r := range reps {
			labels := prometheus.Labels{"partition": fmt.Sprint(r.partition), "replica": r.addr}
			collectors = append(collectors,
				prometheus.NewGaugeFunc(prometheus.GaugeOpts{
					Name: "landmark_coordinator_replica_available", Help: "1 if verified, healthy, and not ejected.",
					ConstLabels: labels,
				}, func() float64 {
					if r.available() {
						return 1
					}
					return 0
				}),
				prometheus.NewGaugeFunc(prometheus.GaugeOpts{
					Name: "landmark_coordinator_replica_outstanding", Help: "In-flight Searches.",
					ConstLabels: labels,
				}, func() float64 { return float64(r.outstanding.Load()) }),
			)
		}
	}
	for _, col := range collectors {
		if err := reg.Register(col); err != nil {
			return nil, fmt.Errorf("coordinator: register metrics: %w", err)
		}
	}
	return m, nil
}

func (m *metrics) attempt(r *replica, outcome string) {
	m.attempts.WithLabelValues(fmt.Sprint(r.partition), r.addr, outcome).Inc()
}

func (m *metrics) observeIdentify(start, end time.Time, resp *landmarkv1.IdentifyResponse, err error) {
	partial := resp != nil && resp.GetPartial()
	m.identify.WithLabelValues(status.Code(err).String(), fmt.Sprint(partial)).Observe(end.Sub(start).Seconds())
	if resp != nil && resp.GetPartitionsTotal() > 0 {
		m.responded.Observe(float64(resp.GetPartitionsResponded()) / float64(resp.GetPartitionsTotal()))
	}
}
