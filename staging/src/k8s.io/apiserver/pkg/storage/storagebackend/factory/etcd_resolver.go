/*
Copyright 2025 The Kubernetes Authors.

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
	"strings"
	"sync"

	"google.golang.org/grpc/resolver"
)

const etcdResolverScheme = "etcd"

// etcdResolverBuilder implements resolver.Builder for etcd endpoints
type etcdResolverBuilder struct{}

func init() {
	resolver.Register(&etcdResolverBuilder{})
}

func (b *etcdResolverBuilder) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	r := &etcdResolver{
		target: target,
		cc:     cc,
	}
	r.start()
	return r, nil
}

func (b *etcdResolverBuilder) Scheme() string {
	return etcdResolverScheme
}

// etcdResolver resolves etcd endpoints
type etcdResolver struct {
	target resolver.Target
	cc     resolver.ClientConn
	mu     sync.Mutex
}

func (r *etcdResolver) start() {
	// Parse comma-separated endpoints from the target
	// The target format is "etcd:///etcd-0:2379,etcd-1:2379,etcd-2:2379"
	// target.Endpoint() returns the authority+path (everything after scheme://)
	// which is "//etcd-0:2379,etcd-1:2379,etcd-2:2379" (with leading slashes removed)
	endpoint := r.target.Endpoint()

	// If Endpoint is empty, try URL.Path which has the path after the authority
	if endpoint == "" && r.target.URL.Path != "" {
		endpoint = r.target.URL.Path
	}

	// Remove any leading slashes that might be present
	endpoint = strings.TrimPrefix(endpoint, "/")

	// Split by comma to get individual endpoints
	endpoints := strings.Split(endpoint, ",")

	var addrs []resolver.Address
	for _, ep := range endpoints {
		ep = strings.TrimSpace(ep)
		if ep != "" {
			addrs = append(addrs, resolver.Address{Addr: ep})
		}
	}

	r.cc.UpdateState(resolver.State{Addresses: addrs})
}

func (r *etcdResolver) ResolveNow(resolver.ResolveNowOptions) {}

func (r *etcdResolver) Close() {}
