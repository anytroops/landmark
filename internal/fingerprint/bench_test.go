package fingerprint

import "testing"

// BenchmarkFingerprint30s times one 30 s clip end to end; use with
// -cpuprofile to see where time goes.
func BenchmarkFingerprint30s(b *testing.B) {
	pcm := syntheticSignal(30 * 11025)
	fp := New(DefaultParams())
	b.ReportAllocs()
	for b.Loop() {
		fp.Fingerprint(pcm)
	}
}
