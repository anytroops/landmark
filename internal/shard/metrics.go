package shard

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/status"
)

type metrics struct {
	latency   *prometheus.HistogramVec // by gRPC status code
	queueWait prometheus.Histogram
	postings  prometheus.Histogram
}

func newMetrics(reg prometheus.Registerer, partition uint32) (*metrics, error) {
	labels := prometheus.Labels{"partition": fmt.Sprint(partition)}
	// Latency buckets from 0.1 ms to ~3 s; tails are what matter here.
	latBuckets := prometheus.ExponentialBuckets(0.0001, 2, 15)
	m := &metrics{
		latency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "landmark_shard_search_seconds", Help: "Search latency, including queue wait.",
			Buckets: latBuckets, ConstLabels: labels,
		}, []string{"code"}),
		queueWait: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "landmark_shard_queue_wait_seconds", Help: "Time waiting for a free worker.",
			Buckets: latBuckets, ConstLabels: labels,
		}),
		postings: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "landmark_shard_postings_scanned", Help: "Postings visited per search.",
			Buckets: prometheus.ExponentialBuckets(1000, 2, 14), ConstLabels: labels,
		}),
	}
	for _, c := range []prometheus.Collector{m.latency, m.queueWait, m.postings} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("shard: register metrics: %w", err)
		}
	}
	return m, nil
}

func (m *metrics) observe(start time.Time, err error) {
	m.latency.WithLabelValues(status.Code(err).String()).Observe(time.Since(start).Seconds())
}
