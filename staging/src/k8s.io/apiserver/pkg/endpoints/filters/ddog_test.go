package filters

import (
	"crypto/tls"
	"crypto/x509"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	auditinternal "k8s.io/apiserver/pkg/apis/audit"
	"k8s.io/apiserver/pkg/audit/policy"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/endpoints/request"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWithDDOGAudits(t *testing.T) {
	handler := func(http.ResponseWriter, *http.Request) {}
	shortRunningPath := "/api/v1/namespaces/default/pods/foo"

	for _, test := range []struct {
		desc        string
		tlsCipher   uint16
		contentType string
		expected    []auditinternal.Event
	}{
		{
			"TLS Cipher set & Content Type set",
			tls.TLS_RSA_WITH_RC4_128_SHA,
			"application/json",
			[]auditinternal.Event{
				{
					Stage:          auditinternal.StageResponseComplete,
					Verb:           "get",
					RequestURI:     shortRunningPath,
					ResponseStatus: &metav1.Status{Code: 200},
					Annotations: map[string]string{
						"audit.datadoghq.com/cipher":      "TLS_RSA_WITH_RC4_128_SHA",
						"audit.datadoghq.com/contentType": "application/json",
					},
				},
			},
		}, {
			"TLS Cipher set & Content Type unset",
			tls.TLS_RSA_WITH_RC4_128_SHA,
			"",
			[]auditinternal.Event{
				{
					Stage:          auditinternal.StageResponseComplete,
					Verb:           "get",
					RequestURI:     shortRunningPath,
					ResponseStatus: &metav1.Status{Code: 200},
					Annotations: map[string]string{
						"audit.datadoghq.com/cipher": "TLS_RSA_WITH_RC4_128_SHA",
					},
				},
			},
		}, {
			"TLS Cipher unset & Content Type unset",
			0,
			"",
			[]auditinternal.Event{
				{
					Stage:          auditinternal.StageResponseComplete,
					Verb:           "get",
					RequestURI:     shortRunningPath,
					ResponseStatus: &metav1.Status{Code: 200},
				},
			},
		}, {
			"TLS Cipher unset & Content Type set",
			0,
			"application/json",
			[]auditinternal.Event{
				{
					Stage:          auditinternal.StageResponseComplete,
					Verb:           "get",
					RequestURI:     shortRunningPath,
					ResponseStatus: &metav1.Status{Code: 200},
					Annotations: map[string]string{
						"audit.datadoghq.com/contentType": "application/json",
					},
				},
			},
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			sink := &fakeAuditSink{}
			fakeRuleEvaluator := policy.NewFakePolicyRuleEvaluator(auditinternal.LevelRequestResponse, []auditinternal.Stage{auditinternal.StageRequestReceived, auditinternal.StageResponseStarted})
			handler := WithAudit(http.HandlerFunc(handler), sink, fakeRuleEvaluator, func(r *http.Request, ri *request.RequestInfo) bool { return true })
			handler = WithAuditInit(handler)
			handler = WithDDOGAudits(handler)

			req, _ := http.NewRequest("GET", shortRunningPath, nil)
			req.RemoteAddr = "127.0.0.1"
			req = withTestContext(req, &user.DefaultInfo{Name: "admin"}, nil)
			res := httptest.NewRecorder()

			if test.tlsCipher > 0 {
				req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{}}, CipherSuite: test.tlsCipher}
			}
			if test.contentType != "" {
				res.Header().Set("Content-Type", test.contentType)
			}

			func() {
				defer func() {
					recover()
				}()
				handler.ServeHTTP(res, req)
			}()

			events := sink.Events()
			t.Logf("audit log: %v", events)
			if len(events) != len(test.expected) {
				t.Fatalf("Unexpected amount of lines in audit log: %d", len(events))
			}
			for i, _ := range test.expected {
				event := events[i]
				if len(event.Annotations) != len(test.expected[i].Annotations) {
					t.Errorf("[%s] expected %d annotations, got %d", test.desc, len(test.expected[i].Annotations), len(event.Annotations))
				}
				for y, _ := range event.Annotations {
					if test.expected[i].Annotations[y] != event.Annotations[y] {
						t.Errorf("[%s] expected annotation %s to be %s, got %s", test.desc, y, test.expected[i].Annotations[y], event.Annotations[y])
					}
				}
			}
		})
	}
}
