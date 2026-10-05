/*
Copyright 2026 The Kubernetes Authors.

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

package factory

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	pb "go.etcd.io/etcd/api/v3/etcdserverpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"k8s.io/apiserver/pkg/storage/storagebackend"
)

const requestsPerAssertion = 20

// fakeEtcdMember serves the subset of the etcd gRPC API used by these tests:
// KV.Range and the standard gRPC health service. etcd reports NOT_SERVING on
// the health service while it is being defragmented when started with
// --experimental-stop-grpc-service-on-defrag.
type fakeEtcdMember struct {
	pb.UnimplementedKVServer

	endpoint string
	health   *health.Server
	ranges   atomic.Int64
}

func (m *fakeEtcdMember) Range(context.Context, *pb.RangeRequest) (*pb.RangeResponse, error) {
	m.ranges.Add(1)
	return &pb.RangeResponse{Header: &pb.ResponseHeader{}}, nil
}

func (m *fakeEtcdMember) setServing(serving bool) {
	status := healthpb.HealthCheckResponse_NOT_SERVING
	if serving {
		status = healthpb.HealthCheckResponse_SERVING
	}
	m.health.SetServingStatus("", status)
}

func (m *fakeEtcdMember) url() string {
	return "http://" + m.endpoint
}

func startFakeEtcdMembers(t *testing.T, n int) []*fakeEtcdMember {
	t.Helper()
	members := make([]*fakeEtcdMember, n)
	for i := range members {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		m := &fakeEtcdMember{endpoint: lis.Addr().String(), health: health.NewServer()}
		srv := grpc.NewServer()
		pb.RegisterKVServer(srv, m)
		healthpb.RegisterHealthServer(srv, m.health)
		go func() { _ = srv.Serve(lis) }()
		t.Cleanup(srv.Stop)
		members[i] = m
	}
	return members
}

func newTestETCD3Client(t *testing.T, endpoints ...string) *clientv3.Client {
	t.Helper()
	client, err := newETCD3Client(storagebackend.TransportConfig{ServerList: endpoints})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client.Client
}

// sendRanges issues n Range requests and returns how many reached each member.
func sendRanges(t *testing.T, client *clientv3.Client, members []*fakeEtcdMember, n int) []int64 {
	t.Helper()
	for _, m := range members {
		m.ranges.Store(0)
	}
	for range n {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err := client.Get(ctx, "/registry/foo")
		cancel()
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
	}
	counts := make([]int64, len(members))
	for i, m := range members {
		counts[i] = m.ranges.Load()
	}
	return counts
}

// eventuallyNoRangesTo retries until a batch of requests skips the given member,
// because the health status update reaches the client's picker asynchronously.
func eventuallyNoRangesTo(t *testing.T, client *clientv3.Client, members []*fakeEtcdMember, skipped int) {
	t.Helper()
	var counts []int64
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		counts = sendRanges(t, client, members, requestsPerAssertion)
		if counts[skipped] == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("NOT_SERVING member %d still received requests, per-member counts: %v", skipped, counts)
}

func TestETCD3ClientSkipsNotServingMember(t *testing.T) {
	t.Setenv(etcdClientGRPCHealthCheckEnv, "true")
	// Same shape as kube-apiserver with one --etcd-servers entry per member.
	members := startFakeEtcdMembers(t, 5)
	urls := make([]string, len(members))
	for i, m := range members {
		urls[i] = m.url()
	}
	client := newTestETCD3Client(t, urls...)

	members[0].setServing(false)
	eventuallyNoRangesTo(t, client, members, 0)

	members[0].setServing(true)
	deadline := time.Now().Add(10 * time.Second)
	for sendRanges(t, client, members, requestsPerAssertion)[0] == 0 {
		if time.Now().After(deadline) {
			t.Fatal("member did not receive requests again after returning to SERVING")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestETCD3ClientWithoutHealthCheckIgnoresNotServing(t *testing.T) {
	members := startFakeEtcdMembers(t, 2)
	client := newTestETCD3Client(t, members[0].url(), members[1].url())

	members[0].setServing(false)
	// Give a hypothetical health watch time to propagate before asserting it is ignored.
	time.Sleep(500 * time.Millisecond)

	if counts := sendRanges(t, client, members, requestsPerAssertion); counts[0] == 0 {
		t.Fatalf("expected the default client to keep routing to the NOT_SERVING member, per-member counts: %v", counts)
	}
}

func TestETCD3ClientGRPCHealthCheckEnabled(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  string
		want bool
	}{
		{name: "unset", env: "", want: false},
		{name: "disabled", env: "false", want: false},
		{name: "invalid", env: "yes please", want: false},
		{name: "enabled", env: "true", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(etcdClientGRPCHealthCheckEnv, tc.env)
			if got := etcdClientGRPCHealthCheckEnabled(); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
