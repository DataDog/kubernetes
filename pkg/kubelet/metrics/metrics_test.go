/*
Copyright 2024 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package metrics

import (
	"os"
	"testing"

	basemetrics "k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/testutil"
)

const imagePullDurationKey = "kubelet_" + ImagePullDurationKey

func TestImagePullDurationMetric(t *testing.T) {
	t.Run("register image pull duration", func(t *testing.T) {
		Register()
		defer clearMetrics()

		// Pairs of image size in bytes and pull duration in seconds
		dataPoints := [][]float64{
			// 0 byets, 0 seconds
			{0, 0},
			// 5MB, 10 seconds
			{5 * 1024 * 1024, 10},
			// 15MB, 20 seconds
			{15 * 1024 * 1024, 20},
			// 500 MB, 200 seconds
			{500 * 1024 * 1024, 200},
			// 15 GB, 6000 seconds,
			{15 * 1024 * 1024 * 1024, 6000},
			// 200 GB, 10000 seconds
			{200 * 1024 * 1024 * 1024, 10000},
		}

		for _, dp := range dataPoints {
			imageSize := int64(dp[0])
			duration := dp[1]
			t.Log(imageSize, duration)
			t.Log(GetImageSizeBucket(uint64(imageSize)))
			ImagePullDuration.WithLabelValues(GetImageSizeBucket(uint64(imageSize))).Observe(duration)
		}

		wants, err := os.Open("testdata/image_pull_duration_metric")
		defer func() {
			if err := wants.Close(); err != nil {
				t.Error(err)
			}
		}()

		if err != nil {
			t.Fatal(err)
		}

		if err := testutil.GatherAndCompare(GetGather(), wants, imagePullDurationKey); err != nil {
			t.Error(err)
		}

	})
}

func TestPodStatusBatchDelayBuckets(t *testing.T) {
	registry := basemetrics.NewKubeRegistry()
	registry.MustRegister(PodStatusBatchDelay)

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if len(families) != 1 {
		t.Fatalf("expected one metric family, got %d", len(families))
	}
	if name := families[0].GetName(); name != "kubelet_"+PodStatusBatchDelayKey {
		t.Fatalf("unexpected metric name %q", name)
	}
	if count := len(families[0].GetMetric()); count != 1 {
		t.Fatalf("expected one histogram, got %d metrics", count)
	}
	histogram := families[0].GetMetric()[0].GetHistogram()
	if histogram == nil {
		t.Fatal("expected a histogram metric")
	}

	for _, tt := range []struct {
		name           string
		delay          float64
		wantUpperBound float64
	}{
		{name: "default window with jitter", delay: 1.2, wantUpperBound: 1.25},
		{name: "maximum window with jitter", delay: 6, wantUpperBound: 6},
		{name: "coalescing at maximum window", delay: 21, wantUpperBound: 25},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, bucket := range histogram.GetBucket() {
				if upperBound := bucket.GetUpperBound(); upperBound >= tt.delay {
					if upperBound != tt.wantUpperBound {
						t.Errorf("bucket covering %gs has upper bound %g, want %g", tt.delay, upperBound, tt.wantUpperBound)
					}
					return
				}
			}
			t.Errorf("no finite histogram bucket covers %gs", tt.delay)
		})
	}
}

func clearMetrics() {
	ImagePullDuration.Reset()
}
