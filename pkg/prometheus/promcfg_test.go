// Copyright 2017 The prometheus-operator Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package prometheus

import (
	"bytes"
	"testing"

	"github.com/blang/semver"
	monitoringv1 "github.com/coreos/prometheus-operator/pkg/client/monitoring/v1"
	"gopkg.in/yaml.v2"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestConfigGeneration(t *testing.T) {
	for _, v := range CompatibilityMatrix {
		cfg, err := generateTestConfig(v)
		if err != nil {
			t.Fatal(err)
		}

		for i := 0; i < 1000; i++ {
			testcfg, err := generateTestConfig(v)
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(cfg, testcfg) {
				t.Fatalf("Config generation is not deterministic.\n\n\nFirst generation: \n\n%s\n\nDifferent generation: \n\n%s\n\n", string(cfg), string(testcfg))
			}
		}
	}
}

func TestNamespaceSetCorrectly(t *testing.T) {
	sm := &monitoringv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testservicemonitor1",
			Namespace: "default",
			Labels: map[string]string{
				"group": "group1",
			},
		},
		Spec: monitoringv1.ServiceMonitorSpec{
			NamespaceSelector: monitoringv1.NamespaceSelector{
				MatchNames: []string{"test"},
			},
		},
	}

	c := k8sSDFromServiceMonitor(sm)
	s, err := yaml.Marshal(yaml.MapSlice{c})
	if err != nil {
		t.Fatal(err)
	}

	expected := `kubernetes_sd_configs:
- role: endpoints
  namespaces:
    names:
    - test
`

	result := string(s)

	if expected != result {
		t.Fatalf("Unexpected result.\n\nGot:\n\n%s\n\nExpected:\n\n%s\n\n", result, expected)
	}
}

func TestAlertmanagerBearerToken(t *testing.T) {
	cfg, err := generateConfig(
		&monitoringv1.Prometheus{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "default",
			},
			Spec: monitoringv1.PrometheusSpec{
				Alerting: &monitoringv1.AlertingSpec{
					Alertmanagers: []monitoringv1.AlertmanagerEndpoints{
						{
							Name:            "alertmanager-main",
							Namespace:       "default",
							Port:            intstr.FromString("web"),
							BearerTokenFile: "/some/file/on/disk",
						},
					},
				},
			},
		},
		nil,
		0,
		map[string]BasicAuthCredentials{},
	)
	if err != nil {
		t.Fatal(err)
	}

	// If this becomes an endless sink of maintenance, then we should just
	// change this to check that just the `bearer_token_file` is set with
	// something like json-path.
	expected := `global:
  evaluation_interval: 30s
  scrape_interval: 30s
  external_labels: {}
scrape_configs: []
alerting:
  alertmanagers:
  - path_prefix: /
    scheme: http
    kubernetes_sd_configs:
    - role: endpoints
      namespaces:
        names:
        - default
    bearer_token_file: /some/file/on/disk
    relabel_configs:
    - action: keep
      source_labels:
      - __meta_kubernetes_service_name
      regex: alertmanager-main
    - action: keep
      source_labels:
      - __meta_kubernetes_endpoint_port_name
      regex: web
`

	result := string(cfg)

	if expected != result {
		t.Fatalf("Unexpected result.\n\nGot:\n\n%s\n\nExpected:\n\n%s\n\n", result, expected)
	}
}

func TestStaticTargets(t *testing.T) {
	ep := monitoringv1.Endpoint{
		Port:        "web",
		Interval:    "30s",
		Path:        "/federate",
		HonorLabels: true,
		Params:      map[string][]string{"metrics[]": {"{__name__=~\"job:.*\"}"}},
		StaticTargets: []string{
			"source-prometheus-1:9090",
			"source-prometheus-2:9090",
			"source-prometheus-3:9090",
		},
	}

	sm := &monitoringv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "statictargetsmonitor",
			Namespace: "default",
			Labels: map[string]string{
				"group": "group1",
			},
		},
		Spec: monitoringv1.ServiceMonitorSpec{
			Endpoints: []monitoringv1.Endpoint{ep},
		},
	}

	smConfig := generateServiceMonitorConfig(semver.Version{}, sm, ep, 0, nil)
	s, err := yaml.Marshal(smConfig)
	if err != nil {
		t.Fatal(err)
	}

	expected := `job_name: default/statictargetsmonitor/0
honor_labels: true
scrape_interval: 30s
metrics_path: /federate
params:
  metrics[]:
  - '{__name__=~"job:.*"}'
static_configs:
- targets:
  - source-prometheus-1:9090
  - source-prometheus-2:9090
  - source-prometheus-3:9090
relabel_configs:
- action: keep
  source_labels:
  - __meta_kubernetes_endpoint_port_name
  regex: web
- source_labels:
  - __meta_kubernetes_namespace
  target_label: namespace
- source_labels:
  - __meta_kubernetes_pod_name
  target_label: pod
- source_labels:
  - __meta_kubernetes_service_name
  target_label: service
- source_labels:
  - __meta_kubernetes_service_name
  target_label: job
  replacement: ${1}
- target_label: endpoint
  replacement: web
`

	result := string(s)

	if expected != result {
		t.Fatalf("Unexpected result.\n\nGot:\n\n%s\n\nExpected:\n\n%s\n\n", result, expected)
	}
}

func generateTestConfig(version string) ([]byte, error) {
	replicas := int32(1)
	return generateConfig(
		&monitoringv1.Prometheus{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "default",
			},
			Spec: monitoringv1.PrometheusSpec{
				Alerting: &monitoringv1.AlertingSpec{
					Alertmanagers: []monitoringv1.AlertmanagerEndpoints{
						{
							Name:      "alertmanager-main",
							Namespace: "default",
							Port:      intstr.FromString("web"),
						},
					},
				},
				ExternalLabels: map[string]string{
					"label1": "value1",
					"label2": "value2",
				},
				Version:  version,
				Replicas: &replicas,
				ServiceMonitorSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"group": "group1",
					},
				},
				RuleSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"role": "rulefile",
					},
				},
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceMemory: resource.MustParse("400Mi"),
					},
				},
				RemoteRead: []monitoringv1.RemoteReadSpec{{
					URL: "https://example.com/remote_read",
				}},
				RemoteWrite: []monitoringv1.RemoteWriteSpec{{
					URL: "https://example.com/remote_write",
				}},
			},
		},
		makeServiceMonitors(),
		1,
		map[string]BasicAuthCredentials{},
	)
}

func makeServiceMonitors() map[string]*monitoringv1.ServiceMonitor {
	res := map[string]*monitoringv1.ServiceMonitor{}

	res["servicemonitor1"] = &monitoringv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testservicemonitor1",
			Namespace: "default",
			Labels: map[string]string{
				"group": "group1",
			},
		},
		Spec: monitoringv1.ServiceMonitorSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"group": "group1",
				},
			},
			Endpoints: []monitoringv1.Endpoint{
				monitoringv1.Endpoint{
					Port:     "web",
					Interval: "30s",
				},
			},
		},
	}

	res["servicemonitor2"] = &monitoringv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testservicemonitor2",
			Namespace: "default",
			Labels: map[string]string{
				"group": "group2",
			},
		},
		Spec: monitoringv1.ServiceMonitorSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"group":  "group2",
					"group3": "group3",
				},
			},
			Endpoints: []monitoringv1.Endpoint{
				monitoringv1.Endpoint{
					Port:     "web",
					Interval: "30s",
				},
			},
		},
	}

	res["servicemonitor3"] = &monitoringv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testservicemonitor3",
			Namespace: "default",
			Labels: map[string]string{
				"group": "group4",
			},
		},
		Spec: monitoringv1.ServiceMonitorSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"group":  "group4",
					"group3": "group5",
				},
			},
			Endpoints: []monitoringv1.Endpoint{
				monitoringv1.Endpoint{
					Port:     "web",
					Interval: "30s",
					Path:     "/federate",
					Params:   map[string][]string{"metrics[]": []string{"{__name__=~\"job:.*\"}"}},
				},
			},
		},
	}

	res["servicemonitor4"] = &monitoringv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testservicemonitor4",
			Namespace: "default",
			Labels: map[string]string{
				"group": "group6",
			},
		},
		Spec: monitoringv1.ServiceMonitorSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"group":  "group6",
					"group3": "group7",
				},
			},
			Endpoints: []monitoringv1.Endpoint{
				monitoringv1.Endpoint{
					Port:     "web",
					Interval: "30s",
					MetricRelabelConfigs: []*monitoringv1.RelabelConfig{
						&monitoringv1.RelabelConfig{
							Action:       "drop",
							Regex:        "my-job-pod-.+",
							SourceLabels: []string{"pod_name"},
						},
						&monitoringv1.RelabelConfig{
							Action:       "drop",
							Regex:        "test",
							SourceLabels: []string{"namespace"},
						},
					},
				},
			},
		},
	}

	res["servicemonitor5"] = &monitoringv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testservicemonitor5",
			Namespace: "default",
			Labels: map[string]string{
				"group": "group4",
			},
		},
		Spec: monitoringv1.ServiceMonitorSpec{
			Endpoints: []monitoringv1.Endpoint{
				{
					Port:     "web",
					Interval: "30s",
					Path:     "/federate",
					Params:   map[string][]string{"metrics[]": {"{__name__=~\"job:.*\"}"}},
					StaticTargets: []string{
						"source-prometheus-1:9090",
						"source-prometheus-2:9090",
						"source-prometheus-3:9090",
					},
				},
			},
		},
	}

	return res
}
