/*
Copyright 2019 The Kubernetes Authors.

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

package filters

import (
	"crypto/tls"
	"k8s.io/apiserver/pkg/audit"
	"net/http"
)

// WithDDOGAudits adds additional information about the request to the audit logs.
// This is useful for debugging and troubleshooting.
// TLS Cipher for fips
// Content-Type of the response to track JSON vs protobuf
func WithDDOGAudits(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()

		// Set the content type annotation if it is set
		if _, ok := w.Header()["Content-Type"]; ok {
			audit.AddAuditAnnotation(ctx, "audit.datadoghq.com/contentType", w.Header().Get("Content-Type"))
		}

		// Set the TLS Cipher annotation if TLS and CipherSuite are set
		if req.TLS != nil {
			if req.TLS.CipherSuite > 0 {
				audit.AddAuditAnnotation(ctx, "audit.datadoghq.com/cipher", tls.CipherSuiteName(req.TLS.CipherSuite))
			}
		}

		handler.ServeHTTP(w, req)
	})
}
